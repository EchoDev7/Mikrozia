// Package acme drives the bundled ACME client for certificate issuance and
// renewal. The client itself is an immutable image build input; this package
// only invokes it and keeps every artifact on the persistent certificate mount.
package acme

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// CertRoot is the persistent certificate mount of the container.
	CertRoot = "/root/cert"
	// AccountHome holds the ACME account, per-domain state, and renewal state.
	AccountHome = "/root/cert/.acme.sh"
	// ToolPath is the bundled client inside the immutable image.
	ToolPath = "/usr/local/share/acme.sh/acme.sh"
	// CertFileName and KeyFileName are the stable names of an issued pair.
	CertFileName = "fullchain.pem"
	KeyFileName  = "privkey.pem"
	// metaDir holds the panel's own non-secret per-domain ACME metadata.
	metaDir = "mikrozia"
)

// Mode selects the ACME validation method.
type Mode string

const (
	// ModeHTTP01 validates over HTTP on port 80 and is renewed manually.
	ModeHTTP01 Mode = "http-01"
	// ModeDNS01 validates through a DNS TXT record and renews automatically.
	ModeDNS01 Mode = "dns-01"
)

// Server is the ACME directory the bundled client talks to.
const Server = "letsencrypt"

// RedactedSecret replaces a secret value in any output the panel records.
const RedactedSecret = "[redacted]"

// Runner executes the bundled client. It is an interface so the issuance
// logic can be exercised without a network or an ACME account.
type Runner interface {
	Run(ctx context.Context, args []string, env []string) (string, error)
}

// ExecRunner runs the bundled client as a child process.
type ExecRunner struct {
	// ToolPath is the client executable inside the image.
	ToolPath string
}

