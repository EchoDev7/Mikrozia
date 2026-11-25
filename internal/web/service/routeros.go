package service

import (
	"context"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/config"
	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/routeros"
)

const (
	routerOSProbeInterval  = 2 * time.Second
	routerOSStaleThreshold = 30 * time.Second
	routerOSHistoryMax     = 60
	routerOSHistoryWindow  = 15 * time.Minute
)

type routerOSCPUSample struct {
	at   time.Time
	load int
}

type routerOSMemSample struct {
	at      time.Time
	percent float64
}

type RouterOSAPIStatus struct {
	State           string    `json:"state" example:"not_configured"`
	CheckedAt       int64     `json:"checkedAt" example:"0"`
	Stale           bool      `json:"stale" example:"false"`
	CPULoad         *int      `json:"cpuLoad"`
	CPUCount        *int      `json:"cpuCount"`
	CPUFrequencyMhz *int      `json:"cpuFrequencyMhz"`
	CpuAvg          *float64  `json:"cpuAvg"`
	CpuPeak         *int      `json:"cpuPeak"`
	CpuHistory      []int     `json:"cpuHistory"`
	MemUsed         *uint64   `json:"memUsed"`
	MemTotal        *uint64   `json:"memTotal"`
	MemPercent      *float64  `json:"memPercent"`
	MemAvg          *float64  `json:"memAvg"`
	MemPeak         *float64  `json:"memPeak"`
	MemHistory      []float64 `json:"memHistory"`
}

type RouterOSAPIService struct {
	mu         sync.Mutex
	status     RouterOSAPIStatus
	samples    []routerOSCPUSample
	memSamples []routerOSMemSample
	lastUptime *int64
	next       time.Time
	checking   bool
	probe      func(context.Context, string, string, string) routeros.ResourceSnapshot
	now        func() time.Time
}

func (s *RouterOSAPIService) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func roundTenth(v float64) float64 {
	return math.Round(v*10) / 10
}

func (s *RouterOSAPIService) pruneAndSummarizeLocked(now time.Time) (*float64, *int, []int) {
	cutoff := now.Add(-routerOSHistoryWindow)
	kept := s.samples[:0]
	for _, sample := range s.samples {
		if !sample.at.Before(cutoff) {
			kept = append(kept, sample)
		}
	}
	s.samples = kept
	if len(s.samples) > routerOSHistoryMax {
		s.samples = append([]routerOSCPUSample(nil), s.samples[len(s.samples)-routerOSHistoryMax:]...)
	}
	if len(s.samples) == 0 {
		return nil, nil, []int{}
	}
	sum := 0
	peak := s.samples[0].load
	history := make([]int, len(s.samples))
	for i, sample := range s.samples {
		history[i] = sample.load
		sum += sample.load
		if sample.load > peak {
			peak = sample.load
		}
	}
	avg := roundTenth(float64(sum) / float64(len(s.samples)))
	return &avg, &peak, history
}

func (s *RouterOSAPIService) pruneAndSummarizeMemLocked(now time.Time) (*float64, *float64, []float64) {
	cutoff := now.Add(-routerOSHistoryWindow)
	kept := s.memSamples[:0]
	for _, sample := range s.memSamples {
		if !sample.at.Before(cutoff) {
			kept = append(kept, sample)
		}
	}
	s.memSamples = kept
	if len(s.memSamples) > routerOSHistoryMax {
		s.memSamples = append([]routerOSMemSample(nil), s.memSamples[len(s.memSamples)-routerOSHistoryMax:]...)
	}
	if len(s.memSamples) == 0 {
		return nil, nil, []float64{}
	}
	sum := 0.0
	peak := s.memSamples[0].percent
	history := make([]float64, len(s.memSamples))
	for i, sample := range s.memSamples {
		history[i] = sample.percent
		sum += sample.percent
		if sample.percent > peak {
			peak = sample.percent
		}
	}
	avg := roundTenth(sum / float64(len(s.memSamples)))
	peakRounded := roundTenth(peak)
	return &avg, &peakRounded, history
}

// resolveRouterOSAPIConfig prefers the container environment configuration
// and falls back to the unified Panel Settings RouterOS API credentials when
// environment variables are unset, so operators can manage telemetry,
// container reads, and NAT with one dedicated RouterOS API account.
func resolveRouterOSAPIConfig() config.RouterOSAPIConfig {
	cfg := config.GetRouterOSAPIConfig()
	if cfg.State != "not_configured" {
		return cfg
	}
	if database.GetDB() == nil {
		return cfg
	}
	var settingService SettingService
	allSetting, err := settingService.GetAllSetting()
	if err != nil || allSetting == nil {
		return cfg
	}
	host := strings.TrimSpace(allSetting.RouterosNatHost)
	user := strings.TrimSpace(allSetting.RouterosNatUsername)
	pass := allSetting.RouterosNatPassword
	if host == "" && user == "" && pass == "" {
		return cfg
	}
	validHost, ok := routeros.ValidatePrivateHost(host)
	if !ok || user == "" || pass == "" {
		return config.RouterOSAPIConfig{State: "invalid_configuration"}
	}
	return config.RouterOSAPIConfig{
		State:    "connecting",
		Host:     validHost,
		Username: user,
		Password: pass,
	}
}

