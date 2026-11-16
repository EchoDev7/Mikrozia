package database

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-sqlite3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
)

// DumpSQLite writes a portable SQL text dump of the SQLite database at srcPath
// to outPath. The output mirrors the `sqlite3 .dump` format (schema + data +
// indexes wrapped in a transaction), so it can be rebuilt with RestoreSQLite or
// loaded by the sqlite3 CLI. The source database is opened read-only in effect
// and left untouched.
func DumpSQLite(srcPath, outPath string) error {
	return DumpSQLiteContext(context.Background(), srcPath, outPath)
}

// DumpSQLiteContext streams a portable SQL text dump of srcPath directly into
// a newly created 0600 file at outPath, removing incomplete output on error or
// cancellation.
func DumpSQLiteContext(ctx context.Context, srcPath, outPath string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	outFile, openErr := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if openErr != nil {
		return openErr
	}
	defer func() {
		if err != nil {
			_ = outFile.Close()
			_ = os.Remove(outPath)
		}
	}()

	if err = WriteSQLiteDump(ctx, srcPath, outFile); err != nil {
		return err
	}
	if err = outFile.Sync(); err != nil {
		return err
	}
	if err = outFile.Close(); err != nil {
		return err
	}
	return nil
}

// DumpSQLiteToBytes builds the same `sqlite3 .dump`-style SQL text as DumpSQLite
// in memory. Kept for small test/import helpers; HTTP migration export uses
// DumpSQLiteContext / WriteSQLiteDump directly.
func DumpSQLiteToBytes(srcPath string) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteSQLiteDump(context.Background(), srcPath, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteSQLiteDump streams a portable `sqlite3 .dump`-style SQL text dump of
// srcPath to dst using bounded buffered I/O without accumulating the full dump
// in memory.
func WriteSQLiteDump(ctx context.Context, srcPath string, dst io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if dst == nil {
		return errors.New("destination writer is required")
	}
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("source sqlite not found at %s: %w", srcPath, err)
	}

	gdb, err := gorm.Open(sqlite.Open(srcPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	// Pin one connection and hold a read transaction for the whole dump so
	// every table comes from the same SQLite snapshot even while the panel
	// keeps committing writes to a live WAL database.
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	}()

	bw := bufio.NewWriterSize(dst, 64*1024)
	if _, err := bw.WriteString("PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\n"); err != nil {
		return err
	}

	// Tables in creation order, each followed by its data.
	tables, err := listSQLiteDumpTables(ctx, conn)
	if err != nil {
		return err
	}

	for _, t := range tables {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := bw.WriteString(t.ddl); err != nil {
			return err
		}
		if _, err := bw.WriteString(";\n"); err != nil {
			return err
		}
		if err := dumpTableDataToWriter(ctx, conn, t.name, bw); err != nil {
			return err
		}
	}

	// AUTOINCREMENT bookkeeping, restored verbatim like the sqlite3 CLI does.
	if sqliteTableExistsContext(ctx, conn, "sqlite_sequence") {
		if _, err := bw.WriteString("DELETE FROM sqlite_sequence;\n"); err != nil {
			return err
		}
		if err := dumpTableDataToWriter(ctx, conn, "sqlite_sequence", bw); err != nil {
			return err
		}
	}

	// Indexes, triggers and views after the data is in place.
	rows2, err := conn.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE type IN ('index','trigger','view') AND sql IS NOT NULL ORDER BY rowid`)
	if err != nil {
		return err
	}
	defer rows2.Close()
	for rows2.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var ddl string
		if err := rows2.Scan(&ddl); err != nil {
			return err
		}
		if _, err := bw.WriteString(ddl); err != nil {
			return err
		}
		if _, err := bw.WriteString(";\n"); err != nil {
			return err
		}
	}
	if err := rows2.Err(); err != nil {
		return err
	}

	if _, err := bw.WriteString("COMMIT;\n"); err != nil {
		return err
	}
	return bw.Flush()
}

// RestoreSQLite rebuilds a SQLite database at dstPath from a SQL text dump
// produced by DumpSQLite (or `sqlite3 .dump`). dstPath must not already exist so
// an existing database is never clobbered silently.
func RestoreSQLite(dumpPath, dstPath string) error {
	return RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
}

// RestoreSQLiteContext streams a SQLite SQL text dump from dumpPath and replays
// each complete statement sequentially into a newly created database at dstPath.
// Bounded memory is used regardless of total dump file size, subject to
// XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB for any single statement.
func RestoreSQLiteContext(ctx context.Context, dumpPath, dstPath string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	maxStmtBytes, err := config.GetSQLiteRestoreMaxStatementBytes()
	if err != nil {
		return err
	}

	dumpFile, err := os.Open(dumpPath)
	if err != nil {
		return err
	}
	defer dumpFile.Close()

	artifacts := newSQLiteRestoreArtifacts(dstPath)
	if err := artifacts.preflight(); err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("destination already exists: %s", dstPath)
		}
		return err
	}
	artifacts.owned = true
	if closeErr := dstFile.Close(); closeErr != nil {
		if cleanupErr := artifacts.cleanup(); cleanupErr != nil {
			return errors.Join(closeErr, cleanupErr)
		}
		return closeErr
	}

	defer func() {
		if err != nil {
			if cleanupErr := artifacts.cleanup(); cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()

	dsn := dstPath + "?_busy_timeout=10000&_journal_mode=DELETE"
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return fmt.Errorf("restore failed opening destination sqlite: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("restore failed getting sql db: %w", err)
	}
	dbClosed := false
	defer func() {
		if !dbClosed {
			if closeErr := sqlDB.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("restore failed closing database: %w", closeErr))
			} else {
				dbClosed = true
			}
		}
	}()

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("restore failed acquiring connection: %w", err)
	}
	connClosed := false
	defer func() {
		if !connClosed {
			if closeErr := conn.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("restore failed closing connection: %w", closeErr))
			} else {
				connClosed = true
			}
		}
	}()

	if err := conn.Raw(func(driverConn any) error {
		sc, ok := driverConn.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("unexpected sqlite driver connection %T", driverConn)
		}
		sc.SetLimit(sqlite3.SQLITE_LIMIT_ATTACHED, 0)
		return nil
	}); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(dumpFile, 64*1024)
	err = streamSQLiteDumpStatements(ctx, reader, maxStmtBytes, func(stmt string, idx int, line, offset int64) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if _, execErr := conn.ExecContext(ctx, stmt); execErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("restore canceled at statement #%d (line %d, byte %d): %w", idx, line, offset, ctxErr)
			}
			if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
				return fmt.Errorf("restore canceled at statement #%d (line %d, byte %d): %w", idx, line, offset, execErr)
			}
			return fmt.Errorf("restore failed at statement #%d (line %d, byte %d): %w", idx, line, offset, execErr)
		}
		return nil
	})
	if err != nil {
		return err
	}

	autoCommit := true
	if rawErr := conn.Raw(func(driverConn any) error {
		sc, ok := driverConn.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("unexpected sqlite driver connection %T", driverConn)
		}
		autoCommit = sc.AutoCommit()
		return nil
	}); rawErr != nil {
		return rawErr
	}
	if !autoCommit {
		return errors.New("restore failed: dump ended with an open transaction")
	}

	if _, err := conn.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		return fmt.Errorf("restore failed setting journal_mode=DELETE: %w", err)
	}

	if err := conn.Close(); err != nil {
		return fmt.Errorf("restore failed closing connection: %w", err)
	}
	connClosed = true
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("restore failed closing database: %w", err)
	}
	dbClosed = true

	if err := os.Chmod(dstPath, 0o600); err != nil {
		return fmt.Errorf("restore failed setting permissions: %w", err)
	}
	if err := syncFile(dstPath); err != nil {
		return fmt.Errorf("restore failed syncing database file: %w", err)
	}

	info, err := os.Stat(dstPath)
	if err != nil {
		return fmt.Errorf("restore failed inspecting destination file: %w", err)
	}
	if info.Size() == 0 {
		return errors.New("restore failed: destination database file is empty")
	}
	for _, sidecar := range artifacts.sidecars() {
		if _, statErr := os.Lstat(sidecar.path); statErr == nil {
			return fmt.Errorf("restore failed: unexpected sqlite %s remains: %s", sidecar.kind, sidecar.path)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("restore failed checking %s %s: %w", sidecar.kind, sidecar.path, statErr)
		}
	}

	if err := ValidateSQLiteDB(dstPath); err != nil {
		return fmt.Errorf("restore failed validation: %w", err)
	}
	return nil
}

type sqliteRestoreArtifact struct {
	kind string
	path string
}

type sqliteRestoreArtifacts struct {
	owned bool
	items [4]sqliteRestoreArtifact
}

func newSQLiteRestoreArtifacts(dstPath string) sqliteRestoreArtifacts {
	return sqliteRestoreArtifacts{
		items: [4]sqliteRestoreArtifact{
			{kind: "destination", path: dstPath},
			{kind: "WAL sidecar", path: dstPath + "-wal"},
			{kind: "SHM sidecar", path: dstPath + "-shm"},
			{kind: "journal sidecar", path: dstPath + "-journal"},
		},
	}
}

func (a *sqliteRestoreArtifacts) sidecars() []sqliteRestoreArtifact {
	return a.items[1:]
}

func (a *sqliteRestoreArtifacts) preflight() error {
	for _, item := range a.items {
		if _, err := os.Lstat(item.path); err == nil {
			return fmt.Errorf("%s already exists: %s", item.kind, item.path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("restore failed checking %s %s: %w", item.kind, item.path, err)
		}
	}
	return nil
}

func (a *sqliteRestoreArtifacts) cleanup() error {
	if !a.owned {
		return nil
	}
	var errs []error
	for _, item := range a.items {
		if rmErr := os.Remove(item.path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			errs = append(errs, rmErr)
		}
	}
	return errors.Join(errs...)
}

// sqliteDumpQueryer is the subset of *sql.DB / *sql.Conn needed to read a
// single consistent SQLite snapshot.
type sqliteDumpQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// dumpTableDataToWriter streams one INSERT statement per row of table to bw.
func dumpTableDataToWriter(ctx context.Context, db sqliteDumpQueryer, table string, bw *bufio.Writer) error {
	rows, err := db.QueryContext(ctx, `SELECT * FROM "`+table+`"`)
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	n := len(cols)
	prefix := `INSERT INTO "` + table + `" VALUES(`

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		vals := make([]any, n)
		ptrs := make([]any, n)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		if _, err := bw.WriteString(prefix); err != nil {
			return err
		}
		for i, v := range vals {
			if i > 0 {
				if err := bw.WriteByte(','); err != nil {
					return err
				}
			}
			if _, err := bw.WriteString(sqliteLiteral(v)); err != nil {
				return err
			}
		}
		if _, err := bw.WriteString(");\n"); err != nil {
			return err
		}
	}
	return rows.Err()
}

// sqliteLiteral renders a scanned column value as a SQLite SQL literal.
func sqliteLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return "1"
		}
		return "0"
	case string:
		return quoteSQLiteText(x)
	case []byte:
		if utf8.Valid(x) {
			return quoteSQLiteText(string(x))
		}
		var sb strings.Builder
		sb.WriteString("X'")
		for _, c := range x {
			fmt.Fprintf(&sb, "%02x", c)
		}
		sb.WriteByte('\'')
		return sb.String()
	default:
		return quoteSQLiteText(fmt.Sprintf("%v", x))
	}
}

func quoteSQLiteText(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

type sqliteDumpTable struct{ name, ddl string }

func listSQLiteDumpTables(ctx context.Context, sqlDB sqliteDumpQueryer) ([]sqliteDumpTable, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND sql IS NOT NULL ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []sqliteDumpTable
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var o sqliteDumpTable
		if err := rows.Scan(&o.name, &o.ddl); err != nil {
			return nil, err
		}
		tables = append(tables, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tables, nil
}

func sqliteTableExists(db *sql.DB, name string) bool {
	return sqliteTableExistsContext(context.Background(), db, name)
}

func sqliteTableExistsContext(ctx context.Context, db sqliteDumpQueryer, name string) bool {
	var found string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	return err == nil
}
