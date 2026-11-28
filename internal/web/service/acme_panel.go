package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/acme"
	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/logger"
	"github.com/echodev7/mikrozia/v3/internal/routeros"
)

const (
	acmeIssueTimeout       = 3 * time.Minute
	acmePort80CloseTimeout = 10 * time.Second
)

// ACMEStatusResponse is the non-secret view of ACME state shown in Settings -> Certificates.
type ACMEStatusResponse struct {
	ToolInstalled     bool                `json:"toolInstalled"`
	RouterOSReady     bool                `json:"routerosReady"`
	ManagedNATEnabled bool                `json:"managedNatEnabled"`
	WebCertFile       string              `json:"webCertFile"`
	WebKeyFile        string              `json:"webKeyFile"`
	SubCertFile       string              `json:"subCertFile"`
	SubKeyFile        string              `json:"subKeyFile"`
	Domains           []acme.DomainStatus `json:"domains"`
}

// ACMEIssueRequest carries the parameters for issuing an SSL certificate from the panel.
type ACMEIssueRequest struct {
	Domain          string `json:"domain" form:"domain"`
	Mode            string `json:"mode" form:"mode"`
	Target          string `json:"target" form:"target"` // "both" (default), "panel", or "sub"
	Email           string `json:"email" form:"email"`
	CloudflareToken string `json:"cloudflareToken" form:"cloudflareToken"`
	AutoOpenPort80  bool   `json:"autoOpenPort80" form:"autoOpenPort80"`
	Staging         bool   `json:"staging" form:"staging"`
}

// ACMEIssueResponse reports the outcome of a panel ACME issuance or renewal.
type ACMEIssueResponse struct {
	Domain            string `json:"domain"`
	Mode              string `json:"mode"`
	Target            string `json:"target"`
	CertPath          string `json:"certPath"`
	KeyPath           string `json:"keyPath"`
	NotAfter          int64  `json:"notAfter"`
	Port80AutoManaged bool   `json:"port80AutoManaged"`
	Port80Closed      bool   `json:"port80Closed"`
	Warning           string `json:"warning,omitempty"`
}

// ACMERenewRequest carries the parameters for renewing an installed ACME domain.
type ACMERenewRequest struct {
	Domain         string `json:"domain" form:"domain"`
	AutoOpenPort80 bool   `json:"autoOpenPort80" form:"autoOpenPort80"`
}

type acmeManagerIface interface {
	Issue(ctx context.Context, domain string, mode acme.Mode, opts acme.IssueOptions) (*acme.InstallResult, error)
	Renew(ctx context.Context, domain string) (*acme.InstallResult, error)
	Statuses() ([]acme.DomainStatus, error)
	Meta(domain string) (acme.Meta, bool)
}

type acmeCertApplier interface {
	ApplyAndReload(result *acme.InstallResult) error
	ApplyAndReloadTarget(result *acme.InstallResult, target CertificateTarget) error
}

type acmePort80Controller interface {
	Open(ctx context.Context, cfg routeros.NATConnConfig, targetIP, inInterface string) (bool, string, string)
	Close(ctx context.Context, cfg routeros.NATConnConfig) (int, string, string)
}

type defaultACMEPort80Controller struct{}

func (defaultACMEPort80Controller) Open(ctx context.Context, cfg routeros.NATConnConfig, targetIP, inInterface string) (bool, string, string) {
	return (routeros.Client{}).OpenTemporaryACMEPort80(ctx, cfg, targetIP, inInterface)
}

func (defaultACMEPort80Controller) Close(ctx context.Context, cfg routeros.NATConnConfig) (int, string, string) {
	return (routeros.Client{}).CloseTemporaryACMEPort80(ctx, cfg)
}

// ACMEPanelService orchestrates in-panel SSL certificate issuance and renewal
// using the bundled acme.sh client, transactional CertificateService reload,
// and optional temporary RouterOS Port 80 NAT management.
type ACMEPanelService struct {
	settingService SettingService
	manager        acmeManagerIface
	applier        acmeCertApplier
	port80         acmePort80Controller
	toolPath       string
}

func (s *ACMEPanelService) effectiveManager() acmeManagerIface {
	if s.manager != nil {
		return s.manager
	}
	return acme.NewManager(acme.Options{})
}

func (s *ACMEPanelService) effectiveApplier() acmeCertApplier {
	if s.applier != nil {
		return s.applier
	}
	return NewCertificateService()
}

func (s *ACMEPanelService) effectivePort80() acmePort80Controller {
	if s.port80 != nil {
		return s.port80
	}
	return defaultACMEPort80Controller{}
}

