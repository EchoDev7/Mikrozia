package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type installDestination struct {
	cert string
	key  string
}

type officialRunner struct {
	certPEM []byte
	keyPEM  []byte

	calls [][]string
	env   [][]string
	saved installDestination

	failCommand string
	partial     bool
	runErr      error
	output      string
	installs    int

	invalidStage bool
	invalidFinal bool
	renewErr     error
	renewCert    []byte
	renewKey     []byte
}

func (r *officialRunner) Run(_ context.Context, args []string, env []string) (string, error) {
	r.calls = append(r.calls, slices.Clone(args))
	r.env = append(r.env, slices.Clone(env))
	if len(args) == 0 {
		return "", errors.New("missing acme.sh command")
	}
	command := args[0]
	switch command {
	case "--issue":
		destination, err := destinationFromArgs(args)
		if err != nil {
			return "", err
		}
		r.saved = destination
		home := argumentValue(args, "--home")
		domain := argumentValue(args, "-d")
		if err := os.MkdirAll(filepath.Join(home, domain+"_ecc"), 0o700); err != nil {
			return "", err
		}
		if r.failed(command) {
			if r.partial {
				_ = os.WriteFile(destination.cert, r.certPEM, 0o600)
			}
			return r.output, r.failure()
		}
		cert, key := r.certPEM, r.keyPEM
		if r.invalidStage {
			cert = []byte("not a certificate")
		}
		return r.output, writeFakePair(destination, cert, key)
	case "--install-cert":
		r.installs++
		destination, err := destinationFromArgs(args)
		if err != nil {
			return "", err
		}
		r.saved = destination
		command = fmt.Sprintf("install-%d", r.installs)
		if r.failed(command) {
			if r.partial {
				_ = os.MkdirAll(filepath.Dir(destination.cert), 0o700)
				_ = os.WriteFile(destination.cert, r.certPEM, 0o600)
			}
			return r.output, r.failure()
		}
		cert, key := r.certPEM, r.keyPEM
		if r.invalidFinal && r.installs == 1 {
			key = []byte("not a private key")
		}
		return r.output, writeFakePair(destination, cert, key)
	case "--renew":
		if r.renewErr != nil {
			return r.output, r.renewErr
		}
		if r.renewCert != nil {
			r.certPEM = r.renewCert
			r.keyPEM = r.renewKey
		}
		return r.output, writeFakePair(r.saved, r.certPEM, r.keyPEM)
	default:
		return "", fmt.Errorf("unexpected acme.sh command %q", command)
	}
}

func (r *officialRunner) failed(command string) bool {
	return r.failCommand == command
}

func (r *officialRunner) failure() error {
	if r.runErr != nil {
		return r.runErr
	}
	return errors.New("injected acme.sh failure")
}

func destinationFromArgs(args []string) (installDestination, error) {
	destination := installDestination{
		cert: argumentValue(args, "--fullchain-file"),
		key:  argumentValue(args, "--key-file"),
	}
	if destination.cert == "" || destination.key == "" {
		return installDestination{}, errors.New("official install destinations were not supplied")
	}
	return destination, nil
}

