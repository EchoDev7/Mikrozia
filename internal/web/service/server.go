package service

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	stdnet "net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/amneziawg"
	"github.com/echodev7/mikrozia/v3/internal/amneziawgnet"
	"github.com/echodev7/mikrozia/v3/internal/config"
	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/logger"
	"github.com/echodev7/mikrozia/v3/internal/routeros"
	"github.com/echodev7/mikrozia/v3/internal/util/common"
	"github.com/echodev7/mikrozia/v3/internal/util/netsafe"
	"github.com/echodev7/mikrozia/v3/internal/util/sys"
	"github.com/echodev7/mikrozia/v3/internal/xray"

	"github.com/google/uuid"
	utls "github.com/refraction-networking/utls"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// ProcessState represents the current state of a system process.
type ProcessState string

// Process state constants
const (
	Running ProcessState = "running" // Process is running normally
	Stop    ProcessState = "stop"    // Process is stopped
	Error   ProcessState = "error"   // Process is in error state
)

// Status represents comprehensive system and application status information.
// It includes CPU, memory, disk, network statistics, and Xray process status.
type Status struct {
	ResourceScope      string   `json:"resourceScope,omitempty"`
	UnavailableMetrics []string `json:"unavailableMetrics,omitempty"`

	T           time.Time `json:"-"`
	Cpu         float64   `json:"cpu"`
	CpuCores    int       `json:"cpuCores"`
	LogicalPro  int       `json:"logicalPro"`
	CpuSpeedMhz float64   `json:"cpuSpeedMhz"`
	Mem         struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"mem"`
	ContainerMem *sys.ContainerMemory `json:"containerMem"`
	Swap         struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"swap"`
	Disk struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"disk"`
	DiskIO struct {
		Read  uint64 `json:"read"`
		Write uint64 `json:"write"`
	} `json:"diskIO"`
	DiskTraffic struct {
		Read  uint64 `json:"read"`
		Write uint64 `json:"write"`
	} `json:"diskTraffic"`
	Xray struct {
		State    ProcessState `json:"state"`
		ErrorMsg string       `json:"errorMsg"`
		Version  string       `json:"version"`
	} `json:"xray"`
	// AmneziaWG gates the overview's AmneziaWG log view: Configured stays true
	// while an inbound exists but its embedded interface isn't up yet, which
	// is exactly when that view's event lines are worth reading.
	AmneziaWG struct {
		Configured bool `json:"configured"`
		Running    bool `json:"running"`
	} `json:"amneziawg"`
	PanelVersion string    `json:"panelVersion"`
	PanelGuid    string    `json:"panelGuid"`
	Uptime       uint64    `json:"uptime"`
	Loads        []float64 `json:"loads"`
	TcpCount     int       `json:"tcpCount"`
	UdpCount     int       `json:"udpCount"`
	NetIO        struct {
		Up      uint64 `json:"up"`
		Down    uint64 `json:"down"`
		PktUp   uint64 `json:"pktUp"`
		PktDown uint64 `json:"pktDown"`
	} `json:"netIO"`
	NetTraffic struct {
		Sent    uint64 `json:"sent"`
		Recv    uint64 `json:"recv"`
		PktSent uint64 `json:"pktSent"`
		PktRecv uint64 `json:"pktRecv"`
	} `json:"netTraffic"`
	PublicIP struct {
		IPv4 string `json:"ipv4"`
		IPv6 string `json:"ipv6"`
	} `json:"publicIP"`
	AppStats struct {
		Threads uint32 `json:"threads"`
		Mem     uint64 `json:"mem"`
		Uptime  uint64 `json:"uptime"`
	} `json:"appStats"`
	TlsCert *TlsCertInfo `json:"tlsCert,omitempty"`
}

// TlsCertInfo summarizes the active panel TLS certificate for the Overview card.
type TlsCertInfo struct {
	Configured            bool   `json:"configured"`
	Subject               string `json:"subject,omitempty"`
	NotAfter              int64  `json:"notAfter,omitempty"`
	DaysLeft              int    `json:"daysLeft,omitempty"`
	Expired               bool   `json:"expired,omitempty"`
	Port80Status          string `json:"port80Status,omitempty"`
	RouterServiceConflict string `json:"routerServiceConflict,omitempty"`
}

// ServerService provides business logic for server monitoring and management.
// It handles system status collection, IP detection, and application statistics.
type ServerService struct {
	lxcMetrics    sys.LXCReader
	netIOCounters func(bool) ([]net.IOCountersStat, error)

	xrayService        XrayService
	inboundService     InboundService
	settingService     SettingService
	cachedIPv4         string
	cachedIPv6         string
	nextIPv4Lookup     time.Time
	nextIPv6Lookup     time.Time
	ipv6RetryInterval  time.Duration
	publicIPLookupDone chan struct{}
	mu                 sync.Mutex
	lastCPUTimes       cpu.TimesStat
	hasLastCPUSample   bool
	hasNativeCPUSample bool
	emaCPU             float64
	cachedCpuSpeedMhz  float64
	lastCpuInfoAttempt time.Time

	lastStatusMu sync.RWMutex
	lastStatus   *Status
	coldStatusMu sync.Mutex

	tlsCertMu       sync.Mutex
	tlsCertCached   *TlsCertInfo
	tlsCertPath     string
	tlsCertCachedAt time.Time

	port80Mu              sync.Mutex
	port80Status          string
	routerServiceConflict string
	port80CheckedAt       time.Time
	port80Checking        bool
}

// allowedHistoryBuckets is the bucket-second whitelist for time-series
// aggregation endpoints (server + node metrics). Restricting it prevents
// callers from triggering arbitrary aggregation work and keeps the
// frontend's bucket selector self-documenting.
var allowedHistoryBuckets = map[int]bool{
	2:     true, // 2m
	30:    true, // 30m
	60:    true, // 1h
	180:   true, // 3h
	360:   true, // 6h
	720:   true, // 12h
	1440:  true, // 24h
	2880:  true, // 2d
	10080: true, // 7d
}

// IsAllowedHistoryBucket reports whether a bucket-seconds value is in the
// whitelist used by /server/history, /server/cpuHistory, /server/xrayMetricsHistory,
// /server/xrayObservatoryHistory, and /nodes/history.
func IsAllowedHistoryBucket(bucketSeconds int) bool {
	return allowedHistoryBuckets[bucketSeconds]
}

// LastStatus returns the most recent Status snapshot collected by
// RefreshStatus. Safe for concurrent readers.
func (s *ServerService) LastStatus() *Status {
	s.lastStatusMu.RLock()
	defer s.lastStatusMu.RUnlock()
	return s.lastStatus
}

func (s *ServerService) applyLiveXrayStatus(status *Status) {
	if s.xrayService.IsXrayRunning() {
		status.Xray.State = Running
		status.Xray.ErrorMsg = s.xrayService.GetHeldBackConfig()
	} else {
		if err := s.xrayService.GetXrayErr(); err != nil {
			status.Xray.State = Error
		} else {
			status.Xray.State = Stop
		}
		status.Xray.ErrorMsg = s.xrayService.GetXrayResult()
	}
	status.Xray.Version = s.xrayService.GetXrayVersion()
}

func (s *ServerService) syncCachedXrayStatus() {
	s.lastStatusMu.Lock()
	defer s.lastStatusMu.Unlock()
	if s.lastStatus == nil {
		return
	}
	cp := *s.lastStatus
	s.applyLiveXrayStatus(&cp)
	s.lastStatus = &cp
}

func (s *ServerService) reconcileStatusXray(status *Status) *Status {
	live := *status
	s.applyLiveXrayStatus(&live)
	if live.Xray == status.Xray {
		return status
	}
	return &live
}

const maxCachedStatusAge = 5 * time.Second

// CurrentStatus never reports "no status yet" or a permanently frozen snapshot:
// if the @2s ticker has not populated LastStatus yet or stalled beyond
// maxCachedStatusAge, collect a fresh snapshot on demand.
func (s *ServerService) CurrentStatus() *Status {
	if status := s.LastStatus(); status != nil && time.Since(status.T) <= maxCachedStatusAge {
		return s.reconcileStatusXray(status)
	}
	s.coldStatusMu.Lock()
	defer s.coldStatusMu.Unlock()
	if status := s.LastStatus(); status != nil && time.Since(status.T) <= maxCachedStatusAge {
		return s.reconcileStatusXray(status)
	}
	return s.RefreshStatus()
}

// RefreshStatus collects a new system snapshot, stores it as LastStatus, and
// appends it to the system-metrics time series. Returns the new snapshot (may
// be nil if collection failed). Called by the background ticker; the caller is
// responsible for any side effects (websocket broadcast, xray metrics sample).
func (s *ServerService) RefreshStatus() *Status {
	next := s.GetStatus(s.LastStatus())
	if next == nil {
		return nil
	}
	s.lastStatusMu.Lock()
	s.lastStatus = next
	s.lastStatusMu.Unlock()
	s.AppendStatusSample(time.Now(), next)
	return next
}

// GetDefaultLogOutboundTags scans the default Xray config for freedom and
// blackhole outbound tags so /getXrayLogs can colour-code log lines without
// the controller re-doing the JSON walk. Falls back to the historical
// "direct"/"blocked" defaults when the config can't be read.
func (s *ServerService) GetDefaultLogOutboundTags() (freedoms, blackholes []string) {
	config, err := s.settingService.GetDefaultXrayConfig()
	if err == nil && config != nil {
		if cfgMap, ok := config.(map[string]any); ok {
			if outbounds, ok := cfgMap["outbounds"].([]any); ok {
				for _, outbound := range outbounds {
					obMap, ok := outbound.(map[string]any)
					if !ok {
						continue
					}
					tag, _ := obMap["tag"].(string)
					if tag == "" {
						continue
					}
					switch obMap["protocol"] {
					case "freedom":
						freedoms = append(freedoms, tag)
					case "blackhole":
						blackholes = append(blackholes, tag)
					}
				}
			}
		}
	}
	if len(freedoms) == 0 {
		freedoms = []string{"direct"}
	}
	if len(blackholes) == 0 {
		blackholes = []string{"blocked"}
	}
	return freedoms, blackholes
}

// AggregateCpuHistory returns up to maxPoints averaged buckets of size bucketSeconds.
// Kept for back-compat with the original /panel/api/server/cpuHistory/:bucket route;
// the response key is "cpu" (not "v") so legacy consumers parse unchanged.
func (s *ServerService) AggregateCpuHistory(bucketSeconds int, maxPoints int) []map[string]any {
	out := systemMetrics.aggregate(s.systemMetricKey("cpu"), bucketSeconds, maxPoints)
	for _, p := range out {
		p["cpu"] = p["v"]
		delete(p, "v")
	}
	return out
}

// AggregateSystemMetric returns up to maxPoints averaged buckets for any
// known system metric (see SystemMetricKeys). Output points have keys
// {"t": unixSec, "v": value}; the caller decides how to format the value.
func (s *ServerService) AggregateSystemMetric(metric string, bucketSeconds int, maxPoints int) []map[string]any {
	return systemMetrics.aggregate(s.systemMetricKey(metric), bucketSeconds, maxPoints)
}

type LogEntry struct {
	DateTime    time.Time `json:"DateTime" example:"2025-01-01T12:00:00Z"`
	FromAddress string    `json:"FromAddress" example:"192.0.2.10:54321"`
	ToAddress   string    `json:"ToAddress" example:"example.com:443"`
	Inbound     string    `json:"Inbound" example:"inbound-443"`
	Outbound    string    `json:"Outbound" example:"direct"`
	Email       string    `json:"Email" example:"alice@example.com"`
	Event       int       `json:"Event" example:"0"`
}

type NewUUIDResponse struct {
	UUID string `json:"uuid" example:"550e8400-e29b-41d4-a716-446655440000"`
}

type MLDSA65Response struct {
	Seed   string `json:"seed" example:"mldsa65-seed"`
	Verify string `json:"verify" example:"mldsa65-verify"`
}

type MLKEM768Response struct {
	Seed   string `json:"seed" example:"mlkem768-seed"`
	Client string `json:"client" example:"mlkem768-client"`
}

func getPublicIP(url string) string {
	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if reqErr != nil {
		return "N/A"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "N/A"
	}
	defer resp.Body.Close()

	// Don't retry if access is blocked or region-restricted
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnavailableForLegalReasons {
		return "N/A"
	}
	if resp.StatusCode != http.StatusOK {
		return "N/A"
	}

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "N/A"
	}

	ipString := strings.TrimSpace(string(ip))
	if ipString == "" {
		return "N/A"
	}

	return ipString
}

var publicIPv4Services = []string{
	"https://api4.ipify.org",
	"https://ipv4.icanhazip.com",
	"https://v4.api.ipinfo.io/ip",
	"https://ipv4.myexternalip.com/raw",
	"https://4.ident.me",
	"https://check-host.net/ip",
}

var publicIPv6Services = []string{
	"https://api6.ipify.org",
	"https://ipv6.icanhazip.com",
	"https://v6.api.ipinfo.io/ip",
	"https://ipv6.myexternalip.com/raw",
	"https://6.ident.me",
}

const (
	publicIPRefreshInterval = 5 * time.Minute
	publicIPRetryInterval   = time.Minute
)

// resolvePublicIPs waits for the initial lookup so cold backups can use it.
// Warm callers use cached addresses while a refresh runs in the background.
func (s *ServerService) resolvePublicIPs() {
	ipv4, ipv6 := s.publicIPs()
	if done := s.resolvePublicIPsInBackground(); done != nil && ipv4 == "" && ipv6 == "" {
		<-done
	}
}

// refreshedPublicIP retains a known address through temporary lookup failures.
func refreshedPublicIP(cached, fetched string, now time.Time) (string, time.Time) {
	if fetched != "" && fetched != "N/A" {
		return fetched, now.Add(publicIPRefreshInterval)
	}
	if cached == "" {
		cached = "N/A"
	}
	return cached, now.Add(publicIPRetryInterval)
}

func nextPublicIPv6RetryInterval(previous time.Duration) time.Duration {
	switch {
	case previous < time.Minute:
		return time.Minute
	case previous < 2*time.Minute:
		return 2 * time.Minute
	case previous < 5*time.Minute:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}

// firstPublicIP returns the first service that answers, or "N/A" when every
// one of them fails.
func firstPublicIP(services []string) string {
	var ip string
	for _, service := range services {
		ip = getPublicIP(service)
		if ip != "N/A" {
			break
		}
	}
	return ip
}

// resolvePublicIPsInBackground shares one lookup without blocking status samples.
// Each address family has its own deadline; cached values stay readable.
func (s *ServerService) resolvePublicIPsInBackground() <-chan struct{} {
	s.mu.Lock()
	if done := s.publicIPLookupDone; done != nil {
		s.mu.Unlock()
		return done
	}
	now := time.Now()
	wantIPv4 := !now.Before(s.nextIPv4Lookup)
	wantIPv6 := !now.Before(s.nextIPv6Lookup)
	if !wantIPv4 && !wantIPv6 {
		s.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	s.publicIPLookupDone = done
	s.mu.Unlock()

	go func() {
		var ipv4, ipv6 string
		if wantIPv4 {
			ipv4 = firstPublicIP(publicIPv4Services)
		}
		if wantIPv6 {
			ipv6 = firstPublicIP(publicIPv6Services)
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		now := time.Now()
		if wantIPv4 {
			s.cachedIPv4, s.nextIPv4Lookup = refreshedPublicIP(s.cachedIPv4, ipv4, now)
		}
		if wantIPv6 {
			s.cachedIPv6, s.nextIPv6Lookup = refreshedPublicIP(s.cachedIPv6, ipv6, now)
			if ipv6 != "" && ipv6 != "N/A" {
				s.ipv6RetryInterval = 0
			} else {
				s.ipv6RetryInterval = nextPublicIPv6RetryInterval(s.ipv6RetryInterval)
				s.nextIPv6Lookup = now.Add(s.ipv6RetryInterval)
			}
		}
		s.publicIPLookupDone = nil
		close(done)
	}()
	return done
}

func (s *ServerService) publicIPs() (ipv4 string, ipv6 string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cachedIPv4, s.cachedIPv6
}

func (s *ServerService) GetStatus(lastStatus *Status) *Status {
	now := time.Now()
	status := &Status{
		T: now,
	}

	scoped, scopeErr := s.lxcMetrics.Read(now)
	if scopeErr != nil {
		logger.Warning("get scoped metrics failed:", scopeErr)
	}
	if scoped != nil {
		applyLXCStats(status, scoped)
	} else {
		// CPU stats
		util, err := s.sampleCPUUtilization()
		if err != nil {
			logger.Warning("get cpu percent failed:", err)
		} else {
			status.Cpu = util
		}

		status.CpuCores, err = cpu.Counts(false)
		if err != nil {
			logger.Warning("get cpu cores count failed:", err)
		}

		status.LogicalPro = runtime.NumCPU()

		if status.CpuSpeedMhz = s.cachedCpuSpeedMhz; s.cachedCpuSpeedMhz == 0 && time.Since(s.lastCpuInfoAttempt) > 5*time.Minute {
			s.lastCpuInfoAttempt = time.Now()
			done := make(chan struct{})
			go func() {
				defer close(done)
				cpuInfos, err := cpu.Info()
				if err != nil {
					logger.Warning("get cpu info failed:", err)
					return
				}
				if len(cpuInfos) > 0 {
					s.cachedCpuSpeedMhz = cpuInfos[0].Mhz
					status.CpuSpeedMhz = s.cachedCpuSpeedMhz
				} else {
					logger.Warning("could not find cpu info")
				}
			}()
			select {
			case <-done:
			case <-time.After(1500 * time.Millisecond):
				logger.Warning("cpu info query timed out; will retry later")
			}
		} else if s.cachedCpuSpeedMhz != 0 {
			status.CpuSpeedMhz = s.cachedCpuSpeedMhz
		}

		// Uptime
		upTime, err := host.Uptime()
		if err != nil {
			logger.Warning("get uptime failed:", err)
		} else {
			status.Uptime = upTime
		}
	}

	// Memory stats
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		logger.Warning("get virtual memory failed:", err)
	} else {
		status.Mem.Current = memInfo.Used
		status.Mem.Total = memInfo.Total
	}

	if scoped == nil {
		swapInfo, err := mem.SwapMemory()
		if err != nil {
			logger.Warning("get swap memory failed:", err)
		} else {
			status.Swap.Current = swapInfo.Used
			status.Swap.Total = swapInfo.Total
		}
	}

	// Disk stats
	diskInfo, err := disk.Usage("/")
	if err != nil {
		logger.Warning("get disk usage failed:", err)
	} else {
		status.Disk.Current = diskInfo.Used
		status.Disk.Total = diskInfo.Total
	}

	diskIOStats, err := disk.IOCounters()
	if err != nil {
		logger.Warning("get disk io counters failed:", err)
	} else {
		var totalRead, totalWrite uint64
		for _, counter := range diskIOStats {
			totalRead += counter.ReadBytes
			totalWrite += counter.WriteBytes
		}
		status.DiskTraffic.Read = totalRead
		status.DiskTraffic.Write = totalWrite

		if lastStatus != nil {
			duration := now.Sub(lastStatus.T)
			seconds := float64(duration) / float64(time.Second)
			if seconds > 0 && status.DiskTraffic.Read >= lastStatus.DiskTraffic.Read {
				status.DiskIO.Read = uint64(float64(status.DiskTraffic.Read-lastStatus.DiskTraffic.Read) / seconds)
			}
			if seconds > 0 && status.DiskTraffic.Write >= lastStatus.DiskTraffic.Write {
				status.DiskIO.Write = uint64(float64(status.DiskTraffic.Write-lastStatus.DiskTraffic.Write) / seconds)
			}
		}
	}

	// Load averages
	avgState, err := load.Avg()
	if err != nil {
		logger.Warning("get load avg failed:", err)
	} else {
		status.Loads = []float64{avgState.Load1, avgState.Load5, avgState.Load15}
	}

	// Network stats
	readNet := s.netIOCounters
	if readNet == nil {
		readNet = net.IOCounters
	}
	ioStats, err := readNet(true)
	collectNetIO(ioStats, err, status, lastStatus, now)

	// TCP/UDP connections
	status.TcpCount, err = sys.GetTCPCount()
	if err != nil {
		logger.Warning("get tcp connections failed:", err)
	}

	status.UdpCount, err = sys.GetUDPCount()
	if err != nil {
		logger.Warning("get udp connections failed:", err)
	}

	s.resolvePublicIPsInBackground()
	status.PublicIP.IPv4, status.PublicIP.IPv6 = s.publicIPs()

	// Xray status
	s.applyLiveXrayStatus(status)

	var amneziawgCount int64
	if err := database.GetDB().Model(model.Inbound{}).
		Where("protocol = ? AND enable = ? AND node_id IS NULL", model.AmneziaWG, true).
		Count(&amneziawgCount).Error; err != nil {
		logger.Warning("count amneziawg inbounds failed:", err)
	}
	status.AmneziaWG.Configured = amneziawgCount > 0
	status.AmneziaWG.Running = amneziawgnet.GetManager().HasRunning()

	status.PanelVersion = config.GetPanelVersion()
	if guid, err := s.settingService.GetPanelGuid(); err == nil {
		status.PanelGuid = guid
	}

	// Application stats
	if rss := sys.SelfRSS(); rss > 0 {
		status.AppStats.Mem = rss
	} else {
		var rtm runtime.MemStats
		runtime.ReadMemStats(&rtm)
		status.AppStats.Mem = rtm.Sys
	}
	status.AppStats.Threads = uint32(runtime.NumGoroutine())
	if process := currentXrayProcess(); process != nil && process.IsRunning() {
		status.AppStats.Uptime = process.GetUptime()
	} else {
		status.AppStats.Uptime = 0
	}

	status.TlsCert = s.readPanelTLSCertInfo(now)
	p80, svcConflict := s.readPort80Status(now)
	status.TlsCert.Port80Status = p80
	status.TlsCert.RouterServiceConflict = svcConflict

	return status
}

func (s *ServerService) readPort80Status(now time.Time) (string, string) {
	cfg := resolveRouterOSAPIConfig()
	if cfg.State != "connecting" {
		s.port80Mu.Lock()
		s.port80Status = "unknown"
		s.routerServiceConflict = ""
		s.port80Mu.Unlock()
		return "unknown", ""
	}

	s.port80Mu.Lock()
	cached := s.port80Status
	cachedConflict := s.routerServiceConflict
	if cached == "" {
		cached = "unknown"
	}
	shouldProbe := !s.port80Checking && (s.port80CheckedAt.IsZero() || now.Sub(s.port80CheckedAt) >= 5*time.Second)
	if shouldProbe {
		s.port80Checking = true
	}
	s.port80Mu.Unlock()

	if shouldProbe {
		go func(natCfg routeros.NATConnConfig) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			snap := (routeros.Client{}).CheckPort80AndServices(ctx, natCfg)
			s.port80Mu.Lock()
			s.port80Status = snap.Port80Status
			s.routerServiceConflict = snap.ServiceConflict
			s.port80CheckedAt = time.Now()
			s.port80Checking = false
			s.port80Mu.Unlock()
		}(routeros.NATConnConfig{
			Host:     cfg.Host,
			Port:     cfg.Port,
			Username: cfg.Username,
			Password: cfg.Password,
		})
	}
	return cached, cachedConflict
}

func (s *ServerService) readPanelTLSCertInfo(now time.Time) *TlsCertInfo {
	if database.GetDB() == nil {
		return &TlsCertInfo{Configured: false}
	}
	certPath, err := s.settingService.GetCertFile()
	if err != nil {
		return &TlsCertInfo{Configured: false}
	}
	certPath = strings.TrimSpace(certPath)
	if certPath == "" {
		return &TlsCertInfo{Configured: false}
	}

	s.tlsCertMu.Lock()
	defer s.tlsCertMu.Unlock()
	if s.tlsCertCached != nil && s.tlsCertPath == certPath && now.Sub(s.tlsCertCachedAt) < time.Minute {
		cp := *s.tlsCertCached
		return &cp
	}

	raw, readErr := os.ReadFile(certPath)
	if readErr != nil {
		info := &TlsCertInfo{Configured: false}
		s.tlsCertCached, s.tlsCertPath, s.tlsCertCachedAt = info, certPath, now
		return &TlsCertInfo{Configured: false}
	}

	var leaf *x509.Certificate
	for len(raw) > 0 {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			parsed, parseErr := x509.ParseCertificate(block.Bytes)
			if parseErr == nil {
				leaf = parsed
				break
			}
		}
	}
	if leaf == nil {
		info := &TlsCertInfo{Configured: false}
		s.tlsCertCached, s.tlsCertPath, s.tlsCertCachedAt = info, certPath, now
		return &TlsCertInfo{Configured: false}
	}

	subject := strings.TrimSpace(leaf.Subject.CommonName)
	if len(leaf.DNSNames) > 0 && strings.TrimSpace(leaf.DNSNames[0]) != "" {
		subject = strings.TrimSpace(leaf.DNSNames[0])
	}
	hoursLeft := leaf.NotAfter.Sub(now).Hours()
	daysLeft := int(math.Floor(hoursLeft / 24))
	info := &TlsCertInfo{
		Configured: true,
		Subject:    subject,
		NotAfter:   leaf.NotAfter.Unix(),
		DaysLeft:   daysLeft,
		Expired:    now.After(leaf.NotAfter),
	}
	s.tlsCertCached, s.tlsCertPath, s.tlsCertCachedAt = info, certPath, now
	cp := *info
	return &cp
}

// AppendCpuSample is preserved for callers that only have the CPU number.
// New callers should prefer AppendStatusSample which writes the full set.
func (s *ServerService) AppendCpuSample(t time.Time, v float64) {
	systemMetrics.append(s.systemMetricKey("cpu"), t, v)
}

// AppendStatusSample writes one tick of every metric we keep — CPU, memory
// percent, network throughput (bytes/s), online client count, and the three
// load averages. Called by RefreshStatus on the same @2s cadence as
// AppendCpuSample, so all series stay aligned.
func (s *ServerService) AppendStatusSample(t time.Time, status *Status) {
	if status == nil {
		return
	}
	appendScopedMetric(status, "cpu", t, status.Cpu)
	if status.ResourceScope == "container" && status.MetricAvailable("cpu") {
		systemMetrics.append("containerCpu", t, status.Cpu)
	}
	if status.Mem.Total > 0 {
		systemMetrics.append("mem", t, float64(status.Mem.Current)*100.0/float64(status.Mem.Total))
	}
	if status.ResourceScope == "container" && status.ContainerMem != nil && status.ContainerMem.Limit != nil && *status.ContainerMem.Limit > 0 {
		systemMetrics.append("containerMem", t, float64(status.ContainerMem.Used)*100.0/float64(*status.ContainerMem.Limit))
	}
	if status.Swap.Total > 0 {
		appendScopedMetric(status, "swap", t, float64(status.Swap.Current)*100.0/float64(status.Swap.Total))
	} else {
		appendScopedMetric(status, "swap", t, 0)
	}
	if status.MetricAvailable("netIO") {
		systemMetrics.append("netUp", t, float64(status.NetIO.Up))
		systemMetrics.append("netDown", t, float64(status.NetIO.Down))
		systemMetrics.append("pktUp", t, float64(status.NetIO.PktUp))
		systemMetrics.append("pktDown", t, float64(status.NetIO.PktDown))
	}
	systemMetrics.append("diskRead", t, float64(status.DiskIO.Read))
	systemMetrics.append("diskWrite", t, float64(status.DiskIO.Write))
	if status.Disk.Total > 0 {
		systemMetrics.append("diskUsage", t, float64(status.Disk.Current)*100.0/float64(status.Disk.Total))
	}
	systemMetrics.append("tcpCount", t, float64(status.TcpCount))
	systemMetrics.append("udpCount", t, float64(status.UdpCount))
	online := 0
	if process := currentXrayProcess(); process != nil && process.IsRunning() {
		online = len(process.GetOnlineClients())
	}
	systemMetrics.append("online", t, float64(online))
	if len(status.Loads) >= 3 {
		systemMetrics.append("load1", t, status.Loads[0])
		systemMetrics.append("load5", t, status.Loads[1])
		systemMetrics.append("load15", t, status.Loads[2])
	}
}

func (s *ServerService) sampleCPUUtilization() (float64, error) {
	// Try native platform-specific CPU implementation first (Windows, Linux, macOS)
	if pct, err := sys.CPUPercentRaw(); err == nil {
		s.mu.Lock()
		// First call to native method returns 0 (initializes baseline)
		if !s.hasNativeCPUSample {
			s.hasNativeCPUSample = true
			s.mu.Unlock()
			return 0, nil
		}
		// Smooth with EMA
		const alpha = 0.3
		if s.emaCPU == 0 {
			s.emaCPU = pct
		} else {
			s.emaCPU = alpha*pct + (1-alpha)*s.emaCPU
		}
		val := s.emaCPU
		s.mu.Unlock()
		return val, nil
	}
	// If native call fails, fall back to gopsutil times
	// Read aggregate CPU times (all CPUs combined)
	times, err := cpu.Times(false)
	if err != nil {
		return 0, err
	}
	if len(times) == 0 {
		return 0, fmt.Errorf("no cpu times available")
	}

	cur := times[0]

	s.mu.Lock()
	defer s.mu.Unlock()

	// If this is the first sample, initialize and return current EMA (0 by default)
	if !s.hasLastCPUSample {
		s.lastCPUTimes = cur
		s.hasLastCPUSample = true
		return s.emaCPU, nil
	}

	// Compute busy and total deltas
	// Note: Guest and GuestNice times are already included in User and Nice respectively,
	// so we exclude them to avoid double-counting (Linux kernel accounting)
	idleDelta := cur.Idle - s.lastCPUTimes.Idle
	busyDelta := (cur.User - s.lastCPUTimes.User) +
		(cur.System - s.lastCPUTimes.System) +
		(cur.Nice - s.lastCPUTimes.Nice) +
		(cur.Iowait - s.lastCPUTimes.Iowait) +
		(cur.Irq - s.lastCPUTimes.Irq) +
		(cur.Softirq - s.lastCPUTimes.Softirq) +
		(cur.Steal - s.lastCPUTimes.Steal)

	totalDelta := busyDelta + idleDelta

	// Update last sample for next time
	s.lastCPUTimes = cur

	// Guard against division by zero or negative deltas (e.g., counter resets)
	if totalDelta <= 0 {
		return s.emaCPU, nil
	}

	raw := 100.0 * (busyDelta / totalDelta)
	if raw < 0 {
		raw = 0
	}
	if raw > 100 {
		raw = 100
	}

	// Exponential moving average to smooth spikes
	const alpha = 0.3 // smoothing factor (0<alpha<=1). Higher = more responsive, lower = smoother
	if s.emaCPU == 0 {
		// Initialize EMA with the first real reading to avoid long warm-up from zero
		s.emaCPU = raw
	} else {
		s.emaCPU = alpha*raw + (1-alpha)*s.emaCPU
	}

	return s.emaCPU, nil
}

func (s *ServerService) StopXrayService() error {
	defer s.syncCachedXrayStatus()
	err := s.xrayService.StopXray()
	if err != nil {
		logger.Error("stop xray failed:", err)
		return err
	}
	return nil
}

func (s *ServerService) RestartXrayService() error {
	defer s.syncCachedXrayStatus()
	err := s.xrayService.RestartXray(true)
	if err != nil {
		logger.Error("start xray failed:", err)
		return err
	}
	return nil
}

// GetLogs returns up to count in-memory application log lines at or below level.
func (s *ServerService) GetLogs(count string, level string) []string {
	c, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || c < 1 || c > 10000 {
		return []string{}
	}

	lvl := strings.ToLower(strings.TrimSpace(level))
	switch lvl {
	case "":
		lvl = "info"
	case "err":
		// Normalize legacy severity alias so older clients keep working.
		lvl = "error"
	}

	switch lvl {
	case "debug", "info", "notice", "warning", "error", "critical":
	default:
		return []string{}
	}

	return logger.GetLogs(c, lvl)
}

func (s *ServerService) GetPanelLogs(count string, level string) []string {
	return s.getLogsBySource(count, level, "panel")
}

func (s *ServerService) GetXrayRuntimeLogs(count string, level string) []string {
	return s.getLogsBySource(count, level, "xray")
}

func (s *ServerService) getLogsBySource(count string, level string, source string) []string {
	c, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || c < 1 || c > 10000 {
		return []string{}
	}
	lvl := strings.ToLower(strings.TrimSpace(level))
	if lvl == "" {
		lvl = "info"
	}
	if lvl == "err" {
		lvl = "error"
	}
	switch lvl {
	case "debug", "info", "notice", "warning", "error", "critical":
	default:
		return []string{}
	}
	return logger.GetLogsBySource(c, lvl, source)
}

// parseAccessLogFields extracts the structured fields from one Xray access-log
// line. Lines are attacker-influenced (a client's requested destination lands in
// the log verbatim) and may be truncated, so every positional lookup is length
// guarded: a malformed line yields a partial entry rather than panicking.
func parseAccessLogFields(line string) LogEntry {
	var entry LogEntry
	parts := strings.Fields(line)

	for i, part := range parts {

		if i == 0 && len(parts) > 1 {
			dateTime, err := time.ParseInLocation("2006/01/02 15:04:05.999999", parts[0]+" "+parts[1], time.Local)
			if err != nil {
				continue
			}
			entry.DateTime = dateTime.UTC()
		}

		if part == "from" && i+1 < len(parts) {
			entry.FromAddress = strings.TrimLeft(parts[i+1], "/")
		} else if part == "accepted" && i+1 < len(parts) {
			entry.ToAddress = strings.TrimLeft(parts[i+1], "/")
		} else if strings.HasPrefix(part, "[") {
			entry.Inbound = part[1:]
		} else if strings.HasSuffix(part, "]") {
			entry.Outbound = part[:len(part)-1]
		} else if part == "email:" && i+1 < len(parts) {
			entry.Email = parts[i+1]
		}
	}

	return entry
}

// PeerActivity is one peer's live embedded-Device-reported state, the
// counterpart of an Xray access-log entry: a tunnel logs no requests, only
// handshakes and bytes.
type PeerActivity struct {
	Interface  string `json:"interface" example:"awg1"`
	Tag        string `json:"tag" example:"inbound-51820"`
	InboundId  int    `json:"inboundId" example:"1"`
	Email      string `json:"email" example:"peer@example.com"`
	Endpoint   string `json:"endpoint" example:"203.0.113.9:51820"`
	AllowedIPs string `json:"allowedIPs" example:"10.8.1.2/32"`
	// Handshake is unix milliseconds, 0 when the peer has never connected.
	Handshake int64 `json:"handshake" example:"1735732800000"`
	Up        int64 `json:"up" example:"1048576"`
	Down      int64 `json:"down" example:"4194304"`
	Online    bool  `json:"online" example:"true"`
}

// amneziawgOnlineWindow mirrors the standard WireGuard convention (and this
// fork's own prior kernel-module behavior): a handshake this recent counts
// as online.
const amneziawgOnlineWindow = 180 * time.Second

// AmneziaWGLogs is what the overview's AmneziaWG log view renders: the live
// per-peer activity of every running embedded interface, plus the panel's
// own recent AmneziaWG lifecycle log lines that explain a peer being absent
// from Peers at all.
type AmneziaWGLogs struct {
	Peers   []PeerActivity `json:"peers"`
	Events  []string       `json:"events" example:"[\"2025/01/01 12:00:00 amneziawg: started interface awg1 for inbound 1\"]"`
	Running bool           `json:"running" example:"true"`
}

// amneziawgEventMarker selects the panel's own AmneziaWG log lines: every
// logger call in internal/amneziawg, internal/amneziawgnet and their jobs
// prefixes its message with it.
const amneziawgEventMarker = "amneziawg"

// amneziawgLogActivity gathers live PeerActivity rows across every enabled,
// non-node-hosted AmneziaWG inbound, newest handshake first. An inbound
// amneziawgnet has no running Device for yet (not reconciled, disabled,
// errored) contributes no rows -- not reported as an error, since the
// caller (GetAmneziaWGLogs) already has a device-agnostic Running flag from
// amneziawgnet.GetManager().HasRunning() for that.
// clampUint64ToInt64 saturates at math.MaxInt64 instead of wrapping negative,
// for a live uint64 byte counter (amneziawgnet's own UAPI-dump snapshot, not
// a DB-accumulated total) going into an int64 API field -- unreachable in
// practice at real traffic volumes, but a silent negative value would be
// worse than a saturated one if it were ever hit.
func clampUint64ToInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

func amneziawgLogActivity() []PeerActivity {
	var inbounds []*model.Inbound
	if err := database.GetDB().
		Where("protocol = ? AND enable = ? AND node_id IS NULL", model.AmneziaWG, true).
		Find(&inbounds).Error; err != nil {
		logger.Warning("amneziawg logs: list inbounds failed:", err)
		return nil
	}

	now := time.Now()
	var out []PeerActivity
	for _, inbound := range inbounds {
		inst, ok := amneziawg.InstanceFromInbound(inbound)
		if !ok {
			continue
		}
		diag := amneziawgnet.Diagnose(inbound.Id, inst.Peers)
		if !diag.Running {
			continue
		}
		for _, cd := range diag.Clients {
			var handshakeMs int64
			online := false
			if !cd.LastHandshake.IsZero() {
				handshakeMs = cd.LastHandshake.UnixMilli()
				online = now.Sub(cd.LastHandshake) < amneziawgOnlineWindow
			}
			out = append(out, PeerActivity{
				Interface:  inst.InterfaceName,
				Tag:        inbound.Tag,
				InboundId:  inbound.Id,
				Email:      cd.Email,
				Endpoint:   cd.Endpoint,
				AllowedIPs: cd.AllowedIPs,
				Handshake:  handshakeMs,
				Up:         clampUint64ToInt64(cd.RxBytes),
				Down:       clampUint64ToInt64(cd.TxBytes),
				Online:     online,
			})
		}
	}
	slices.SortFunc(out, func(a, b PeerActivity) int {
		if a.Handshake != b.Handshake {
			return cmp.Compare(b.Handshake, a.Handshake)
		}
		return strings.Compare(a.Email, b.Email)
	})
	return out
}

// GetAmneziaWGLogs returns at most count peer rows and count event lines,
// optionally narrowed to rows whose text contains filter (case-insensitive),
// mirroring GetXrayLogs' own count+filter contract.
func (s *ServerService) GetAmneziaWGLogs(count string, filter string) *AmneziaWGLogs {
	limit, err := strconv.Atoi(count)
	if err != nil || limit < 1 || limit > 10000 {
		limit = 100
	}
	needle := strings.ToLower(strings.TrimSpace(filter))

	logs := &AmneziaWGLogs{Peers: []PeerActivity{}, Events: []string{}, Running: amneziawgnet.GetManager().HasRunning()}

	for _, peer := range amneziawgLogActivity() {
		if len(logs.Peers) >= limit {
			break
		}
		if needle != "" && !strings.Contains(strings.ToLower(peer.Email+" "+peer.Tag+" "+peer.Interface+" "+peer.Endpoint+" "+peer.AllowedIPs), needle) {
			continue
		}
		logs.Peers = append(logs.Peers, peer)
	}

	for _, line := range logger.GetLogs(10000, "debug") {
		if len(logs.Events) >= limit {
			break
		}
		if !strings.Contains(strings.ToLower(line), amneziawgEventMarker) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(line), needle) {
			continue
		}
		logs.Events = append(logs.Events, line)
	}
	return logs
}

func (s *ServerService) GetXrayLogs(
	count string,
	filter string,
	showDirect string,
	showBlocked string,
	showProxy string,
	freedoms []string,
	blackholes []string,
) []LogEntry {
	const (
		Direct = iota
		Blocked
		Proxied
	)

	countInt, _ := strconv.Atoi(count)
	var entries []LogEntry

	pathToAccessLog, err := xray.GetAccessLogPath()
	if err != nil {
		return nil
	}

	file, err := os.Open(pathToAccessLog)
	if err != nil {
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.Contains(line, "api -> api") {
			// skipping empty lines and api calls
			continue
		}

		if filter != "" && !strings.Contains(line, filter) {
			// applying filter if it's not empty
			continue
		}

		entry := parseAccessLogFields(line)

		if logEntryContains(line, freedoms) {
			if showDirect == "false" {
				continue
			}
			entry.Event = Direct
		} else if logEntryContains(line, blackholes) {
			if showBlocked == "false" {
				continue
			}
			entry.Event = Blocked
		} else {
			if showProxy == "false" {
				continue
			}
			entry.Event = Proxied
		}

		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil
	}

	if len(entries) > countInt {
		entries = entries[len(entries)-countInt:]
	}

	return entries
}

func markMetricUnavailable(status *Status, metric string) {
	if status == nil || slices.Contains(status.UnavailableMetrics, metric) {
		return
	}
	status.UnavailableMetrics = append(status.UnavailableMetrics, metric)
}

func hasHostBridgeInterface(ioStats []net.IOCountersStat) bool {
	for _, iface := range ioStats {
		name := strings.ToLower(iface.Name)
		if strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "virbr") {
			return true
		}
	}
	return false
}

func collectNetIO(ioStats []net.IOCountersStat, err error, status *Status, lastStatus *Status, now time.Time) {
	if err != nil {
		logger.Warning("get io counters failed:", err)
		markMetricUnavailable(status, "netIO")
		return
	}
	allowVeth := status.ResourceScope == "container" && !hasHostBridgeInterface(ioStats)
	var usable int
	var totalSent, totalRecv, totalPktSent, totalPktRecv uint64
	for _, iface := range ioStats {
		name := strings.ToLower(iface.Name)
		if isVirtualInterfaceInScope(name, allowVeth) {
			continue
		}
		usable++
		totalSent += iface.BytesSent
		totalRecv += iface.BytesRecv
		totalPktSent += iface.PacketsSent
		totalPktRecv += iface.PacketsRecv
	}
	if usable == 0 {
		// An explicit name is considered only after the default rules find no NIC.
		// It selects one exact counter in this process's network namespace.
		selected, ok := selectExplicitContainerInterface(ioStats, status)
		if !ok {
			markMetricUnavailable(status, "netIO")
			return
		}
		totalSent = selected.BytesSent
		totalRecv = selected.BytesRecv
		totalPktSent = selected.PacketsSent
		totalPktRecv = selected.PacketsRecv
	}
	status.NetTraffic.Sent = totalSent
	status.NetTraffic.Recv = totalRecv
	status.NetTraffic.PktSent = totalPktSent
	status.NetTraffic.PktRecv = totalPktRecv

	if lastStatus != nil && lastStatus.MetricAvailable("netIO") {
		seconds := now.Sub(lastStatus.T).Seconds()
		if seconds > 0 && status.NetTraffic.Sent >= lastStatus.NetTraffic.Sent {
			status.NetIO.Up = uint64(float64(status.NetTraffic.Sent-lastStatus.NetTraffic.Sent) / seconds)
		}
		if seconds > 0 && status.NetTraffic.Recv >= lastStatus.NetTraffic.Recv {
			status.NetIO.Down = uint64(float64(status.NetTraffic.Recv-lastStatus.NetTraffic.Recv) / seconds)
		}
		if seconds > 0 && status.NetTraffic.PktSent >= lastStatus.NetTraffic.PktSent {
			status.NetIO.PktUp = uint64(float64(status.NetTraffic.PktSent-lastStatus.NetTraffic.PktSent) / seconds)
		}
		if seconds > 0 && status.NetTraffic.PktRecv >= lastStatus.NetTraffic.PktRecv {
			status.NetIO.PktDown = uint64(float64(status.NetTraffic.PktRecv-lastStatus.NetTraffic.PktRecv) / seconds)
		}
	}
}

// selectExplicitContainerInterface resolves one exact counter in the local netns.
// Proven container scope keeps its existing automatic veth behavior.
func selectExplicitContainerInterface(ioStats []net.IOCountersStat, status *Status) (net.IOCountersStat, bool) {
	if status == nil || status.ResourceScope == "container" {
		return net.IOCountersStat{}, false
	}
	name := config.GetMikroziaContainerInterface()
	if name == "" {
		return net.IOCountersStat{}, false
	}
	for _, iface := range ioStats {
		if iface.Name == name {
			return iface, true
		}
	}
	return net.IOCountersStat{}, false
}

// isVirtualInterfaceInScope returns true for loopback and virtual/tunnel interfaces
// that should be excluded from network traffic statistics.
func isVirtualInterfaceInScope(name string, allowVeth bool) bool {
	// Exact matches
	if name == "lo" || name == "lo0" {
		return true
	}
	if allowVeth && strings.HasPrefix(name, "veth") {
		return false
	}
	// Prefix matches for virtual/tunnel interfaces
	virtualPrefixes := []string{
		"loopback",
		"docker",
		"br-",
		"veth",
		"virbr",
		"tun",
		"tap",
		"wg",
		"tailscale",
		"zt",
	}
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func logEntryContains(line string, suffixes []string) bool {
	for _, sfx := range suffixes {
		if strings.Contains(line, sfx+"]") {
			return true
		}
	}
	return false
}

func (s *ServerService) GetConfigJson() (any, error) {
	config, err := s.xrayService.GetXrayConfig()
	if err != nil {
		return nil, err
	}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}

	var jsonData any
	err = json.Unmarshal(contents, &jsonData)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}

// DatabaseArtifact represents a prepared, file-backed database backup or
// migration export ready for streaming. Callers must call Cleanup when finished.
type DatabaseArtifact struct {
	Path     string
	Filename string
	Size     int64
	Backend  string
	cleanup  func()
	once     sync.Once
}

// NewDatabaseArtifact constructs a DatabaseArtifact with an idempotent cleanup hook.
func NewDatabaseArtifact(path, filename, backend string, size int64, cleanup func()) *DatabaseArtifact {
	return &DatabaseArtifact{
		Path:     path,
		Filename: filename,
		Size:     size,
		Backend:  backend,
		cleanup:  cleanup,
	}
}

// Open opens the prepared database artifact file for reading.
func (a *DatabaseArtifact) Open() (*os.File, error) {
	if a == nil || a.Path == "" {
		return nil, errors.New("database artifact is empty")
	}
	return os.Open(a.Path)
}

// Cleanup removes the temporary artifact directory and file. It is safe
// to call multiple times or on a nil artifact.
func (a *DatabaseArtifact) Cleanup() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		if a.cleanup != nil {
			a.cleanup()
		}
	})
}

var artifactGenerationSem = make(chan struct{}, 1)

func acquireArtifactGenerationLock(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("database export request canceled while another backup or migration is running: %w", err)
	}
	select {
	case artifactGenerationSem <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-artifactGenerationSem
			})
		}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("database export request canceled while another backup or migration is running: %w", ctx.Err())
	}
}

// PrepareBackup creates a consistent, file-backed database backup in a private
// temporary directory under the panel database folder so callers can stream it
// without holding the full backup in RAM.
func (s *ServerService) PrepareBackup(ctx context.Context, requestHost string) (*DatabaseArtifact, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := acquireArtifactGenerationLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	filename := s.BackupFilename(requestHost)
	backupDir, err := os.MkdirTemp(config.GetDBFolderPath(), ".mikrozia-backup-")
	if err != nil {
		return nil, err
	}
	if chmodErr := os.Chmod(backupDir, 0o700); chmodErr != nil {
		_ = os.RemoveAll(backupDir)
		return nil, chmodErr
	}
	cleanup := func() { _ = os.RemoveAll(backupDir) }

	if database.IsPostgres() {
		backupPath := filepath.Join(backupDir, "backup.dump")
		size, err := s.exportPostgresDBFile(ctx, backupPath)
		if err != nil {
			cleanup()
			return nil, err
		}
		return &DatabaseArtifact{
			Path:     backupPath,
			Filename: filename,
			Size:     size,
			Backend:  "postgres",
			cleanup:  cleanup,
		}, nil
	}

	backupPath := filepath.Join(backupDir, "backup.db")
	if err := database.BackupSQLiteContext(ctx, backupPath); err != nil {
		cleanup()
		return nil, err
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		cleanup()
		return nil, err
	}
	return &DatabaseArtifact{
		Path:     backupPath,
		Filename: filename,
		Size:     info.Size(),
		Backend:  "sqlite",
		cleanup:  cleanup,
	}, nil
}

// BackupFilename returns the filename for a database backup, named after the
// panel's address so a downloaded or Telegram-sent backup identifies the server
// it came from, followed by the current date and time (_YYYY-MM-DD_HHMMSS) so
// files accumulated in Telegram chat history group by server then sort
// chronologically and same-day backups stay distinct. requestHost is the
// browser's address: the getDb handler passes c.Request.Host so a panel download
// is named after whatever address the user reached the panel with, no Listen
// Domain needed. The Telegram bot has no request and passes "", falling back to
// the configured Listen Domain (webDomain) and then the public IP. The extension
// is .dump on PostgreSQL and .db on SQLite; the base falls back to "mikrozia" when
// no address is known.
func (s *ServerService) BackupFilename(requestHost string) string {
	ext := ".db"
	if database.IsPostgres() {
		ext = ".dump"
	}
	return s.backupHost(requestHost) + backupDateSuffix(time.Now()) + ext
}

// backupDateSuffix returns the _YYYY-MM-DD_HHMMSS chronological suffix appended
// after the host in backup filenames. Uses server-local time for consistency
// with the timestamp printed in the Telegram backup message body.
func backupDateSuffix(now time.Time) string {
	return "_" + now.Format("2006-01-02_150405")
}

// backupHost picks the address used to name backup files: the browser's request
// host (port stripped) when available, otherwise the configured Listen Domain
// (webDomain) and then the resolved public IP (IPv4 before IPv6), reduced to safe
// filename characters. The public IP is resolved directly rather than read from
// LastStatus so callers whose ServerService never runs the status ticker —
// notably the Telegram bot — still get a real address instead of the "mikrozia"
// fallback.
func (s *ServerService) backupHost(requestHost string) string {
	host := extractHostname(strings.TrimSpace(requestHost))
	if host == "" {
		if domain, err := s.settingService.GetWebDomain(); err == nil {
			host = strings.TrimSpace(domain)
		}
	}
	if host == "" {
		s.resolvePublicIPs()
		ipv4, ipv6 := s.publicIPs()
		if ipv4 != "" && ipv4 != "N/A" {
			host = ipv4
		} else if ipv6 != "" && ipv6 != "N/A" {
			host = ipv6
		}
	}
	return sanitizeBackupHost(host)
}

// sanitizeBackupHost reduces a host to characters safe in a download filename
// (the getDb handler enforces ^[a-zA-Z0-9_\-.]+$). IPv6 brackets are stripped
// and any other character — such as the colons in an IPv6 address — becomes a
// hyphen. Returns "mikrozia" when nothing usable remains.
func sanitizeBackupHost(host string) string {
	host = strings.Trim(host, "[]")
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if out == "" {
		return "mikrozia"
	}
	return out
}

// PrepareMigration produces a file-backed cross-engine migration artifact in a
// private temporary directory under config.GetDBFolderPath(): on a SQLite panel
// it streams a portable .dump (SQL text), and on a PostgreSQL panel it exports
// a self-contained .db SQLite database. Callers must call Cleanup on the
// returned artifact.
func (s *ServerService) PrepareMigration(ctx context.Context) (*DatabaseArtifact, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := acquireArtifactGenerationLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	migDir, err := os.MkdirTemp(config.GetDBFolderPath(), ".mikrozia-migration-")
	if err != nil {
		return nil, err
	}
	if chmodErr := os.Chmod(migDir, 0o700); chmodErr != nil {
		_ = os.RemoveAll(migDir)
		return nil, chmodErr
	}
	cleanup := func() { _ = os.RemoveAll(migDir) }

	if database.IsPostgres() {
		outPath := filepath.Join(migDir, "migration.db")
		if err := database.ExportPostgresToSQLiteContext(ctx, config.GetDBDSN(), outPath); err != nil {
			cleanup()
			return nil, err
		}
		info, err := os.Stat(outPath)
		if err != nil {
			cleanup()
			return nil, err
		}
		if info.Size() == 0 {
			cleanup()
			return nil, common.NewError("exported migration file is empty")
		}
		return NewDatabaseArtifact(outPath, "x-ui.db", "postgres", info.Size(), cleanup), nil
	}

	snapPath := filepath.Join(migDir, "snapshot.db")
	if err := database.BackupSQLiteContext(ctx, snapPath); err != nil {
		cleanup()
		return nil, err
	}
	dumpPath := filepath.Join(migDir, "migration.dump")
	err = database.DumpSQLiteContext(ctx, snapPath, dumpPath)
	_ = os.Remove(snapPath)
	if err != nil {
		cleanup()
		return nil, err
	}
	info, err := os.Stat(dumpPath)
	if err != nil {
		cleanup()
		return nil, err
	}
	if info.Size() == 0 {
		cleanup()
		return nil, common.NewError("exported migration dump is empty")
	}
	return NewDatabaseArtifact(dumpPath, "x-ui.dump", "sqlite", info.Size(), cleanup), nil
}

// hostBoundSettingKeys are the settings that describe *this* machine rather
// than the configuration being carried: where the panel and the subscription
// service listen, the certificates they present, and the identity this panel
// uses towards its nodes. An import that overwrites them leaves the
// destination unreachable on its own address, or impersonating the source.
var hostBoundSettingKeys = []string{
	"webListen", "webDomain", "webPort", "webCertFile", "webKeyFile", "webBasePath",
	"subListen", "subDomain", "subPort", "subCertFile", "subKeyFile", "subURI", "subJsonURI",
	"secret", "panelGuid",
	"nodeMtlsCaCertPem", "nodeMtlsCaKeyPem", "nodeMtlsClientCertPem",
	"nodeMtlsClientKeyPem", "nodeMtlsClientCertSha256", "nodeMtlsClientCAPem",
}

// hostBoundSnapshot records this machine's values, and just as importantly
// which keys it had no row for: an absent row means the built-in default is in
// force, and leaving the imported row in place would silently adopt the source
// machine's certificate path or listen address.
type hostBoundSnapshot struct {
	values  map[string]string
	present map[string]struct{}
	taken   bool
}

var sqliteCaptureHostSettingHook func() error

func captureHostBoundSettingsStrict() (hostBoundSnapshot, error) {
	if sqliteCaptureHostSettingHook != nil {
		if err := sqliteCaptureHostSettingHook(); err != nil {
			return hostBoundSnapshot{}, err
		}
	}
	db := database.GetDB()
	if db == nil {
		return hostBoundSnapshot{}, errors.New("database is not initialized")
	}
	var rows []model.Setting
	if err := db.Model(&model.Setting{}).Where("key IN ?", hostBoundSettingKeys).Find(&rows).Error; err != nil {
		return hostBoundSnapshot{}, fmt.Errorf("could not read host-bound settings: %w", err)
	}
	snap := hostBoundSnapshot{
		values:  make(map[string]string, len(rows)),
		present: make(map[string]struct{}, len(rows)),
		taken:   true,
	}
	for _, row := range rows {
		snap.values[row.Key] = row.Value
		snap.present[row.Key] = struct{}{}
	}
	return snap, nil
}

func captureHostBoundSettings() hostBoundSnapshot {
	snap, err := captureHostBoundSettingsStrict()
	if err != nil {
		logger.Warningf("Import: could not read this machine's settings, they will come from the uploaded file: %v", err)
		return hostBoundSnapshot{}
	}
	return snap
}

var sqliteRestoreHostSettingHook func(key string) error

func restoreHostBoundSettings(snap hostBoundSnapshot) {
	if err := restoreHostBoundSettingsStrict(snap); err != nil {
		logger.Warningf("Import: %v", err)
	}
}

func restoreHostBoundSettingsStrict(snap hostBoundSnapshot) error {
	if !snap.taken {
		return nil
	}
	db := database.GetDB()
	if db == nil {
		return errors.New("database is not initialized")
	}
	settingSvc := &SettingService{}
	for _, key := range hostBoundSettingKeys {
		if sqliteRestoreHostSettingHook != nil {
			if err := sqliteRestoreHostSettingHook(key); err != nil {
				return fmt.Errorf("could not restore setting %q: %w", key, err)
			}
		}
		if _, had := snap.present[key]; !had {
			// Absent because it is minted on demand, not because a default applied:
			// the imported copy is the only one that exists, so keep it (#6227).
			if lazilyMintedSettingKeys[key] {
				continue
			}
			if err := db.Where("key = ?", key).Delete(&model.Setting{}).Error; err != nil {
				return fmt.Errorf("could not drop imported setting %q: %w", key, err)
			}
			continue
		}
		// saveSetting rather than Assign(struct): GORM drops zero-valued fields from
		// the assignment map, so an empty local value never overwrote the import.
		if err := settingSvc.saveSetting(key, snap.values[key]); err != nil {
			return fmt.Errorf("could not restore setting %q for this machine: %w", key, err)
		}
	}
	return nil
}

// Minted on demand, so a fresh install has no row: dropping the imported copy
// would destroy the only one that exists, CA private key included.
var lazilyMintedSettingKeys = map[string]bool{
	"nodeMtlsCaCertPem":     true,
	"nodeMtlsCaKeyPem":      true,
	"nodeMtlsClientCertPem": true,
	"nodeMtlsClientKeyPem":  true,
	"nodeMtlsClientCAPem":   true,
}

func (s *ServerService) ImportDB(stage *ImportUploadStage, keepHostSettings bool) error {
	if stage == nil || stage.Path == "" {
		return common.NewError("staged import file is missing")
	}
	if database.IsPostgres() {
		return s.importPostgresDB(stage, keepHostSettings)
	}
	switch stage.Kind {
	case importKindSQLiteDB, importKindSQLiteDump:
	case importKindPgDump:
		return common.NewError("This file is a PostgreSQL backup; it can only be restored on a panel running PostgreSQL")
	default:
		return common.NewError("Invalid file: expected a SQLite database (.db) from Back Up or a SQLite migration dump (.dump)")
	}

	return s.importSQLiteDB(stage, keepHostSettings, s.StopXrayService, s.RestartXrayService)
}

var (
	sqliteCloseLiveDBHook = func(tx *database.SQLiteRestoreTransaction) error { return tx.CloseLiveDB() }
	sqliteInitLiveDBHook  = func(tx *database.SQLiteRestoreTransaction) error { return tx.InitLiveDB() }
	sqliteCommitTxHook    = func(tx *database.SQLiteRestoreTransaction) (bool, error) { return tx.Commit() }
)

func (s *ServerService) importSQLiteDB(stage *ImportUploadStage, keepHostSettings bool, stopXray, restartXray func() error) (retErr error) {
	tx, err := database.BeginSQLiteRestore(config.GetDBPath())
	if err != nil {
		return err
	}
	applied := false
	finished := false
	committedDurable := false
	defer func() {
		if !finished && !applied {
			_ = tx.AbortBeforeApply()
		}
	}()

	if err := tx.PrepareCandidate(stage.Path, stage.Kind == importKindSQLiteDump); err != nil {
		return err
	}

	xrayStopped := false
	defer func() {
		if xrayStopped {
			if errR := restartXray(); errR != nil {
				logger.Warningf("Failed to restart Xray after DB import error: %v", errR)
			}
		}
	}()
	// Bounded panic boundary: runs before outer defers (LIFO) to avoid double Xray restarts.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		logger.Errorf("Import panic (applied=%v, durable=%v, type=%T)\n%s", applied, committedDurable, r, debug.Stack())
		if committedDurable {
			retErr = common.NewErrorf("Imported DB and restarted Xray, but post-commit cleanup encountered an internal error")
			return
		}
		if applied {
			xrayStopped = false
			finished = true
			retErr = tx.Rollback(common.NewErrorf("Unexpected internal error while applying imported DB"), restartXray)
			return
		}
		retErr = common.NewErrorf("Unexpected internal error before applying imported DB")
	}()
	// An already-stopped Xray satisfies the "not running during replacement"
	// invariant; failing here would block restore precisely when the core is down.
	if errStop := stopXray(); errStop != nil && !errors.Is(errStop, ErrXrayNotRunning) {
		return common.NewErrorf("Failed to stop Xray before DB import: %v", errStop)
	}
	xrayStopped = true

	var keptSettings hostBoundSnapshot
	if keepHostSettings {
		keptSettings, err = captureHostBoundSettingsStrict()
		if err != nil {
			return common.NewErrorf("Failed to capture host-bound settings before DB import: %v", err)
		}
	}

	if err := tx.CreateFallbackSnapshot(); err != nil {
		return common.NewErrorf("Failed to create fallback snapshot before DB import: %v", err)
	}
	if err := tx.WritePreparedState(); err != nil {
		return common.NewErrorf("Failed to persist restore state before DB import: %v", err)
	}

	if errClose := sqliteCloseLiveDBHook(tx); errClose != nil {
		return common.NewErrorf("Failed to close existing DB before replacement: %v", errClose)
	}

	applied = true
	if err := tx.Apply(); err != nil {
		xrayStopped = false
		return tx.Rollback(common.NewErrorf("Error applying database replacement: %v", err), restartXray)
	}

	if err := sqliteInitLiveDBHook(tx); err != nil {
		xrayStopped = false
		return tx.Rollback(common.NewErrorf("Error migrating db: %v", err), restartXray)
	}
	tx.MarkPostInitFailpoint()

	if keepHostSettings {
		if err := restoreHostBoundSettingsStrict(keptSettings); err != nil {
			xrayStopped = false
			return tx.Rollback(common.NewErrorf("Failed restoring host-bound settings: %v", err), restartXray)
		}
	}
	if err := s.inboundService.MigrateDBStrict(); err != nil {
		xrayStopped = false
		return tx.Rollback(common.NewErrorf("Error running post-import DB migrations: %v", err), restartXray)
	}

	if err := tx.ValidateLiveDB(); err != nil {
		xrayStopped = false
		return tx.Rollback(common.NewErrorf("Imported DB failed validation: %v", err), restartXray)
	}
	if err := tx.SyncLiveDB(); err != nil {
		xrayStopped = false
		return tx.Rollback(common.NewErrorf("Imported DB failed sync: %v", err), restartXray)
	}

	xrayStopped = false
	if err := restartXray(); err != nil {
		return tx.Rollback(common.NewErrorf("Imported DB but failed to start Xray: %v", err), restartXray)
	}

	finished = true
	durable, err := sqliteCommitTxHook(tx)
	committedDurable = durable
	if err != nil {
		if !durable {
			return tx.Rollback(common.NewErrorf("Failed writing committed restore state: %v", err), restartXray)
		}
		return common.NewErrorf("Imported DB and restarted Xray, but post-commit cleanup failed: %v", err)
	}
	return nil
}

const (
	pgRestoreProbeTimeout   = 15 * time.Second
	pgRestoreVersionTimeout = 15 * time.Second
	pgDumpTimeout           = 10 * time.Minute
	pgRestoreTimeout        = 15 * time.Minute
)

const maxPgToolStderrBytes = 64 * 1024

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.limit <= 0 {
		l.truncated = true
		return len(p), nil
	}
	remaining := l.limit - l.buf.Len()
	if remaining <= 0 {
		l.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = l.buf.Write(p[:remaining])
		l.truncated = true
		return len(p), nil
	}
	_, _ = l.buf.Write(p)
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	s := strings.TrimSpace(l.buf.String())
	if l.truncated && s != "" {
		return s + " ... [stderr truncated]"
	}
	return s
}

func runBoundedCommand(ctx context.Context, bin string, args, env []string) ([]byte, string, bool, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	if env != nil {
		cmd.Env = env
	}
	configurePgToolProcessGroup(cmd)
	cmd.WaitDelay = 3 * time.Second

	var stdout bytes.Buffer
	stderr := &limitedBuffer{limit: maxPgToolStderrBytes}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, stderr.String(), true, ctx.Err()
	}
	return stdout.Bytes(), stderr.String(), false, err
}

func runBoundedCommandToFile(ctx context.Context, bin string, args, env []string, stdoutFile *os.File) (string, bool, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	if env != nil {
		cmd.Env = env
	}
	configurePgToolProcessGroup(cmd)
	cmd.WaitDelay = 3 * time.Second

	stderr := &limitedBuffer{limit: maxPgToolStderrBytes}
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderr

	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return stderr.String(), true, ctx.Err()
	}
	return stderr.String(), false, err
}

var pgResolveCanonicalSchemaHook = func(ctx context.Context) (string, error) {
	return database.ResolveCanonicalPostgresSchemaFromGorm(ctx, database.GetDB())
}

func (s *ServerService) exportPostgresDBFile(ctx context.Context, destPath string) (int64, error) {
	bin, err := exec.LookPath("pg_dump")
	if err != nil {
		return 0, common.NewError("pg_dump was not found in this container; official Mikrozia container images bundle PostgreSQL client tools at build time, so this indicates an incomplete, unofficial, or incompatible image — replace or upgrade the container using an official Mikrozia image (your persistent /etc/x-ui and /root/cert mounts remain intact)")
	}
	dsn := config.GetDBDSN()
	toolEnv, err := database.PreparePostgresToolEnv(dsn)
	if err != nil {
		return 0, common.NewErrorf("invalid PostgreSQL DSN: %v", database.RedactPostgresError(err, dsn))
	}
	defer func() { _ = toolEnv.Cleanup() }()

	if ctx == nil {
		ctx = context.Background()
	}
	resolveCtx, resolveCancel := context.WithTimeout(ctx, pgRestoreProbeTimeout)
	canonicalSchema, err := pgResolveCanonicalSchemaHook(resolveCtx)
	resolveCancel()
	if err != nil {
		return 0, common.NewErrorf("failed to resolve PostgreSQL schema for backup: %v", database.RedactPostgresError(err, dsn))
	}
	dumpArgs, err := database.PgDumpArgs(toolEnv.Database, canonicalSchema)
	if err != nil {
		return 0, common.NewErrorf("failed to build pg_dump arguments: %v", database.RedactPostgresError(err, dsn))
	}

	runCtx, cancel := context.WithTimeout(ctx, pgDumpTimeout)
	defer cancel()

	destFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	cleanupFile := true
	defer func() {
		if cleanupFile {
			_ = destFile.Close()
			_ = os.Remove(destPath)
		}
	}()

	stderr, timedOut, err := runBoundedCommandToFile(
		runCtx,
		bin,
		dumpArgs,
		toolEnv.Env,
		destFile,
	)
	if timedOut {
		return 0, common.NewErrorf("pg_dump timed out after %s: %s", pgDumpTimeout, database.RedactPostgresString(stderr, dsn))
	}
	if err != nil {
		return 0, common.NewErrorf("pg_dump failed: %v: %s", database.RedactPostgresError(err, dsn), database.RedactPostgresString(stderr, dsn))
	}
	if err := destFile.Sync(); err != nil {
		return 0, err
	}
	if err := destFile.Close(); err != nil {
		return 0, err
	}
	cleanupFile = false

	info, err := os.Stat(destPath)
	if err != nil {
		_ = os.Remove(destPath)
		return 0, err
	}
	if info.Size() == 0 {
		_ = os.Remove(destPath)
		return 0, common.NewError("pg_dump produced an empty backup file")
	}
	return info.Size(), nil
}

var (
	pgUnsupportedDumpVersionPattern = regexp.MustCompile(`unsupported version \((\d+\.\d+)\) in file header`)
	pgToolVersionPattern            = regexp.MustCompile(`\d+(?:\.\d+)+`)
)

var pgArchiveVersionIntroducedIn = map[string]int{
	"1.15": 16,
	"1.16": 17,
}

// checkPgRestoreCanRead probes the dump with pg_restore --list (reads only the
// TOC, no database connection) so an unreadable file fails before Xray is stopped.
func checkPgRestoreCanRead(bin, dumpPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pgRestoreProbeTimeout)
	defer cancel()

	_, stderr, timedOut, err := runBoundedCommand(ctx, bin, []string{"--list", dumpPath}, nil)
	if err == nil && !timedOut {
		return nil
	}
	if timedOut {
		return common.NewErrorf("pg_restore --list timed out after %s: %s", pgRestoreProbeTimeout, stderr)
	}
	return pgRestoreReadFailureError(stderr, pgRestoreVersion(bin))
}

func pgRestoreReadFailureError(probeOutput, localVersion string) error {
	m := pgUnsupportedDumpVersionPattern.FindStringSubmatch(probeOutput)
	if m == nil {
		return common.NewErrorf("pg_restore cannot read this dump file: %s", probeOutput)
	}
	if localVersion == "" {
		localVersion = "unknown"
	}
	if major, known := pgArchiveVersionIntroducedIn[m[1]]; known {
		return common.NewErrorf("This backup was created by pg_dump from PostgreSQL %d or newer, but this Mikrozia image's pg_restore is version %s and cannot read it; upgrade Mikrozia to a newer official container image with PostgreSQL %d+ client tools (your persistent mounts remain intact), then retry the import", major, localVersion, major)
	}
	return common.NewErrorf("This backup was created by a newer pg_dump than this Mikrozia image's pg_restore (version %s) can read; upgrade Mikrozia to a newer official container image (your persistent mounts remain intact) and retry the import", localVersion)
}

func pgRestoreVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), pgRestoreVersionTimeout)
	defer cancel()

	out, _, timedOut, err := runBoundedCommand(ctx, bin, []string{"--version"}, nil)
	if err != nil || timedOut {
		return ""
	}
	return parsePgToolVersion(string(out))
}

func parsePgToolVersion(versionOutput string) string {
	return pgToolVersionPattern.FindString(versionOutput)
}

const (
	importKindUnknown = iota
	importKindPgDump
	importKindSQLiteDB
	importKindSQLiteDump
)

// sniffImportKind classifies an uploaded restore file by its leading bytes:
// a pg_dump custom archive, a raw SQLite database, or a SQLite SQL text dump.
func sniffImportKind(header []byte) int {
	if bytes.HasPrefix(header, []byte("PGDMP")) {
		return importKindPgDump
	}
	if bytes.HasPrefix(header, []byte("SQLite format 3\x00")) {
		return importKindSQLiteDB
	}
	text := bytes.TrimLeft(bytes.TrimPrefix(header, []byte("\xef\xbb\xbf")), " \t\r\n")
	if bytes.HasPrefix(text, []byte("PRAGMA")) || bytes.HasPrefix(text, []byte("BEGIN TRANSACTION")) {
		return importKindSQLiteDump
	}
	return importKindUnknown
}

func (s *ServerService) importPostgresDB(stage *ImportUploadStage, keepHostSettings bool) error {
	switch stage.Kind {
	case importKindPgDump:
		return s.restorePostgresDump(stage.Path, keepHostSettings)
	case importKindSQLiteDB:
		return s.migrateSQLiteIntoPostgres(stage.Path, stage.Dir, false, keepHostSettings)
	case importKindSQLiteDump:
		return s.migrateSQLiteIntoPostgres(stage.Path, stage.Dir, true, keepHostSettings)
	default:
		return common.NewError("Invalid file: expected a PostgreSQL custom-format dump (.dump) from this panel's Back Up, a SQLite database (.db), or a SQLite migration dump")
	}
}

const (
	postgresSnapshotDirPrefix = ".mikrozia-pg-restore-"
	postgresSnapshotFileName  = "pre-import.dump"
	postgresSnapshotHeadroom  = uint64(16 << 20)
)

type postgresSnapshot struct {
	Dir  string
	Path string
}

type postgresSnapshotConfig struct {
	dumpBin         string
	restoreBin      string
	dsn             string
	canonicalSchema string
	toolEnv         *database.PostgresToolEnv
}

var (
	postgresSchemaSizeHook = database.PostgresSchemaSize
	postgresDiskUsageHook  = disk.Usage
)

func postgresSnapshotRequiredBytes(schemaSize int64) uint64 {
	if schemaSize <= 0 {
		return postgresSnapshotHeadroom
	}
	size := uint64(schemaSize)
	if size > math.MaxUint64-size/4-postgresSnapshotHeadroom {
		return math.MaxUint64
	}
	return size + size/4 + postgresSnapshotHeadroom
}

func createPostgresSnapshot(ctx context.Context, cfg postgresSnapshotConfig) (snapshot *postgresSnapshot, retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.toolEnv == nil {
		return nil, errors.New("postgres tool environment is unavailable")
	}
	dbFolder := config.GetDBFolderPath()
	if err := os.MkdirAll(dbFolder, 0o700); err != nil {
		return nil, fmt.Errorf("create PostgreSQL snapshot root: %w", err)
	}

	schemaSize, err := postgresSchemaSizeHook(ctx, cfg.dsn, cfg.canonicalSchema)
	if err != nil {
		return nil, fmt.Errorf("measure PostgreSQL schema before snapshot: %w", database.RedactPostgresError(err, cfg.dsn))
	}
	usage, err := postgresDiskUsageHook(dbFolder)
	if err != nil {
		return nil, fmt.Errorf("check free space for PostgreSQL snapshot: %w", err)
	}
	required := postgresSnapshotRequiredBytes(schemaSize)
	if usage.Free < required {
		return nil, fmt.Errorf("not enough free space for a safe PostgreSQL snapshot: need at least %d bytes, have %d bytes", required, usage.Free)
	}

	opDir, err := os.MkdirTemp(dbFolder, postgresSnapshotDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL snapshot directory: %w", err)
	}
	snapshot = &postgresSnapshot{Dir: opDir, Path: filepath.Join(opDir, postgresSnapshotFileName)}
	cleanup := true
	defer func() {
		if !cleanup {
			return
		}
		if removeErr := os.RemoveAll(opDir); removeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove failed PostgreSQL snapshot directory: %w", removeErr))
		}
		if syncErr := syncPostgresSnapshotDir(dbFolder); syncErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("sync PostgreSQL snapshot root after cleanup: %w", syncErr))
		}
	}()
	if err := os.Chmod(opDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure PostgreSQL snapshot directory: %w", err)
	}
	if err := syncPostgresSnapshotDir(dbFolder); err != nil {
		return nil, fmt.Errorf("sync PostgreSQL snapshot root: %w", err)
	}

	dumpArgs, err := database.PgDumpArgs(cfg.toolEnv.Database, cfg.canonicalSchema)
	if err != nil {
		return nil, err
	}
	snapshotFile, err := os.OpenFile(snapshot.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL snapshot file: %w", err)
	}
	fileOpen := true
	defer func() {
		if fileOpen {
			_ = snapshotFile.Close()
		}
	}()

	dumpCtx, dumpCancel := context.WithTimeout(ctx, pgDumpTimeout)
	stderr, timedOut, runErr := runBoundedCommandToFile(dumpCtx, cfg.dumpBin, dumpArgs, cfg.toolEnv.Env, snapshotFile)
	dumpCancel()
	if timedOut {
		return nil, fmt.Errorf("pre-import pg_dump timed out after %s: %s", pgDumpTimeout, database.RedactPostgresString(stderr, cfg.dsn))
	}
	if runErr != nil {
		return nil, fmt.Errorf("pre-import pg_dump failed: %w: %s", database.RedactPostgresError(runErr, cfg.dsn), database.RedactPostgresString(stderr, cfg.dsn))
	}
	if err := snapshotFile.Sync(); err != nil {
		return nil, fmt.Errorf("sync PostgreSQL snapshot file: %w", err)
	}
	if err := snapshotFile.Close(); err != nil {
		fileOpen = false
		return nil, fmt.Errorf("close PostgreSQL snapshot file: %w", err)
	}
	fileOpen = false
	if err := os.Chmod(snapshot.Path, 0o600); err != nil {
		return nil, fmt.Errorf("secure PostgreSQL snapshot file: %w", err)
	}
	info, err := os.Stat(snapshot.Path)
	if err != nil {
		return nil, fmt.Errorf("stat PostgreSQL snapshot: %w", err)
	}
	if info.Size() == 0 {
		return nil, errors.New("pre-import pg_dump produced an empty snapshot")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("PostgreSQL snapshot has insecure permissions %#o", info.Mode().Perm())
	}
	if err := checkPgRestoreCanRead(cfg.restoreBin, snapshot.Path); err != nil {
		return nil, fmt.Errorf("validate PostgreSQL snapshot: %w", err)
	}
	if err := syncPostgresSnapshotDir(opDir); err != nil {
		return nil, fmt.Errorf("sync PostgreSQL snapshot directory: %w", err)
	}
	cleanup = false
	return snapshot, nil
}

func cleanupPostgresSnapshot(snapshot *postgresSnapshot) error {
	if snapshot == nil || snapshot.Dir == "" {
		return nil
	}
	root := filepath.Dir(snapshot.Dir)
	if err := os.RemoveAll(snapshot.Dir); err != nil {
		return fmt.Errorf("remove PostgreSQL snapshot directory: %w", err)
	}
	if err := syncPostgresSnapshotDir(root); err != nil {
		return fmt.Errorf("sync PostgreSQL snapshot root after removal: %w", err)
	}
	return nil
}

type postgresImportOps struct {
	stopXray         func() error
	restartXray      func() error
	closeDB          func() error
	initDB           func() error
	migrateData      func(string, string) error
	migrateDB        func() error
	captureSettings  func() (hostBoundSnapshot, error)
	restoreSettings  func(hostBoundSnapshot) error
	createSnapshot   func(context.Context, postgresSnapshotConfig) (*postgresSnapshot, error)
	rollbackSnapshot func(context.Context, postgresImportOps, postgresSnapshotConfig, *postgresSnapshot) error
	cleanupSnapshot  func(*postgresSnapshot) error
}

func (s *ServerService) defaultPostgresImportOps() postgresImportOps {
	ops := postgresImportOps{
		stopXray:    s.StopXrayService,
		restartXray: s.RestartXrayService,
		closeDB:     database.CloseDB,
		initDB:      func() error { return database.InitDB(config.GetDBPath()) },
		migrateData: database.MigrateData,
		migrateDB: func() error {
			return s.inboundService.MigrateDBStrict()
		},
		captureSettings: captureHostBoundSettingsStrict,
		restoreSettings: restoreHostBoundSettingsStrict,
		createSnapshot:  createPostgresSnapshot,
		cleanupSnapshot: cleanupPostgresSnapshot,
	}
	ops.rollbackSnapshot = rollbackPostgresSnapshot
	return ops
}

func restartXrayAfterPostgresFailure(ops postgresImportOps, primary error) error {
	if err := ops.restartXray(); err != nil {
		return errors.Join(primary, fmt.Errorf("failed to restart Xray after PostgreSQL import failure: %w", err))
	}
	return primary
}

func reopenPostgresAfterPreCommitFailure(ops postgresImportOps, primary error, dsn string, snapshot *postgresSnapshot) error {
	if err := ops.initDB(); err != nil {
		primary = errors.Join(
			primary,
			fmt.Errorf("the import did not commit, but reopening the existing PostgreSQL database failed: %w", database.RedactPostgresError(err, dsn)),
		)
	}
	primary = restartXrayAfterPostgresFailure(ops, primary)
	if snapshot != nil {
		if err := ops.cleanupSnapshot(snapshot); err != nil {
			primary = errors.Join(primary, fmt.Errorf("the unchanged database is active, but its temporary snapshot could not be removed: %w", err))
		}
	}
	return primary
}

func rollbackPostgresSnapshot(ctx context.Context, ops postgresImportOps, cfg postgresSnapshotConfig, snapshot *postgresSnapshot) error {
	if snapshot == nil || snapshot.Path == "" {
		return errors.New("PostgreSQL rollback snapshot is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ops.closeDB(); err != nil {
		return fmt.Errorf("close failed imported database: %w", database.RedactPostgresError(err, cfg.dsn))
	}
	if err := database.ClearPostgresSchemaObjects(ctx, cfg.dsn, cfg.canonicalSchema); err != nil {
		return fmt.Errorf("clear canonical schema before rollback: %w", database.RedactPostgresError(err, cfg.dsn))
	}
	restoreArgs, err := database.PgRollbackRestoreArgs(cfg.toolEnv.Database, cfg.canonicalSchema, snapshot.Path)
	if err != nil {
		return fmt.Errorf("build PostgreSQL rollback arguments: %w", err)
	}
	restoreCtx, cancel := context.WithTimeout(ctx, pgRestoreTimeout)
	defer cancel()
	_, stderr, timedOut, runErr := runBoundedCommand(restoreCtx, cfg.restoreBin, restoreArgs, cfg.toolEnv.Env)
	if timedOut {
		return fmt.Errorf("PostgreSQL rollback timed out after %s: %s", pgRestoreTimeout, database.RedactPostgresString(stderr, cfg.dsn))
	}
	if runErr != nil {
		return fmt.Errorf("restore PostgreSQL rollback snapshot: %w: %s", database.RedactPostgresError(runErr, cfg.dsn), database.RedactPostgresString(stderr, cfg.dsn))
	}
	if err := ops.initDB(); err != nil {
		return fmt.Errorf("reopen rolled-back PostgreSQL database: %w", database.RedactPostgresError(err, cfg.dsn))
	}
	if err := ops.restartXray(); err != nil {
		return fmt.Errorf("restart Xray after PostgreSQL rollback: %w", err)
	}
	if err := ops.cleanupSnapshot(snapshot); err != nil {
		return fmt.Errorf("rollback succeeded but snapshot cleanup failed: %w", err)
	}
	return nil
}

func rollbackPostgresImport(ops postgresImportOps, cfg postgresSnapshotConfig, snapshot *postgresSnapshot, primary error) error {
	if err := ops.rollbackSnapshot(context.Background(), ops, cfg, snapshot); err != nil {
		return errors.Join(
			primary,
			fmt.Errorf("automatic PostgreSQL rollback failed; recovery snapshot was retained at %s for manual recovery: %w", snapshot.Path, err),
		)
	}
	return errors.Join(primary, errors.New("the previous PostgreSQL database was restored automatically"))
}

func runPostgresImportTransaction(
	ops postgresImportOps,
	cfg postgresSnapshotConfig,
	keepHostSettings bool,
	mutation func() error,
	operationName string,
) (retErr error) {
	// Same invariant as the SQLite path: not running already means stopped.
	if err := ops.stopXray(); err != nil && !errors.Is(err, ErrXrayNotRunning) {
		retErr = fmt.Errorf("failed to stop Xray before PostgreSQL import; database was not changed: %w", err)
		if restartErr := ops.restartXray(); restartErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("failed to restore Xray after the stop failure: %w", restartErr))
		}
		return
	}

	var (
		snapshot          *postgresSnapshot
		keptSettings      hostBoundSnapshot
		dbClosed          bool
		mutationStarted   bool
		operationAccepted bool
	)
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Errorf("PostgreSQL import panic (mutation_started=%t, accepted=%t, type=%T)\n%s", mutationStarted, operationAccepted, recovered, debug.Stack())
			primary := common.NewError("Unexpected internal error while applying PostgreSQL import")
			switch {
			case operationAccepted:
				retErr = primary
			case mutationStarted && snapshot != nil:
				retErr = rollbackPostgresImport(ops, cfg, snapshot, primary)
			case dbClosed:
				retErr = reopenPostgresAfterPreCommitFailure(ops, primary, cfg.dsn, snapshot)
			default:
				retErr = restartXrayAfterPostgresFailure(ops, primary)
			}
		}
	}()

	if keepHostSettings {
		var err error
		keptSettings, err = ops.captureSettings()
		if err != nil {
			return restartXrayAfterPostgresFailure(ops, common.NewErrorf("failed to preserve this router's settings; database was not changed: %v", err))
		}
	}
	if err := ops.closeDB(); err != nil {
		primary := common.NewErrorf("failed to close the existing database before %s; database mutation was not run: %v", operationName, database.RedactPostgresError(err, cfg.dsn))
		return reopenPostgresAfterPreCommitFailure(ops, primary, cfg.dsn, nil)
	}
	dbClosed = true

	var err error
	snapshot, err = ops.createSnapshot(context.Background(), cfg)
	if err != nil {
		primary := common.NewErrorf("failed to create a verified PostgreSQL recovery snapshot before %s; database was not changed: %v", operationName, database.RedactPostgresError(err, cfg.dsn))
		return reopenPostgresAfterPreCommitFailure(ops, primary, cfg.dsn, nil)
	}

	mutationStarted = true
	if err := mutation(); err != nil {
		primary := common.NewErrorf("%s failed before commit; the PostgreSQL transaction was rolled back and the database was left unchanged: %v", operationName, database.RedactPostgresError(err, cfg.dsn))
		return reopenPostgresAfterPreCommitFailure(ops, primary, cfg.dsn, snapshot)
	}

	if err := ops.initDB(); err != nil {
		primary := common.NewErrorf("%s committed, but reopening the imported database failed: %v", operationName, database.RedactPostgresError(err, cfg.dsn))
		return rollbackPostgresImport(ops, cfg, snapshot, primary)
	}
	if keepHostSettings {
		if err := ops.restoreSettings(keptSettings); err != nil {
			primary := common.NewErrorf("%s committed, but restoring this router's settings failed: %v", operationName, err)
			return rollbackPostgresImport(ops, cfg, snapshot, primary)
		}
	}
	if err := ops.migrateDB(); err != nil {
		primary := common.NewErrorf("%s committed, but applying panel data migrations failed: %v", operationName, database.RedactPostgresError(err, cfg.dsn))
		return rollbackPostgresImport(ops, cfg, snapshot, primary)
	}
	if err := ops.restartXray(); err != nil {
		primary := common.NewErrorf("%s committed, but restarting Xray with the imported database failed: %v", operationName, err)
		return rollbackPostgresImport(ops, cfg, snapshot, primary)
	}
	operationAccepted = true
	if err := ops.cleanupSnapshot(snapshot); err != nil {
		return common.NewErrorf("%s was accepted, but its temporary recovery snapshot could not be removed: %v", operationName, err)
	}
	return nil
}

func (s *ServerService) restorePostgresDump(dumpPath string, keepHostSettings bool) error {
	return s.restorePostgresDumpWithOps(dumpPath, keepHostSettings, s.defaultPostgresImportOps())
}

func (s *ServerService) restorePostgresDumpWithOps(dumpPath string, keepHostSettings bool, ops postgresImportOps) error {
	restoreBin, err := exec.LookPath("pg_restore")
	if err != nil {
		return common.NewError("pg_restore was not found in this container; official Mikrozia container images bundle PostgreSQL client tools at build time, so this indicates an incomplete, unofficial, or incompatible image — replace or upgrade the container using an official Mikrozia image (your persistent /etc/x-ui and /root/cert mounts remain intact)")
	}
	dumpBin, err := exec.LookPath("pg_dump")
	if err != nil {
		return common.NewError("pg_dump was not found in this container; a verified pre-restore snapshot is mandatory, so replace or upgrade the container using an official Mikrozia image before importing")
	}
	dsn := config.GetDBDSN()
	toolEnv, err := database.PreparePostgresToolEnv(dsn)
	if err != nil {
		return common.NewErrorf("invalid PostgreSQL DSN: %v", database.RedactPostgresError(err, dsn))
	}
	defer func() { _ = toolEnv.Cleanup() }()

	if err := checkPgRestoreCanRead(restoreBin, dumpPath); err != nil {
		return err
	}

	resolveCtx, resolveCancel := context.WithTimeout(context.Background(), pgRestoreProbeTimeout)
	canonicalSchema, err := pgResolveCanonicalSchemaHook(resolveCtx)
	resolveCancel()
	if err != nil {
		return common.NewErrorf("failed to resolve PostgreSQL schema for restore: %v", database.RedactPostgresError(err, dsn))
	}
	restoreArgs, err := database.PgRestoreArgs(toolEnv.Database, canonicalSchema, dumpPath)
	if err != nil {
		return common.NewErrorf("failed to build pg_restore arguments: %v", database.RedactPostgresError(err, dsn))
	}
	cfg := postgresSnapshotConfig{
		dumpBin:         dumpBin,
		restoreBin:      restoreBin,
		dsn:             dsn,
		canonicalSchema: canonicalSchema,
		toolEnv:         toolEnv,
	}
	return runPostgresImportTransaction(ops, cfg, keepHostSettings, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), pgRestoreTimeout)
		defer cancel()
		_, stderr, timedOut, runErr := runBoundedCommand(ctx, restoreBin, restoreArgs, toolEnv.Env)

		if timedOut {
			return common.NewErrorf("pg_restore timed out after %s: %s", pgRestoreTimeout, database.RedactPostgresString(stderr, dsn))
		}
		if runErr != nil {
			return common.NewErrorf("pg_restore failed: %v: %s", database.RedactPostgresError(runErr, dsn), database.RedactPostgresString(stderr, dsn))
		}
		return nil
	}, "PostgreSQL restore")
}

func (s *ServerService) migrateSQLiteIntoPostgres(srcPath, stageDir string, isSQLDump, keepHostSettings bool) error {
	return s.migrateSQLiteIntoPostgresWithOps(srcPath, stageDir, isSQLDump, keepHostSettings, s.defaultPostgresImportOps())
}

func (s *ServerService) migrateSQLiteIntoPostgresWithOps(srcPath, stageDir string, isSQLDump, keepHostSettings bool, ops postgresImportOps) error {
	dbPath := srcPath
	if isSQLDump {
		if stageDir == "" {
			stageDir = filepath.Dir(srcPath)
		}
		dbPath = filepath.Join(stageDir, "restored.db")
		if err := database.RestoreSQLite(srcPath, dbPath); err != nil {
			return common.NewErrorf("Error rebuilding a SQLite database from the migration dump: %v", err)
		}
	}
	if err := database.ValidateSQLiteDB(dbPath); err != nil {
		return common.NewErrorf("Invalid or corrupt db file: %v", err)
	}
	if err := database.PrepareSQLiteForMigration(dbPath); err != nil {
		return common.NewErrorf("This file cannot be imported: %v", err)
	}

	dumpBin, err := exec.LookPath("pg_dump")
	if err != nil {
		return common.NewError("pg_dump was not found in this container; a verified pre-migration snapshot is mandatory, so replace or upgrade the container using an official Mikrozia image before importing")
	}
	restoreBin, err := exec.LookPath("pg_restore")
	if err != nil {
		return common.NewError("pg_restore was not found in this container; automatic rollback is mandatory, so replace or upgrade the container using an official Mikrozia image before importing")
	}
	dsn := config.GetDBDSN()
	toolEnv, err := database.PreparePostgresToolEnv(dsn)
	if err != nil {
		return common.NewErrorf("invalid PostgreSQL DSN: %v", database.RedactPostgresError(err, dsn))
	}
	defer func() { _ = toolEnv.Cleanup() }()
	resolveCtx, resolveCancel := context.WithTimeout(context.Background(), pgRestoreProbeTimeout)
	canonicalSchema, err := pgResolveCanonicalSchemaHook(resolveCtx)
	resolveCancel()
	if err != nil {
		return common.NewErrorf("failed to resolve PostgreSQL schema before migration: %v", database.RedactPostgresError(err, dsn))
	}
	cfg := postgresSnapshotConfig{
		dumpBin:         dumpBin,
		restoreBin:      restoreBin,
		dsn:             dsn,
		canonicalSchema: canonicalSchema,
		toolEnv:         toolEnv,
	}
	return runPostgresImportTransaction(ops, cfg, keepHostSettings, func() error {
		return ops.migrateData(dbPath, dsn)
	}, "SQLite-to-PostgreSQL import")
}

func stageSQLiteUpload(srcPath string, kind int, tempPath string) error {
	if kind == importKindSQLiteDump {
		if err := database.RestoreSQLite(srcPath, tempPath); err != nil {
			return common.NewErrorf("Error rebuilding a SQLite database from the migration dump: %v", err)
		}
		return nil
	}
	if err := os.Rename(srcPath, tempPath); err != nil {
		return common.NewErrorf("Error moving staged db: %v", err)
	}
	return nil
}

// parseXrayKeyPairOutput reads the two-line "Label: value" output that xray's
// key-generation subcommands (x25519, mldsa65, mlkem768) print and returns the
// two values. Short or label-less output yields an error instead of panicking
// on an out-of-range slice index, so a future xray version that changes the
// format degrades to a 500 with a message rather than a crash.
func parseXrayKeyPairOutput(output string) (string, string, error) {
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		return "", "", common.NewError("unexpected key generator output")
	}
	first := strings.Split(lines[0], ":")
	second := strings.Split(lines[1], ":")
	if len(first) < 2 || len(second) < 2 {
		return "", "", common.NewError("unexpected key generator output")
	}
	return strings.TrimSpace(first[1]), strings.TrimSpace(second[1]), nil
}

func (s *ServerService) GetNewX25519Cert() (any, error) {
	// Run the command
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "x25519")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	privateKey, publicKey, err := parseXrayKeyPairOutput(out.String())
	if err != nil {
		return nil, err
	}

	keyPair := map[string]any{
		"privateKey": privateKey,
		"publicKey":  publicKey,
	}

	return keyPair, nil
}

func (s *ServerService) GetNewmldsa65() (*MLDSA65Response, error) {
	// Run the command
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "mldsa65")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	seed, verify, err := parseXrayKeyPairOutput(out.String())
	if err != nil {
		return nil, err
	}

	keyPair := &MLDSA65Response{
		Seed:   seed,
		Verify: verify,
	}

	return keyPair, nil
}

// GetCertHash parses a certificate (from a file path or inline PEM/DER content)
// and returns the hex-encoded SHA-256 over each certificate's raw DER — the
// value xray-core's pinnedPeerCertSha256 (pcs) expects. Lets the panel fill the
// pinned-cert field from the inbound's own certificate without the user
// computing the hash by hand.
func (s *ServerService) GetCertHash(certFile string, certContent string) ([]string, error) {
	var certBytes []byte
	if path := strings.TrimSpace(certFile); path != "" {
		// Guard against path traversal: only hash certificate files the panel
		// already references in its own configuration (an inbound's TLS
		// certificateFile or the panel's own web cert). The path handed to
		// os.ReadFile comes from that allow-list, never directly from the
		// caller-supplied value.
		known, ok := s.resolveKnownCertFile(path)
		if !ok {
			return nil, common.NewError("certificate file is not referenced by any inbound or panel setting")
		}
		b, err := os.ReadFile(known)
		if err != nil {
			return nil, err
		}
		certBytes = b
	} else if strings.TrimSpace(certContent) != "" {
		certBytes = []byte(certContent)
	} else {
		return nil, common.NewError("no certificate provided")
	}

	var certs []*x509.Certificate
	if bytes.Contains(certBytes, []byte("BEGIN")) {
		rest := certBytes
		for {
			block, remain := pem.Decode(rest)
			if block == nil {
				break
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, common.NewError("unable to decode certificate: ", err)
			}
			certs = append(certs, cert)
			rest = remain
		}
	} else {
		parsed, err := x509.ParseCertificates(certBytes)
		if err != nil {
			return nil, common.NewError("unable to parse certificates: ", err)
		}
		certs = parsed
	}

	if len(certs) == 0 {
		return nil, common.NewError("no certificates found")
	}

	hashes := make([]string, 0, len(certs))
	for _, cert := range certs {
		sum := sha256.Sum256(cert.Raw)
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	return hashes, nil
}

// resolveKnownCertFile checks the caller-supplied certificate path against the
// set of certificate files the panel already references (inbound TLS configs
// plus the panel's own web cert) and, on a match, returns the path taken from
// that configuration — not the caller's value. This both confines reads to
// known certificates and breaks the user-input-to-filesystem taint flow.
func (s *ServerService) resolveKnownCertFile(certFile string) (string, bool) {
	want := filepath.Clean(certFile)
	for _, known := range s.knownCertFiles() {
		if filepath.Clean(known) == want {
			return known, true
		}
	}
	return "", false
}

// knownCertFiles collects every certificate file path the panel legitimately
// references: the certificateFile of each inbound's TLS settings and the
// panel's own web TLS certificate.
func (s *ServerService) knownCertFiles() []string {
	var files []string
	if cert, err := s.settingService.GetCertFile(); err == nil {
		if cert = strings.TrimSpace(cert); cert != "" {
			files = append(files, cert)
		}
	}
	if inbounds, err := s.inboundService.GetAllInbounds(); err == nil {
		for _, inbound := range inbounds {
			files = collectCertFiles(inbound.StreamSettings, files)
		}
	}
	return files
}

// collectCertFiles walks a stream-settings JSON document and appends the value
// of every "certificateFile" field it finds (TLS settings may nest them under
// several keys depending on the security type).
func collectCertFiles(streamSettings string, out []string) []string {
	streamSettings = strings.TrimSpace(streamSettings)
	if streamSettings == "" {
		return out
	}
	var parsed any
	if err := json.Unmarshal([]byte(streamSettings), &parsed); err != nil {
		return out
	}
	return walkCertFiles(parsed, out)
}

func walkCertFiles(node any, out []string) []string {
	switch v := node.(type) {
	case map[string]any:
		for key, val := range v {
			if key == "certificateFile" {
				if path, ok := val.(string); ok {
					if path = strings.TrimSpace(path); path != "" {
						out = append(out, path)
					}
				}
			}
			out = walkCertFiles(val, out)
		}
	case []any:
		for _, item := range v {
			out = walkCertFiles(item, out)
		}
	}
	return out
}

// GetRemoteCertHash opens a uTLS (Chrome fingerprint) handshake to a remote
// endpoint and returns the hex-encoded SHA-256 of its leaf certificate — the
// value to put in pinnedPeerCertSha256 (pcs) when pinning a server whose
// certificate file you don't hold (a CDN front, a REALITY dest, an external
// proxy). A native handshake replaces the old `xray tls ping` subprocess so the
// real dial/handshake failure (connection refused, timeout, …) surfaces
// verbatim. `server` may be host or host:port; the port defaults to 443.
// allowPrivate lifts the SSRF guard for this one probe (the panel's confirmed opt-in).
func (s *ServerService) GetRemoteCertHash(server string, allowPrivate bool) ([]string, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return nil, common.NewError("no server provided")
	}

	host, port := server, "443"
	if h, p, err := stdnet.SplitHostPort(server); err == nil {
		host, port = h, p
	}

	ctx, cancel := context.WithTimeout(netsafe.ContextWithAllowPrivate(context.Background(), allowPrivate), 10*time.Second)
	defer cancel()
	tcpConn, err := netsafe.SSRFGuardedDialContext(ctx, "tcp", stdnet.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", stdnet.JoinHostPort(host, port), err)
	}
	defer tcpConn.Close()
	_ = tcpConn.SetDeadline(time.Now().Add(15 * time.Second))

	tlsConn := utls.UClient(tcpConn, &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
	}, utls.HelloChrome_Auto)
	defer tlsConn.Close()
	if err := tlsConn.Handshake(); err != nil {
		return nil, common.NewErrorf("tls handshake with %s failed: %s", host, err)
	}

	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, common.NewError("no certificate returned by ", host)
	}
	// PeerCertificates[0] is always the leaf the connection verifies against —
	// robust for IP-only self-signed certs that carry no DNS SANs.
	sum := sha256.Sum256(certs[0].Raw)
	return []string{hex.EncodeToString(sum[:])}, nil
}

func (s *ServerService) GetNewEchCert(sni string) (any, error) {
	// Run the command
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "tls", "ech", "--serverName", sni)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")
	if len(lines) < 4 {
		return nil, common.NewError("invalid ech cert")
	}

	configList := lines[1]
	serverKeys := lines[3]

	return map[string]any{
		"echServerKeys": serverKeys,
		"echConfigList": configList,
	}, nil
}

func (s *ServerService) GetNewVlessEnc() (any, error) {
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "vlessenc")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	auths := parseVlessEncAuths(out.String())
	auths = append(auths, deriveVlessEncModes(auths)...)

	return map[string]any{
		"auths": auths,
	}, nil
}

func deriveVlessEncModes(auths []map[string]string) []map[string]string {
	var extra []map[string]string
	for _, a := range auths {
		for _, mode := range []string{"xorpub", "random"} {
			dec := strings.Replace(a["decryption"], ".native.", "."+mode+".", 1)
			enc := strings.Replace(a["encryption"], ".native.", "."+mode+".", 1)
			if dec == a["decryption"] && enc == a["encryption"] {
				continue
			}
			extra = append(extra, map[string]string{
				"id":         a["id"] + "_" + mode,
				"label":      a["label"] + " (" + mode + ")",
				"decryption": dec,
				"encryption": enc,
			})
		}
	}
	return extra
}

func parseVlessEncAuths(output string) []map[string]string {
	lines := strings.Split(output, "\n")
	var auths []map[string]string
	var current map[string]string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Authentication:") {
			if current != nil {
				auths = append(auths, current)
			}
			label := strings.TrimSpace(strings.TrimPrefix(line, "Authentication:"))
			current = map[string]string{
				"id":    vlessEncAuthID(label),
				"label": label,
			}
		} else if strings.HasPrefix(line, `"decryption"`) || strings.HasPrefix(line, `"encryption"`) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && current != nil {
				key := strings.Trim(parts[0], `" `)
				val := strings.TrimSpace(parts[1])
				val = strings.TrimSuffix(val, ",")
				val = strings.Trim(val, `" `)
				current[key] = val
			}
		}
	}

	if current != nil {
		auths = append(auths, current)
	}

	return auths
}

func vlessEncAuthID(label string) string {
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(label))
	switch {
	case strings.Contains(normalized, "mlkem768"):
		return "mlkem768"
	case strings.Contains(normalized, "x25519"):
		return "x25519"
	default:
		return normalized
	}
}

func (s *ServerService) GetNewUUID() (*NewUUIDResponse, error) {
	newUUID, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate UUID: %w", err)
	}

	return &NewUUIDResponse{
		UUID: newUUID.String(),
	}, nil
}

func (s *ServerService) GetNewmlkem768() (*MLKEM768Response, error) {
	// Run the command
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "mlkem768")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	seed, client, err := parseXrayKeyPairOutput(out.String())
	if err != nil {
		return nil, err
	}

	keyPair := &MLKEM768Response{
		Seed:   seed,
		Client: client,
	}

	return keyPair, nil
}