// Run executes the client and returns its combined output.
func (r ExecRunner) Run(ctx context.Context, args []string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, r.ToolPath, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Options configures a Manager.
type Options struct {
	ToolPath    string
	AccountHome string
	CertRoot    string
	Runner      Runner
}

// Manager issues, renews, and installs certificates through the bundled client.
type Manager struct {
	toolPath    string
	accountHome string
	certRoot    string
	runner      Runner
}

// NewManager builds a Manager with the image defaults.
func NewManager(opts Options) *Manager {
	m := &Manager{
		toolPath:    opts.ToolPath,
		accountHome: opts.AccountHome,
		certRoot:    opts.CertRoot,
		runner:      opts.Runner,
	}
	if m.toolPath == "" {
		m.toolPath = ToolPath
	}
	if m.accountHome == "" {
		m.accountHome = AccountHome
	}
	if m.certRoot == "" {
		m.certRoot = CertRoot
	}
	if m.runner == nil {
		m.runner = ExecRunner{ToolPath: m.toolPath}
	}
	return m
}

// IssueOptions carries the inputs of one issuance.
type IssueOptions struct {
	// Email registers the ACME account contact; it is optional.
	Email string
	// CloudflareToken is the least-privilege API token for DNS-01. It is
	// passed to the client through the environment and never recorded.
	CloudflareToken string
	// Staging uses the ACME staging directory, which issues untrusted
	// certificates and never counts against the issuance quota.
	Staging bool
}

// ErrTokenRequired reports a DNS-01 issuance without an API token.
var ErrTokenRequired = errors.New("a Cloudflare API token is required for DNS-01 issuance")

// Issue requests a certificate for domain and installs it on the persistent
// mount. HTTP-01 issuance needs a temporary operator-owned port-80
// reachability; Mikrozia never creates a RouterOS rule for it.
func (m *Manager) Issue(ctx context.Context, domain string, mode Mode, opts IssueOptions) (*InstallResult, error) {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	if mode != ModeHTTP01 && mode != ModeDNS01 {
		return nil, fmt.Errorf("unsupported ACME mode %q", mode)
	}
	release, err := m.lockInstall()
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	token := strings.TrimSpace(opts.CloudflareToken)
	if mode == ModeDNS01 && token == "" {
		stored, ok := m.CloudflareToken(domain)
		if !ok {
			return nil, ErrTokenRequired
		}
		token = stored
	}

	pending, err := m.beginInstall(domain)
	if err != nil {
		return nil, err
	}
	defer pending.close()

	args := []string{"--issue", "--home", m.accountHome, "--server", m.serverURL(opts.Staging), "--ecc"}
	switch mode {
	case ModeHTTP01:
		args = append(args, "--standalone")
	case ModeDNS01:
		args = append(args, "--dns", "cf")
	}
	args = append(args, "-d", domain, "--fullchain-file", pending.stageCert, "--key-file", pending.stageKey)
	if email := strings.TrimSpace(opts.Email); email != "" {
		args = append(args, "--accountemail", email)
	}

	var env []string
	if mode == ModeDNS01 {
		env = append(env, "CF_Token="+token)
	}
	output, err := m.runner.Run(ctx, args, env)
	if err != nil {
		pending.discard()
		return nil, redactError("ACME issuance failed", err, output, token)
	}
	result, err := pending.finish(ctx)
	if err != nil {
		return nil, err
	}
	result.Reconcile = true
	if err := m.writeMeta(domain, mode, opts.Email); err != nil {
		return nil, rollbackResult(result, err)
	}
	if mode == ModeDNS01 && token != "" {
		if err := m.SetCloudflareToken(domain, token); err != nil {
			return nil, rollbackResult(result, err)
		}
	}
	result.release = release
	transferred = true
	return result, nil
}

// Renew renews the certificate of domain and installs it when it changed.
func (m *Manager) Renew(ctx context.Context, domain string) (result *InstallResult, err error) {
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	meta, ok := m.Meta(domain)
	if !ok || meta.Mode != ModeDNS01 {
		return nil, ErrManualRenewal
	}
	release, err := m.lockInstall()
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	if err := m.recordRenewal(domain, "pending"); err != nil {
		return nil, fmt.Errorf("persist renewal attempt: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			if persistErr := m.recordRenewal(domain, "failed"); persistErr != nil {
				err = errors.Join(err, fmt.Errorf("persist failed renewal result: %w", persistErr))
			}
		}
	}()
	pending, err := m.beginInstall(domain)
	if err != nil {
		return nil, err
	}
	defer pending.close()

	output, err := m.runInstall(ctx, domain, pending.stageCert, pending.stageKey)
	if err != nil {
		pending.discard()
		return nil, redactError("saving ACME renewal staging paths failed", err, output, "")
	}
	var env []string
	token, hasToken := m.CloudflareToken(domain)
	if hasToken {
		env = append(env, "CF_Token="+token)
	}
	output, err = m.runner.Run(ctx, []string{"--renew", "--home", m.accountHome, "--ecc", "-d", domain}, env)
	if err != nil && !isRenewalSkip(err) {
		pending.discard()
		return nil, redactError("ACME renewal failed", err, output, token)
	}
	result, err = pending.finish(ctx)
	if err == nil {
		result.renewalManager = m
		result.domain = domain
		complete = true
		result.release = release
		transferred = true
	}
	return result, err
}

func isRenewalSkip(err error) bool {
	var exitError interface{ ExitCode() int }
	return errors.As(err, &exitError) && exitError.ExitCode() == 2
}

// InstallResult describes an installed pair whose prior state remains
// recoverable until the settings update and listener reload have committed.
type InstallResult struct {
	CertPath       string
	KeyPath        string
	Changed        bool
	NotAfter       time.Time
	previous       *pairSnapshot
	Reconcile      bool
	renewalManager *Manager
	domain         string
	release        func()
}

// Commit releases the prior-pair snapshot after settings and reload succeed.
func (r *InstallResult) Commit() error {
	if r != nil {
		if r.renewalManager != nil {
			outcome := "unchanged"
			if r.Changed {
				outcome = "renewed"
			}
			if err := r.renewalManager.recordRenewal(r.domain, outcome); err != nil {
				return fmt.Errorf("persist renewal result: %w", err)
			}
		}
		r.previous = nil
		r.unlock()
	}
	return nil
}

