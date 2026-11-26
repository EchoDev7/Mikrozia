package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/config"
	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/logger"
	"github.com/echodev7/mikrozia/v3/internal/routeros"
)

const (
	dockerHubTagsURL       = "https://hub.docker.com/v2/repositories/echodev7/mikrozia/tags/?page_size=25&ordering=last_updated"
	dockerHubCacheTTL      = 5 * time.Minute
	cleanupPreviousMarker  = ".mikrozia-cleanup-previous"
)

var semverTagRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)-mikrozia$`)

type DockerHubTagInfo struct {
	LatestVersion string   `json:"latestVersion"`
	LastUpdated   string   `json:"lastUpdated,omitempty"`
	Architectures []string `json:"architectures,omitempty"`
	Error         string   `json:"error,omitempty"`
}

type RouterOSUpdateInfo struct {
	CurrentVersion   string                            `json:"currentVersion"`
	LatestVersion    string                            `json:"latestVersion"`
	LatestUpdatedAt  string                            `json:"latestUpdatedAt,omitempty"`
	Architectures    []string                          `json:"architectures,omitempty"`
	UpdateAvailable  bool                              `json:"updateAvailable"`
	DockerHubError   string                            `json:"dockerHubError,omitempty"`
	RouterOSState    string                            `json:"routerosState"`
	FreeHddSpace     uint64                            `json:"freeHddSpace"`
	TotalHddSpace    uint64                            `json:"totalHddSpace"`
	CurrentContainer *routeros.ContainerUpdateSpec     `json:"currentContainer,omitempty"`
	StagedContainer  *routeros.ContainerUpdateSpec     `json:"stagedContainer,omitempty"`
	Previous         []routeros.ContainerUpdateSpec    `json:"previous,omitempty"`
	ManualStageCmd   string                            `json:"manualStageCmd,omitempty"`
	ManualCutoverCmd string                            `json:"manualCutoverCmd,omitempty"`
	ManualCleanupCmd string                            `json:"manualCleanupCmd,omitempty"`
}

type RouterOSUpdaterService struct {
	settingService SettingService
	client         routeros.Client
	httpClient     *http.Client
	tagsURL        string
}

var (
	dockerHubCacheMu    sync.Mutex
	dockerHubCacheValue *DockerHubTagInfo
	dockerHubCacheAt    time.Time
)

func parseMikroziaSemver(v string) ([3]int, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	m := semverTagRe.FindStringSubmatch(v)
	if len(m) != 4 {
		return [3]int{}, false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return [3]int{maj, min, pat}, true
}

func isNewerMikroziaVersion(candidate, current string) bool {
	cVer, okC := parseMikroziaSemver(candidate)
	if !okC {
		return false
	}
	curVer, okCur := parseMikroziaSemver(current)
	if !okCur {
		return true
	}
	for i := 0; i < 3; i++ {
		if cVer[i] != curVer[i] {
			return cVer[i] > curVer[i]
		}
	}
	return false
}

func (s *RouterOSUpdaterService) fetchDockerHubLatest(ctx context.Context, force bool) DockerHubTagInfo {
	if !force {
		dockerHubCacheMu.Lock()
		if dockerHubCacheValue != nil && time.Since(dockerHubCacheAt) < dockerHubCacheTTL {
			cp := *dockerHubCacheValue
			dockerHubCacheMu.Unlock()
			return cp
		}
		dockerHubCacheMu.Unlock()
	}

	url := s.tagsURL
	if url == "" {
		url = dockerHubTagsURL
	}
	hc := s.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 8 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return DockerHubTagInfo{Error: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mikrozia-Updater")

	resp, err := hc.Do(req)
	if err != nil {
		return DockerHubTagInfo{Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DockerHubTagInfo{Error: fmt.Sprintf("Docker Hub returned HTTP %d", resp.StatusCode)}
	}

	var payload struct {
		Results []struct {
			Name        string `json:"name"`
			LastUpdated string `json:"last_updated"`
			Images      []struct {
				Architecture string `json:"architecture"`
			} `json:"images"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return DockerHubTagInfo{Error: err.Error()}
	}

	var bestTag string
	var bestUpdated string
	var bestArchs []string
	for _, r := range payload.Results {
		if _, ok := parseMikroziaSemver(r.Name); !ok {
			continue
		}
		if bestTag == "" || isNewerMikroziaVersion(r.Name, bestTag) {
			bestTag = r.Name
			bestUpdated = r.LastUpdated
			seen := map[string]bool{}
			bestArchs = nil
			for _, img := range r.Images {
				arch := strings.TrimSpace(img.Architecture)
				if arch != "" && arch != "unknown" && !seen[arch] {
					seen[arch] = true
					bestArchs = append(bestArchs, arch)
				}
			}
		}
	}

	info := DockerHubTagInfo{
		LatestVersion: bestTag,
		LastUpdated:   bestUpdated,
		Architectures: bestArchs,
	}
	if bestTag == "" {
		info.Error = "No versioned Mikrozia release tag found on Docker Hub"
	} else {
		dockerHubCacheMu.Lock()
		cp := info
		dockerHubCacheValue = &cp
		dockerHubCacheAt = time.Now()
		dockerHubCacheMu.Unlock()
	}
	return info
}

