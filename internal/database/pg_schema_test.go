package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestQuotePgDumpSchemaPattern(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   string
	}{
		{name: "public", schema: "public", want: `"public"`},
		{name: "uppercase", schema: "TenantA", want: `"TenantA"`},
		{name: "space", schema: "tenant space", want: `"tenant space"`},
		{name: "dot", schema: "tenant.name", want: `"tenant.name"`},
		{name: "double quote", schema: `tenant"name`, want: `"tenant""name"`},
		{name: "asterisk wildcard", schema: "tenant*", want: `"tenant*"`},
		{name: "question wildcard", schema: "tenant?", want: `"tenant?"`},
		{name: "bracket class", schema: "tenant[1]", want: `"tenant[1]"`},
		{name: "leading dash", schema: "-tenant", want: `"-tenant"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := QuotePgDumpSchemaPattern(tc.schema)
			if err != nil {
				t.Fatalf("QuotePgDumpSchemaPattern(%q) unexpected error: %v", tc.schema, err)
			}
			if got != tc.want {
				t.Fatalf("QuotePgDumpSchemaPattern(%q) = %q, want %q", tc.schema, got, tc.want)
			}
		})
	}

	if _, err := QuotePgDumpSchemaPattern(""); err == nil {
		t.Fatal("QuotePgDumpSchemaPattern(\"\") expected error, got nil")
	}
}

func TestIsSystemPostgresSchema(t *testing.T) {
	system := []string{"pg_catalog", "information_schema", "pg_toast", "pg_temp_1", "pg_toast_temp_1"}
	for _, s := range system {
		if !isSystemPostgresSchema(s) {
			t.Fatalf("expected %q to be classified as system schema", s)
		}
	}
	user := []string{"public", "TenantA", "pg_custom", "my_pg_temp"}
	for _, s := range user {
		if isSystemPostgresSchema(s) {
			t.Fatalf("expected %q NOT to be classified as system schema", s)
		}
	}
}

func TestPgDumpAndRestoreArgs(t *testing.T) {
	dumpArgs, err := PgDumpArgs("xui", `tenant"space.1*`)
	if err != nil {
		t.Fatalf("PgDumpArgs failed: %v", err)
	}
	wantDump := []string{
		"--format=custom",
		"--no-owner",
		"--no-privileges",
		"--strict-names",
		`--schema="tenant""space.1*"`,
		"--dbname",
		"xui",
	}
	if !reflect.DeepEqual(dumpArgs, wantDump) {
		t.Fatalf("PgDumpArgs = %#v, want %#v", dumpArgs, wantDump)
	}

	restoreArgs, err := PgRestoreArgs("xui", `tenant"space.1*`, "/tmp/backup.dump")
	if err != nil {
		t.Fatalf("PgRestoreArgs failed: %v", err)
	}
	wantRestore := []string{
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-privileges",
		"--strict-names",
		`--schema="tenant""space.1*"`,
		"--single-transaction",
		"--dbname",
		"xui",
		"/tmp/backup.dump",
	}
	if !reflect.DeepEqual(restoreArgs, wantRestore) {
		t.Fatalf("PgRestoreArgs = %#v, want %#v", restoreArgs, wantRestore)
	}

	rollbackArgs, err := PgRollbackRestoreArgs("xui", `tenant"space.1*`, "/tmp/pre-import.dump")
	if err != nil {
		t.Fatalf("PgRollbackRestoreArgs failed: %v", err)
	}
	wantRollback := []string{
		"--no-owner",
		"--no-privileges",
		"--strict-names",
		`--schema="tenant""space.1*"`,
		"--single-transaction",
		"--dbname",
		"xui",
		"/tmp/pre-import.dump",
	}
	if !reflect.DeepEqual(rollbackArgs, wantRollback) {
		t.Fatalf("PgRollbackRestoreArgs = %#v, want %#v", rollbackArgs, wantRollback)
	}

	if _, err := PgDumpArgs("", "public"); err == nil {
		t.Fatal("expected error for empty db name in PgDumpArgs")
	}
	if _, err := PgDumpArgs("xui", ""); err == nil {
		t.Fatal("expected error for empty schema in PgDumpArgs")
	}
	if _, err := PgRestoreArgs("", "public", "/tmp/a.dump"); err == nil {
		t.Fatal("expected error for empty db name in PgRestoreArgs")
	}
	if _, err := PgRestoreArgs("xui", "", "/tmp/a.dump"); err == nil {
		t.Fatal("expected error for empty schema in PgRestoreArgs")
	}
	if _, err := PgRestoreArgs("xui", "public", " "); err == nil {
		t.Fatal("expected error for empty dump path in PgRestoreArgs")
	}
	if _, err := PgRollbackRestoreArgs("", "public", "/tmp/a.dump"); err == nil {
		t.Fatal("expected error for empty db name in PgRollbackRestoreArgs")
	}
	if _, err := PgRollbackRestoreArgs("xui", "", "/tmp/a.dump"); err == nil {
		t.Fatal("expected error for empty schema in PgRollbackRestoreArgs")
	}
	if _, err := PgRollbackRestoreArgs("xui", "public", " "); err == nil {
		t.Fatal("expected error for empty snapshot path in PgRollbackRestoreArgs")
	}
}

func TestResolveCanonicalPostgresSchema_ValidationErrors(t *testing.T) {
	if _, err := ResolveCanonicalPostgresSchema(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil *sql.DB")
	}
	if _, err := ResolveCanonicalPostgresSchemaFromGorm(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil *gorm.DB")
	}
}

func TestLivePostgres_ResolveCanonicalSchema(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("XUI_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("XUI_TEST_PG_DSN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminDB, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("sql.Open admin failed: %v", err)
	}
	defer func() { _ = adminDB.Close() }()

	suffixBytes := make([]byte, 4)
	if _, err := rand.Read(suffixBytes); err != nil {
		t.Fatal(err)
	}
	runID := hex.EncodeToString(suffixBytes)

	createSchemaWithTables := func(t *testing.T, schema string, tables []string) {
		t.Helper()
		qSchema := pgx.Identifier{schema}.Sanitize()
		if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+qSchema); err != nil {
			t.Fatalf("CREATE SCHEMA %s failed: %v", qSchema, err)
		}
		t.Cleanup(func() {
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanCancel()
			_, _ = adminDB.ExecContext(cleanCtx, "DROP SCHEMA IF EXISTS "+qSchema+" CASCADE")
		})
		for _, tbl := range tables {
			qTable := pgx.Identifier{schema, tbl}.Sanitize()
			if _, err := adminDB.ExecContext(ctx, "CREATE TABLE "+qTable+" (id integer primary key)"); err != nil {
				t.Fatalf("CREATE TABLE %s failed: %v", qTable, err)
			}
		}
	}

	openWithSearchPath := func(t *testing.T, schemas ...string) *sql.DB {
		t.Helper()
		parts := make([]string, len(schemas))
		for i, s := range schemas {
			parts[i] = pgx.Identifier{s}.Sanitize()
		}
		u, err := url.Parse(baseDSN)
		if err != nil {
			t.Fatalf("parse baseDSN: %v", err)
		}
		q := u.Query()
		q.Set("search_path", strings.Join(parts, ","))
		u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
		db, err := sql.Open("pgx", u.String())
		if err != nil {
			t.Fatalf("sql.Open search_path=%v: %v", schemas, err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	t.Run("exact schemas including uppercase, space, dot, double quote", func(t *testing.T) {
		names := []string{
			"simple_" + runID,
			"TenantA_" + runID,
			"tenant space_" + runID,
			"tenant.name_" + runID,
			`tenant"quote_` + runID,
		}
		for _, schema := range names {
			t.Run(schema, func(t *testing.T) {
				createSchemaWithTables(t, schema, canonicalCoreTables)
				db := openWithSearchPath(t, schema)
				got, err := ResolveCanonicalPostgresSchema(ctx, db)
				if err != nil {
					t.Fatalf("ResolveCanonicalPostgresSchema(%q) failed: %v", schema, err)
				}
				if got != schema {
					t.Fatalf("got schema %q, want %q", got, schema)
				}
			})
		}
	})

	t.Run("search_path shadowing resolves to first matching schema", func(t *testing.T) {
		first := "shadow_first_" + runID
		second := "shadow_second_" + runID
		createSchemaWithTables(t, first, canonicalCoreTables)
		createSchemaWithTables(t, second, canonicalCoreTables)

		db := openWithSearchPath(t, first, second)
		got, err := ResolveCanonicalPostgresSchema(ctx, db)
		if err != nil {
			t.Fatalf("ResolveCanonicalPostgresSchema shadowing failed: %v", err)
		}
		if got != first {
			t.Fatalf("got %q, want shadowed first schema %q", got, first)
		}
	})

	t.Run("split core tables across schemas fails", func(t *testing.T) {
		s1 := "split_one_" + runID
		s2 := "split_two_" + runID
		createSchemaWithTables(t, s1, []string{"users"})
		createSchemaWithTables(t, s2, []string{"settings", "inbounds"})

		db := openWithSearchPath(t, s1, s2)
		if _, err := ResolveCanonicalPostgresSchema(ctx, db); err == nil {
			t.Fatal("expected error for split schemas, got nil")
		} else if !strings.Contains(err.Error(), "split across schemas") {
			t.Fatalf("unexpected error for split schemas: %v", err)
		}
	})

	t.Run("missing core table fails", func(t *testing.T) {
		incomplete := "incomplete_" + runID
		createSchemaWithTables(t, incomplete, []string{"users", "settings"})

		db := openWithSearchPath(t, incomplete)
		if _, err := ResolveCanonicalPostgresSchema(ctx, db); err == nil {
			t.Fatal("expected error when inbounds is missing, got nil")
		} else if !strings.Contains(err.Error(), "inbounds") {
			t.Fatalf("unexpected error for missing table: %v", err)
		}
	})

	t.Run("cancelled context fails cleanly", func(t *testing.T) {
		s := "cancel_" + runID
		createSchemaWithTables(t, s, canonicalCoreTables)
		db := openWithSearchPath(t, s)
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire live connection: %v", err)
		}
		defer func() { _ = conn.Close() }()

		cancelCtx, cancelNow := context.WithCancel(ctx)
		cancelNow()
		_, err = resolveCanonicalPostgresSchemaOnConn(cancelCtx, conn)
		if err == nil {
			t.Fatal("expected context cancellation error, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled in error chain, got %v", err)
		}
		if strings.Contains(err.Error(), "was not found in active search_path") {
			t.Fatalf("context cancellation was misreported as a missing table: %v", err)
		}
	})

	t.Run("exact cleanup preserves schema identity and unrelated schemas", func(t *testing.T) {
		target := "rollback_target_" + runID
		unrelated := "rollback_other_" + runID
		createSchemaWithTables(t, target, canonicalCoreTables)
		createSchemaWithTables(t, unrelated, []string{"untouched"})
		qTarget := pgx.Identifier{target}.Sanitize()
		statements := []string{
			"GRANT USAGE ON SCHEMA " + qTarget + " TO PUBLIC",
			"CREATE SEQUENCE " + pgx.Identifier{target, "new_sequence"}.Sanitize(),
			"CREATE VIEW " + pgx.Identifier{target, "new_view"}.Sanitize() + " AS SELECT 1 AS value",
			"CREATE MATERIALIZED VIEW " + pgx.Identifier{target, "new_matview"}.Sanitize() + " AS SELECT 1 AS value",
			"CREATE TYPE " + pgx.Identifier{target, "new_enum"}.Sanitize() + " AS ENUM ('a', 'b')",
			"CREATE DOMAIN " + pgx.Identifier{target, "new_domain"}.Sanitize() + " AS text CHECK (VALUE <> '')",
			"CREATE TYPE " + pgx.Identifier{target, "new_composite"}.Sanitize() + " AS (value integer)",
			"CREATE FUNCTION " + pgx.Identifier{target, "new_function"}.Sanitize() + "() RETURNS integer LANGUAGE sql AS 'SELECT 1'",
		}
		for _, statement := range statements {
			if _, err := adminDB.ExecContext(ctx, statement); err != nil {
				t.Fatalf("prepare exact cleanup fixture with %q: %v", statement, err)
			}
		}

		var ownerBefore uint32
		var aclBefore sql.NullString
		if err := adminDB.QueryRowContext(ctx, `SELECT nspowner, nspacl::text FROM pg_catalog.pg_namespace WHERE nspname = $1`, target).Scan(&ownerBefore, &aclBefore); err != nil {
			t.Fatalf("read schema identity before cleanup: %v", err)
		}
		size, err := PostgresSchemaSize(ctx, baseDSN, target)
		if err != nil || size <= 0 {
			t.Fatalf("PostgresSchemaSize = %d, err=%v, want positive", size, err)
		}

		if err := ClearPostgresSchemaObjects(ctx, baseDSN, target); err != nil {
			t.Fatalf("ClearPostgresSchemaObjects: %v", err)
		}

		var ownerAfter uint32
		var aclAfter sql.NullString
		if err := adminDB.QueryRowContext(ctx, `SELECT nspowner, nspacl::text FROM pg_catalog.pg_namespace WHERE nspname = $1`, target).Scan(&ownerAfter, &aclAfter); err != nil {
			t.Fatalf("canonical schema was dropped: %v", err)
		}
		if ownerAfter != ownerBefore || aclAfter != aclBefore {
			t.Fatalf("schema identity changed: owner %d->%d acl %v->%v", ownerBefore, ownerAfter, aclBefore, aclAfter)
		}

		var targetRelations, targetRoutines, targetTypes int
		if err := adminDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p','v','m','S','f')`, target).Scan(&targetRelations); err != nil {
			t.Fatal(err)
		}
		if err := adminDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1`, target).Scan(&targetRoutines); err != nil {
			t.Fatal(err)
		}
		if err := adminDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace LEFT JOIN pg_catalog.pg_class c ON c.oid=t.typrelid WHERE n.nspname=$1 AND t.typisdefined AND t.typtype IN ('c','d','e') AND (t.typrelid=0 OR c.relkind='c')`, target).Scan(&targetTypes); err != nil {
			t.Fatal(err)
		}
		if targetRelations != 0 || targetRoutines != 0 || targetTypes != 0 {
			t.Fatalf("canonical schema not empty after cleanup: relations=%d routines=%d types=%d", targetRelations, targetRoutines, targetTypes)
		}
		var unrelatedCount int
		if err := adminDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='untouched'`, unrelated).Scan(&unrelatedCount); err != nil {
			t.Fatal(err)
		}
		if unrelatedCount != 1 {
			t.Fatalf("cleanup touched unrelated schema: untouched table count=%d", unrelatedCount)
		}
	})
}