// Rollback restores the exact certificate and key captured before installation.
func (r *InstallResult) Rollback() error {
	if r != nil {
		defer r.unlock()
	}
	if r == nil || r.previous == nil {
		return nil
	}
	if err := r.previous.restore(); err != nil {
		return fmt.Errorf("restore the previous certificate pair: %w", err)
	}
	r.previous = nil
	if r.renewalManager != nil {
		return r.renewalManager.recordRenewal(r.domain, "failed")
	}
	return nil
}

func (r *InstallResult) unlock() {
	if r.release != nil {
		r.release()
		r.release = nil
	}
}

func rollbackResult(result *InstallResult, err error) error {
	if rollbackErr := result.Rollback(); rollbackErr != nil {
		return errors.Join(err, rollbackErr)
	}
	return err
}

type fileSnapshot struct {
	path    string
	content []byte
	mode    os.FileMode
	exists  bool
}

type pairSnapshot struct {
	cert       fileSnapshot
	key        fileSnapshot
	dir        string
	dirExisted bool
}

type pendingInstall struct {
	manager   *Manager
	domain    string
	stageDir  string
	stageCert string
	stageKey  string
	certPath  string
	keyPath   string
	previous  *pairSnapshot
}

func (m *Manager) beginInstall(domain string) (*pendingInstall, error) {
	if err := m.checkStorage(domain); err != nil {
		return nil, err
	}
	targetDir := filepath.Join(m.certRoot, domain)
	_, statErr := os.Stat(targetDir)
	dirExisted := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("inspect certificate directory: %w", statErr)
	}
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return nil, fmt.Errorf("create certificate directory: %w", err)
	}
	previous := &pairSnapshot{
		cert:       fileSnapshot{path: filepath.Join(targetDir, CertFileName)},
		key:        fileSnapshot{path: filepath.Join(targetDir, KeyFileName)},
		dir:        targetDir,
		dirExisted: dirExisted,
	}
	for _, snapshot := range []*fileSnapshot{&previous.cert, &previous.key} {
		if err := snapshot.capture(); err != nil {
			return nil, err
		}
	}
	stageDir, err := os.MkdirTemp(targetDir, ".mikrozia-acme-stage-*")
	if err != nil {
		return nil, fmt.Errorf("create certificate staging directory: %w", err)
	}
	return &pendingInstall{
		manager:   m,
		domain:    domain,
		stageDir:  stageDir,
		stageCert: filepath.Join(stageDir, CertFileName),
		stageKey:  filepath.Join(stageDir, KeyFileName),
		certPath:  previous.cert.path,
		keyPath:   previous.key.path,
		previous:  previous,
	}, nil
}

func (p *pendingInstall) close() {
	if p.stageDir != "" {
		_ = os.RemoveAll(p.stageDir)
		p.stageDir = ""
	}
}

func (p *pendingInstall) discard() {
	p.close()
	if !p.previous.dirExisted {
		_ = os.Remove(p.previous.dir)
	}
}

