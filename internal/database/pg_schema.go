package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

var canonicalCoreTables = []string{"users", "settings", "inbounds"}

const resolveTableSchemaQuery = `
SELECT n.nspname
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.oid = pg_catalog.to_regclass($1)
  AND c.relkind IN ('r', 'p')
`

// ResolveCanonicalPostgresSchema resolves the single live PostgreSQL schema
// selected by the session search_path for core tables on one pinned connection.
func ResolveCanonicalPostgresSchema(ctx context.Context, sqlDB *sql.DB) (string, error) {
	if sqlDB == nil {
		return "", errors.New("postgres database handle is nil")
	}

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("pin postgres connection for schema resolution: %w", err)
	}
	defer func() { _ = conn.Close() }()
	return resolveCanonicalPostgresSchemaOnConn(ctx, conn)
}

func resolveCanonicalPostgresSchemaOnConn(ctx context.Context, conn *sql.Conn) (string, error) {
	if conn == nil {
		return "", errors.New("postgres connection is nil")
	}
	var canonical string
	for _, table := range canonicalCoreTables {
		var schemaName string
		err := conn.QueryRowContext(ctx, resolveTableSchemaQuery, table).Scan(&schemaName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("core table %q was not found in active search_path", table)
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", fmt.Errorf("resolve schema for core table %q: %w", table, errors.Join(ctxErr, err))
			}
			return "", fmt.Errorf("resolve schema for core table %q: %w", table, err)
		}
		if schemaName == "" {
			return "", fmt.Errorf("core table %q was not found in active search_path", table)
		}
		if isSystemPostgresSchema(schemaName) {
			return "", fmt.Errorf("core table %q resolved to unsupported system schema %q", table, schemaName)
		}
		if canonical == "" {
			canonical = schemaName
			continue
		}
		if schemaName != canonical {
			return "", fmt.Errorf("core tables are split across schemas (%q vs %q)", canonical, schemaName)
		}
	}

	return canonical, nil
}

// ResolveCanonicalPostgresSchemaFromGorm extracts the underlying *sql.DB from
// gdb and resolves the canonical live PostgreSQL schema on a pinned connection.
func ResolveCanonicalPostgresSchemaFromGorm(ctx context.Context, gdb *gorm.DB) (string, error) {
	if gdb == nil {
		return "", errors.New("postgres gorm handle is nil")
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return "", fmt.Errorf("obtain underlying sql.DB for schema resolution: %w", err)
	}
	return ResolveCanonicalPostgresSchema(ctx, sqlDB)
}

func isSystemPostgresSchema(schema string) bool {
	switch schema {
	case "pg_catalog", "information_schema", "pg_toast":
		return true
	default:
		return strings.HasPrefix(schema, "pg_temp") || strings.HasPrefix(schema, "pg_toast_temp")
	}
}

// QuotePgDumpSchemaPattern formats an exact PostgreSQL schema name as a
// literal double-quoted pg_dump --schema pattern to disable wildcard matching.
func QuotePgDumpSchemaPattern(schema string) (string, error) {
	if schema == "" {
		return "", errors.New("postgres schema name cannot be empty")
	}
	return `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`, nil
}

// PgDumpArgs builds discrete pg_dump arguments scoped strictly to canonicalSchema.
func PgDumpArgs(databaseName, canonicalSchema string) ([]string, error) {
	if strings.TrimSpace(databaseName) == "" {
		return nil, errors.New("postgres database name cannot be empty")
	}
	pattern, err := QuotePgDumpSchemaPattern(canonicalSchema)
	if err != nil {
		return nil, err
	}
	return []string{
		"--format=custom",
		"--no-owner",
		"--no-privileges",
		"--strict-names",
		"--schema=" + pattern,
		"--dbname",
		databaseName,
	}, nil
}

// PgRestoreArgs builds discrete pg_restore arguments scoped strictly to canonicalSchema.
func PgRestoreArgs(databaseName, canonicalSchema, dumpPath string) ([]string, error) {
	if strings.TrimSpace(databaseName) == "" {
		return nil, errors.New("postgres database name cannot be empty")
	}
	pattern, err := QuotePgDumpSchemaPattern(canonicalSchema)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(dumpPath) == "" {
		return nil, errors.New("postgres dump path cannot be empty")
	}
	return []string{
		"--clean",
		"--if-exists",
		"--no-owner",
		"--no-privileges",
		"--strict-names",
		"--schema=" + pattern,
		"--single-transaction",
		"--dbname",
		databaseName,
		dumpPath,
	}, nil
}

// PgRollbackRestoreArgs restores a pre-import snapshot after the canonical
// schema has been emptied explicitly. It intentionally omits --clean so
// pg_restore cannot drop and recreate the schema itself (which would change its
// owner or ACL).
func PgRollbackRestoreArgs(databaseName, canonicalSchema, snapshotPath string) ([]string, error) {
	if strings.TrimSpace(databaseName) == "" {
		return nil, errors.New("postgres database name cannot be empty")
	}
	pattern, err := QuotePgDumpSchemaPattern(canonicalSchema)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(snapshotPath) == "" {
		return nil, errors.New("postgres snapshot path cannot be empty")
	}
	return []string{
		"--no-owner",
		"--no-privileges",
		"--strict-names",
		"--schema=" + pattern,
		"--single-transaction",
		"--dbname",
		databaseName,
		snapshotPath,
	}, nil
}

