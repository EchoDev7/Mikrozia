package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/mhsanaei/3x-ui/v3/internal/acme"
	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/web/global"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// acmeUsage prints the help of the "x-ui acme" command group.
func acmeUsage() {
	fmt.Println(`Usage: x-ui acme <command> [options]

Commands:
    status                     Show the certificate state of every domain
    check                      Show the prerequisites of an issuance (diagnostics only)
    issue                      Issue and install a certificate
    renew                      Renew and install a certificate
    cloudflare setup           Store the Cloudflare API token for a domain

Options:
    --domain <domain>          Domain the command applies to
    --http                     Use HTTP-01 validation (manual renewal)
    --dns                      Use Cloudflare DNS-01 validation (automatic renewal)
    --email <address>          ACME account contact address (optional)
    --staging                  Use the ACME staging directory (untrusted certificates)
    --stdin                    Read the Cloudflare API token from standard input

The Cloudflare API token is read from a hidden interactive prompt, the
MIKROZIA_CF_API_TOKEN environment variable, or one line of standard input when
--stdin is explicit. It is never accepted as a command-line argument, never
printed, and never written outside the persistent certificate mount.
RouterOS Environment values are visible to anyone who can read or export the
RouterOS configuration.`)
}

// runAcmeCommand dispatches the "x-ui acme" subcommands.
func runAcmeCommand(args []string) {
	if len(args) == 0 {
		acmeUsage()
		return
	}
	switch args[0] {
	case "status":
		acmeStatus(args[1:])
	case "check":
		acmeCheck(args[1:])
	case "issue":
		acmeIssue(args[1:])
	case "renew":
		acmeRenew(args[1:])
	case "cloudflare":
		acmeCloudflare(args[1:])
	default:
		fmt.Println("unknown acme subcommand:", args[0])
		acmeUsage()
	}
}

// acmeStatus prints the certificate state of every registered domain.
func acmeStatus(args []string) {
	fs := flag.NewFlagSet("acme status", flag.ExitOnError)
	domain := fs.String("domain", "", "report only this domain")
	if err := fs.Parse(args); err != nil {
		return
	}

	manager := acme.NewManager(acme.Options{})
	if strings.TrimSpace(*domain) != "" {
		status, err := manager.Status(*domain)
		if err != nil {
			fmt.Println("read certificate state failed:", err)
			os.Exit(1)
		}
		printAcmeStatus(status)
		return
	}

	statuses, err := manager.Statuses()
	if err != nil {
		fmt.Println("read certificate state failed:", err)
		os.Exit(1)
	}
	if len(statuses) == 0 {
		fmt.Println("No certificate is registered on this panel.")
		fmt.Println("Certificates live on the persistent mount:", acme.CertRoot)
		return
	}
	for _, status := range statuses {
		printAcmeStatus(status)
		fmt.Println()
	}
}

// printAcmeStatus prints the non-sensitive state of one domain.
func printAcmeStatus(status acme.DomainStatus) {
	fmt.Println("domain:", status.Domain)
	fmt.Println("validation mode:", acmeModeLabel(status.Mode))
	fmt.Println("renewal:", acmeRenewalLabel(status))
	fmt.Println("installed:", yesNo(status.Installed))
	if status.Installed {
		fmt.Println("expires:", status.Expiry.UTC().Format(time.RFC3339))
	}
	fmt.Println("certificate:", status.CertPath)
	fmt.Println("private key:", status.KeyPath)
	fmt.Println("cloudflare token configured:", yesNo(status.TokenConfigured))
	if status.LastRenewal.Result != "" {
		fmt.Println("last renewal:", status.LastRenewal.Result, status.LastRenewal.At.Format(time.RFC3339))
	}
}

// acmeModeLabel names the validation method of a domain.
func acmeModeLabel(mode acme.Mode) string {
	switch mode {
	case acme.ModeHTTP01:
		return "HTTP-01"
	case acme.ModeDNS01:
		return "DNS-01 (Cloudflare)"
	default:
		return "none"
	}
}