func (s *RouterOSUpdaterService) resolveConnAndComment() (routeros.NATConnConfig, string, bool) {
	port := routeros.DefaultAPIPort
	comment := strings.TrimSpace(os.Getenv("MIKROZIA_ROUTEROS_CONTAINER_COMMENT"))
	if comment == "" {
		comment = strings.TrimSpace(os.Getenv("MIKROZIA_ROS_CONTAINER_COMMENT"))
	}

	// Prefer the write-capable Panel Settings RouterOS API account (mikrozia-nat)
	// over the read-only telemetry env account (mikrozia-api).
	if database.GetDB() != nil {
		if allSetting, err := s.settingService.GetAllSetting(); err == nil && allSetting != nil {
			if allSetting.RouterosNatPort > 0 {
				port = allSetting.RouterosNatPort
			}
			if c := strings.TrimSpace(allSetting.RouterosContainerComment); c != "" {
				comment = c
			}
			host := strings.TrimSpace(allSetting.RouterosNatHost)
			user := strings.TrimSpace(allSetting.RouterosNatUsername)
			pass := allSetting.RouterosNatPassword
			if validHost, ok := routeros.ValidatePrivateHost(host); ok && user != "" && pass != "" {
				if comment == "" {
					comment = "[Mikrozia:container] Container"
				}
				return routeros.NATConnConfig{
					Host:     validHost,
					Port:     port,
					Username: user,
					Password: pass,
				}, comment, true
			}
		}
	}

	apiCfg := resolveRouterOSAPIConfig()
	if apiCfg.State != "connecting" {
		return routeros.NATConnConfig{}, "", false
	}
	if comment == "" {
		comment = "[Mikrozia:container] Container"
	}
	return routeros.NATConnConfig{
		Host:     apiCfg.Host,
		Port:     port,
		Username: apiCfg.Username,
		Password: apiCfg.Password,
	}, comment, true
}

func buildManualCommands(current *routeros.ContainerUpdateSpec, targetTag, activeComment string) (string, string, string) {
	if targetTag == "" {
		return "", "", ""
	}
	iface := "veth-mikrozia"
	mounts := "MIKROZIA_DATA,MIKROZIA_CERT"
	envlist := "MIKROZIA_ENV"
	dns := "1.1.1.1,8.8.8.8"
	memHigh := "512M"
	memMax := "1024M"
	rootDir := "containers/mikrozia/root-" + targetTag
	if current != nil {
		if current.Interface != "" {
			iface = current.Interface
		}
		if current.Mountlists != "" {
			mounts = current.Mountlists
		}
		if current.Envlist != "" {
			envlist = current.Envlist
		}
		if current.DNS != "" {
			dns = current.DNS
		}
		if current.MemoryHigh != "" {
			memHigh = current.MemoryHigh
		}
		if current.MemoryMax != "" {
			memMax = current.MemoryMax
		}
		rootDir = routeros.DeriveNextRootDir(current.RootDir, targetTag)
	}
	if activeComment == "" {
		activeComment = "[Mikrozia:container] Container"
	}
	stagingComment := routeros.StagingComment(targetTag)
	stageCmd := fmt.Sprintf(
		`/container/add remote-image="echodev7/mikrozia:%s" interface=%s root-dir=%s mountlists="%s" envlist=%s dns="%s" memory-high=%s memory-max=%s logging=yes start-on-boot=yes comment="%s"`,
		targetTag, iface, rootDir, mounts, envlist, dns, memHigh, memMax, stagingComment,
	)
	cutoverCmd := fmt.Sprintf(
		"/container/set [find where comment=\"%s\"] comment=\"%s\" start-on-boot=no\n/container/stop [find where comment=\"%s\"]\n:delay 3s\n/container/set [find where comment=\"%s\"] comment=\"%s\" start-on-boot=yes\n/container/start [find where comment=\"%s\"]",
		activeComment, routeros.PreviousContainerComment,
		routeros.PreviousContainerComment,
		stagingComment, activeComment,
		activeComment,
	)
	cleanupCmd := fmt.Sprintf(
		`/container/remove [find where comment="%s" and status="stopped"]`,
		routeros.PreviousContainerComment,
	)
	return stageCmd, cutoverCmd, cleanupCmd
}

