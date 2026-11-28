package service

import (
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/echodev7/mikrozia/v3/internal/acme"
	"github.com/echodev7/mikrozia/v3/internal/web/global"
)

// CertificatePaths contains every panel and subscription TLS path that must
// change as part of one certificate transaction.
type CertificatePaths struct {
	PanelCert string
	PanelKey  string
	SubCert   string
	SubKey    string
}

type certificateSettings interface {
	GetCertificatePaths() (CertificatePaths, error)
	SetCertificatePaths(CertificatePaths) error
}

// CertificateReloader reloads every listener that serves the certificate.
type CertificateReloader interface {
	Reload() error
}

type panelCertificateReloader struct{}

func (r *panelCertificateReloader) Reload() error {
	return global.ReloadCertificates()
}

// CertificateService applies one ACME pair transaction to settings and listeners.
type CertificateService struct {
	settings certificateSettings
	reloader CertificateReloader
}

func NewCertificateService() *CertificateService {
	return NewCertificateServiceWithReloader(&panelCertificateReloader{})
}

func NewCertificateServiceWithReloader(reloader CertificateReloader) *CertificateService {
	return &CertificateService{settings: &SettingService{}, reloader: reloader}
}

// CertificateTarget specifies which TLS listener paths an ACME transaction updates.
type CertificateTarget string

const (
	CertificateTargetBoth  CertificateTarget = "both"
	CertificateTargetPanel CertificateTarget = "panel"
	CertificateTargetSub   CertificateTarget = "sub"
)

// ApplyAndReload commits a changed pair and automatically preserves separated
// Panel vs Subscription certificates when renewing one of two distinct domains.
func (s *CertificateService) ApplyAndReload(result *acme.InstallResult) error {
	if result == nil {
		return errors.New("an ACME installation result is required")
	}
	target := CertificateTargetBoth
	if previous, err := s.settings.GetCertificatePaths(); err == nil && result.CertPath != "" {
		panelMatches := previous.PanelCert == result.CertPath
		subMatches := previous.SubCert == result.CertPath
		if panelMatches && !subMatches && previous.SubCert != "" {
			target = CertificateTargetPanel
		} else if subMatches && !panelMatches && previous.PanelCert != "" {
			target = CertificateTargetSub
		}
	}
	return s.ApplyAndReloadTarget(result, target)
}

// ApplyAndReloadTarget commits a changed pair only after the targeted settings
// and listeners have been updated. Any failure restores the prior pair and paths.
func (s *CertificateService) ApplyAndReloadTarget(result *acme.InstallResult, target CertificateTarget) error {
	if result == nil {
		return errors.New("an ACME installation result is required")
	}
	if target == "" {
		target = CertificateTargetBoth
	}
	if target != CertificateTargetBoth && target != CertificateTargetPanel && target != CertificateTargetSub {
		return s.rollbackCertificate(result, nil, false, fmt.Errorf("unsupported certificate target %q", target))
	}
	if !result.Changed && !result.Reconcile {
		return commitCertificate(result)
	}
	if result.CertPath == "" || result.KeyPath == "" {
		return s.rollbackCertificate(result, nil, false, errors.New("both certificate and private key paths are required"))
	}
	if _, err := tls.LoadX509KeyPair(result.CertPath, result.KeyPath); err != nil {
		return s.rollbackCertificate(result, nil, false, fmt.Errorf("validate certificate pair: %w", err))
	}
	previous, err := s.settings.GetCertificatePaths()
	if err != nil {
		return s.rollbackCertificate(result, nil, false, fmt.Errorf("read previous certificate settings: %w", err))
	}
	next := previous
	if target == CertificateTargetBoth || target == CertificateTargetPanel {
		next.PanelCert = result.CertPath
		next.PanelKey = result.KeyPath
	}
	if target == CertificateTargetBoth || target == CertificateTargetSub {
		next.SubCert = result.CertPath
		next.SubKey = result.KeyPath
	}
	if !result.Changed && next == previous {
		return commitCertificate(result)
	}
	if err := s.settings.SetCertificatePaths(next); err != nil {
		return s.rollbackCertificate(result, &previous, true, fmt.Errorf("write certificate settings: %w", err))
	}
	if err := s.reloader.Reload(); err != nil {
		reloadErr := fmt.Errorf("reload certificate listeners: %w", err)
		if rollbackErr := s.rollbackCertificate(result, &previous, true, reloadErr); rollbackErr != nil {
			recoveryErr := s.reloader.Reload()
			if recoveryErr != nil {
				return errors.Join(rollbackErr, fmt.Errorf("reload the restored certificate listeners: %w", recoveryErr))
			}
			return rollbackErr
		}
	}
	if err := result.Commit(); err != nil {
		rollbackErr := s.rollbackCertificate(result, &previous, true, err)
		return errors.Join(rollbackErr, s.reloader.Reload())
	}
	return nil
}

func commitCertificate(result *acme.InstallResult) error {
	if err := result.Commit(); err != nil {
		return errors.Join(err, result.Rollback())
	}
	return nil
}

func (s *CertificateService) rollbackCertificate(result *acme.InstallResult, previous *CertificatePaths, restoreSettings bool, cause error) error {
	errs := []error{cause}
	if err := result.Rollback(); err != nil {
		errs = append(errs, err)
	}
	if restoreSettings && previous != nil {
		if err := s.settings.SetCertificatePaths(*previous); err != nil {
			errs = append(errs, fmt.Errorf("restore previous certificate settings: %w", err))
		}
	}
	return errors.Join(errs...)
}
