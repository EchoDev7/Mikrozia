package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/acme"
	"github.com/echodev7/mikrozia/v3/internal/routeros"
)

type fakeACMEManager struct {
	issueErr  error
	issueRes  *acme.InstallResult
	issueCall int
	lastMode  acme.Mode
	metaByDom map[string]acme.Meta
}

func (f *fakeACMEManager) Issue(_ context.Context, _ string, mode acme.Mode, _ acme.IssueOptions) (*acme.InstallResult, error) {
	f.issueCall++
	f.lastMode = mode
	if f.issueErr != nil {
		return nil, f.issueErr
	}
	return f.issueRes, nil
}

func (f *fakeACMEManager) Renew(_ context.Context, _ string) (*acme.InstallResult, error) {
	return f.issueRes, f.issueErr
}

func (f *fakeACMEManager) Statuses() ([]acme.DomainStatus, error) {
	return []acme.DomainStatus{}, nil
}

func (f *fakeACMEManager) Meta(domain string) (acme.Meta, bool) {
	m, ok := f.metaByDom[domain]
	return m, ok
}

type fakeACMEApplier struct {
	applied    int
	lastTarget CertificateTarget
	err        error
}

func (f *fakeACMEApplier) ApplyAndReload(_ *acme.InstallResult) error {
	f.applied++
	f.lastTarget = CertificateTargetBoth
	return f.err
}

func (f *fakeACMEApplier) ApplyAndReloadTarget(_ *acme.InstallResult, target CertificateTarget) error {
	f.applied++
	f.lastTarget = target
	return f.err
}

type fakePort80Ctrl struct {
	openCalled  int
	closeCalled int
	openOK      bool
	openState   string
	openDetail  string
	closeState  string
}

func (f *fakePort80Ctrl) Open(_ context.Context, _ routeros.NATConnConfig, _, _ string) (bool, string, string) {
	f.openCalled++
	return f.openOK, f.openState, f.openDetail
}

func (f *fakePort80Ctrl) Close(_ context.Context, _ routeros.NATConnConfig) (int, string, string) {
	f.closeCalled++
	if f.closeState == "" {
		return 1, "ok", ""
	}
	return 0, f.closeState, "failed"
}

