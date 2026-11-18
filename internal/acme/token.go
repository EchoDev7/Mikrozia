package acme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CloudflareTokenEnv carries the Cloudflare API token into the CLI without
// putting it into the shell history of a command line argument.
const CloudflareTokenEnv = "MIKROZIA_CF_API_TOKEN"

// tokenFile holds the API token of one domain inside the account home, so the
// automatic renewal works after a container restart or an image replacement.
const tokenFile = "cf.token"

// tokenPath returns the file that holds the API token of a domain.
func (m *Manager) tokenPath(domain string) string {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return ""
	}
	return filepath.Join(m.accountHome, metaDir, domain+"."+tokenFile)
}

// SetCloudflareToken stores the API token of a domain in the account home.
// The file is readable by the container root only, and the token is never
// echoed back by any status or diagnostic output.
func (m *Manager) SetCloudflareToken(domain string, token string) error {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("a Cloudflare API token is required")
	}
	dir := filepath.Join(m.accountHome, metaDir)
	if strings.ContainsAny(token, "\r\n\x00") {
		return fmt.Errorf("the API token must be a single line")
	}
	if err := checkPath(m.tokenPath(domain)); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create ACME metadata directory: %w", err)
	}
	content := "CF_Token=" + token + "\n"
	if err := writeFileAtomic(m.tokenPath(domain), []byte(content), 0o600); err != nil {
		return fmt.Errorf("store the Cloudflare API token: %w", err)
	}
	return nil
}

// CloudflareToken returns the stored API token of a domain.
func (m *Manager) CloudflareToken(domain string) (string, bool) {
	content, err := readStorageFile(m.tokenPath(domain))
	if err != nil {
		return "", false
	}
	_, value, found := strings.Cut(strings.TrimSpace(string(content)), "=")
	if !found || value == "" {
		return "", false
	}
	return value, true
}

// HasCloudflareToken reports whether an API token is configured for a domain.
// It never returns the token itself.
func (m *Manager) HasCloudflareToken(domain string) bool {
	_, ok := m.CloudflareToken(domain)
	return ok
}