func (s *ACMEPanelService) effectiveToolPath() string {
	if s.toolPath != "" {
		return s.toolPath
	}
	return acme.ToolPath
}

// Status returns non-secret ACME metadata, current cert paths, and RouterOS Port 80 readiness.
func (s *ACMEPanelService) Status() (ACMEStatusResponse, error) {
	resp := ACMEStatusResponse{
		Domains: []acme.DomainStatus{},
	}
	if info, err := os.Stat(s.effectiveToolPath()); err == nil && !info.IsDir() {
		resp.ToolInstalled = true
	}
	rosCfg := resolveRouterOSAPIConfig()
	resp.RouterOSReady = rosCfg.State == "connecting"

	if database.GetDB() != nil {
		if allSetting, err := s.settingService.GetAllSetting(); err == nil && allSetting != nil {
			resp.ManagedNATEnabled = allSetting.RouterosNatEnable
			resp.WebCertFile = allSetting.WebCertFile
			resp.WebKeyFile = allSetting.WebKeyFile
			resp.SubCertFile = allSetting.SubCertFile
			resp.SubKeyFile = allSetting.SubKeyFile
		}
	}
	if statuses, err := s.effectiveManager().Statuses(); err == nil && statuses != nil {
		resp.Domains = statuses
	}
	return resp, nil
}

func (s *ACMEPanelService) loadRouterOSPort80Params() (routeros.NATConnConfig, string, string, error) {
	rosCfg := resolveRouterOSAPIConfig()
	if rosCfg.State != "connecting" {
		return routeros.NATConnConfig{}, "", "", errors.New("RouterOS API is not configured in Settings -> RouterOS")
	}
	natCfg := routeros.NATConnConfig{
		Host:     rosCfg.Host,
		Port:     rosCfg.Port,
		Username: rosCfg.Username,
		Password: rosCfg.Password,
	}
	var targetIP, inIface string
	if database.GetDB() != nil {
		if allSetting, err := s.settingService.GetAllSetting(); err == nil && allSetting != nil {
			targetIP = strings.TrimSpace(allSetting.RouterosNatTargetIP)
			inIface = strings.TrimSpace(allSetting.RouterosNatInInterface)
		}
	}
	if targetIP == "" {
		targetIP = DetectContainerIPv4()
	}
	if targetIP == "" {
		return routeros.NATConnConfig{}, "", "", errors.New("could not detect container private IPv4 for temporary Port 80 NAT rule")
	}
	return natCfg, targetIP, inIface, nil
}

// Issue requests a certificate for req.Domain, optionally managing a temporary
// RouterOS Port 80 NAT rule for HTTP-01 with guaranteed deferred cleanup, and
// atomically applies and reloads the panel and subscription TLS listeners.
func (s *ACMEPanelService) Issue(ctx context.Context, req ACMEIssueRequest) (resp ACMEIssueResponse, err error) {
	domain, err := acme.NormalizeDomain(req.Domain)
	if err != nil {
		return ACMEIssueResponse{}, err
	}
	mode := acme.Mode(strings.ToLower(strings.TrimSpace(req.Mode)))
	if mode == "" {
		mode = acme.ModeHTTP01
	}
	if mode != acme.ModeHTTP01 && mode != acme.ModeDNS01 {
		return ACMEIssueResponse{}, fmt.Errorf("unsupported ACME mode %q", req.Mode)
	}
	target := CertificateTarget(strings.ToLower(strings.TrimSpace(req.Target)))
	if target == "" {
		target = CertificateTargetBoth
	}
	if target != CertificateTargetBoth && target != CertificateTargetPanel && target != CertificateTargetSub {
		return ACMEIssueResponse{}, fmt.Errorf("unsupported certificate target %q", req.Target)
	}

	resp = ACMEIssueResponse{
		Domain: domain,
		Mode:   string(mode),
		Target: string(target),
	}

	if mode == acme.ModeHTTP01 && req.AutoOpenPort80 {
		natCfg, targetIP, inIface, cfgErr := s.loadRouterOSPort80Params()
		if cfgErr != nil {
			return ACMEIssueResponse{}, cfgErr
		}
		opened, st, detail := s.effectivePort80().Open(ctx, natCfg, targetIP, inIface)
		if !opened || st != "ok" {
			if detail != "" {
				return ACMEIssueResponse{}, fmt.Errorf("failed to open temporary Port 80 on RouterOS (%s): %s", st, detail)
			}
			return ACMEIssueResponse{}, fmt.Errorf("failed to open temporary Port 80 on RouterOS (%s)", st)
		}
		resp.Port80AutoManaged = true
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), acmePort80CloseTimeout)
			defer cancel()
			_, closeSt, closeDetail := s.effectivePort80().Close(closeCtx, natCfg)
			if closeSt == "ok" {
				resp.Port80Closed = true
			} else {
				resp.Warning = fmt.Sprintf("temporary Port 80 rule could not be removed automatically (%s %s); please remove comment=%s on RouterOS", closeSt, closeDetail, routeros.ManagedACMEHTTP01Comment)
				logger.Warning("acme temporary port 80 cleanup failed:", resp.Warning)
			}
		}()
	}

	issueCtx, cancel := context.WithTimeout(ctx, acmeIssueTimeout)
	defer cancel()

	installRes, err := s.effectiveManager().Issue(issueCtx, domain, mode, acme.IssueOptions{
		Email:           strings.TrimSpace(req.Email),
		CloudflareToken: strings.TrimSpace(req.CloudflareToken),
		Staging:         req.Staging,
	})
	if err != nil {
		return ACMEIssueResponse{}, err
	}

	if err := s.effectiveApplier().ApplyAndReloadTarget(installRes, target); err != nil {
		return ACMEIssueResponse{}, err
	}

	resp.CertPath = installRes.CertPath
	resp.KeyPath = installRes.KeyPath
	if !installRes.NotAfter.IsZero() {
		resp.NotAfter = installRes.NotAfter.Unix()
	}
	return resp, nil
}