func (p *pendingInstall) finish(ctx context.Context) (*InstallResult, error) {
	if err := checkPath(p.stageCert); err != nil {
		return nil, p.recover(err)
	}
	if err := checkPath(p.stageKey); err != nil {
		return nil, p.recover(err)
	}
	if err := p.manager.checkStorage(p.domain); err != nil {
		return nil, p.recover(err)
	}
	if err := os.Chmod(p.stageCert, 0o644); err != nil {
		p.discard()
		return nil, fmt.Errorf("set staged certificate permissions: %w", err)
	}
	if err := os.Chmod(p.stageKey, 0o600); err != nil {
		p.discard()
		return nil, fmt.Errorf("set staged private key permissions: %w", err)
	}
	_, _, _, err := validatePair(p.stageCert, p.stageKey)
	if err != nil {
		p.discard()
		return nil, fmt.Errorf("validate the staged certificate pair: %w", err)
	}
	output, err := p.manager.runInstall(ctx, p.domain, p.certPath, p.keyPath)
	if err != nil {
		return nil, p.recover(redactError("install the certificate pair failed", err, output, ""))
	}
	if err := os.Chmod(p.certPath, 0o644); err != nil {
		return nil, p.recover(fmt.Errorf("set certificate permissions: %w", err))
	}
	if err := os.Chmod(p.keyPath, 0o600); err != nil {
		return nil, p.recover(fmt.Errorf("set private key permissions: %w", err))
	}
	activeCert, activeKey, leaf, err := validatePair(p.certPath, p.keyPath)
	if err != nil {
		return nil, p.recover(fmt.Errorf("validate the installed certificate pair: %w", err))
	}
	changed := !p.previous.cert.exists || !p.previous.key.exists ||
		!bytes.Equal(p.previous.cert.content, activeCert) ||
		!bytes.Equal(p.previous.key.content, activeKey)
	p.close()
	return &InstallResult{
		CertPath: p.certPath,
		KeyPath:  p.keyPath,
		Changed:  changed,
		NotAfter: leaf.NotAfter,
		previous: p.previous,
	}, nil
}

func (p *pendingInstall) recover(err error) error {
	p.close()
	result := &InstallResult{previous: p.previous}
	return rollbackResult(result, err)
}

func (m *Manager) runInstall(ctx context.Context, domain, certPath, keyPath string) (string, error) {
	return m.runner.Run(ctx, []string{
		"--install-cert",
		"--home", m.accountHome,
		"--ecc",
		"-d", domain,
		"--fullchain-file", certPath,
		"--key-file", keyPath,
	}, nil)
}

func validatePair(certPath, keyPath string) ([]byte, []byte, *x509.Certificate, error) {
	certPEM, err := readStorageFile(certPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read certificate: %w", err)
	}
	keyPEM, err := readStorageFile(keyPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read private key: %w", err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, nil, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, nil, err
	}
	return certPEM, keyPEM, leaf, nil
}

func (s *fileSnapshot) capture() error {
	info, err := os.Lstat(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect previous certificate file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("previous certificate path is not a regular file: %s", s.path)
	}
	content, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("read previous certificate file: %w", err)
	}
	s.content = content
	s.mode = info.Mode().Perm()
	s.exists = true
	return nil
}

