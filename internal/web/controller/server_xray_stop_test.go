package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/echodev7/mikrozia/v3/internal/database/dbtest"
	"github.com/echodev7/mikrozia/v3/internal/web/service"
	"github.com/echodev7/mikrozia/v3/internal/xray"
)

func TestStopAndRestartXrayEndpointsReflectImmediateStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake xray binary test")
	}
	gin.SetMode(gin.TestMode)
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))

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

	restoreProcess := service.SetXrayProcessForTest(nil)
	t.Cleanup(func() {
		_ = (&service.XrayService{}).StopXray()
		restoreProcess()
	})

	ctrl := &ServerController{}
	router := gin.New()
	ctrl.initRouter(router.Group("/panel/api/server"))

	call := func(method, path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s status=%d body=%s", method, path, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s invalid json: %v", method, path, err)
		}
		return body
	}

	// Stopping when Xray is not running must return success=false.
	stopWhenDown := call(http.MethodPost, "/panel/api/server/stopXrayService")
	if stopWhenDown["success"] != false {
		t.Fatalf("stopXrayService while stopped returned %v, want success=false", stopWhenDown)
	}

	// Start Xray, warm the cached status, and confirm Running.
	startResp := call(http.MethodPost, "/panel/api/server/restartXrayService")
	if startResp["success"] != true {
		t.Fatalf("restartXrayService failed: %v", startResp)
	}
	statusRunning := call(http.MethodGet, "/panel/api/server/status")
	xrayObj := statusRunning["obj"].(map[string]any)["xray"].(map[string]any)
	if xrayObj["state"] != string(service.Running) {
		t.Fatalf("status after start = %v, want running", xrayObj["state"])
	}

	// Stop Xray and immediately fetch status without waiting for the 2s ticker.
	stopResp := call(http.MethodPost, "/panel/api/server/stopXrayService")
	if stopResp["success"] != true {
		t.Fatalf("stopXrayService failed: %v", stopResp)
	}
	statusStopped := call(http.MethodGet, "/panel/api/server/status")
	xrayObj = statusStopped["obj"].(map[string]any)["xray"].(map[string]any)
	if xrayObj["state"] != string(service.Stop) {
		t.Fatalf("status immediately after stopXrayService = %v, want stop", xrayObj["state"])
	}

	// Restart Xray again and confirm immediate Running state.
	restartResp := call(http.MethodPost, "/panel/api/server/restartXrayService")
	if restartResp["success"] != true {
		t.Fatalf("restartXrayService after stop failed: %v", restartResp)
	}
	statusRestarted := call(http.MethodGet, "/panel/api/server/status")
	xrayObj = statusRestarted["obj"].(map[string]any)["xray"].(map[string]any)
	if xrayObj["state"] != string(service.Running) {
		t.Fatalf("status immediately after restartXrayService = %v, want running", xrayObj["state"])
	}
}
