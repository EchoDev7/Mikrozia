package database

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func writeTestCertFiles(t *testing.T) (caPath, certPath, keyPath string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "root", "cert")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mikrozia-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	caPath = filepath.Join(dir, "pg-ca.crt")
	certPath = filepath.Join(dir, "client.crt")
	keyPath = filepath.Join(dir, "client.key")
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return caPath, certPath, keyPath
}

func TestParsePostgresConnSpec_SupportedFormats(t *testing.T) {
	caPath, certPath, keyPath := writeTestCertFiles(t)
	cases := []struct {
		name     string
		dsn      string
		wantHost string
		wantPort string
		wantDB   string
		wantUser string
		wantPass string
		wantSSL  string
		wantRoot string
		wantCert string
		wantKey  string
		wantCT   int
		wantApp  string
	}{
		{
			name:     "standard postgres URI",
			dsn:      "postgres://xui:pass123@db.example.com:5433/mikrozia",
			wantHost: "db.example.com",
			wantPort: "5433",
			wantDB:   "mikrozia",
			wantUser: "xui",
			wantPass: "pass123",
		},
		{
			name:     "postgresql scheme",
			dsn:      "postgresql://xui:pass123@db.example.com:5432/mikrozia",
			wantHost: "db.example.com",
			wantPort: "5432",
			wantDB:   "mikrozia",
			wantUser: "xui",
			wantPass: "pass123",
		},
		{
			name:     "URI with percent-encoded complex password",
			dsn:      "postgres://xui%2Badmin:p%40ss%3A%2F%5C%20word%231@192.0.2.10:5432/xui?sslmode=require&connect_timeout=12&application_name=mikrozia-panel",
			wantHost: "192.0.2.10",
			wantPort: "5432",
			wantDB:   "xui",
			wantUser: "xui+admin",
			wantPass: `p@ss:/\ word#1`,
			wantSSL:  "require",
			wantCT:   12,
			wantApp:  "mikrozia-panel",
		},
		{
			name:     "keyword-value with quoted space backslash colon at-sign",
			dsn:      fmt.Sprintf(`host=db.internal port=5432 user=xui password='complex \'secret\\:p@ss' dbname=xui sslmode=verify-ca sslrootcert=%s sslcert=%s sslkey=%s connect_timeout=15 application_name='mikrozia app'`, caPath, certPath, keyPath),
			wantHost: "db.internal",
			wantPort: "5432",
			wantDB:   "xui",
			wantUser: "xui",
			wantPass: `complex 'secret\:p@ss`,
			wantSSL:  "verify-ca",
			wantRoot: caPath,
			wantCert: certPath,
			wantKey:  keyPath,
			wantCT:   15,
			wantApp:  "mikrozia app",
		},
		{
			name:     "IPv4 URI",
			dsn:      "postgres://u:p@203.0.113.55:5432/db1?sslmode=disable",
			wantHost: "203.0.113.55",
			wantPort: "5432",
			wantDB:   "db1",
			wantUser: "u",
			wantPass: "p",
			wantSSL:  "disable",
		},
		{
			name:     "IPv6 bracketed URI with verify-full",
			dsn:      fmt.Sprintf("postgres://u:p@[2001:db8::10]:5434/db6?sslmode=verify-full&sslrootcert=%s", caPath),
			wantHost: "2001:db8::10",
			wantPort: "5434",
			wantDB:   "db6",
			wantUser: "u",
			wantPass: "p",
			wantSSL:  "verify-full",
			wantRoot: caPath,
		},
		{
			name:     "IPv6 keyword-value",
			dsn:      "host=::1 port=5432 user=u dbname=db6 sslmode=require",
			wantHost: "::1",
			wantPort: "5432",
			wantDB:   "db6",
			wantUser: "u",
			wantPass: "",
			wantSSL:  "require",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := ParsePostgresConnSpec(tc.dsn)
			if err != nil {
				t.Fatalf("ParsePostgresConnSpec failed: %v", err)
			}
			if spec.Host != tc.wantHost || spec.Port != tc.wantPort || spec.Database != tc.wantDB ||
				spec.User != tc.wantUser || spec.Password != tc.wantPass || spec.SSLMode != tc.wantSSL ||
				spec.SSLRootCert != tc.wantRoot || spec.SSLCert != tc.wantCert || spec.SSLKey != tc.wantKey ||
				spec.ConnectTimeout != tc.wantCT || spec.ApplicationName != tc.wantApp {
				t.Fatalf("got %+v, want host=%q port=%q db=%q user=%q pass=%q ssl=%q root=%q cert=%q key=%q ct=%d app=%q",
					spec, tc.wantHost, tc.wantPort, tc.wantDB, tc.wantUser, tc.wantPass, tc.wantSSL,
					tc.wantRoot, tc.wantCert, tc.wantKey, tc.wantCT, tc.wantApp)
			}
		})
	}
}

