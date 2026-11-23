package job

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/acme"
)

type renewalManagerFake struct {
	toolPath string
	domains  []string
	metadata map[string]acme.Meta
	results  map[string]*acme.InstallResult
	errs     map[string]error
	renewed  []string
}

func (m *renewalManagerFake) ToolPath() string { return m.toolPath }

func (m *renewalManagerFake) Domains() ([]string, error) {
	return slices.Clone(m.domains), nil
}

func (m *renewalManagerFake) Meta(domain string) (acme.Meta, bool) {
	meta, ok := m.metadata[domain]
	return meta, ok
}

func (m *renewalManagerFake) Renew(_ context.Context, domain string) (*acme.InstallResult, error) {
	m.renewed = append(m.renewed, domain)
	if err := m.errs[domain]; err != nil {
		return nil, err
	}
	return m.results[domain], nil
}

type recordingApplier struct {
	results []*acme.InstallResult
	err     error
}

func (a *recordingApplier) ApplyAndReload(result *acme.InstallResult) error {
	a.results = append(a.results, result)
	return a.err
}

func newRenewalManagerFake(t *testing.T) *renewalManagerFake {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "acme.sh")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write tool placeholder: %v", err)
	}
	return &renewalManagerFake{
		toolPath: tool,
		domains:  []string{"auto.example.com", "manual.example.com"},
		metadata: map[string]acme.Meta{
			"auto.example.com":   {Mode: acme.ModeDNS01},
			"manual.example.com": {Mode: acme.ModeHTTP01},
		},
		results: map[string]*acme.InstallResult{
			"auto.example.com": {Changed: true, CertPath: "/root/cert/auto.example.com/fullchain.pem"},
		},
		errs: make(map[string]error),
	}
}

func TestRenewalJobOnlyRenewsDNS01AndUsesSharedApplyFlow(t *testing.T) {
	manager := newRenewalManagerFake(t)
	applier := &recordingApplier{}

	NewAcmeRenewJobWith(manager, applier).Run()

	if !slices.Equal(manager.renewed, []string{"auto.example.com"}) {
		t.Fatalf("renewed domains = %v", manager.renewed)
	}
	if len(applier.results) != 1 || applier.results[0] != manager.results["auto.example.com"] {
		t.Fatalf("shared apply calls = %v", applier.results)
	}
}

func TestRenewalJobSkipsUnchangedCertificate(t *testing.T) {
	manager := newRenewalManagerFake(t)
	manager.results["auto.example.com"].Changed = false
	applier := &recordingApplier{}

	NewAcmeRenewJobWith(manager, applier).Run()

	if len(applier.results) != 0 {
		t.Fatalf("an unchanged renewal reached the apply/reload flow: %v", applier.results)
	}
}

func TestRenewalJobDoesNotRetryFailedApply(t *testing.T) {
	manager := newRenewalManagerFake(t)
	applier := &recordingApplier{err: errors.New("injected apply/reload failure")}

	NewAcmeRenewJobWith(manager, applier).Run()

	if len(applier.results) != 1 {
		t.Fatalf("apply calls = %d, want exactly one", len(applier.results))
	}
}

func TestRenewalJobDoesNotApplyFailedRenewal(t *testing.T) {
	manager := newRenewalManagerFake(t)
	manager.errs["auto.example.com"] = errors.New("injected renewal failure")
	applier := &recordingApplier{}

	NewAcmeRenewJobWith(manager, applier).Run()

	if len(applier.results) != 0 {
		t.Fatalf("a failed renewal reached the apply/reload flow: %v", applier.results)
	}
}

func TestRenewalJobDoesNothingWithoutBundledClient(t *testing.T) {
	manager := newRenewalManagerFake(t)
	manager.toolPath = filepath.Join(t.TempDir(), "missing-acme.sh")
	applier := &recordingApplier{}

	NewAcmeRenewJobWith(manager, applier).Run()

	if len(manager.renewed) != 0 || len(applier.results) != 0 {
		t.Fatalf("missing client caused renewals %v and applies %v", manager.renewed, applier.results)
	}
}
