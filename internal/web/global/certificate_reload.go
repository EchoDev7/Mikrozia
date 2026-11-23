package global

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/lifecycle"
	"github.com/echodev7/mikrozia/v3/internal/web/network"
)

var (
	certificateHookMu sync.RWMutex
	certificateHook   func() error
)

func SetCertificateReloadHook(fn func() error) {
	certificateHookMu.Lock()
	defer certificateHookMu.Unlock()
	certificateHook = fn
}

func ReloadCertificates() error {
	certificateHookMu.RLock()
	fn := certificateHook
	certificateHookMu.RUnlock()
	if fn == nil {
		return errors.New("certificate reload service is unavailable")
	}
	return fn()
}

func certificateSocket() string {
	return filepath.Join(os.TempDir(), ".mikrozia-certificate-reload.sock")
}

// ServeCertificateReload exposes acknowledged reload only through a root-private local socket.
func ServeCertificateReload() (func(), error) {
	path := certificateSocket()
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("certificate reload path is not a socket")
		}
		probe, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "unix", path)
		if err == nil {
			_ = probe.Close()
			return nil, errors.New("certificate reload socket is already active")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/reload" {
			http.NotFound(w, r)
			return
		}
		if err := ReloadCertificates(); err != nil {
			var conflict *lifecycle.ConflictError
			if errors.As(err, &conflict) {
				http.Error(w, conflict.Error(), http.StatusConflict)
				return
			}
			if errors.Is(err, lifecycle.ErrClosing) {
				http.Error(w, lifecycle.ErrClosing.Error(), http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "certificate listener reload failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	go network.ServeHTTP(server, listener, "Certificate reload service")
	return func() { _ = server.Close(); _ = listener.Close() }, nil
}

func RequestCertificateReload() error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", certificateSocket())
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://localhost/reload", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("the running panel certificate reload service could not be reached")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusNoContent {
		if response.StatusCode == http.StatusConflict {
			return errors.New("server lifecycle operation is in progress; retry certificate reload after it completes")
		}
		if response.StatusCode == http.StatusServiceUnavailable {
			return lifecycle.ErrClosing
		}
		return fmt.Errorf("certificate listener reload returned HTTP %d", response.StatusCode)
	}
	return nil
}
