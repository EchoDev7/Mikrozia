package controller

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/database/dbtest"
	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/util/crypto"
	"github.com/echodev7/mikrozia/v3/internal/web/session"
)

// newAPIAuthTestEngine builds a gin engine that mirrors the production auth
// wiring: the sessions middleware, then checkAPIAuth guarding a sentinel
// handler that reports whether c.Next() was reached and whether api_authed was
// set. The APIController is the zero value, exactly as NewAPIController leaves
// its service fields (they query the global DB), so this exercises the real
// auth path. A fresh temp DB is initialised per test.
func newAPIAuthTestEngine(t *testing.T) (*gin.Engine, *APIController) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	engine := gin.New()
	store := cookie.NewStore([]byte("api-auth-test-secret"))
	engine.Use(sessions.Sessions("mikrozia", store))

	a := &APIController{}

	// Logs in as the first user so the session path can be exercised over a
	// cookie round-trip without reaching into checkAPIAuth's internals.
	engine.GET("/test-login", func(c *gin.Context) {
		u, err := a.userService.GetFirstUser()
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		if err := session.SetLoginUser(c, u); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})

	api := engine.Group("/panel/api")
	api.Use(a.checkAPIAuth)
	api.Use(a.enforceTokenScope)
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"api_authed": c.GetBool("api_authed")})
	})
	api.GET("/server/status", func(c *gin.Context) {
		scope, _ := c.Get("api_token_scope")
		c.JSON(http.StatusOK, gin.H{"api_authed": c.GetBool("api_authed"), "scope": scope})
	})
	api.POST("/clients/:email/detach", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"reached": true})
	})
	api.POST("/inbounds/:id/resetTraffic", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"reached": true})
	})
	api.POST("/clients/clientIpsByGuid", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"reached": true})
	})
	api.POST("/server/adminOnly", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"reached": true})
	})
	return engine, a
}

// TestCheckAPIAuth_BearerSuccess characterizes the bearer-token path: a valid
// token reaches the handler and sets api_authed (the contract the later
// client-cert branch must match).
func TestCheckAPIAuth_BearerSuccess(t *testing.T) {
	engine, _ := newAPIAuthTestEngine(t)

	const plaintext = "characterization-token-value"
	if err := database.GetDB().Create(&model.ApiToken{
		Name:    "t1",
		Token:   crypto.HashTokenSHA256(plaintext),
		Enabled: true,
		Scope:   model.ApiScopeAdmin,
	}).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/panel/api/ping", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"api_authed":true}` {
		t.Fatalf("body = %s, want api_authed true", got)
	}
}

