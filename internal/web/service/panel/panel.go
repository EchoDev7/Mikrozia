package panel

import (
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/global"
)

// PanelService provides business logic for panel management operations.
// It handles panel restart and version reporting.
type PanelService struct{}

// PanelUpdateInfo contains current panel version information for read-only display.
type PanelUpdateInfo struct {
	Channel         string `json:"channel"`
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	CurrentCommit   string `json:"currentCommit,omitempty"`
	LatestCommit    string `json:"latestCommit,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

// RestartPanel restarts the panel after the specified delay.
func (s *PanelService) RestartPanel(delay time.Duration) error {
	go func() {
		time.Sleep(delay)
		if global.TriggerRestart() {
			return
		}
		if runtime.GOOS == "windows" {
			logger.Error("panel restart: no restart hook registered (SIGHUP unsupported on Windows)")
			return
		}
		p, err := os.FindProcess(syscall.Getpid())
		if err != nil {
			logger.Error("panel restart: FindProcess failed:", err)
			return
		}
		if err := p.Signal(syscall.SIGHUP); err != nil {
			logger.Error("failed to send SIGHUP signal:", err)
		}
	}()
	return nil
}

// GetUpdateInfo returns the current running Mikrozia version without contacting upstream servers.
func (s *PanelService) GetUpdateInfo() (*PanelUpdateInfo, error) {
	current := config.GetMikroziaVersion()
	return &PanelUpdateInfo{
		Channel:         "stable",
		CurrentVersion:  current,
		LatestVersion:   current,
		UpdateAvailable: false,
	}, nil
}
