package acme

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcmeRejectsUnsafeDomainsBeforeAnyIO(t *testing.T) {
	for _, domain := range []string{"../escape", "/absolute", `C:\escape`, `a\b.com`, "x/../a.com", "a.com\x00", "a.com\n", "a b.com", ".", "..", "a.com.meta/../x", "a.com.", "a..com", "-a.com", "a_.com", "127.0.0.1", "*.example.com"} {
		t.Run(domain, func(t *testing.T) {
			runner := newOfficialRunner(t, "panel.example.com")
			manager := newTestManager(t, runner)
			for _, mode := range []Mode{ModeHTTP01, ModeDNS01} {
				if _, err := manager.Issue(context.Background(), domain, mode, IssueOptions{}); !errors.Is(err, ErrInvalidDomain) {
					t.Fatalf("issue error = %v", err)
				}
			}
			if _, err := manager.Renew(context.Background(), domain); !errors.Is(err, ErrInvalidDomain) {
				t.Fatalf("renew: %v", err)
			}
			if _, err := manager.Status(domain); !errors.Is(err, ErrInvalidDomain) {
				t.Fatalf("status: %v", err)
			}
			if err := manager.SetCloudflareToken(domain, "generated-test-value"); !errors.Is(err, ErrInvalidDomain) {
				t.Fatalf("setup: %v", err)
			}
			if err := manager.SetMetadata(domain, ModeDNS01, ""); !errors.Is(err, ErrInvalidDomain) {
				t.Fatalf("metadata: %v", err)
			}
			if manager.CertPath(domain) != "" || manager.KeyPath(domain) != "" || manager.HasCloudflareToken(domain) {
				t.Fatal("unsafe lookup accepted")
			}
			if len(runner.calls) != 0 {
				t.Fatal("unsafe input invoked the ACME client")
			}
		})
	}
}