// Renew renews an already-issued ACME domain (DNS-01 via acme.sh --renew,
// HTTP-01 via re-issuance with optional temporary Port 80 NAT management).
func (s *ACMEPanelService) Renew(ctx context.Context, req ACMERenewRequest) (ACMEIssueResponse, error) {
	domain, err := acme.NormalizeDomain(req.Domain)
	if err != nil {
		return ACMEIssueResponse{}, err
	}
	meta, ok := s.effectiveManager().Meta(domain)
	renewTarget := CertificateTargetBoth
	if database.GetDB() != nil {
		if paths, pErr := s.settingService.GetCertificatePaths(); pErr == nil {
			certPath := acme.NewManager(acme.Options{}).CertPath(domain)
			panelMatches := paths.PanelCert == certPath
			subMatches := paths.SubCert == certPath
			if panelMatches && !subMatches && paths.SubCert != "" {
				renewTarget = CertificateTargetPanel
			} else if subMatches && !panelMatches && paths.PanelCert != "" {
				renewTarget = CertificateTargetSub
			}
		}
	}
	if !ok || meta.Mode == acme.ModeHTTP01 {
		return s.Issue(ctx, ACMEIssueRequest{
			Domain:         domain,
			Mode:           string(acme.ModeHTTP01),
			Target:         string(renewTarget),
			AutoOpenPort80: req.AutoOpenPort80,
		})
	}

	renewCtx, cancel := context.WithTimeout(ctx, acmeIssueTimeout)
	defer cancel()

	installRes, err := s.effectiveManager().Renew(renewCtx, domain)
	if err != nil {
		return ACMEIssueResponse{}, err
	}
	if err := s.effectiveApplier().ApplyAndReloadTarget(installRes, renewTarget); err != nil {
		return ACMEIssueResponse{}, err
	}
	resp := ACMEIssueResponse{
		Domain:   domain,
		Mode:     string(meta.Mode),
		Target:   string(renewTarget),
		CertPath: installRes.CertPath,
		KeyPath:  installRes.KeyPath,
	}
	if !installRes.NotAfter.IsZero() {
		resp.NotAfter = installRes.NotAfter.Unix()
	}
	return resp, nil
}

// CleanupOrphanedPort80Rule removes any leftover temporary ACME Port 80 NAT rule
// on RouterOS (for example if the container restarted mid-issuance).
func (s *ACMEPanelService) CleanupOrphanedPort80Rule(ctx context.Context) {
	rosCfg := resolveRouterOSAPIConfig()
	if rosCfg.State != "connecting" {
		return
	}
	natCfg := routeros.NATConnConfig{
		Host:     rosCfg.Host,
		Port:     rosCfg.Port,
		Username: rosCfg.Username,
		Password: rosCfg.Password,
	}
	cleanCtx, cancel := context.WithTimeout(ctx, acmePort80CloseTimeout)
	defer cancel()
	removed, st, detail := s.effectivePort80().Close(cleanCtx, natCfg)
	if st == "ok" && removed > 0 {
		logger.Infof("cleaned up %d orphaned temporary ACME Port 80 NAT rule(s) on RouterOS", removed)
	} else if st != "ok" && detail != "" {
		logger.Debug("startup ACME Port 80 cleanup skipped:", st, detail)
	}
}
