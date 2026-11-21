package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

func TestRouterOSContainerRoutesAuthAndNoSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("MIKROZIA_ROUTEROS_API_HOST", "")
	t.Setenv("MIKROZIA_ROUTEROS_API_USERNAME", "")
	t.Setenv("MIKROZIA_ROUTEROS_API_PASSWORD", "")
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_USERNAME", "")
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_PASSWORD", "")
	t.Setenv("MIKROZIA_ROUTEROS_CONTAINER_COMMENT", "")

	for _, path := range []string{"/routerosContainerStatus", "/routerosContainerLogs/20", "/restartRouterosContainer"} {
		method := http.MethodGet
		if strings.HasPrefix(path, "/restart") {
			method = http.MethodPost
		}
		t.Run(path, func(t *testing.T) {
			open := gin.New()
			(&ServerController{}).initRouter(open.Group("/panel/api/server"))
			req := httptest.NewRequest(method, "/panel/api/server"+path, nil)
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			w := httptest.NewRecorder()
			open.ServeHTTP(w, req)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "not_configured") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var result map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "username") {
				t.Fatal("credentials leaked")
			}

			secured := gin.New()
			secured.Use(sessions.Sessions("3x-ui", cookie.NewStore([]byte("test-secret"))))
			base := &BaseController{}
			(&ServerController{}).initRouter(secured.Group("/panel/api/server", base.checkLogin))
			blocked := httptest.NewRecorder()
			secured.ServeHTTP(blocked, req)
			if blocked.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated status=%d, want 401", blocked.Code)
			}
		})
	}
}