func (s *RouterOSAPIService) effectiveRouterOSAPIConfig() config.RouterOSAPIConfig {
	return resolveRouterOSAPIConfig()
}

// Status coalesces all viewers into at most one four-second probe per fifteen seconds.
func (s *RouterOSAPIService) Status() RouterOSAPIStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.currentTime()
	cfg := s.effectiveRouterOSAPIConfig()
	if cfg.State != "connecting" {
		s.samples = nil
		s.memSamples = nil
		s.lastUptime = nil
		s.status = RouterOSAPIStatus{State: cfg.State, CpuHistory: []int{}, MemHistory: []float64{}}
		s.next = time.Time{}
		return RouterOSAPIStatus{State: cfg.State, CpuHistory: []int{}, MemHistory: []float64{}}
	}
	if s.status.State == "" || s.status.State == "not_configured" || s.status.State == "invalid_configuration" {
		s.status.State = "connecting"
	}
	if !s.checking && !now.Before(s.next) {
		s.checking = true
		probe := s.probe
		if probe == nil {
			probe = (routeros.Client{}).ResourceSnapshot
		}
		go func() {
			snap := probe(context.Background(), cfg.Host, cfg.Username, cfg.Password)
			s.mu.Lock()
			defer s.mu.Unlock()
			doneAt := s.currentTime()
			currentCfg := s.effectiveRouterOSAPIConfig()
			if currentCfg.State != "connecting" {
				s.samples = nil
				s.memSamples = nil
				s.lastUptime = nil
				s.status = RouterOSAPIStatus{State: currentCfg.State, CpuHistory: []int{}, MemHistory: []float64{}}
				s.next, s.checking = time.Time{}, false
				return
			}
			if snap.State == "connected" && snap.CPULoad != nil {
				if s.lastUptime != nil && snap.UptimeSeconds != nil && *snap.UptimeSeconds < *s.lastUptime {
					s.samples = nil
					s.memSamples = nil
				}
				if snap.UptimeSeconds != nil {
					up := *snap.UptimeSeconds
					s.lastUptime = &up
				}
				s.samples = append(s.samples, routerOSCPUSample{at: doneAt, load: *snap.CPULoad})
				avg, peak, history := s.pruneAndSummarizeLocked(doneAt)
				loadVal := *snap.CPULoad
				var cntPtr, freqPtr *int
				if snap.CPUCount != nil {
					c := *snap.CPUCount
					cntPtr = &c
				}
				if snap.CPUFrequencyMhz != nil {
					f := *snap.CPUFrequencyMhz
					freqPtr = &f
				}

				var memUsedPtr, memTotalPtr *uint64
				var memPctPtr *float64
				if snap.MemUsed != nil && snap.MemTotal != nil && *snap.MemTotal > 0 && *snap.MemUsed <= *snap.MemTotal {
					u := *snap.MemUsed
					tot := *snap.MemTotal
					pct := roundTenth(float64(u) / float64(tot) * 100)
					memUsedPtr = &u
					memTotalPtr = &tot
					memPctPtr = &pct
					s.memSamples = append(s.memSamples, routerOSMemSample{at: doneAt, percent: pct})
				}
				memAvg, memPeak, memHistory := s.pruneAndSummarizeMemLocked(doneAt)

				s.status = RouterOSAPIStatus{
					State:           "connected",
					CheckedAt:       doneAt.Unix(),
					CPULoad:         &loadVal,
					CPUCount:        cntPtr,
					CPUFrequencyMhz: freqPtr,
					CpuAvg:          avg,
					CpuPeak:         peak,
					CpuHistory:      history,
					MemUsed:         memUsedPtr,
					MemTotal:        memTotalPtr,
					MemPercent:      memPctPtr,
					MemAvg:          memAvg,
					MemPeak:         memPeak,
					MemHistory:      memHistory,
				}
			} else {
				avg, peak, history := s.pruneAndSummarizeLocked(doneAt)
				memAvg, memPeak, memHistory := s.pruneAndSummarizeMemLocked(doneAt)
				state := snap.State
				if state == "" || state == "connected" {
					state = "invalid_reply"
				}
				s.status = RouterOSAPIStatus{
					State:      state,
					CheckedAt:  doneAt.Unix(),
					CpuAvg:     avg,
					CpuPeak:    peak,
					CpuHistory: history,
					MemAvg:     memAvg,
					MemPeak:    memPeak,
					MemHistory: memHistory,
				}
			}
			s.next, s.checking = doneAt.Add(routerOSProbeInterval), false
		}()
	}
	avg, peak, history := s.pruneAndSummarizeLocked(now)
	memAvg, memPeak, memHistory := s.pruneAndSummarizeMemLocked(now)
	status := s.status
	status.CpuAvg = avg
	status.CpuPeak = peak
	status.CpuHistory = history
	status.MemAvg = memAvg
	status.MemPeak = memPeak
	status.MemHistory = memHistory
	status.Stale = status.CheckedAt != 0 && now.Sub(time.Unix(status.CheckedAt, 0)) > routerOSStaleThreshold
	return status
}
