package sub

import (
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/database/dbtest"
	"github.com/echodev7/mikrozia/v3/internal/web/network"
)

func TestCertificateReloadPreservesSubscriptionHandler(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "existing-subscription-state") })
	s := &Server{listener: listener, httpServer: &http.Server{Handler: handler}}
	go network.ServeHTTP(s.httpServer, listener, "Test subscription")
	t.Cleanup(func() { _ = s.httpServer.Close() })
	if err := s.ReloadTLS(); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://" + s.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "existing-subscription-state" {
		t.Fatalf("handler state=%q err=%v", body, err)
	}
}
