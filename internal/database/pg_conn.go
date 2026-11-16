package database

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresConnSpec holds validated connection and TLS parameters extracted from
// a PostgreSQL URI or keyword/value DSN.
type PostgresConnSpec struct {
	Host            string
	Port            string
	Database        string
	User            string
	Password        string
	SSLMode         string
	SSLRootCert     string
	SSLCert         string
	SSLKey          string
	ConnectTimeout  int
	ApplicationName string
}

// PostgresToolEnv provides a sanitized environment and a 0600 PGPASSFILE for
// pg_dump and pg_restore without ever placing credentials in PGPASSWORD or CLI args.
type PostgresToolEnv struct {
	Env          []string
	Database     string
	PassfilePath string
	cleanupOnce  sync.Once
}

var allowedPostgresDSNKeys = []string{
	"host",
	"port",
	"dbname",
	"database",
	"user",
	"password",
	"sslmode",
	"sslrootcert",
	"sslcert",
	"sslkey",
	"connect_timeout",
	"application_name",
	"search_path",
}

var libpqEnvVars = map[string]struct{}{
	"PGHOST":                  {},
	"PGHOSTADDR":              {},
	"PGPORT":                  {},
	"PGDATABASE":              {},
	"PGUSER":                  {},
	"PGPASSWORD":              {},
	"PGPASSFILE":              {},
	"PGSERVICE":               {},
	"PGSERVICEFILE":           {},
	"PGREALM":                 {},
	"PGOPTIONS":               {},
	"PGAPPNAME":               {},
	"PGSSLMODE":               {},
	"PGREQUIRESSL":            {},
	"PGSSLCOMPRESSION":        {},
	"PGSSLCERT":               {},
	"PGSSLKEY":                {},
	"PGSSLROOTCERT":           {},
	"PGSSLCRL":                {},
	"PGSSLCRLDIR":             {},
	"PGSSLPASSWORD":           {},
	"PGSSLMINPROTOCOLVERSION": {},
	"PGSSLMAXPROTOCOLVERSION": {},
	"PGSSLNEGOTIATION":        {},
	"PGREQUIREPEER":           {},
	"PGKRBSRVNAME":            {},
	"PGGSSLIB":                {},
	"PGCONNECT_TIMEOUT":       {},
	"PGCLIENTENCODING":        {},
	"PGTARGETSESSIONATTRS":    {},
	"PGCHANNELBINDING":        {},
	"PGDATESTYLE":             {},
	"PGTZ":                    {},
	"PGGEQO":                  {},
	"PGSYSCONFDIR":            {},
	"PGLOCALEDIR":             {},
	"PGREQUIREAUTH":           {},
}

var (
	uriCredentialPattern = regexp.MustCompile(`(postgres(?:ql)?://)([^:@/\s]+):([^@/\s]+)@`)
	kwPasswordPattern    = regexp.MustCompile(`(?i)(\bpassword\s*=\s*)('(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"|\S*)`)
	pgPassEnvPattern     = regexp.MustCompile(`(\bPGPASS(?:WORD|FILE)=)(\S+)`)
)

// ParsePostgresConnSpec validates a PostgreSQL URI or keyword/value DSN using
// pgx/v5/pgconn and extracts matching TLS and libpq parameters.
func ParsePostgresConnSpec(dsn string) (*PostgresConnSpec, error) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return nil, errors.New("PostgreSQL DSN is empty")
	}

	params, isURI, err := extractDSNParams(trimmed)
	if err != nil {
		return nil, RedactPostgresError(err, trimmed)
	}

	cfg, err := pgconn.ParseConfigWithOptions(trimmed, pgconn.ParseConfigOptions{
		ConnStringAllowedKeys: allowedPostgresDSNKeys,
	})
	if err != nil {
		return nil, RedactPostgresError(err, trimmed)
	}

	for _, fb := range cfg.Fallbacks {
		if fb.Host != cfg.Host || fb.Port != cfg.Port {
			return nil, errors.New("multi-host PostgreSQL DSNs are not supported; specify a single host")
		}
	}

	if strings.Contains(params["host"], ",") || strings.Contains(params["port"], ",") {
		return nil, errors.New("multi-host PostgreSQL DSNs are not supported; specify a single host")
	}

	dbname := cfg.Database
	if isURI {
		if u, uErr := url.Parse(trimmed); uErr == nil {
			pathDB := strings.TrimPrefix(u.Path, "/")
			if pathDB == "" && params["dbname"] == "" && params["database"] == "" {
				dbname = ""
			}
		}
	} else if params["dbname"] == "" && params["database"] == "" {
		dbname = ""
	}
	if strings.TrimSpace(dbname) == "" {
		return nil, errors.New("PostgreSQL DSN is missing a database name")
	}

	var connectTimeout int
	if cfg.ConnectTimeout > 0 {
		connectTimeout = int(cfg.ConnectTimeout.Seconds())
	} else if rawCT := params["connect_timeout"]; rawCT != "" {
		if n, convErr := strconv.Atoi(rawCT); convErr == nil && n > 0 {
			connectTimeout = n
		}
	}

	spec := &PostgresConnSpec{
		Host:            cfg.Host,
		Port:            strconv.FormatUint(uint64(cfg.Port), 10),
		Database:        dbname,
		User:            cfg.User,
		Password:        cfg.Password,
		SSLMode:         params["sslmode"],
		SSLRootCert:     params["sslrootcert"],
		SSLCert:         params["sslcert"],
		SSLKey:          params["sslkey"],
		ConnectTimeout:  connectTimeout,
		ApplicationName: cfg.RuntimeParams["application_name"],
	}
	if spec.Host == "" {
		spec.Host = "127.0.0.1"
	}
	if spec.Port == "" || spec.Port == "0" {
		spec.Port = "5432"
	}
	return spec, nil
}

