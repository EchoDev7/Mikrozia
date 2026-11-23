package service

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/xray"
)

func installFakeXrayBinary(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake xray binary test")
	}
	binDir := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", binDir)
	binPath := filepath.Join(binDir, xray.GetBinaryName())
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-version\" ] || [ \"$1\" = \"version\" ]; then\n" +
		"  echo 'Xray 26.10.21 (Xray, Penetrates Everything.)'\n" +
		"  exit 0\n" +
		"fi\n" +
		"trap 'exit 0' TERM INT\n" +
		"while true; do sleep 1; done\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake xray binary: %v", err)
	}
}

func TestRestartXrayRespectsManualStop(t *testing.T) {
	setupSettingTestDB(t)
	if err := (&SettingService{}).saveSetting("xrayTemplateConfig", "{ not valid json"); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	t.Cleanup(func() { isManuallyStopped.Store(false) })

	isManuallyStopped.Store(true)
	_ = (&XrayService{}).RestartXray(false)

	if !isManuallyStopped.Load() {
		t.Fatal("a non-forced restart cleared a deliberate manual stop and would revive xray")
	}
}

func TestApplyPendingRestartReArmsFlagOnFailure(t *testing.T) {
	setupSettingTestDB(t)
	if err := (&SettingService{}).saveSetting("xrayTemplateConfig", "{ not valid json"); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	t.Cleanup(func() {
		isManuallyStopped.Store(false)
		isNeedXrayRestart.Store(false)
	})
	isManuallyStopped.Store(false)

	svc := &XrayService{}
	svc.SetToNeedRestart()
	svc.ApplyPendingRestart()

	if !isNeedXrayRestart.Load() {
		t.Fatal("a failed restart must re-arm the need-restart flag so the pending config change is retried")
	}
}

func TestStopXrayUpdatesCachedStatusAndBlocksAutoRestart(t *testing.T) {
	installFakeXrayBinary(t)
	setupSettingTestDB(t)

	restoreProcess := SetXrayProcessForTest(nil)
	t.Cleanup(func() {
		_ = (&XrayService{}).StopXray()
		isManuallyStopped.Store(false)
		isNeedXrayRestart.Store(false)
		isNeedXrayReconcile.Store(false)
		restoreProcess()
	})

	serverSvc := &ServerService{}
	xraySvc := &XrayService{}

	// Stopping before Xray is running must fail truthfully.
	if err := serverSvc.StopXrayService(); !errors.Is(err, ErrXrayNotRunning) {
		t.Fatalf("StopXrayService while stopped = %v, want ErrXrayNotRunning", err)
	}

	// Start fake Xray and seed a cached Running status snapshot.
	if err := serverSvc.RestartXrayService(); err != nil {
		t.Fatalf("RestartXrayService failed: %v", err)
	}
	if !xraySvc.IsXrayRunning() {
		t.Fatal("expected fake Xray to be running after RestartXrayService")
	}
	serverSvc.lastStatusMu.Lock()
	serverSvc.lastStatus = &Status{}
	serverSvc.lastStatus.Xray.State = Running
	serverSvc.lastStatusMu.Unlock()

	if got := serverSvc.CurrentStatus().Xray.State; got != Running {
		t.Fatalf("CurrentStatus before stop = %q, want %q", got, Running)
	}

	// Manual stop must immediately flip CurrentStatus to Stop and suppress auto-restart.
	if err := serverSvc.StopXrayService(); err != nil {
		t.Fatalf("StopXrayService failed: %v", err)
	}
	if xraySvc.IsXrayRunning() {
		t.Fatal("expected fake Xray to be stopped after StopXrayService")
	}
	if got := serverSvc.CurrentStatus().Xray.State; got != Stop {
		t.Fatalf("CurrentStatus immediately after StopXrayService = %q, want %q", got, Stop)
	}
	if xraySvc.DidXrayCrash() {
		t.Fatal("DidXrayCrash must be false after manual stop")
	}
	if !xraySvc.IsManuallyStopped() {
		t.Fatal("IsManuallyStopped must be true after manual stop")
	}

	// Pending reconcile/restart must not revive a manually stopped core.
	xraySvc.SetToNeedRestart()
	xraySvc.ApplyPendingRestart()
	if xraySvc.IsXrayRunning() || serverSvc.CurrentStatus().Xray.State != Stop {
		t.Fatal("ApplyPendingRestart revived a manually stopped Xray process")
	}

	// Explicit RestartXrayService must clear manual stop and report Running immediately.
	if err := serverSvc.RestartXrayService(); err != nil {
		t.Fatalf("RestartXrayService after manual stop failed: %v", err)
	}
	if xraySvc.IsManuallyStopped() {
		t.Fatal("IsManuallyStopped must be cleared after explicit restart")
	}
	if got := serverSvc.CurrentStatus().Xray.State; got != Running {
		t.Fatalf("CurrentStatus immediately after RestartXrayService = %q, want %q", got, Running)
	}
}
