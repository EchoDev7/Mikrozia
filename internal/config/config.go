// Package config provides configuration management utilities for the 3x-ui panel,
// including version information, logging levels, database paths, and environment variable handling.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

//go:embed version
var version string

//go:embed name
var name string

// mikroziaVersion is injected at build time via `-ldflags -X 'github.com/mhsanaei/3x-ui/v3/internal/config.mikroziaVersion=...'`.
// buildCommit and buildDate are retained for diagnostics.
var (
	mikroziaVersion string
	buildCommit     string
	buildDate       string
)

// devVersion is the truthful identity for binaries without a valid release
// version; it must never be a stale production tag.
const devVersion = "dev"

var releaseVersionPattern = regexp.MustCompile(`^v([0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?|[0-9]+-mikrozia)$`)

// LogLevel represents the logging level for the application.
type LogLevel string

// Logging level constants
const (
	Debug   LogLevel = "debug"
	Info    LogLevel = "info"
	Notice  LogLevel = "notice"
	Warning LogLevel = "warning"
	Error   LogLevel = "error"
)

// GetBaseVersion returns the raw embedded upstream release version of 3x-ui (e.g. "3.11.0").
func GetBaseVersion() string {
	return strings.TrimSpace(version)
}

// GetUpstreamVersion returns the raw embedded upstream release version of 3x-ui (e.g. "3.11.0").
func GetUpstreamVersion() string {
	return GetBaseVersion()
}

// GetMikroziaVersion returns the release version injected at build time, or
// "dev" when the build carries no valid, byte-exact Mikrozia release identity.
func GetMikroziaVersion() string {
	if !releaseVersionPattern.MatchString(mikroziaVersion) {
		return devVersion
	}
	return mikroziaVersion
}

// GetName returns the name of the application.
func GetName() string {
	return strings.TrimSpace(name)
}

// GetBuildCommit returns the short git commit this binary was built from, or an
// empty string for a plain/local build or a stable tagged release.
func GetBuildCommit() string {
	return strings.TrimSpace(buildCommit)
}

// GetBuildDate returns the UTC build timestamp injected at build time, or empty.
func GetBuildDate() string {
	return strings.TrimSpace(buildDate)
}

// GetPanelVersion returns the release version displayed across the panel UI, API, and CLI.
func GetPanelVersion() string {
	return GetMikroziaVersion()
}

// GetLogLevel returns the current logging level based on environment variables or defaults to Info.
func GetLogLevel() LogLevel {
	if IsDebug() {
		return Debug
	}
	logLevel := os.Getenv("XUI_LOG_LEVEL")
	if logLevel == "" {
		return Info
	}
	return LogLevel(logLevel)
}

// IsDebug returns true if debug mode is enabled via the XUI_DEBUG environment variable.
func IsDebug() bool {
	return os.Getenv("XUI_DEBUG") == "true"
}

// IsSkipHSTS returns true if skipping HSTS mode is enabled via the XUI_SKIP_HSTS environment variable.
func IsSkipHSTS() bool {
	return os.Getenv("XUI_SKIP_HSTS") == "true"
}

func GetPortOverride() (port int, configured bool, err error) {
	value, ok := os.LookupEnv("XUI_PORT")
	if !ok || strings.TrimSpace(value) == "" {
		return 0, false, nil
	}

	port, err = strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, true, fmt.Errorf("parse XUI_PORT: %w", err)
	}
	if port < 1 || port > 65535 {
		return 0, true, fmt.Errorf("XUI_PORT must be between 1 and 65535")
	}

	return port, true, nil
}

// AdminUsernameEnv and AdminPasswordEnv are the RouterOS Container Environment
// entries that own the panel administrator account.
const (
	AdminUsernameEnv = "MIKROZIA_ADMIN_USERNAME"
	AdminPasswordEnv = "MIKROZIA_ADMIN_PASSWORD"
)

// GetAdminUsername returns the environment-provided administrator username.
func GetAdminUsername() string {
	return strings.TrimSpace(os.Getenv(AdminUsernameEnv))
}

// GetAdminPassword returns the environment-provided administrator password.
func GetAdminPassword() string {
	return os.Getenv(AdminPasswordEnv)
}