func TestParsePostgresConnSpec_RejectsUnknownAndMultiHostWithoutLeakingSecret(t *testing.T) {
	cases := []struct {
		name   string
		dsn    string
		secret string
	}{
		{
			name:   "unknown parameter in URI",
			dsn:    "postgres://xui:SuperSecret99@db.example:5432/xui?unknown_opt=val",
			secret: "SuperSecret99",
		},
		{
			name:   "unknown parameter in keyword-value",
			dsn:    "host=db.example port=5432 user=xui password='Secret Pass' dbname=xui bogus_key=1",
			secret: "Secret Pass",
		},
		{
			name:   "multi-host URI",
			dsn:    "postgres://xui:MultiSecret1@db1.example:5432,db2.example:5433/xui?sslmode=require",
			secret: "MultiSecret1",
		},
		{
			name:   "multi-host keyword-value",
			dsn:    "host=db1.example,db2.example port=5432,5433 user=xui password=MultiSecret2 dbname=xui",
			secret: "MultiSecret2",
		},
		{
			name:   "missing database name",
			dsn:    "postgres://xui:NoDBSecret@db.example:5432",
			secret: "NoDBSecret",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePostgresConnSpec(tc.dsn)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if strings.Contains(err.Error(), tc.secret) {
				t.Fatalf("error leaked secret %q: %v", tc.secret, err)
			}
		})
	}
}