// acmeRenewalLabel reports how a domain is renewed. HTTP-01 domains are
// renewed manually and are never reported as automatic.
func acmeRenewalLabel(status acme.DomainStatus) string {
	if status.Mode == acme.ModeDNS01 {
		return "automatic (scheduled by the panel)"
	}
	return "manual (repeat the issuance with the port-80 reachability open)"
}

// yesNo renders a boolean as a plain word.
func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// acmeCheck reports the prerequisites of an issuance. It only observes the
// environment; it never creates, changes, or removes a RouterOS rule.
func acmeCheck(args []string) {
	fs := flag.NewFlagSet("acme check", flag.ExitOnError)
	domain := fs.String("domain", "", "domain to check (required)")
	useDNS := fs.Bool("dns", false, "check the DNS-01 prerequisites")
	useHTTP := fs.Bool("http", false, "check the HTTP-01 prerequisites")
	if err := fs.Parse(args); err != nil {
		return
	}
	domainName, domainErr := acme.NormalizeDomain(*domain)
	if domainErr != nil {
		fmt.Println(domainErr)
		os.Exit(1)
	}
	if *useDNS && *useHTTP {
		fmt.Println("choose one validation mode: --http or --dns")
		os.Exit(1)
	}
	if !*useDNS && !*useHTTP {
		*useHTTP = true
	}

	manager := acme.NewManager(acme.Options{})
	fmt.Println("domain:", domainName)
	if _, err := os.Stat(manager.ToolPath()); err != nil {
		fmt.Println("acme client:", "not available in this build ("+manager.ToolPath()+")")
	} else {
		fmt.Println("acme client:", manager.ToolPath())
	}
	if _, err := os.Stat(manager.AccountHome()); err != nil {
		fmt.Println("acme account home:", "not created yet ("+manager.AccountHome()+")")
	} else {
		fmt.Println("acme account home:", manager.AccountHome())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if addresses, err := (&net.Resolver{}).LookupHost(ctx, domainName); err != nil {
		fmt.Println("dns resolution:", "failed:", err)
	} else {
		fmt.Println("dns resolution:", strings.Join(addresses, ", "))
	}

	if *useHTTP {
		fmt.Println(acmeHTTP01PublicWarning)
		fmt.Println("note: Mikrozia never inspects or changes RouterOS firewall, NAT, or port-forward rules.")
	}
	if *useDNS {
		fmt.Println("cloudflare token configured:", yesNo(manager.HasCloudflareToken(domainName)))
		fmt.Println("note: DNS-01 needs no public port 80. The token comes from a hidden interactive prompt, the environment, or explicit --stdin and stays on the persistent mount.")
	}
}

const acmeHTTP01PublicWarning = "Mikrozia cannot verify public HTTP-01 reachability from inside the container.\nThe RouterOS operator must temporarily make TCP 80 reachable from the public Internet."

// acmeIssue issues a certificate, installs it on the persistent mount, and
// points the panel and the subscription server at it.
func acmeIssue(args []string) {
	fs := flag.NewFlagSet("acme issue", flag.ExitOnError)
	domain := fs.String("domain", "", "domain to issue the certificate for (required)")
	useDNS := fs.Bool("dns", false, "use Cloudflare DNS-01 validation")
	useHTTP := fs.Bool("http", false, "use HTTP-01 validation")
	email := fs.String("email", "", "ACME account contact address (optional)")
	staging := fs.Bool("staging", false, "use the ACME staging directory")
	if err := fs.Parse(args); err != nil {
		return
	}
	domainName, domainErr := acme.NormalizeDomain(*domain)
	if domainErr != nil {
		fmt.Println(domainErr)
		os.Exit(1)
	}
	if *useDNS && *useHTTP {
		fmt.Println("choose one validation mode: --http or --dns")
		os.Exit(1)
	}

	mode := acme.ModeHTTP01
	if *useDNS {
		mode = acme.ModeDNS01
	} else {
		fmt.Println(acmeHTTP01PublicWarning)
	}

	manager := acme.NewManager(acme.Options{})
	if _, err := os.Stat(manager.ToolPath()); err != nil {
		fmt.Println("the bundled ACME client is not available in this build:", manager.ToolPath())
		os.Exit(1)
	}

	result, err := manager.Issue(context.Background(), domainName, mode, acme.IssueOptions{
		Email:           strings.TrimSpace(*email),
		CloudflareToken: os.Getenv(acme.CloudflareTokenEnv),
		Staging:         *staging,
	})
	if err != nil {
		fmt.Println("the issuance failed:", err)
		fmt.Println("the previous certificate and the previous panel settings are unchanged.")
		os.Exit(1)
	}

	if err := applyAcmeCertificate(result); err != nil {
		fmt.Println("the certificate apply/reload transaction failed:", err)
		os.Exit(1)
	}
	fmt.Println("certificate installed:", result.CertPath)
	fmt.Println("private key installed:", result.KeyPath)
	fmt.Println("expires:", result.NotAfter.UTC().Format(time.RFC3339))
	if result.Changed {
		fmt.Println("the panel, subscription listener, and Xray were reloaded.")
	}
}

// acmeRenew renews a certificate and installs it when it changed.
func acmeRenew(args []string) {
	fs := flag.NewFlagSet("acme renew", flag.ExitOnError)
	domain := fs.String("domain", "", "domain to renew the certificate for (required)")
	if err := fs.Parse(args); err != nil {
		return
	}
	domainName, domainErr := acme.NormalizeDomain(*domain)
	if domainErr != nil {
		fmt.Println(domainErr)
		os.Exit(1)
	}

	manager := acme.NewManager(acme.Options{})
	status, err := manager.Status(domainName)
	if err != nil {
		fmt.Println("read certificate state failed:", err)
		os.Exit(1)
	}
	if status.Mode == acme.ModeHTTP01 {
		fmt.Println("HTTP-01 certificates are renewed manually: open the port-80 reachability you own and run the issuance again.")
	}

	result, err := manager.Renew(context.Background(), domainName)
	if err != nil {
		fmt.Println("the renewal failed:", err)
		fmt.Println("the previous certificate and the previous panel settings are unchanged.")
		os.Exit(1)
	}
	if !result.Changed {
		if err := result.Commit(); err != nil {
			fmt.Println(errors.Join(err, result.Rollback()))
			os.Exit(1)
		}
		fmt.Println("the certificate was not renewed; the installed certificate is still valid.")
		return
	}
	if err := applyAcmeCertificate(result); err != nil {
		fmt.Println("the certificate apply/reload transaction failed:", err)
		os.Exit(1)
	}
	fmt.Println("certificate installed:", result.CertPath)
	fmt.Println("expires:", result.NotAfter.UTC().Format(time.RFC3339))
	fmt.Println("the panel, subscription listener, and Xray were reloaded.")
}

// acmeCloudflare stores the Cloudflare API token of a domain.
func acmeCloudflare(args []string) {
	if len(args) == 0 || args[0] != "setup" {
		fmt.Println("usage: x-ui acme cloudflare setup --domain <domain> [--stdin]")
		return
	}
	fs := flag.NewFlagSet("acme cloudflare setup", flag.ExitOnError)
	domain := fs.String("domain", "", "domain the token belongs to (required)")
	fromStdin := fs.Bool("stdin", false, "read the token from standard input")
	if err := fs.Parse(args[1:]); err != nil {
		return
	}
	domainName, domainErr := acme.NormalizeDomain(*domain)
	if domainErr != nil {
		fmt.Println(domainErr)
		os.Exit(1)
	}

	token, err := readAcmeToken(*fromStdin)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	manager := acme.NewManager(acme.Options{})
	if err := manager.SetCloudflareToken(domainName, token); err != nil {
		fmt.Println("storing the Cloudflare API token failed:", err)
		os.Exit(1)
	}
	fmt.Println("the Cloudflare API token of", domainName, "was stored on", manager.AccountHome())
	fmt.Println("the token is never printed, logged, or returned by the panel API.")
}

type passwordTerminal interface {
	IsTerminal(fd int) bool
	GetState(fd int) (any, error)
	Restore(fd int, state any) error
	ReadPassword(fd int) ([]byte, error)
}

type acmePasswordTerminal struct{}

func (acmePasswordTerminal) IsTerminal(fd int) bool { return term.IsTerminal(fd) }

func (acmePasswordTerminal) GetState(fd int) (any, error) { return term.GetState(fd) }

func (acmePasswordTerminal) Restore(fd int, state any) error {
	terminalState, ok := state.(*term.State)
	if !ok {
		return errors.New("invalid terminal state")
	}
	return term.Restore(fd, terminalState)
}

func (acmePasswordTerminal) ReadPassword(fd int) ([]byte, error) {
	return term.ReadPassword(fd)
}

// readAcmeToken reads a token only from a deliberate environment, explicit
// stdin, or no-echo TTY path. It is never accepted as a process argument.
func readAcmeToken(fromStdin bool) (string, error) {
	return readAcmeTokenWith(os.Stdin, os.Stderr, fromStdin, os.Getenv, acmePasswordTerminal{})
}

func readAcmeTokenWith(stdin *os.File, prompt io.Writer, fromStdin bool, getenv func(string) string, terminal passwordTerminal) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", errors.New("reading the token from explicit standard input failed")
		}
		token := strings.TrimSpace(line)
		if token == "" {
			return "", errors.New("no token was read from explicit standard input")
		}
		return token, nil
	}
	if token := strings.TrimSpace(getenv(acme.CloudflareTokenEnv)); token != "" {
		return token, nil
	}
	fd := int(stdin.Fd())
	if !terminal.IsTerminal(fd) {
		return "", fmt.Errorf("no Cloudflare API token was found: set %s or use explicit --stdin for noninteractive input", acme.CloudflareTokenEnv)
	}
	_, _ = fmt.Fprintln(prompt, "Cloudflare API token (input hidden):")
	password, err := readAcmePassword(terminal, fd)
	_, _ = fmt.Fprintln(prompt)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(password))
	if token == "" {
		return "", errors.New("no token was entered")
	}
	return token, nil
}