const postgresSchemaSizeQuery = `
SELECT COALESCE(SUM(pg_catalog.pg_total_relation_size(c.oid)), 0)::bigint
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1
  AND c.relkind IN ('r', 'p', 'm', 'S', 'f')
`

// PostgresSchemaSize returns the live on-server size used by relations in one
// exact schema. It opens an independent connection so it remains usable after
// the panel's GORM pool has been closed for an import.
func PostgresSchemaSize(ctx context.Context, dsn, schema string) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := ParsePostgresConnSpec(dsn); err != nil {
		return 0, err
	}
	if schema == "" || isSystemPostgresSchema(schema) {
		return 0, fmt.Errorf("unsupported postgres schema %q", schema)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return 0, RedactPostgresError(err, dsn)
	}
	defer func() { _ = db.Close() }()

	var size int64
	if err := db.QueryRowContext(ctx, postgresSchemaSizeQuery, schema).Scan(&size); err != nil {
		return 0, RedactPostgresError(err, dsn)
	}
	if size < 0 {
		return 0, errors.New("postgres schema size cannot be negative")
	}
	return size, nil
}

const postgresSchemaDropStatementsQuery = `
SELECT ddl
FROM (
    SELECT 10 AS priority,
           CASE p.prokind
             WHEN 'p' THEN pg_catalog.format('DROP PROCEDURE IF EXISTS %I.%I(%s) CASCADE', n.nspname, p.proname, pg_catalog.pg_get_function_identity_arguments(p.oid))
             WHEN 'a' THEN pg_catalog.format('DROP AGGREGATE IF EXISTS %I.%I(%s) CASCADE', n.nspname, p.proname, pg_catalog.pg_get_function_identity_arguments(p.oid))
             ELSE pg_catalog.format('DROP FUNCTION IF EXISTS %I.%I(%s) CASCADE', n.nspname, p.proname, pg_catalog.pg_get_function_identity_arguments(p.oid))
           END AS ddl
    FROM pg_catalog.pg_proc p
    JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = $1

    UNION ALL

    SELECT 20 AS priority,
           pg_catalog.format(
             'DROP %s IF EXISTS %I.%I CASCADE',
             CASE c.relkind
               WHEN 'v' THEN 'VIEW'
               WHEN 'm' THEN 'MATERIALIZED VIEW'
               WHEN 'S' THEN 'SEQUENCE'
               WHEN 'f' THEN 'FOREIGN TABLE'
               ELSE 'TABLE'
             END,
             n.nspname,
             c.relname
           ) AS ddl
    FROM pg_catalog.pg_class c
    JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = $1
      AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')

    UNION ALL

    SELECT 30 AS priority,
           pg_catalog.format('DROP TYPE IF EXISTS %I.%I CASCADE', n.nspname, t.typname) AS ddl
    FROM pg_catalog.pg_type t
    JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
    LEFT JOIN pg_catalog.pg_class c ON c.oid = t.typrelid
    WHERE n.nspname = $1
      AND t.typisdefined
      AND t.typtype IN ('c', 'd', 'e')
      AND (t.typrelid = 0 OR c.relkind = 'c')
) objects
ORDER BY priority, ddl
`

// ClearPostgresSchemaObjects removes every supported object owned by exactly
// one canonical schema in a single transaction while preserving the schema
// itself, its owner, and its ACL. Other schemas are never addressed.
func ClearPostgresSchemaObjects(ctx context.Context, dsn, schema string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := ParsePostgresConnSpec(dsn); err != nil {
		return err
	}
	if schema == "" || isSystemPostgresSchema(schema) {
		return fmt.Errorf("unsupported postgres schema %q", schema)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return RedactPostgresError(err, dsn)
	}
	defer func() { _ = db.Close() }()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return RedactPostgresError(err, dsn)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.QueryContext(ctx, postgresSchemaDropStatementsQuery, schema)
	if err != nil {
		return RedactPostgresError(err, dsn)
	}
	defer func() { _ = rows.Close() }()
	var statements []string
	for rows.Next() {
		var statement string
		if scanErr := rows.Scan(&statement); scanErr != nil {
			_ = rows.Close()
			return RedactPostgresError(scanErr, dsn)
		}
		statements = append(statements, statement)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return RedactPostgresError(rowsErr, dsn)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return RedactPostgresError(closeErr, dsn)
	}

	for _, statement := range statements {
		if _, execErr := tx.ExecContext(ctx, statement); execErr != nil {
			return RedactPostgresError(execErr, dsn)
		}
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return RedactPostgresError(commitErr, dsn)
	}
	return nil
}