func TestAcmeCanonicalDomainsAndHostileEnumeration(t *testing.T) {
	manager := newTestManager(t, newOfficialRunner(t, "panel.example.com"))
	if err := manager.SetMetadata("Panel.Example.COM", ModeDNS01, ""); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(manager.accountHome, metaDir)
	for _, name := range []string{"bad name.meta", "a\\b.com.meta", "..meta"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("mode=dns-01\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "panel.example.com.meta"), filepath.Join(dir, "link.example.com.meta")); err != nil {
		t.Fatal(err)
	}
	domains, err := manager.Domains()
	if err != nil || len(domains) != 1 || domains[0] != "panel.example.com" {
		t.Fatalf("domains=%v err=%v", domains, err)
	}
	if got, err := NormalizeDomain("bücher.example"); err != nil || got != "xn--bcher-kva.example" {
		t.Fatalf("IDNA=%q %v", got, err)
	}
}

func TestAcmeRedactsUnknownAssignments(t *testing.T) {
	for _, input := range []string{"CF_Token=unrecognized", "export CF_Token='two words'", `CF_Token="two words"`, "CF_Token = unrecognized\nnext line", `CF_Token="escaped\"two words"`, "CF_Token='two words"} {
		output := Redact(input, "")
		if strings.Contains(output, "unrecognized") || strings.Contains(output, "two words") || !strings.Contains(output, RedactedSecret) {
			t.Fatalf("assignment not redacted")
		}
	}
}

func TestAcmeHTTPRenewalRefusesWithoutClientInvocation(t *testing.T) {
	runner := newOfficialRunner(t, "panel.example.com")
	manager := newTestManager(t, runner)
	if err := manager.SetMetadata("panel.example.com", ModeHTTP01, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Renew(context.Background(), "panel.example.com"); !errors.Is(err, ErrManualRenewal) {
		t.Fatalf("renew error=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("HTTP renewal invoked client")
	}
}

func TestAcmeSymlinkDestinationsNeverReachClientOrEscapeStorage(t *testing.T) {
	for _, target := range []string{"domain", "certificate", "key", "metadata", "token", "account"} {
		t.Run(target, func(t *testing.T) {
			runner := newOfficialRunner(t, "panel.example.com")
			m := newTestManager(t, runner)
			outside := t.TempDir()
			victim := filepath.Join(outside, "untouched")
			if err := os.WriteFile(victim, []byte("sentinel"), 0o600); err != nil {
				t.Fatal(err)
			}
			var link, destination string
			switch target {
			case "domain":
				link, destination = filepath.Dir(m.CertPath("panel.example.com")), outside
			case "certificate":
				link, destination = m.CertPath("panel.example.com"), victim
			case "key":
				link, destination = m.KeyPath("panel.example.com"), victim
			case "metadata":
				link, destination = m.metaPath("panel.example.com"), victim
			case "token":
				link, destination = m.tokenPath("panel.example.com"), victim
			case "account":
				link, destination = m.accountHome, outside
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Fatal(err)
			}
			_, err := m.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{})
			if !errors.Is(err, ErrUnsafePath) || len(runner.calls) != 0 {
				t.Fatalf("unsafe destination reached issuance: %v", err)
			}
			if target == "token" {
				if err := m.SetCloudflareToken("panel.example.com", "ephemeral-test-value"); !errors.Is(err, ErrUnsafePath) {
					t.Fatal("token followed symlink")
				}
			}
			content, err := os.ReadFile(victim)
			if err != nil || string(content) != "sentinel" {
				t.Fatal("outside data changed")
			}
		})
	}
}

func TestAcmeRenewalOutcomeSurvivesManagerReplacement(t *testing.T) {
	for _, outcome := range []string{"renewed", "unchanged", "failed", "rollback"} {
		t.Run(outcome, func(t *testing.T) {
			runner := newOfficialRunner(t, "panel.example.com")
			m := newTestManager(t, runner)
			first, err := m.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := first.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := m.SetMetadata("panel.example.com", ModeDNS01, ""); err != nil {
				t.Fatal(err)
			}
			if outcome == "renewed" {
				runner.renewCert, runner.renewKey = testCertificate(t, "panel.example.com")
			}
			if outcome == "failed" {
				runner.renewErr = errors.New("injected renewal failure")
			}
			result, renewErr := m.Renew(context.Background(), "panel.example.com")
			if outcome == "failed" {
				if renewErr == nil {
					t.Fatal("injected failure was lost")
				}
			} else {
				if renewErr != nil {
					t.Fatal(renewErr)
				}
				if outcome == "rollback" {
					err = result.Rollback()
				} else {
					err = result.Commit()
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			reopened := NewManager(Options{AccountHome: m.accountHome, CertRoot: m.certRoot, Runner: runner})
			status, err := reopened.Status("panel.example.com")
			expected := outcome
			if outcome == "rollback" {
				expected = "failed"
			}
			if err != nil || status.LastRenewal.Result != expected || status.LastRenewal.At.IsZero() {
				t.Fatalf("persisted outcome=%s err=%v", status.LastRenewal.Result, err)
			}
		})
	}
}

func TestAcmeConcurrentInstallationRefusesUntilTransactionFinishes(t *testing.T) {
	runner := newOfficialRunner(t, "panel.example.com")
	m := newTestManager(t, runner)
	first, err := m.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	other := NewManager(Options{AccountHome: m.accountHome, CertRoot: m.certRoot, Runner: runner})
	_, err = other.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{})
	if err == nil || !strings.Contains(err.Error(), "another certificate installation is in progress") {
		t.Fatalf("concurrent installation=%v", err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	second, err := other.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestAcmeFailedOutcomeCommitRetainsRollbackAndReleasesLock(t *testing.T) {
	runner := newOfficialRunner(t, "panel.example.com")
	m := newTestManager(t, runner)
	first, err := m.Issue(context.Background(), "panel.example.com", ModeHTTP01, IssueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := m.SetMetadata("panel.example.com", ModeDNS01, ""); err != nil {
		t.Fatal(err)
	}
	result, err := m.Renew(context.Background(), "panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer result.Rollback()
	path := m.renewalPath("panel.example.com")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := result.Commit(); err == nil || !strings.Contains(err.Error(), "persist renewal result") {
		t.Fatalf("commit error=%v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := result.Rollback(); err != nil {
		t.Fatal(err)
	}
	status, err := m.Status("panel.example.com")
	if err != nil || status.LastRenewal.Result != "failed" {
		t.Fatalf("rollback outcome=%s err=%v", status.LastRenewal.Result, err)
	}
	next, err := m.Renew(context.Background(), "panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Commit(); err != nil {
		t.Fatal(err)
	}
}