func extractDSNParams(dsn string) (map[string]string, bool, error) {
	lower := strings.ToLower(dsn)
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, true, errors.New("invalid PostgreSQL URI")
		}
		out := make(map[string]string)
		for k, vals := range u.Query() {
			if len(vals) > 0 {
				out[strings.ToLower(k)] = vals[len(vals)-1]
			}
		}
		if host := u.Hostname(); host != "" {
			out["host"] = host
		}
		if port := u.Port(); port != "" {
			out["port"] = port
		}
		return out, true, nil
	}

	out, err := parseLibpqKeywordValue(dsn)
	if err != nil {
		return nil, false, err
	}
	return out, false, nil
}

// parseLibpqKeywordValue parses libpq keyword=value connection strings,
// supporting single-quoted values and \' / \\ escapes.
func parseLibpqKeywordValue(dsn string) (map[string]string, error) {
	out := make(map[string]string)
	i := 0
	n := len(dsn)
	for i < n {
		for i < n && unicode.IsSpace(rune(dsn[i])) {
			i++
		}
		if i >= n {
			break
		}
		keyStart := i
		for i < n && dsn[i] != '=' && !unicode.IsSpace(rune(dsn[i])) {
			i++
		}
		key := strings.ToLower(dsn[keyStart:i])
		for i < n && unicode.IsSpace(rune(dsn[i])) {
			i++
		}
		if i >= n || dsn[i] != '=' {
			return nil, errors.New("invalid keyword/value PostgreSQL DSN syntax")
		}
		i++ // skip '='
		for i < n && unicode.IsSpace(rune(dsn[i])) {
			i++
		}

		var val strings.Builder
		if i < n && dsn[i] == '\'' {
			i++ // skip opening quote
			closed := false
			for i < n {
				ch := dsn[i]
				if ch == '\\' && i+1 < n {
					val.WriteByte(dsn[i+1])
					i += 2
					continue
				}
				if ch == '\'' {
					i++
					closed = true
					break
				}
				val.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, errors.New("unterminated quoted value in PostgreSQL DSN")
			}
		} else {
			for i < n && !unicode.IsSpace(rune(dsn[i])) {
				if dsn[i] == '\\' && i+1 < n {
					val.WriteByte(dsn[i+1])
					i += 2
					continue
				}
				val.WriteByte(dsn[i])
				i++
			}
		}
		if key != "" {
			out[key] = val.String()
		}
	}
	return out, nil
}

// EscapePgPassField escapes backslashes and colons for a PostgreSQL .pgpass entry.
func EscapePgPassField(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(escaped, ":", `\:`)
}

// FormatPgPassLine formats a single .pgpass line for spec.
func FormatPgPassLine(spec *PostgresConnSpec) string {
	user := spec.User
	if user == "" {
		user = "*"
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s\n",
		EscapePgPassField(spec.Host),
		EscapePgPassField(spec.Port),
		EscapePgPassField(spec.Database),
		EscapePgPassField(user),
		EscapePgPassField(spec.Password),
	)
}

// PreparePostgresToolEnv parses dsn and prepares an isolated child environment
// with a 0600 PGPASSFILE based on os.Environ().
func PreparePostgresToolEnv(dsn string) (*PostgresToolEnv, error) {
	spec, err := ParsePostgresConnSpec(dsn)
	if err != nil {
		return nil, err
	}
	return spec.PrepareToolEnv(os.Environ())
}