func argumentValue(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func writeFakePair(destination installDestination, cert, key []byte) error {
	if err := os.MkdirAll(filepath.Dir(destination.cert), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(destination.cert, cert, 0o600); err != nil {
		return err
	}
	return os.WriteFile(destination.key, key, 0o600)
}

type fakeExitError int

func (e fakeExitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e fakeExitError) ExitCode() int { return int(e) }

func testCertificate(t *testing.T, commonName string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		DNSNames:     []string{commonName},
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

func newTestManager(t *testing.T, runner Runner) *Manager {
	t.Helper()
	root := t.TempDir()
	tool := filepath.Join(root, "acme.sh")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write tool placeholder: %v", err)
	}
	return NewManager(Options{
		ToolPath:    tool,
		AccountHome: filepath.Join(root, "account"),
		CertRoot:    filepath.Join(root, "cert"),
		Runner:      runner,
	})
}

func newOfficialRunner(t *testing.T, domain string) *officialRunner {
	t.Helper()
	cert, key := testCertificate(t, domain)
	return &officialRunner{certPEM: cert, keyPEM: key}
}

func seedInstalledPair(t *testing.T, manager *Manager, domain string, cert, key []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(manager.CertPath(domain)), 0o700); err != nil {
		t.Fatalf("create certificate directory: %v", err)
	}
	if err := os.WriteFile(manager.CertPath(domain), cert, 0o644); err != nil {
		t.Fatalf("seed certificate: %v", err)
	}
	if err := os.WriteFile(manager.KeyPath(domain), key, 0o600); err != nil {
		t.Fatalf("seed private key: %v", err)
	}
}

func assertInstalledPair(t *testing.T, manager *Manager, domain string, cert, key []byte) {
	t.Helper()
	gotCert, err := os.ReadFile(manager.CertPath(domain))
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	gotKey, err := os.ReadFile(manager.KeyPath(domain))
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	if !slices.Equal(gotCert, cert) || !slices.Equal(gotKey, key) {
		t.Fatal("the active certificate pair differs from the expected bytes")
	}
}

func TestIssueUsesOfficialInstallForAnECCPair(t *testing.T) {
	runner := newOfficialRunner(t, "panel.example.com")
	manager := newTestManager(t, runner)

	result, err := manager.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	defer result.Commit()
	if !result.Changed || result.NotAfter.IsZero() {
		t.Fatalf("unexpected installation result: %+v", result)
	}
	if len(runner.calls) != 2 || runner.calls[0][0] != "--issue" || runner.calls[1][0] != "--install-cert" {
		t.Fatalf("expected issue followed by official installation, got %v", runner.calls)
	}
	stage, err := destinationFromArgs(runner.calls[0])
	if err != nil {
		t.Fatalf("issue destinations: %v", err)
	}
	if stage.cert == result.CertPath || stage.key == result.KeyPath {
		t.Fatal("issuance must stage and validate the pair before replacing active files")
	}
	if got := argumentValue(runner.calls[1], "--fullchain-file"); got != result.CertPath {
		t.Fatalf("final fullchain path = %q, want %q", got, result.CertPath)
	}
	if got := argumentValue(runner.calls[1], "--key-file"); got != result.KeyPath {
		t.Fatalf("final key path = %q, want %q", got, result.KeyPath)
	}
	assertInstalledPair(t, manager, "panel.example.com", runner.certPEM, runner.keyPEM)

	certInfo, err := os.Stat(result.CertPath)
	if err != nil {
		t.Fatalf("stat certificate: %v", err)
	}
	keyInfo, err := os.Stat(result.KeyPath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if certInfo.Mode().Perm() != 0o644 || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("pair modes = %o/%o, want 644/600", certInfo.Mode().Perm(), keyInfo.Mode().Perm())
	}
	for _, dir := range []string{
		filepath.Join(manager.AccountHome(), "panel.example.com"),
		filepath.Join(manager.AccountHome(), "panel.example.com_ecc"),
	} {
		for _, name := range []string{"panel.example.com.cer", "panel.example.com.key"} {
			if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Fatalf("Mikrozia must not read the guessed source %s", filepath.Join(dir, name))
			}
		}
	}
}

func TestInstallationFailuresRestoreTheExactPriorPair(t *testing.T) {
	for _, tc := range []struct {
		name         string
		configure    func(*officialRunner)
		wantInstalls int
	}{
		{
			name: "failure after certificate staging",
			configure: func(r *officialRunner) {
				r.failCommand = "--issue"
				r.partial = true
			},
		},
		{
			name: "staged pair validation",
			configure: func(r *officialRunner) {
				r.invalidStage = true
			},
		},
		{
			name: "failure during final key installation",
			configure: func(r *officialRunner) {
				r.failCommand = "install-1"
				r.partial = true
			},
			wantInstalls: 1,
		},
		{
			name: "active pair validation",
			configure: func(r *officialRunner) {
				r.invalidFinal = true
			},
			wantInstalls: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := newOfficialRunner(t, "new-panel.example.com")
			manager := newTestManager(t, runner)
			oldCert, oldKey := testCertificate(t, "old-panel.example.com")
			seedInstalledPair(t, manager, "panel.example.com", oldCert, oldKey)
			tc.configure(runner)

			if _, err := manager.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{}); err == nil {
				t.Fatal("the injected installation failure must be returned")
			}
			assertInstalledPair(t, manager, "panel.example.com", oldCert, oldKey)
			if runner.installs != tc.wantInstalls {
				t.Fatalf("install calls = %d, want %d", runner.installs, tc.wantInstalls)
			}
		})
	}
}