func TestPgPassEscapeAndToolEnvSecurity(t *testing.T) {
	caPath, certPath, keyPath := writeTestCertFiles(t)
	dsn := fmt.Sprintf(`host=2001:db8::10 port=5432 user=xui password='p:a\\s@word' dbname=xui sslmode=verify-full sslrootcert=%s sslcert=%s sslkey=%s connect_timeout=9 application_name=mikrozia`, caPath, certPath, keyPath)
	spec, err := ParsePostgresConnSpec(dsn)
	if err != nil {
		t.Fatalf("ParsePostgresConnSpec failed: %v", err)
	}

	line := FormatPgPassLine(spec)
	wantLine := `2001\:db8\:\:10:5432:xui:xui:p\:a\\s@word` + "\n"
	if line != wantLine {
		t.Fatalf("FormatPgPassLine = %q, want %q", line, wantLine)
	}

	baseEnv := []string{
		"PATH=/usr/bin:/bin",
		"PGPASSWORD=stale-secret",
		"PGPASSFILE=/tmp/stale-pgpass",
		"PGHOST=wrong-host",
		"PGSSLMODE=disable",
	}
	toolEnv, err := spec.PrepareToolEnv(baseEnv)
	if err != nil {
		t.Fatalf("PrepareToolEnv failed: %v", err)
	}

	info, err := os.Stat(toolEnv.PassfilePath)
	if err != nil {
		t.Fatalf("stat PGPASSFILE failed: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("PGPASSFILE mode = %04o, want 0600", mode)
	}

	content, err := os.ReadFile(toolEnv.PassfilePath)
	if err != nil {
		t.Fatalf("read PGPASSFILE failed: %v", err)
	}
	if string(content) != wantLine {
		t.Fatalf("PGPASSFILE content = %q, want %q", string(content), wantLine)
	}

	for _, entry := range toolEnv.Env {
		if strings.HasPrefix(entry, "PGPASSWORD=") {
			t.Fatalf("PGPASSWORD must not be present in child env, got %q", entry)
		}
		if strings.Contains(entry, `p:a\s@word`) {
			t.Fatalf("password leaked into child env entry: %q", entry)
		}
	}
	expectedVars := []string{
		"PATH=/usr/bin:/bin",
		"PGHOST=2001:db8::10",
		"PGPORT=5432",
		"PGDATABASE=xui",
		"PGUSER=xui",
		"PGPASSFILE=" + toolEnv.PassfilePath,
		"PGSSLMODE=verify-full",
		"PGSSLROOTCERT=" + caPath,
		"PGSSLCERT=" + certPath,
		"PGSSLKEY=" + keyPath,
		"PGCONNECT_TIMEOUT=9",
		"PGAPPNAME=mikrozia",
	}
	for _, want := range expectedVars {
		if !slices.Contains(toolEnv.Env, want) {
			t.Fatalf("child env missing %q; env=%v", want, toolEnv.Env)
		}
	}
	if slices.Contains(toolEnv.Env, "PGPASSFILE=/tmp/stale-pgpass") ||
		slices.Contains(toolEnv.Env, "PGHOST=wrong-host") ||
		slices.Contains(toolEnv.Env, "PGSSLMODE=disable") {
		t.Fatalf("stale PG* env vars were not stripped: %v", toolEnv.Env)
	}

	if err := toolEnv.Cleanup(); err != nil {
		t.Fatalf("first Cleanup failed: %v", err)
	}
	if err := toolEnv.Cleanup(); err != nil {
		t.Fatalf("second Cleanup failed: %v", err)
	}
	if _, err := os.Stat(toolEnv.PassfilePath); !os.IsNotExist(err) {
		t.Fatalf("PGPASSFILE still exists after Cleanup: %v", err)
	}
}

func TestRedactPostgresError_RedactsAllSecretForms(t *testing.T) {
	cases := []struct {
		name    string
		dsn     string
		rawErr  string
		secrets []string
	}{
		{
			name:    "URI password and percent encoding",
			dsn:     "postgres://xui:p%40ss%3A123@db.example:5432/xui?sslmode=require",
			rawErr:  `failed to connect postgres://xui:p%40ss%3A123@db.example:5432/xui?sslmode=require: auth failed for p@ss:123`,
			secrets: []string{"p%40ss%3A123", "p@ss:123"},
		},
		{
			name:    "keyword quoted password",
			dsn:     `host=db.example port=5432 user=xui password='my secret @1' dbname=xui`,
			rawErr:  `cannot parse host=db.example port=5432 user=xui password='my secret @1' dbname=xui with PGPASSWORD=envsecret PGPASSFILE=/tmp/x-ui-pgpass-123`,
			secrets: []string{"my secret @1", "envsecret", "/tmp/x-ui-pgpass-123"},
		},
		{
			name:    "unquoted password and raw DSN in parser error",
			dsn:     "host=db.example user=xui password=rawpass dbname=xui",
			rawErr:  "pgx parse error: invalid port in host=db.example user=xui password=rawpass dbname=xui",
			secrets: []string{"rawpass"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redacted := RedactPostgresError(errors.New(tc.rawErr), tc.dsn)
			if redacted == nil {
				t.Fatal("expected non-nil redacted error")
			}
			msg := redacted.Error()
			for _, s := range tc.secrets {
				if strings.Contains(msg, s) {
					t.Fatalf("redacted error still contains secret %q: %s", s, msg)
				}
			}
		})
	}
}

func TestOpenPostgresWithRetryPolicy_DeadlineAndSuccess(t *testing.T) {
	dsn := "postgres://xui:RetrySecret99@127.0.0.1:5432/xui?sslmode=disable"

	t.Run("stops at overall deadline and redacts error/log", func(t *testing.T) {
		var logBuf bytes.Buffer
		origWriter := log.Writer()
		log.SetOutput(&logBuf)
		defer log.SetOutput(origWriter)

		attempts := 0
		start := time.Now()
		_, err := openPostgresWithRetryPolicy(
			dsn,
			&gorm.Config{},
			120*time.Millisecond,
			40*time.Millisecond,
			[]time.Duration{0, 60 * time.Millisecond, 60 * time.Millisecond, 60 * time.Millisecond},
			func(ctx context.Context, _ string, _ *gorm.Config) (*gorm.DB, error) {
				attempts++
				return nil, fmt.Errorf("dial %s: connection refused", dsn)
			},
		)
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("expected error after deadline, got nil")
		}
		if elapsed > 500*time.Millisecond {
			t.Fatalf("retry exceeded bounded deadline window: %v", elapsed)
		}
		if attempts == 0 || attempts > 3 {
			t.Fatalf("unexpected attempt count %d under 120ms deadline", attempts)
		}
		if strings.Contains(err.Error(), "RetrySecret99") || strings.Contains(logBuf.String(), "RetrySecret99") {
			t.Fatalf("secret leaked in err=%q or log=%q", err.Error(), logBuf.String())
		}
	})

	t.Run("succeeds before deadline after transient failures", func(t *testing.T) {
		attempts := 0
		fakeDB := &gorm.DB{}
		got, err := openPostgresWithRetryPolicy(
			dsn,
			&gorm.Config{},
			300*time.Millisecond,
			50*time.Millisecond,
			[]time.Duration{0, 20 * time.Millisecond, 20 * time.Millisecond},
			func(ctx context.Context, _ string, _ *gorm.Config) (*gorm.DB, error) {
				attempts++
				if attempts < 3 {
					return nil, errors.New("not ready")
				}
				return fakeDB, nil
			},
		)
		if err != nil {
			t.Fatalf("expected eventual success, got: %v", err)
		}
		if got != fakeDB || attempts != 3 {
			t.Fatalf("got=%v attempts=%d, want fakeDB and 3 attempts", got, attempts)
		}
	})
}

