package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
)

func TestTopInboundsController_SchemaAuthAndRouting(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	gin.SetMode(gin.TestMode)
	db := database.GetDB()

	// Seed user 1 (session.GetLoginUser returns Id=0 when no session user is set;
	// verify both explicit user_id=0 fallback and API token path which sets user_id=1).
	for _, ib := range []model.Inbound{
		{UserId: 1, Remark: "u1-top", Protocol: model.VLESS, Port: 443, Tag: "u1-443", Up: 300, Down: 700, Total: 5000, Enable: true},
		{UserId: 1, Remark: "u1-second", Protocol: model.Trojan, Port: 8443, Tag: "u1-8443", Up: 100, Down: 200, Enable: true},
	} {
		if err := db.Create(&ib).Error; err != nil {
			t.Fatalf("seed inbound: %v", err)
		}
	}

	normRead, _ := panel.NormalizeScope("read_only")
	normSync, _ := panel.NormalizeScope(model.ApiScopeNodeSync)
	for name, tc := range map[string]struct {
		token string
		scope string
	}{
		"read-only": {token: "test-read-token", scope: normRead},
		"node-sync": {token: "test-sync-token", scope: normSync},
	} {
		row := &model.ApiToken{
			Name:    name,
			Token:   crypto.HashTokenSHA256(tc.token),
			Enabled: true,
			Scope:   tc.scope,
		}
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("seed token %s: %v", name, err)
		}
	}

	t.Run("authenticated API token returns exact 6 keys sorted by traffic DESC", func(t *testing.T) {
		engine := gin.New()
		api := engine.Group("/panel/api")
		a := &APIController{}
		api.Use(a.checkAPIAuth, a.enforceTokenScope)
		NewInboundController(api.Group("/inbounds"))

		req := httptest.NewRequest(http.MethodGet, "/panel/api/inbounds/top", nil)
		req.Header.Set("Authorization", "Bearer test-read-token")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("GET /panel/api/inbounds/top = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}

		var env struct {
			Success bool             `json:"success"`
			Obj     []map[string]any `json:"obj"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if !env.Success || len(env.Obj) != 2 {
			t.Fatalf("unexpected response: %s", w.Body.String())
		}
		if env.Obj[0]["remark"] != "u1-top" || env.Obj[0]["traffic"] != float64(1000) {
			t.Fatalf("first row = %v, want u1-top traffic=1000", env.Obj[0])
		}

		keys := make([]string, 0, len(env.Obj[0]))
		for k := range env.Obj[0] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		wantKeys := []string{"enabled", "id", "port", "protocol", "remark", "traffic"}
		if len(keys) != len(wantKeys) {
			t.Fatalf("keys = %v, want %v", keys, wantKeys)
		}
		for i := range wantKeys {
			if keys[i] != wantKeys[i] {
				t.Fatalf("keys = %v, want %v", keys, wantKeys)
			}
		}
	})

	t.Run("node_sync token is rejected on /panel/api/inbounds/top", func(t *testing.T) {
		engine := gin.New()
		api := engine.Group("/panel/api")
		a := &APIController{}
		api.Use(a.checkAPIAuth, a.enforceTokenScope)
		NewInboundController(api.Group("/inbounds"))

		req := httptest.NewRequest(http.MethodGet, "/panel/api/inbounds/top", nil)
		req.Header.Set("Authorization", "Bearer test-sync-token")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("node_sync GET /panel/api/inbounds/top = %d, want 403", w.Code)
		}
	})

	t.Run("unauthenticated request is rejected by checkLogin", func(t *testing.T) {
		engine := gin.New()
		engine.Use(sessions.Sessions("3x-ui", cookie.NewStore([]byte("test-secret"))))
		base := &BaseController{}
		g := engine.Group("/panel/api/inbounds", base.checkLogin)
		NewInboundController(g)

		req := httptest.NewRequest(http.MethodGet, "/panel/api/inbounds/top", nil)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated GET /panel/api/inbounds/top = %d, want 401", w.Code)
		}
	})
}