func (p *pairSnapshot) restore() error {
	var errs []error
	for _, snapshot := range []fileSnapshot{p.cert, p.key} {
		var err error
		if snapshot.exists {
			err = writeFileAtomic(snapshot.path, snapshot.content, snapshot.mode)
		} else {
			err = os.Remove(snapshot.path)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if !p.dirExisted {
		if err := os.Remove(p.dir); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	if err := checkPath(path); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".mikrozia-acme-restore-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

// ToolPath returns the path of the bundled client this manager invokes.
func (m *Manager) ToolPath() string {
	return m.toolPath
}

// AccountHome returns the persistent directory that holds the ACME account
// and the per-domain renewal state.
func (m *Manager) AccountHome() string {
	return m.accountHome
}

// CertRoot returns the persistent certificate mount.
func (m *Manager) CertRoot() string {
	return m.certRoot
}

// Domains lists Mikrozia's registered domains without inspecting acme.sh's
// domain-directory naming, which differs between RSA and ECC certificates.
func (m *Manager) Domains() ([]string, error) {
	if err := checkPath(filepath.Join(m.accountHome, metaDir)); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(m.accountHome, metaDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var domains []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".meta" {
			continue
		}
		domain := strings.TrimSuffix(entry.Name(), ".meta")
		if normalized, err := NormalizeDomain(domain); err == nil && normalized == domain {
			domains = append(domains, domain)
		}
	}
	return domains, nil
}

// Meta is the panel's non-secret metadata of one ACME domain.
type Meta struct {
	Mode  Mode
	Email string
}

// Meta returns the stored metadata of domain.
func (m *Manager) Meta(domain string) (Meta, bool) {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return Meta{}, false
	}
	content, err := readStorageFile(m.metaPath(domain))
	if err != nil {
		return Meta{}, false
	}
	meta := Meta{}
	for _, line := range strings.Split(string(content), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "mode":
			meta.Mode = Mode(value)
		case "email":
			meta.Email = value
		}
	}
	return meta, meta.Mode == ModeHTTP01 || meta.Mode == ModeDNS01
}

func (m *Manager) metaPath(domain string) string {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return ""
	}
	return filepath.Join(m.accountHome, metaDir, domain+".meta")
}

// SetMetadata stores the panel's own non-secret metadata of a domain.
func (m *Manager) SetMetadata(domain string, mode Mode, email string) error {
	return m.writeMeta(domain, mode, email)
}

func (m *Manager) writeMeta(domain string, mode Mode, email string) error {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.accountHome, metaDir)
	if mode != ModeHTTP01 && mode != ModeDNS01 {
		return errors.New("invalid ACME metadata mode")
	}
	if strings.ContainsAny(email, "\r\n\x00") {
		return errors.New("invalid ACME contact address")
	}
	if err := checkPath(m.metaPath(domain)); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create ACME metadata directory: %w", err)
	}
	content := fmt.Sprintf("mode=%s\nemail=%s\n", mode, strings.TrimSpace(email))
	if err := writeFileAtomic(m.metaPath(domain), []byte(content), 0o600); err != nil {
		return fmt.Errorf("write ACME metadata: %w", err)
	}
	return nil
}

// CertPath returns the stable certificate path of a domain.
func (m *Manager) CertPath(domain string) string {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return ""
	}
	return filepath.Join(m.certRoot, domain, CertFileName)
}

// KeyPath returns the stable private key path of a domain.
func (m *Manager) KeyPath(domain string) string {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return ""
	}
	return filepath.Join(m.certRoot, domain, KeyFileName)
}

// Installed reports the expiry of the installed certificate of a domain.
func (m *Manager) Installed(domain string) (time.Time, bool) {
	content, err := readStorageFile(m.CertPath(domain))
	if err != nil {
		return time.Time{}, false
	}
	block, _ := decodePEM(content)
	if block == nil {
		return time.Time{}, false
	}
	return block.NotAfter, true
}

// decodePEM parses the first certificate of a PEM chain.
func decodePEM(content []byte) (*x509.Certificate, []byte) {
	block, rest := pem.Decode(content)
	if block == nil {
		return nil, nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, rest
	}
	return cert, rest
}

func (m *Manager) serverURL(staging bool) string {
	if staging {
		return "https://acme-staging-v02.api.letsencrypt.org/directory"
	}
	return "https://acme-v02.api.letsencrypt.org/directory"
}

// Redact removes a secret value from text the panel may record.
func Redact(text string, secret string) string {
	text = strings.TrimSpace(text)
	if secret != "" {
		text = strings.ReplaceAll(text, secret, RedactedSecret)
	}
	return tokenAssignment.ReplaceAllString(text, "${1}"+RedactedSecret)
}

var tokenAssignment = regexp.MustCompile(`(?im)(\bCF_Token[^\S\r\n]*=[^\S\r\n]*)[^\r\n]*`)

var ErrManualRenewal = errors.New("HTTP-01 requires manual re-issue: temporarily enable operator-owned public TCP 80 and run x-ui acme issue --http --domain <domain>")

// redactError builds a failure message whose secret values are removed. The
// client output can repeat the token, so both the error and the output are
// redacted before they are recorded.
func redactError(prefix string, err error, output string, secret string) error {
	message := prefix
	if err != nil {
		message += ": " + Redact(err.Error(), secret)
	}
	if trimmed := Redact(output, secret); trimmed != "" {
		message += ": " + trimmed
	}
	return errors.New(message)
}
