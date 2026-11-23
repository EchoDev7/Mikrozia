package job

import (
	"context"
	"os"

	"github.com/echodev7/mikrozia/v3/internal/acme"
	"github.com/echodev7/mikrozia/v3/internal/logger"
	"github.com/echodev7/mikrozia/v3/internal/web/service"
)

// CertificateApplier applies a renewed certificate and reloads the listeners
// that serve it as one recoverable transaction.
type CertificateApplier interface {
	ApplyAndReload(result *acme.InstallResult) error
}

type renewalManager interface {
	ToolPath() string
	Domains() ([]string, error)
	Meta(domain string) (acme.Meta, bool)
	Renew(ctx context.Context, domain string) (*acme.InstallResult, error)
}

// AcmeRenewJob renews the certificates that are set up for automatic renewal.
// HTTP-01 domains are renewed manually by the operator, so the job never
// touches them and never claims they renew themselves.
type AcmeRenewJob struct {
	manager            renewalManager
	certificateService CertificateApplier
}

// NewAcmeRenewJob creates the automatic renewal job with the image defaults.
func NewAcmeRenewJob() *AcmeRenewJob {
	return &AcmeRenewJob{
		manager:            acme.NewManager(acme.Options{}),
		certificateService: service.NewCertificateService(),
	}
}

// NewAcmeRenewJobWith creates the job with an explicit manager and applier, so
// the renewal behaviour can be exercised against a temporary certificate mount.
func NewAcmeRenewJobWith(manager renewalManager, applier CertificateApplier) *AcmeRenewJob {
	return &AcmeRenewJob{manager: manager, certificateService: applier}
}

// Run renews every DNS-01 domain and reloads the panel only when a
// certificate actually changed.
func (j *AcmeRenewJob) Run() {
	if j.manager == nil {
		return
	}
	if _, err := os.Stat(j.manager.ToolPath()); err != nil {
		// The ACME client is not part of this build; there is nothing to renew.
		return
	}
	domains, err := j.manager.Domains()
	if err != nil {
		logger.Warning("acme renewal: reading the certificate state failed:", err)
		return
	}
	for _, domain := range domains {
		meta, ok := j.manager.Meta(domain)
		if !ok || meta.Mode != acme.ModeDNS01 {
			continue
		}
		result, err := j.manager.Renew(context.Background(), domain)
		if err != nil {
			logger.Warning("acme renewal failed for", domain, ":", err)
			continue
		}
		if !result.Changed {
			if err := result.Commit(); err != nil {
				if rollbackErr := result.Rollback(); rollbackErr != nil {
					logger.Warning("acme renewal: rollback failed:", rollbackErr)
				}
				logger.Warning("acme renewal: persisting unchanged result failed:", err)
			}
			continue
		}
		if err := j.certificateService.ApplyAndReload(result); err != nil {
			logger.Warning("acme renewal: the certificate apply/reload transaction failed:", err)
			continue
		}
		logger.Info("acme renewal: the certificate was replaced and the services reloaded")
	}
}