// PrepareToolEnv writes a 0600 temporary .pgpass file and builds a child env
// with all inherited PG* connection overrides removed.
func (s *PostgresConnSpec) PrepareToolEnv(baseEnv []string) (*PostgresToolEnv, error) {
	if s == nil {
		return nil, errors.New("nil PostgreSQL connection spec")
	}

	tmpFile, err := os.CreateTemp("", "x-ui-pgpass-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary PGPASSFILE: %w", err)
	}
	passPath := tmpFile.Name()

	cleanupOnErr := func(cause error) (*PostgresToolEnv, error) {
		_ = tmpFile.Close()
		_ = os.Remove(passPath)
		return nil, cause
	}

	if err := os.Chmod(passPath, 0o600); err != nil {
		return cleanupOnErr(fmt.Errorf("set PGPASSFILE permissions: %w", err))
	}
	if s.Password != "" {
		if _, err := tmpFile.WriteString(FormatPgPassLine(s)); err != nil {
			return cleanupOnErr(fmt.Errorf("write PGPASSFILE: %w", err))
		}
	}
	if err := tmpFile.Close(); err != nil {
		return cleanupOnErr(fmt.Errorf("close PGPASSFILE: %w", err))
	}

	env := make([]string, 0, len(baseEnv)+12)
	for _, entry := range baseEnv {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, isPG := libpqEnvVars[strings.ToUpper(key)]; isPG {
			continue
		}
		env = append(env, entry)
	}

	env = append(env,
		"PGHOST="+s.Host,
		"PGPORT="+s.Port,
		"PGDATABASE="+s.Database,
		"PGPASSFILE="+passPath,
	)
	if s.User != "" {
		env = append(env, "PGUSER="+s.User)
	}
	if s.SSLMode != "" {
		env = append(env, "PGSSLMODE="+s.SSLMode)
	}
	if s.SSLRootCert != "" {
		env = append(env, "PGSSLROOTCERT="+s.SSLRootCert)
	}
	if s.SSLCert != "" {
		env = append(env, "PGSSLCERT="+s.SSLCert)
	}
	if s.SSLKey != "" {
		env = append(env, "PGSSLKEY="+s.SSLKey)
	}
	if s.ConnectTimeout > 0 {
		env = append(env, fmt.Sprintf("PGCONNECT_TIMEOUT=%d", s.ConnectTimeout))
	}
	if s.ApplicationName != "" {
		env = append(env, "PGAPPNAME="+s.ApplicationName)
	}

	return &PostgresToolEnv{
		Env:          env,
		Database:     s.Database,
		PassfilePath: passPath,
	}, nil
}

// Cleanup removes the temporary PGPASSFILE. Calling Cleanup more than once is safe.
func (t *PostgresToolEnv) Cleanup() error {
	if t == nil {
		return nil
	}
	var removeErr error
	t.cleanupOnce.Do(func() {
		if t.PassfilePath != "" {
			if err := os.Remove(t.PassfilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				removeErr = err
			}
		}
	})
	return removeErr
}

// RedactPostgresError removes DSN credentials, PGPASSWORD, PGPASSFILE paths,
// and raw DSN strings from err without losing diagnostic context.
func RedactPostgresError(err error, dsn string) error {
	if err == nil {
		return nil
	}
	return errors.New(RedactPostgresString(err.Error(), dsn))
}

// RedactPostgresString scrubs PostgreSQL secrets and raw DSNs from text.
func RedactPostgresString(text, dsn string) string {
	if text == "" {
		return ""
	}
	redacted := text
	trimmedDSN := strings.TrimSpace(dsn)
	if trimmedDSN != "" {
		for _, secret := range extractSecretsForRedaction(trimmedDSN) {
			if len(secret) >= 4 {
				redacted = strings.ReplaceAll(redacted, secret, "[REDACTED]")
			}
		}
		redacted = strings.ReplaceAll(redacted, trimmedDSN, "[REDACTED_DSN]")
	}
	redacted = uriCredentialPattern.ReplaceAllString(redacted, "${1}${2}:[REDACTED]@")
	redacted = kwPasswordPattern.ReplaceAllString(redacted, "${1}[REDACTED]")
	redacted = pgPassEnvPattern.ReplaceAllString(redacted, "${1}[REDACTED]")
	return redacted
}

func extractSecretsForRedaction(dsn string) []string {
	var secrets []string
	add := func(s string) {
		if s == "" {
			return
		}
		secrets = append(secrets, s)
		if q := url.QueryEscape(s); q != s {
			secrets = append(secrets, q)
		}
		if p := url.PathEscape(s); p != s {
			secrets = append(secrets, p)
		}
		if e := EscapePgPassField(s); e != s {
			secrets = append(secrets, e)
		}
	}

	lower := strings.ToLower(dsn)
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil && u.User != nil {
			if pass, ok := u.User.Password(); ok {
				add(pass)
			}
		}
	} else if params, err := parseLibpqKeywordValue(dsn); err == nil {
		if pass := params["password"]; pass != "" {
			add(pass)
		}
	}
	if cfg, err := pgconn.ParseConfig(dsn); err == nil && cfg.Password != "" {
		add(cfg.Password)
	}
	return secrets
}