func TestSQLiteStartupUnchanged(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	t.Setenv("XUI_DB_TYPE", "")
	t.Setenv("XUI_DB_DSN", "")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB sqlite failed: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	if db == nil {
		t.Fatal("expected initialized SQLite db, got nil")
	}
}

func TestPostgresMigrationNextStepsMessage_IsRouterOSAware(t *testing.T) {
	msg := postgresMigrationNextStepsMessage()
	for _, want := range []string{"/container/envs", "XUI_DB_TYPE=postgres", "XUI_DB_DSN", "/etc/x-ui", "/root/cert"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("migration message %q missing %q", msg, want)
		}
	}
	for _, forbidden := range []string{"/etc/default/x-ui", "systemctl", "restart x-ui", "XUI_DB_DSN="} {
		if strings.Contains(msg, forbidden) {
			t.Fatalf("migration message %q contains forbidden VPS/secret text %q", msg, forbidden)
		}
	}
}

func TestPrepareToolEnv_NeverEmitsPGOPTIONS(t *testing.T) {
	spec, err := ParsePostgresConnSpec("postgres://xui:OptSecret1@db.example:5432/xui?sslmode=require&search_path=tenant_a%20-c%20bad")
	if err != nil {
		t.Fatalf("ParsePostgresConnSpec failed: %v", err)
	}
	toolEnv, err := spec.PrepareToolEnv([]string{"PATH=/usr/bin", "PGOPTIONS=-c statement_timeout=1"})
	if err != nil {
		t.Fatalf("PrepareToolEnv failed: %v", err)
	}
	t.Cleanup(func() { _ = toolEnv.Cleanup() })
	for _, entry := range toolEnv.Env {
		if strings.HasPrefix(entry, "PGOPTIONS=") {
			t.Fatalf("PGOPTIONS must never be passed to native tools, got %q", entry)
		}
	}
}

func TestLivePostgres_URI_Keyword_AutoMigrate_Ping(t *testing.T) {
	uriDSN := strings.TrimSpace(os.Getenv("XUI_TEST_PG_DSN"))
	if uriDSN == "" {
		t.Skip("set XUI_TEST_PG_DSN to run live PostgreSQL 18 integration test")
	}

	spec, err := ParsePostgresConnSpec(uriDSN)
	if err != nil {
		t.Fatalf("ParsePostgresConnSpec failed: %v", err)
	}

	kwDSN := fmt.Sprintf("host=%s port=%s user=%s password='%s' dbname=%s sslmode=%s",
		spec.Host, spec.Port, spec.User,
		strings.ReplaceAll(strings.ReplaceAll(spec.Password, `\`, `\\`), `'`, `\'`),
		spec.Database,
		func() string {
			if spec.SSLMode == "" {
				return "disable"
			}
			return spec.SSLMode
		}(),
	)

	for _, tc := range []struct {
		name string
		dsn  string
	}{
		{"URI DSN", uriDSN},
		{"Keyword/Value DSN", kwDSN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XUI_DB_TYPE", "postgres")
			t.Setenv("XUI_DB_DSN", tc.dsn)
			if err := InitDB(filepath.Join(t.TempDir(), "unused.db")); err != nil {
				t.Fatalf("InitDB(%s) failed: %v", tc.name, err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatalf("db.DB() failed: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := sqlDB.PingContext(ctx); err != nil {
				t.Fatalf("PingContext failed: %v", err)
			}
			_ = CloseDB()
		})
	}

	t.Run("wrong password never leaks secret in error or log", func(t *testing.T) {
		var logBuf bytes.Buffer
		origWriter := log.Writer()
		log.SetOutput(&logBuf)
		defer log.SetOutput(origWriter)

		badSecret := "WrongLiveSecret!99"
		badDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			spec.User, badSecret, spec.Host, spec.Port, spec.Database)
		_, err := openPostgresWithRetryPolicy(
			badDSN,
			&gorm.Config{},
			3*time.Second,
			2*time.Second,
			[]time.Duration{0},
			openAndPingPostgres,
		)
		if err == nil {
			t.Fatal("expected auth error with wrong password, got nil")
		}
		if strings.Contains(err.Error(), badSecret) || strings.Contains(logBuf.String(), badSecret) {
			t.Fatalf("secret %q leaked in err=%q or log=%q", badSecret, err.Error(), logBuf.String())
		}
	})
}
