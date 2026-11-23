package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/acme"
)

type certificateTestRunner struct {
	cert []byte
	key  []byte
}

func (r *certificateTestRunner) Run(_ context.Context, args []string, _ []string) (string, error) {
	certPath := certificateTestArgument(args, "--fullchain-file")
	keyPath := certificateTestArgument(args, "--key-file")
	if certPath == "" || keyPath == "" {
		return "", errors.New("missing official install destination")
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(certPath, r.cert, 0o600); err != nil {
		return "", err
	}
	return "", os.WriteFile(keyPath, r.key, 0o600)
}

func certificateTestArgument(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func certificateTestPair(t *testing.T, domain string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{domain},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

type certificateSettingsFake struct {
	paths       CertificatePaths
	getErr      error
	setErr      error
	setFailures int
	setCalls    int
}

func (s *certificateSettingsFake) GetCertificatePaths() (CertificatePaths, error) {
	return s.paths, s.getErr
}

func (s *certificateSettingsFake) SetCertificatePaths(paths CertificatePaths) error {
	s.setCalls++
	s.paths = paths
	if s.setFailures > 0 {
		s.setFailures--
		return s.setErr
	}
	return nil
}

type certificateReloaderFake struct {
	calls        int
	failures     int
	err          error
	observedCert []byte
	observedKey  []byte
	certificate  string
	privateKey   string
}

func (r *certificateReloaderFake) Reload() error {
	r.calls++
	r.observedCert, _ = os.ReadFile(r.certificate)
	r.observedKey, _ = os.ReadFile(r.privateKey)
	if r.failures > 0 {
		r.failures--
		return r.err
	}
	return nil
}

func newCertificateTransaction(t *testing.T, oldCert, oldKey, newCert, newKey []byte, unchanged bool) (*acme.Manager, *acme.InstallResult) {
	t.Helper()
	root := t.TempDir()
	tool := filepath.Join(root, "acme.sh")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write tool placeholder: %v", err)
	}
	manager := acme.NewManager(acme.Options{
		ToolPath:    tool,
		AccountHome: filepath.Join(root, "account"),
		CertRoot:    filepath.Join(root, "cert"),
		Runner:      &certificateTestRunner{cert: newCert, key: newKey},
	})
	if err := os.MkdirAll(filepath.Dir(manager.CertPath("panel.example.com")), 0o700); err != nil {
		t.Fatalf("create certificate directory: %v", err)
	}
	initialCert, initialKey := oldCert, oldKey
	if unchanged {
		initialCert, initialKey = newCert, newKey
	}
	if err := os.WriteFile(manager.CertPath("panel.example.com"), initialCert, 0o644); err != nil {
		t.Fatalf("seed certificate: %v", err)
	}
	if err := os.WriteFile(manager.KeyPath("panel.example.com"), initialKey, 0o600); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	result, err := manager.Issue(context.Background(), "panel.example.com", acme.ModeHTTP01, acme.IssueOptions{})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return manager, result
}

func previousCertificatePaths() CertificatePaths {
	return CertificatePaths{
		PanelCert: "/previous/panel-fullchain.pem",
		PanelKey:  "/previous/panel-privkey.pem",
		SubCert:   "/previous/sub-fullchain.pem",
		SubKey:    "/previous/sub-privkey.pem",
	}
}

func assertCertificateBytes(t *testing.T, manager *acme.Manager, cert, key []byte) {
	t.Helper()
	gotCert, err := os.ReadFile(manager.CertPath("panel.example.com"))
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	gotKey, err := os.ReadFile(manager.KeyPath("panel.example.com"))
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	if !slices.Equal(gotCert, cert) || !slices.Equal(gotKey, key) {
		t.Fatal("the exact prior pair was not restored")
	}
}

func TestCertificateApplyAndReloadCommitsOnce(t *testing.T) {
	oldCert, oldKey := certificateTestPair(t, "old.example.com")
	newCert, newKey := certificateTestPair(t, "panel.example.com")
	manager, result := newCertificateTransaction(t, oldCert, oldKey, newCert, newKey, false)
	settings := &certificateSettingsFake{paths: previousCertificatePaths()}
	reloader := &certificateReloaderFake{certificate: result.CertPath, privateKey: result.KeyPath}
	service := &CertificateService{settings: settings, reloader: reloader}

	if err := service.ApplyAndReload(result); err != nil {
		t.Fatalf("apply and reload: %v", err)
	}
	if reloader.calls != 1 {
		t.Fatalf("reload calls = %d, want 1", reloader.calls)
	}
	if !slices.Equal(reloader.observedCert, newCert) || !slices.Equal(reloader.observedKey, newKey) {
		t.Fatal("the listeners reloaded before the new pair was ready")
	}
	want := CertificatePaths{PanelCert: result.CertPath, PanelKey: result.KeyPath, SubCert: result.CertPath, SubKey: result.KeyPath}
	if settings.paths != want || settings.setCalls != 1 {
		t.Fatalf("settings = %+v after %d writes, want %+v after one write", settings.paths, settings.setCalls, want)
	}
	assertCertificateBytes(t, manager, newCert, newKey)
}

func TestCertificateSettingsFailureRestoresPairAndAllSettingsWithoutReload(t *testing.T) {
	oldCert, oldKey := certificateTestPair(t, "old.example.com")
	newCert, newKey := certificateTestPair(t, "panel.example.com")
	manager, result := newCertificateTransaction(t, oldCert, oldKey, newCert, newKey, false)
	previous := previousCertificatePaths()
	settings := &certificateSettingsFake{paths: previous, setErr: errors.New("injected settings failure"), setFailures: 1}
	reloader := &certificateReloaderFake{}
	service := &CertificateService{settings: settings, reloader: reloader}

	if err := service.ApplyAndReload(result); err == nil {
		t.Fatal("the settings failure must be returned")
	}
	if reloader.calls != 0 {
		t.Fatalf("a settings failure must not reload, got %d calls", reloader.calls)
	}
	if settings.paths != previous {
		t.Fatalf("settings = %+v, want exact previous %+v", settings.paths, previous)
	}
	assertCertificateBytes(t, manager, oldCert, oldKey)
}

func TestCertificateReadFailureRestoresPairWithoutTouchingSettings(t *testing.T) {
	oldCert, oldKey := certificateTestPair(t, "old.example.com")
	newCert, newKey := certificateTestPair(t, "panel.example.com")
	manager, result := newCertificateTransaction(t, oldCert, oldKey, newCert, newKey, false)
	settings := &certificateSettingsFake{paths: previousCertificatePaths(), getErr: errors.New("injected read failure")}
	reloader := &certificateReloaderFake{}
	service := &CertificateService{settings: settings, reloader: reloader}

	if err := service.ApplyAndReload(result); err == nil {
		t.Fatal("the settings read failure must be returned")
	}
	if settings.setCalls != 0 || reloader.calls != 0 {
		t.Fatalf("settings writes = %d, reloads = %d; both must be zero", settings.setCalls, reloader.calls)
	}
	assertCertificateBytes(t, manager, oldCert, oldKey)
}

func TestCertificateReloadFailureRestoresPairAndSettingsAndReportsFailure(t *testing.T) {
	oldCert, oldKey := certificateTestPair(t, "old.example.com")
	newCert, newKey := certificateTestPair(t, "panel.example.com")
	manager, result := newCertificateTransaction(t, oldCert, oldKey, newCert, newKey, false)
	previous := previousCertificatePaths()
	settings := &certificateSettingsFake{paths: previous}
	reloader := &certificateReloaderFake{
		certificate: result.CertPath,
		privateKey:  result.KeyPath,
		err:         errors.New("injected reload failure"),
		failures:    1,
	}
	service := &CertificateService{settings: settings, reloader: reloader}

	err := service.ApplyAndReload(result)
	if err == nil || !errors.Is(err, reloader.err) {
		t.Fatalf("reload failure = %v, want the injected error", err)
	}
	if reloader.calls != 2 {
		t.Fatalf("reload calls = %d, want failed reload plus recovery reload", reloader.calls)
	}
	if !slices.Equal(reloader.observedCert, oldCert) || !slices.Equal(reloader.observedKey, oldKey) {
		t.Fatal("recovery reload did not observe the restored pair")
	}
	if settings.paths != previous {
		t.Fatalf("settings = %+v, want exact previous %+v", settings.paths, previous)
	}
	assertCertificateBytes(t, manager, oldCert, oldKey)
}

func TestUnchangedCertificateDoesNotWriteSettingsOrReload(t *testing.T) {
	oldCert, oldKey := certificateTestPair(t, "old.example.com")
	newCert, newKey := certificateTestPair(t, "panel.example.com")
	manager, result := newCertificateTransaction(t, oldCert, oldKey, newCert, newKey, true)
	result.Reconcile = false
	settings := &certificateSettingsFake{paths: previousCertificatePaths()}
	reloader := &certificateReloaderFake{}
	service := &CertificateService{settings: settings, reloader: reloader}

	if result.Changed {
		t.Fatal("test setup must produce an unchanged result")
	}
	if err := service.ApplyAndReload(result); err != nil {
		t.Fatalf("unchanged apply: %v", err)
	}
	if settings.setCalls != 0 || reloader.calls != 0 {
		t.Fatalf("settings writes = %d, reloads = %d; both must be zero", settings.setCalls, reloader.calls)
	}
	assertCertificateBytes(t, manager, newCert, newKey)
}

func TestValidationFailureAtApplyRestoresThePriorPair(t *testing.T) {
	oldCert, oldKey := certificateTestPair(t, "old.example.com")
	newCert, newKey := certificateTestPair(t, "panel.example.com")
	manager, result := newCertificateTransaction(t, oldCert, oldKey, newCert, newKey, false)
	if err := os.WriteFile(result.CertPath, []byte("corrupted after installation"), 0o644); err != nil {
		t.Fatalf("corrupt certificate: %v", err)
	}
	settings := &certificateSettingsFake{paths: previousCertificatePaths()}
	reloader := &certificateReloaderFake{}
	service := &CertificateService{settings: settings, reloader: reloader}

	if err := service.ApplyAndReload(result); err == nil {
		t.Fatal("the validation failure must be returned")
	}
	if settings.setCalls != 0 || reloader.calls != 0 {
		t.Fatalf("settings writes = %d, reloads = %d; both must be zero", settings.setCalls, reloader.calls)
	}
	assertCertificateBytes(t, manager, oldCert, oldKey)
}