func TestRenewalUsesSavedStagingDestinationsThenStablePaths(t *testing.T) {
	runner := newOfficialRunner(t, "panel.example.com")
	manager := newTestManager(t, runner)
	first, err := manager.Issue(context.Background(), "panel.example.com", ModeDNS01, IssueOptions{CloudflareToken: "cf-token"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	first.Commit()

	runner.renewCert, runner.renewKey = testCertificate(t, "panel.example.com")
	result, err := manager.Renew(context.Background(), "panel.example.com")
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	defer result.Commit()
	if !result.Changed {
		t.Fatal("a renewed pair must be reported as changed")
	}
	assertInstalledPair(t, manager, "panel.example.com", runner.renewCert, runner.renewKey)

	calls := runner.calls[len(runner.calls)-3:]
	if calls[0][0] != "--install-cert" || calls[1][0] != "--renew" || calls[2][0] != "--install-cert" {
		t.Fatalf("unexpected renewal sequence: %v", calls)
	}
	stage, err := destinationFromArgs(calls[0])
	if err != nil {
		t.Fatalf("renewal staging destination: %v", err)
	}
	if stage.cert == result.CertPath || stage.key == result.KeyPath {
		t.Fatal("renewal must save staging destinations before invoking acme.sh --renew")
	}
	if argumentValue(calls[1], "--fullchain-file") != "" || argumentValue(calls[1], "--key-file") != "" {
		t.Fatal("acme.sh --renew uses saved install paths; unsupported renew flags must not be invented")
	}
	if argumentValue(calls[2], "--fullchain-file") != result.CertPath || argumentValue(calls[2], "--key-file") != result.KeyPath {
		t.Fatal("renewal must finish by saving the stable active install paths")
	}
}

func TestSkippedRenewalIsUnchangedAndPreservesThePair(t *testing.T) {
	runner := newOfficialRunner(t, "panel.example.com")
	manager := newTestManager(t, runner)
	first, err := manager.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	first.Commit()
	if err := manager.SetMetadata("panel.example.com", ModeDNS01, ""); err != nil {
		t.Fatal(err)
	}
	runner.renewErr = fakeExitError(2)

	result, err := manager.Renew(context.Background(), "panel.example.com")
	if err != nil {
		t.Fatalf("a not-due renewal must be a successful no-op: %v", err)
	}
	defer result.Commit()
	if result.Changed {
		t.Fatal("a skipped renewal must not report a change")
	}
	assertInstalledPair(t, manager, "panel.example.com", runner.certPEM, runner.keyPEM)
}

func TestFailedRenewalRestoresTheExactPriorPair(t *testing.T) {
	runner := newOfficialRunner(t, "panel.example.com")
	manager := newTestManager(t, runner)
	first, err := manager.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	first.Commit()
	if err := manager.SetMetadata("panel.example.com", ModeDNS01, ""); err != nil {
		t.Fatal(err)
	}
	oldCert, oldKey := runner.certPEM, runner.keyPEM
	runner.renewCert, runner.renewKey = testCertificate(t, "panel.example.com")
	runner.failCommand = "install-3"
	runner.partial = true

	if _, err := manager.Renew(context.Background(), "panel.example.com"); err == nil {
		t.Fatal("the final renewal installation failure must be returned")
	}
	assertInstalledPair(t, manager, "panel.example.com", oldCert, oldKey)
}

func TestDNS01IssuanceRequiresAToken(t *testing.T) {
	manager := newTestManager(t, &officialRunner{})
	_, err := manager.Issue(context.Background(), "panel.example.com", ModeDNS01, IssueOptions{})
	if !errors.Is(err, ErrTokenRequired) {
		t.Fatalf("expected ErrTokenRequired, got %v", err)
	}
}

func TestCloudflareTokenUsesEnvironmentOnlyAndIsRedacted(t *testing.T) {
	const token = "cf-token-value-that-must-not-leak"
	runner := newOfficialRunner(t, "panel.example.com")
	manager := newTestManager(t, runner)
	if _, err := manager.Issue(context.Background(), "panel.example.com", ModeDNS01, IssueOptions{CloudflareToken: token}); err != nil {
		t.Fatalf("issue: %v", err)
	}
	found := false
	for i, args := range runner.calls {
		for _, arg := range args {
			if strings.Contains(arg, token) {
				t.Fatalf("token appeared in process arguments: %v", args)
			}
		}
		for _, entry := range runner.env[i] {
			if entry == "CF_Token="+token {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("CF_Token was not passed through the child environment")
	}
	status, err := manager.Status("panel.example.com")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.TokenConfigured || strings.Contains(fmt.Sprintf("%+v", status), token) {
		t.Fatalf("status exposes a token or omits its configured state: %+v", status)
	}

	failing := newOfficialRunner(t, "panel.example.com")
	failing.failCommand = "--issue"
	failing.runErr = errors.New("server rejected CF_Token=" + token)
	failing.output = "output CF_Token=" + token
	manager = newTestManager(t, failing)
	_, err = manager.Issue(context.Background(), "panel.example.com", ModeDNS01, IssueOptions{CloudflareToken: token})
	if err == nil || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), RedactedSecret) {
		t.Fatalf("token redaction failed: %v", err)
	}
}

func TestDomainsAndStatusUsePersistentMetadataNotECCAccountLayout(t *testing.T) {
	runner := newOfficialRunner(t, "panel.example.com")
	manager := newTestManager(t, runner)
	if _, err := manager.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{}); err != nil {
		t.Fatalf("issue: %v", err)
	}
	domains, err := manager.Domains()
	if err != nil {
		t.Fatalf("domains: %v", err)
	}
	if !slices.Equal(domains, []string{"panel.example.com"}) {
		t.Fatalf("domains = %v", domains)
	}
	status, err := manager.Status("panel.example.com")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.AutoRenew || !status.Installed || status.Mode != ModeHTTP01 {
		t.Fatalf("unexpected HTTP-01 status: %+v", status)
	}
}