// TestCheckAPIAuth_AcceptsVerifiedClientCert ensures verified mTLS authenticates
// as node-sync rather than bypassing scope checks as admin.
func TestCheckAPIAuth_AcceptsVerifiedClientCert(t *testing.T) {
	engine, _ := newAPIAuthTestEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/panel/api/server/status", nil)
	req.TLS = &tls.ConnectionState{
		VerifiedChains: [][]*x509.Certificate{{&x509.Certificate{}}},
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"api_authed":true,"scope":"node-sync"}` {
		t.Fatalf("body = %s, want node-sync scope", got)
	}

	forbidden := httptest.NewRequest(http.MethodPost, "/panel/api/server/adminOnly", nil)
	forbidden.TLS = &tls.ConnectionState{
		VerifiedChains: [][]*x509.Certificate{{&x509.Certificate{}}},
	}
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, forbidden)
	if w.Code != http.StatusForbidden {
		t.Fatalf("adminOnly status = %d, want 403; body=%s", w.Code, w.Body.String())
	}

	for _, legacyScope := range []string{model.ApiScopeAdmin, model.ApiScopeMonitor, model.ApiScopeNodeSync} {
		plaintext := "mtls-" + legacyScope + "-token"
		if err := database.GetDB().Create(&model.ApiToken{
			Name:    "mtls-" + legacyScope,
			Token:   crypto.HashTokenSHA256(plaintext),
			Enabled: true,
			Scope:   legacyScope,
		}).Error; err != nil {
			t.Fatalf("seed %s token: %v", legacyScope, err)
		}
		legacy := httptest.NewRequest(http.MethodGet, "/panel/api/server/status", nil)
		legacy.Header.Set("Authorization", "Bearer "+plaintext)
		legacy.TLS = &tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{&x509.Certificate{}}},
		}
		w = httptest.NewRecorder()
		engine.ServeHTTP(w, legacy)
		if w.Code != http.StatusOK || w.Body.String() != `{"api_authed":true,"scope":"node-sync"}` {
			t.Fatalf("mTLS %s response = %d %s, want preserved node-sync scope", legacyScope, w.Code, w.Body.String())
		}
	}

	const plaintext = "mtls-node-admin-token"
	if err := database.GetDB().Create(&model.ApiToken{
		Name:    "mtls-node-admin",
		Token:   crypto.HashTokenSHA256(plaintext),
		Enabled: true,
		Scope:   "node-admin",
	}).Error; err != nil {
		t.Fatalf("seed node-admin token: %v", err)
	}
	syncReq := httptest.NewRequest(http.MethodPost, "/panel/api/clients/clientIpsByGuid", nil)
	syncReq.Header.Set("Authorization", "Bearer "+plaintext)
	syncReq.TLS = &tls.ConnectionState{
		VerifiedChains: [][]*x509.Certificate{{&x509.Certificate{}}},
	}
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, syncReq)
	if w.Code != http.StatusOK || w.Body.String() != `{"reached":true}` {
		t.Fatalf("mTLS node-admin response = %d %s, want 200", w.Code, w.Body.String())
	}
}

// TestMigratedTokenStrictNodeSyncAuthority verifies that tokens migrated to
// node-sync have exactly node-sync permissions and no admin privileges.
func TestMigratedTokenStrictNodeSyncAuthority(t *testing.T) {
	engine, _ := newAPIAuthTestEngine(t)

	const tokenVal = "migrated-node-sync-token"
	if err := database.GetDB().Create(&model.ApiToken{
		Name:    "migrated-token",
		Token:   crypto.HashTokenSHA256(tokenVal),
		Enabled: true,
		Scope:   model.ApiScopeNodeSync,
	}).Error; err != nil {
		t.Fatalf("seed migrated token: %v", err)
	}

	// 1. Allowed on node-sync endpoints
	req1 := httptest.NewRequest(http.MethodGet, "/panel/api/server/status", nil)
	req1.Header.Set("Authorization", "Bearer "+tokenVal)
	w1 := httptest.NewRecorder()
	engine.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("status on /server/status = %d, want 200", w1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/panel/api/clients/clientIpsByGuid", nil)
	req2.Header.Set("Authorization", "Bearer "+tokenVal)
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status on /clients/clientIpsByGuid = %d, want 200", w2.Code)
	}

	// 2. Forbidden on admin endpoints
	reqAdmin := httptest.NewRequest(http.MethodPost, "/panel/api/server/adminOnly", nil)
	reqAdmin.Header.Set("Authorization", "Bearer "+tokenVal)
	wAdmin := httptest.NewRecorder()
	engine.ServeHTTP(wAdmin, reqAdmin)
	if wAdmin.Code != http.StatusForbidden {
		t.Fatalf("admin endpoint accessed with node-sync token! status = %d, want 403", wAdmin.Code)
	}

	reqPing := httptest.NewRequest(http.MethodGet, "/panel/api/ping", nil)
	reqPing.Header.Set("Authorization", "Bearer "+tokenVal)
	wPing := httptest.NewRecorder()
	engine.ServeHTTP(wPing, reqPing)
	if wPing.Code != http.StatusForbidden {
		t.Fatalf("ping endpoint accessed with node-sync token! status = %d, want 403", wPing.Code)
	}
}

func TestNodeSyncScopeUsesFullPathPatterns(t *testing.T) {
	engine, _ := newAPIAuthTestEngine(t)
	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"detach email parameter", http.MethodPost, "/panel/api/clients/alice@example.com/detach", http.StatusOK},
		{"reset inbound id parameter", http.MethodPost, "/panel/api/inbounds/42/resetTraffic", http.StatusOK},
		{"client IP by guid endpoint", http.MethodPost, "/panel/api/clients/clientIpsByGuid", http.StatusOK},
		{"admin endpoint forbidden", http.MethodPost, "/panel/api/server/adminOnly", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.TLS = &tls.ConnectionState{
				VerifiedChains: [][]*x509.Certificate{{&x509.Certificate{}}},
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// TestCheckAPIAuth_EmptyVerifiedChainsFallsThrough asserts a TLS request with no
// verified client chain is NOT treated as authenticated (it falls through to the
// bearer/session paths) — so the cert branch can't accidentally authorize plain
// browser HTTPS.
func TestCheckAPIAuth_EmptyVerifiedChainsFallsThrough(t *testing.T) {
	engine, _ := newAPIAuthTestEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/panel/api/ping", nil)
	req.TLS = &tls.ConnectionState{} // handshake done, but no client cert verified
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (unauthenticated, no verified chain)", w.Code)
	}
}

// TestCheckAPIAuth_RejectsUnauthenticated characterizes the reject paths:
// no credential → 404 (masking); XHR or a presented (but invalid) Bearer → 401
// so script authors can tell auth failure from a wrong base path.
func TestCheckAPIAuth_RejectsUnauthenticated(t *testing.T) {
	engine, _ := newAPIAuthTestEngine(t)

	cases := []struct {
		name   string
		xhr    bool
		bearer string // empty = omit Authorization header
		want   int
	}{
		{"xhr gets 401", true, "", http.StatusUnauthorized},
		{"non-xhr gets 404", false, "", http.StatusNotFound},
		{"invalid bearer gets 401", false, "definitely-not-a-token", http.StatusUnauthorized},
		{"invalid bearer xhr gets 401", true, "definitely-not-a-token", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/panel/api/ping", nil)
			if c.xhr {
				req.Header.Set("X-Requested-With", "XMLHttpRequest")
			}
			if c.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+c.bearer)
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != c.want {
				t.Fatalf("status = %d, want %d", w.Code, c.want)
			}
		})
	}
}

// TestCheckAPIAuth_SessionLoginPasses characterizes the session path: a
// logged-in browser session (no bearer token) reaches the handler.
func TestCheckAPIAuth_SessionLoginPasses(t *testing.T) {
	engine, _ := newAPIAuthTestEngine(t)

	db := database.GetDB()
	var n int64
	if err := db.Model(&model.User{}).Count(&n).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n == 0 {
		if err := db.Create(&model.User{Username: "sess", Password: "x"}).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}

	ts := httptest.NewServer(engine)
	defer ts.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginResp, err := client.Get(ts.URL + "/test-login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResp.StatusCode)
	}

	pingResp, err := client.Get(ts.URL + "/panel/api/ping")
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	pingResp.Body.Close()
	if pingResp.StatusCode != http.StatusOK {
		t.Fatalf("session ping status = %d, want 200", pingResp.StatusCode)
	}
}