// AdminPasswordConfigured reports whether a non-blank administrator password is
// present in the environment.
func AdminPasswordConfigured() bool {
	return strings.TrimSpace(GetAdminPassword()) != ""
}

// AdminCredentialEnvConfigured reports whether the environment owns the
// administrator account; a half-configured pair is not a configured account.
func AdminCredentialEnvConfigured() bool {
	return GetAdminUsername() != "" && AdminPasswordConfigured()
}

// AdminCredentialMutationForbidden includes legacy accounts in the supported container runtime.
func AdminCredentialMutationForbidden() bool {
	return os.Getenv("XUI_IN_DOCKER") == "true" || AdminCredentialEnvConfigured()
}

// GetBinFolderPath returns the path to the binary folder, defaulting to "bin" if not set via XUI_BIN_FOLDER.
func GetBinFolderPath() string {
	binFolderPath := os.Getenv("XUI_BIN_FOLDER")
	if binFolderPath == "" {
		binFolderPath = "bin"
	}
	return binFolderPath
}

func getBaseDir() string {
	exePath, err := os.Executable()
	if err != nil {
		return "."
	}
	exeDir := filepath.Dir(exePath)
	exeDirLower := strings.ToLower(filepath.ToSlash(exeDir))
	if strings.Contains(exeDirLower, "/appdata/local/temp/") || strings.Contains(exeDirLower, "/go-build") {
		wd, err := os.Getwd()
		if err != nil {
			return "."
		}
		return wd
	}
	return exeDir
}

// GetDBFolderPath returns the path to the database folder based on environment variables or platform defaults.
func GetDBFolderPath() string {
	dbFolderPath := os.Getenv("XUI_DB_FOLDER")
	if dbFolderPath != "" {
		return dbFolderPath
	}
	if runtime.GOOS == "windows" {
		return getBaseDir()
	}
	return "/etc/x-ui"
}

// GetDBPath returns the full path to the database file.
func GetDBPath() string {
	return fmt.Sprintf("%s/%s.db", GetDBFolderPath(), GetName())
}

// GetDBKind returns the configured database backend: "sqlite" (default) or "postgres".
func GetDBKind() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("XUI_DB_TYPE")))
	switch v {
	case "postgres", "postgresql", "pg":
		return "postgres"
	default:
		return "sqlite"
	}
}

// GetDBDSN returns the PostgreSQL DSN from XUI_DB_DSN. Empty for sqlite.
func GetDBDSN() string {
	return strings.TrimSpace(os.Getenv("XUI_DB_DSN"))
}

const (
	DefaultDBImportMaxMiB          int64 = 256
	MinDBImportMaxMiB              int64 = 32
	MaxDBImportMaxMiB              int64 = 2048
	DBImportMultipartOverheadBytes int64 = 1 << 20

	DefaultSQLiteRestoreMaxStatementMiB int64 = 16
	MinSQLiteRestoreMaxStatementMiB     int64 = 1
	MaxSQLiteRestoreMaxStatementMiB     int64 = 256
)

var (
	// ErrInvalidDBImportMaxMiB indicates XUI_DB_IMPORT_MAX_MIB is malformed or out of range.
	ErrInvalidDBImportMaxMiB = errors.New("invalid XUI_DB_IMPORT_MAX_MIB")
	// ErrInvalidSQLiteRestoreMaxStatementMiB indicates XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB is malformed or out of range.
	ErrInvalidSQLiteRestoreMaxStatementMiB = errors.New("invalid XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB")
)

// GetDBImportMaxBytes parses XUI_DB_IMPORT_MAX_MIB (32..2048 MiB, default 256 MiB)
// into an exact byte limit without silent fallback on invalid values.
func GetDBImportMaxBytes() (int64, error) {
	return parseEnvMiBLimit(
		"XUI_DB_IMPORT_MAX_MIB",
		DefaultDBImportMaxMiB,
		MinDBImportMaxMiB,
		MaxDBImportMaxMiB,
		ErrInvalidDBImportMaxMiB,
	)
}

