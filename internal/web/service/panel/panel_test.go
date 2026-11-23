package panel

import (
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/config"
	"github.com/echodev7/mikrozia/v3/internal/web/global"
)

func TestGetUpdateInfo(t *testing.T) {
	svc := &PanelService{}
	info, err := svc.GetUpdateInfo()
	if err != nil {
		t.Fatalf("GetUpdateInfo failed: %v", err)
	}
	if info == nil {
		t.Fatal("GetUpdateInfo returned nil info")
	}

	expectedVersion := config.FormatMikroziaVersion(config.GetMikroziaVersion())
	if info.CurrentVersion != expectedVersion {
		t.Errorf("CurrentVersion = %q, want %q", info.CurrentVersion, expectedVersion)
	}
	if info.LatestVersion != expectedVersion {
		t.Errorf("LatestVersion = %q, want %q", info.LatestVersion, expectedVersion)
	}
	if info.UpdateAvailable {
		t.Errorf("UpdateAvailable = %v, want false", info.UpdateAvailable)
	}
	if info.Channel != "stable" {
		t.Errorf("Channel = %q, want %q", info.Channel, "stable")
	}
}

func TestRestartPanelTriggersRegisteredHook(t *testing.T) {
	triggered := make(chan struct{}, 1)
	global.SetRestartHook(func() {
		select {
		case triggered <- struct{}{}:
		default:
		}
	})
	t.Cleanup(func() { global.SetRestartHook(nil) })

	svc := &PanelService{}
	if err := svc.RestartPanel(time.Millisecond); err != nil {
		t.Fatalf("RestartPanel returned error: %v", err)
	}

	select {
	case <-triggered:
	case <-time.After(2 * time.Second):
		t.Fatal("RestartPanel did not trigger registered restart hook within timeout")
	}
}