func TestACMEPanelServiceIssuePort80Lifecycle(t *testing.T) {
	setTestRouterOSConfig(t, RouterOSAPIConfig{
		State:    "connecting",
		Host:     "172.16.10.1",
		Port:     8728,
		Username: "mikrozia-nat",
		Password: "secret",
	})

	t.Run("opens port 80, issues cert, applies reload, and closes port 80", func(t *testing.T) {
		mgr := &fakeACMEManager{
			issueRes: &acme.InstallResult{
				CertPath: "/root/cert/panel.example.com/fullchain.pem",
				KeyPath:  "/root/cert/panel.example.com/privkey.pem",
				Changed:  true,
				NotAfter: time.Unix(1800000000, 0),
			},
		}
		applier := &fakeACMEApplier{}
		p80 := &fakePort80Ctrl{openOK: true, openState: "ok"}
		svc := &ACMEPanelService{manager: mgr, applier: applier, port80: p80}

		resp, err := svc.Issue(context.Background(), ACMEIssueRequest{
			Domain:         "panel.example.com",
			Mode:           "http-01",
			AutoOpenPort80: true,
		})
		if err != nil {
			t.Fatalf("Issue() error = %v", err)
		}
		if p80.openCalled != 1 || p80.closeCalled != 1 {
			t.Fatalf("expected open=1 close=1, got open=%d close=%d", p80.openCalled, p80.closeCalled)
		}
		if applier.applied != 1 || resp.CertPath != "/root/cert/panel.example.com/fullchain.pem" || !resp.Port80AutoManaged || !resp.Port80Closed {
			t.Fatalf("unexpected response: %+v (applied=%d)", resp, applier.applied)
		}
	})

	t.Run("closes port 80 even when ACME issuance fails", func(t *testing.T) {
		mgr := &fakeACMEManager{issueErr: errors.New("challenge failed")}
		applier := &fakeACMEApplier{}
		p80 := &fakePort80Ctrl{openOK: true, openState: "ok"}
		svc := &ACMEPanelService{manager: mgr, applier: applier, port80: p80}

		_, err := svc.Issue(context.Background(), ACMEIssueRequest{
			Domain:         "panel.example.com",
			Mode:           "http-01",
			AutoOpenPort80: true,
		})
		if err == nil {
			t.Fatal("expected error from failed issuance")
		}
		if p80.openCalled != 1 || p80.closeCalled != 1 || applier.applied != 0 {
			t.Fatalf("expected open=1 close=1 applied=0, got open=%d close=%d applied=%d", p80.openCalled, p80.closeCalled, applier.applied)
		}
	})

	t.Run("aborts before running acme.sh when port 80 has a RouterOS conflict", func(t *testing.T) {
		mgr := &fakeACMEManager{}
		applier := &fakeACMEApplier{}
		p80 := &fakePort80Ctrl{openOK: false, openState: "conflict", openDetail: "www on port 80"}
		svc := &ACMEPanelService{manager: mgr, applier: applier, port80: p80}

		_, err := svc.Issue(context.Background(), ACMEIssueRequest{
			Domain:         "panel.example.com",
			Mode:           "http-01",
			AutoOpenPort80: true,
		})
		if err == nil {
			t.Fatal("expected conflict error")
		}
		if mgr.issueCall != 0 || p80.closeCalled != 0 {
			t.Fatalf("expected issueCall=0 closeCalled=0 on pre-flight conflict, got %d / %d", mgr.issueCall, p80.closeCalled)
		}
	})

	t.Run("dns-01 never touches port 80", func(t *testing.T) {
		mgr := &fakeACMEManager{
			issueRes: &acme.InstallResult{
				CertPath: "/root/cert/panel.example.com/fullchain.pem",
				KeyPath:  "/root/cert/panel.example.com/privkey.pem",
				Changed:  true,
			},
		}
		applier := &fakeACMEApplier{}
		p80 := &fakePort80Ctrl{openOK: true, openState: "ok"}
		svc := &ACMEPanelService{manager: mgr, applier: applier, port80: p80}

		resp, err := svc.Issue(context.Background(), ACMEIssueRequest{
			Domain:          "panel.example.com",
			Mode:            "dns-01",
			CloudflareToken: "cf-test-token",
			AutoOpenPort80:  true,
		})
		if err != nil {
			t.Fatalf("Issue(dns-01) error = %v", err)
		}
		if p80.openCalled != 0 || p80.closeCalled != 0 || resp.Port80AutoManaged {
			t.Fatalf("dns-01 must not open port 80: open=%d close=%d resp=%+v", p80.openCalled, p80.closeCalled, resp)
		}
	})

	t.Run("sub-only target passes CertificateTargetSub without touching panel", func(t *testing.T) {
		mgr := &fakeACMEManager{
			issueRes: &acme.InstallResult{
				CertPath: "/root/cert/sub.example.com/fullchain.pem",
				KeyPath:  "/root/cert/sub.example.com/privkey.pem",
				Changed:  true,
			},
		}
		applier := &fakeACMEApplier{}
		svc := &ACMEPanelService{manager: mgr, applier: applier}

		resp, err := svc.Issue(context.Background(), ACMEIssueRequest{
			Domain:          "sub.example.com",
			Mode:            "dns-01",
			Target:          "sub",
			CloudflareToken: "cf-test-token",
		})
		if err != nil {
			t.Fatalf("Issue(sub) error = %v", err)
		}
		if applier.lastTarget != CertificateTargetSub || resp.Target != "sub" {
			t.Fatalf("expected target=sub, got applier=%q resp=%q", applier.lastTarget, resp.Target)
		}
	})
}
