package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/echodev7/mikrozia/v3/internal/config"
	panelService "github.com/echodev7/mikrozia/v3/internal/web/service/panel"
)

func newFullControllerTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	serverCtrl := &ServerController{}
	serverCtrl.initRouter(engine.Group("/panel/api/server"))

	nodeCtrl := &NodeController{}
	nodeCtrl.initRouter(engine.Group("/panel/api/nodes"))

	return engine
}

// TestRemovedUpdateEndpointsReturn404 confirms that all legacy Mikrozia self-update
// routes have been completely removed and return 404 Not Found.
func TestRemovedUpdateEndpointsReturn404(t *testing.T) {
	engine := newFullControllerTestEngine()

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/panel/api/server/updatePanel"},
		{http.MethodPost, "/panel/api/server/setUpdateChannel"},
		{http.MethodGet, "/panel/api/server/getUpdateStatus"},
		{http.MethodPost, "/panel/api/nodes/updatePanel"},
		{http.MethodGet, "/panel/api/server/getXrayVersion"},
		{http.MethodPost, "/panel/api/server/installXray/v26.10.21"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("%s %s = %d, want %d Not Found (body: %s)", ep.method, ep.path, w.Code, http.StatusNotFound, w.Body.String())
			}
		})
	}
}

// TestGetPanelUpdateInfo_ReturnsCurrentVersionWithoutNetwork ensures that
// GET /panel/api/server/getPanelUpdateInfo returns the current Mikrozia version
// in a read-only format without contacting upstream servers or reporting an update.
func TestGetPanelUpdateInfo_ReturnsCurrentVersionWithoutNetwork(t *testing.T) {
	engine := newFullControllerTestEngine()

	req := httptest.NewRequest(http.MethodGet, "/panel/api/server/getPanelUpdateInfo", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("getPanelUpdateInfo = %d, want 200 OK (body: %s)", w.Code, w.Body.String())
	}

	var env struct {
		Success bool                         `json:"success"`
		Msg     string                       `json:"msg"`
		Obj     panelService.PanelUpdateInfo `json:"obj"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response: %v, body: %s", err, w.Body.String())
	}

	if !env.Success {
		t.Fatalf("expected success=true, got false (msg: %s)", env.Msg)
	}

	expectedVersion := config.FormatMikroziaVersion(config.GetMikroziaVersion())
	if env.Obj.CurrentVersion != expectedVersion {
		t.Errorf("CurrentVersion = %q, want %q", env.Obj.CurrentVersion, expectedVersion)
	}
	if env.Obj.LatestVersion != expectedVersion {
		t.Errorf("LatestVersion = %q, want %q", env.Obj.LatestVersion, expectedVersion)
	}
	if env.Obj.UpdateAvailable {
		t.Errorf("UpdateAvailable = %v, want false", env.Obj.UpdateAvailable)
	}
}