// GetSQLiteRestoreMaxStatementBytes parses XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB
// (1..256 MiB, default 16 MiB) into an exact byte limit without silent fallback.
func GetSQLiteRestoreMaxStatementBytes() (int64, error) {
	return parseEnvMiBLimit(
		"XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB",
		DefaultSQLiteRestoreMaxStatementMiB,
		MinSQLiteRestoreMaxStatementMiB,
		MaxSQLiteRestoreMaxStatementMiB,
		ErrInvalidSQLiteRestoreMaxStatementMiB,
	)
}

func parseEnvMiBLimit(envName string, defaultMiB, minMiB, maxMiB int64, sentinel error) (int64, error) {
	const bytesPerMiB int64 = 1024 * 1024
	raw, ok := os.LookupEnv(envName)
	if !ok {
		return defaultMiB * bytesPerMiB, nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("%w: must be a decimal integer between %d and %d MiB", sentinel, minMiB, maxMiB)
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] < '0' || trimmed[i] > '9' {
			return 0, fmt.Errorf("%w: %q is not a positive decimal integer (allowed: %d-%d MiB)", sentinel, trimmed, minMiB, maxMiB)
		}
	}
	mib, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || mib < minMiB || mib > maxMiB {
		return 0, fmt.Errorf("%w: %q is out of range (allowed: %d-%d MiB)", sentinel, trimmed, minMiB, maxMiB)
	}
	if mib > math.MaxInt64/bytesPerMiB {
		return 0, fmt.Errorf("%w: %q overflows byte limit", sentinel, trimmed)
	}
	return mib * bytesPerMiB, nil
}

// GetNodeTokenEncryptionMode returns off, migration, or required. Explicit
// policy prevents a missing key from silently downgrading encrypted storage.
func GetNodeTokenEncryptionMode() string {
	return strings.TrimSpace(os.Getenv("NODE_TOKEN_ENCRYPTION"))
}

// GetNodeTokenKeyFile returns the mode-0600 keyring path, configurable through
// XUI_NODE_TOKEN_KEY_FILE.
func GetNodeTokenKeyFile() string {
	if p := strings.TrimSpace(os.Getenv("XUI_NODE_TOKEN_KEY_FILE")); p != "" {
		return p
	}
	return "/etc/x-ui/node_token_key.json"
}

// GetNodeTokenKeyEnv returns the name of the env var holding a single base64
// 32-byte node-token key (secondary to the key file). Empty value => unused.
func GetNodeTokenKeyEnv() string {
	return "XUI_NODE_TOKEN_KEY"
}

// GetEnvFilePaths returns the candidate service environment file paths (the file
// systemd loads via EnvironmentFile) across the supported distro families.
func GetEnvFilePaths() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	return []string{
		"/etc/default/x-ui",
		"/etc/conf.d/x-ui",
		"/etc/sysconfig/x-ui",
	}
}

// GetLogFolder returns the path to the log folder based on environment variables or platform defaults.
func GetLogFolder() string {
	logFolderPath := os.Getenv("XUI_LOG_FOLDER")
	if logFolderPath != "" {
		return logFolderPath
	}
	// Under `go test` the Windows default below is CWD-relative ("./log"), which
	// scatters a log/ directory through the source tree (one per tested package).
	// Redirect test runs to a shared temp folder so the source tree stays clean.
	if testing.Testing() {
		return filepath.Join(os.TempDir(), "3x-ui-test-log")
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(".", "log")
	}
	return "/var/log/x-ui"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	return out.Sync()
}

func init() {
	if runtime.GOOS != "windows" {
		return
	}
	if os.Getenv("XUI_DB_FOLDER") != "" {
		return
	}
	oldDBFolder := "/etc/x-ui"
	oldDBPath := fmt.Sprintf("%s/%s.db", oldDBFolder, GetName())
	newDBFolder := GetDBFolderPath()
	newDBPath := fmt.Sprintf("%s/%s.db", newDBFolder, GetName())
	_, err := os.Stat(newDBPath)
	if err == nil {
		return // new exists
	}
	_, err = os.Stat(oldDBPath)
	if os.IsNotExist(err) {
		return // old does not exist
	}
	_ = copyFile(oldDBPath, newDBPath) // ignore error
}