func (s *RouterOSUpdaterService) GetUpdateInfo(ctx context.Context, forceRefresh bool) RouterOSUpdateInfo {
	curVer := strings.TrimSpace(config.GetMikroziaVersion())
	hub := s.fetchDockerHubLatest(ctx, forceRefresh)

	out := RouterOSUpdateInfo{
		CurrentVersion:  curVer,
		LatestVersion:   hub.LatestVersion,
		LatestUpdatedAt: hub.LastUpdated,
		Architectures:   hub.Architectures,
		UpdateAvailable: hub.LatestVersion != "" && isNewerMikroziaVersion(hub.LatestVersion, curVer),
		DockerHubError:  hub.Error,
		RouterOSState:   "not_configured",
	}

	connCfg, comment, ok := s.resolveConnAndComment()
	if ok {
		inv := s.client.InspectContainersForUpdate(ctx, connCfg, comment)
		out.RouterOSState = inv.State
		out.FreeHddSpace = inv.FreeHddSpace
		out.TotalHddSpace = inv.TotalHddSpace
		out.CurrentContainer = inv.Current
		out.StagedContainer = inv.Staged
		out.Previous = inv.Previous
	}

	targetTag := hub.LatestVersion
	if targetTag == "" {
		targetTag = curVer
	}
	out.ManualStageCmd, out.ManualCutoverCmd, out.ManualCleanupCmd = buildManualCommands(out.CurrentContainer, targetTag, comment)
	return out
}

func (s *RouterOSUpdaterService) StageUpdate(ctx context.Context, targetTag string) (routeros.ContainerUpdateSpec, error) {
	connCfg, comment, ok := s.resolveConnAndComment()
	if !ok {
		return routeros.ContainerUpdateSpec{}, errors.New("RouterOS API is not configured")
	}
	inv := s.client.InspectContainersForUpdate(ctx, connCfg, comment)
	if inv.State != "ok" || inv.Current == nil {
		return routeros.ContainerUpdateSpec{}, fmt.Errorf("could not locate active Mikrozia container on RouterOS (%s)", inv.State)
	}
	spec, st, detail := s.client.StageContainerUpdate(ctx, connCfg, *inv.Current, targetTag)
	if st != "ok" {
		if detail != "" {
			return routeros.ContainerUpdateSpec{}, fmt.Errorf("%s: %s", st, detail)
		}
		return routeros.ContainerUpdateSpec{}, errors.New(st)
	}
	return spec, nil
}

func (s *RouterOSUpdaterService) CutoverUpdate(ctx context.Context, cleanupPreviousAfterBoot bool) (string, string, error) {
	connCfg, comment, ok := s.resolveConnAndComment()
	if !ok {
		return "not_configured", "", errors.New("RouterOS API is not configured")
	}
	inv := s.client.InspectContainersForUpdate(ctx, connCfg, comment)
	if inv.State != "ok" || inv.Current == nil || inv.Staged == nil {
		return "not_ready", "", errors.New("both current and staged Mikrozia containers must exist before cutover")
	}
	if strings.EqualFold(inv.Staged.Status, "extracting") {
		return "extracting", "", errors.New("staged container is still extracting; wait until extraction finishes")
	}

	if cleanupPreviousAfterBoot {
		markerPath := filepath.Join(config.GetDBFolderPath(), cleanupPreviousMarker)
		_ = os.WriteFile(markerPath, []byte(inv.Staged.Comment), 0o600)
	}

	st, detail := s.client.ScheduleContainerCutover(ctx, connCfg, inv.Current.ID, inv.Staged.ID, comment)
	if st != "ok" {
		if cleanupPreviousAfterBoot {
			_ = os.Remove(filepath.Join(config.GetDBFolderPath(), cleanupPreviousMarker))
		}
		if st == "permission_required" {
			return st, detail, nil
		}
		if detail != "" {
			return st, detail, fmt.Errorf("%s: %s", st, detail)
		}
		return st, detail, errors.New(st)
	}
	return "ok", "", nil
}

func (s *RouterOSUpdaterService) CleanupPrevious(ctx context.Context) (int, error) {
	connCfg, _, ok := s.resolveConnAndComment()
	if !ok {
		return 0, errors.New("RouterOS API is not configured")
	}
	removed, st, detail := s.client.RemovePreviousContainers(ctx, connCfg)
	if st != "ok" {
		if detail != "" {
			return removed, fmt.Errorf("%s: %s", st, detail)
		}
		return removed, errors.New(st)
	}
	return removed, nil
}

// RunDeferredPreviousCleanupIfArmed removes stopped [Mikrozia:previous] containers
// once the newly updated Mikrozia container has booted cleanly.
func (s *RouterOSUpdaterService) RunDeferredPreviousCleanupIfArmed() {
	markerPath := filepath.Join(config.GetDBFolderPath(), cleanupPreviousMarker)
	if _, err := os.Stat(markerPath); err != nil {
		return
	}
	time.Sleep(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	removed, err := s.CleanupPrevious(ctx)
	if err != nil {
		logger.Warning("deferred cleanup of previous Mikrozia container failed:", err)
		return
	}
	_ = os.Remove(markerPath)
	if removed > 0 {
		logger.Infof("Removed %d stopped previous Mikrozia container(s) after successful update", removed)
	}
}
