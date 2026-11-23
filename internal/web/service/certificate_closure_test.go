package service

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/lifecycle"
	"github.com/echodev7/mikrozia/v3/internal/web/network"
)

type lifecycleCertificateReloader struct {
	coordinator *lifecycle.Coordinator
	live        *liveCertificateReloader
	release     chan struct{}
	done        chan error
}

func (r *lifecycleCertificateReloader) Reload() error {
	err := r.coordinator.Run("certificate reload", r.live.Reload)
	if r.release != nil {
		close(r.release)
		r.release = nil
		if stopErr := <-r.done; stopErr != nil {
			return errors.Join(err, stopErr)
		}
	}
	return err
}

func TestCertificateLifecycleConflictRestoresBytesSettingsAndLiveTLS(t *testing.T) {
	oldCert, oldKey := certificateTestPair(t, "old.example.com")
	newCert, newKey := certificateTestPair(t, "panel.example.com")
	manager, result := newCertificateTransaction(t, oldCert, oldKey, newCert, newKey, false)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	live := &liveCertificateReloader{server: &http.Server{}, listener: raw, cert: result.CertPath, key: result.KeyPath}
	defer func() { _ = live.server.Close(); _ = live.listener.Close() }()
	c := lifecycle.NewCoordinator()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- c.Wait(context.Background(), "panel/subscription restart", func() error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("restart not entered")
	}
	previous := CertificatePaths{PanelCert: "previous-cert", PanelKey: "previous-key"}
	settings := &certificateSettingsFake{paths: previous}
	svc := &CertificateService{settings: settings, reloader: &lifecycleCertificateReloader{coordinator: c, live: live, release: release, done: done}}
	if err := svc.ApplyAndReload(result); err == nil || !strings.Contains(err.Error(), "panel/subscription restart") {
		t.Fatalf("conflict result=%v", err)
	}
	assertCertificateBytes(t, manager, oldCert, oldKey)
	if settings.paths != previous {
		t.Fatal("conflict did not restore settings")
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", live.listener.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.ConnectionState().PeerCertificates[0].Subject.CommonName; got != "old.example.com" {
		t.Fatalf("recovery TLS=%s", got)
	}
}

type liveCertificateReloader struct {
	server    *http.Server
	listener  net.Listener
	cert, key string
	occupied  net.Listener
	fail      bool
}

func (r *liveCertificateReloader) Reload() error {
	if r.fail {
		r.fail = false
		address := r.listener.Addr().String()
		if err := r.server.Close(); err != nil {
			return err
		}
		var err error
		r.occupied, err = net.Listen("tcp", address)
		if err != nil {
			return err
		}
		defer r.occupied.Close()
	}
	return network.ReloadHTTP(&r.server, &r.listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "alive") }), nil, r.cert, r.key)
}

func TestCertificateRealListenerBindFailureRollsBackAndRecoversTLS(t *testing.T) {
	oldCert, oldKey := certificateTestPair(t, "old.example.com")
	newCert, newKey := certificateTestPair(t, "panel.example.com")
	manager, result := newCertificateTransaction(t, oldCert, oldKey, newCert, newKey, false)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := raw.Addr().String()
	reloader := &liveCertificateReloader{server: &http.Server{}, listener: raw, cert: result.CertPath, key: result.KeyPath, fail: true}
	defer func() { _ = reloader.server.Close(); _ = reloader.listener.Close() }()
	previous := CertificatePaths{PanelCert: result.CertPath, PanelKey: result.KeyPath, SubCert: result.CertPath, SubKey: result.KeyPath}
	settings := &certificateSettingsFake{paths: previous}
	svc := &CertificateService{settings: settings, reloader: reloader}
	if err := svc.ApplyAndReload(result); err == nil {
		t.Fatal("real occupied-port failure was hidden")
	}
	assertCertificateBytes(t, manager, oldCert, oldKey)
	if settings.paths != previous {
		t.Fatal("TLS settings were not restored")
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.ConnectionState().PeerCertificates[0].Subject.CommonName != "old.example.com" {
		t.Fatal("recovery did not serve old TLS certificate")
	}
}

func TestCertificateUnchangedIssueReconcilesWrongPaths(t *testing.T) {
	cert, key := certificateTestPair(t, "panel.example.com")
	_, result := newCertificateTransaction(t, cert, key, cert, key, true)
	settings := &certificateSettingsFake{}
	reloader := &certificateReloaderFake{certificate: result.CertPath, privateKey: result.KeyPath}
	svc := &CertificateService{settings: settings, reloader: reloader}
	if err := svc.ApplyAndReload(result); err != nil {
		t.Fatal(err)
	}
	if settings.paths.PanelCert != result.CertPath || settings.paths.SubKey != result.KeyPath || reloader.calls != 1 {
		t.Fatal("unchanged issue left incorrect settings")
	}
}

func TestCertificateRealListenerSuccessServesNewTLS(t *testing.T) {
	oldCert, oldKey := certificateTestPair(t, "old.example.com")
	cert, key := certificateTestPair(t, "panel.example.com")
	_, result := newCertificateTransaction(t, oldCert, oldKey, cert, key, false)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &liveCertificateReloader{server: &http.Server{}, listener: raw, cert: result.CertPath, key: result.KeyPath}
	defer func() { _ = r.server.Close(); _ = r.listener.Close() }()
	svc := &CertificateService{settings: &certificateSettingsFake{}, reloader: r}
	if err := svc.ApplyAndReload(result); err != nil {
		t.Fatal(err)
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", r.listener.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.ConnectionState().PeerCertificates[0].Subject.CommonName != "panel.example.com" {
		t.Fatal(errors.New("replacement listener serves incorrect certificate"))
	}
}