func readAcmePassword(terminal passwordTerminal, fd int) (password []byte, err error) {
	state, err := terminal.GetState(fd)
	if err != nil {
		return nil, errors.New("reading terminal state for hidden token input failed")
	}
	defer func() {
		if restoreErr := terminal.Restore(fd, state); restoreErr != nil {
			err = errors.Join(err, errors.New("restoring terminal state after token input failed"))
		}
	}()
	password, err = terminal.ReadPassword(fd)
	if err != nil {
		return nil, errors.New("reading the hidden interactive token failed")
	}
	return password, nil
}

type acmeCommandReloader struct{}

func (acmeCommandReloader) Reload() error {
	return global.RequestCertificateReload()
}

// applyAcmeCertificate uses the same transaction as the scheduler. A separate
// CLI process reloads the running services through the supported x-ui command.
func applyAcmeCertificate(result *acme.InstallResult) (err error) {
	defer func() {
		if rollbackErr := result.Rollback(); rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if err := database.RecoverPendingSQLiteRestore(config.GetDBPath()); err != nil {
		return fmt.Errorf("recovering the pending database restore failed: %w", err)
	}
	if err := database.InitDB(config.GetDBPath()); err != nil {
		return fmt.Errorf("open database failed: %w", err)
	}
	return service.NewCertificateServiceWithReloader(acmeCommandReloader{}).ApplyAndReload(result)
}
