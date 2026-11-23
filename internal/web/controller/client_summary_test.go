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

	"github.com/echodev7/mikrozia/v3/internal/database/dbtest"
)

func TestClientSummaryController_ExactSchemaNoPIIAndAuth(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	gin.SetMode(gin.TestMode)

	t.Run("authenticated handler returns exact 6 count keys and no PII", func(t *testing.T) {
		engine := gin.New()
		NewClientController(engine.Group("/panel/api/clients"))

		req := httptest.NewRequest(http.MethodGet, "/panel/api/clients/summary", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("GET /panel/api/clients/summary = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}

		var env struct {
			Success bool           `json:"success"`
			Obj     map[string]any `json:"obj"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if !env.Success {
			t.Fatalf("expected success=true, got body %s", w.Body.String())
		}

		keys := make([]string, 0, len(env.Obj))
		for k := range env.Obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		wantKeys := []string{"active", "depleting", "disabled", "ended", "online", "total"}
		if len(keys) != len(wantKeys) {
			t.Fatalf("keys = %v, want %v", keys, wantKeys)
		}
		for i := range wantKeys {
			if keys[i] != wantKeys[i] {
				t.Fatalf("keys = %v, want %v", keys, wantKeys)
			}
		}
	})

	t.Run("unauthenticated request is rejected by checkLogin", func(t *testing.T) {
		engine := gin.New()
		engine.Use(sessions.Sessions("mikrozia", cookie.NewStore([]byte("test-secret"))))
		base := &BaseController{}
		g := engine.Group("/panel/api/clients", base.checkLogin)
		NewClientController(g)

		req := httptest.NewRequest(http.MethodGet, "/panel/api/clients/summary", nil)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated GET /panel/api/clients/summary = %d, want 401", w.Code)
		}
	})
}
