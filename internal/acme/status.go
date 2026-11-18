package acme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DomainStatus is the non-sensitive view of one domain's ACME state. It never
// contains the API token or any part of the private key.
type DomainStatus struct {
	LastRenewal RenewalResult `json:"lastRenewal"`
	// Domain is the certificate subject.
	Domain string `json:"domain"`
	// Mode is the validation method the domain was issued with.
	Mode Mode `json:"mode"`
	// Expiry is the expiry of the installed certificate.
	Expiry time.Time `json:"expiry"`
	// Installed reports whether a certificate file is installed.
	Installed bool `json:"installed"`
	// AutoRenew reports whether the domain renews automatically. HTTP-01
	// domains are renewed manually and never report automatic renewal.
	AutoRenew bool `json:"autoRenew"`
	// TokenConfigured reports whether a Cloudflare API token is stored.
	TokenConfigured bool `json:"tokenConfigured"`
	// CertPath and KeyPath are the installed paths.
	CertPath string `json:"certPath"`
	KeyPath  string `json:"keyPath"`
}

// Status returns the state of one domain.
func (m *Manager) Status(domain string) (DomainStatus, error) {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return DomainStatus{}, err
	}
	status := DomainStatus{
		Domain:          domain,
		CertPath:        m.CertPath(domain),
		KeyPath:         m.KeyPath(domain),
		TokenConfigured: m.HasCloudflareToken(domain),
	}
	if meta, ok := m.Meta(domain); ok {
		status.Mode = meta.Mode
	}
	status.AutoRenew = status.Mode == ModeDNS01
	if content, err := readStorageFile(m.renewalPath(domain)); err == nil {
		var saved RenewalResult
		if json.Unmarshal(content, &saved) == nil && (saved.Result == "pending" || saved.Result == "failed" || saved.Result == "renewed" || saved.Result == "unchanged") {
			status.LastRenewal = saved
		}
	}
	if expiry, ok := m.Installed(domain); ok {
		status.Installed = true
		status.Expiry = expiry
	}
	return status, nil
}

type RenewalResult struct {
	At     time.Time `json:"at"`
	Result string    `json:"result"`
}

func (m *Manager) renewalPath(domain string) string {
	domain, err := NormalizeDomain(domain)
	if err != nil {
		return ""
	}
	return filepath.Join(m.accountHome, metaDir, domain+".renewal.json")
}

func (m *Manager) recordRenewal(domain, result string) error {
	if _, err := NormalizeDomain(domain); err != nil {
		return err
	}
	if err := checkPath(m.renewalPath(domain)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(m.accountHome, metaDir), 0o700); err != nil {
		return err
	}
	content, err := json.Marshal(RenewalResult{At: time.Now().UTC(), Result: result})
	if err != nil {
		return err
	}
	return writeFileAtomic(m.renewalPath(domain), content, 0o600)
}

// Statuses returns the state of every domain the account home knows about.
func (m *Manager) Statuses() ([]DomainStatus, error) {
	domains, err := m.Domains()
	if err != nil {
		return nil, err
	}
	statuses := make([]DomainStatus, 0, len(domains))
	for _, domain := range domains {
		status, err := m.Status(domain)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Domain < statuses[j].Domain
	})
	return statuses, nil
}
