package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/xray"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// migrationModels is the FK-aware order in which tables are created and copied
// during `x-ui migrate-db --dsn` (SQLite → PostgreSQL data migration) and in
// related tests.
//
// Important: When adding a new top-level model (like OutboundSubscription),
// you must add it here **in addition to** allModels() in internal/database/db.go;
// TestMigrationModelsMatchPanelModels fails when the two lists drift apart.
// This list is used for:
//   - Creating the destination schema during cross-DB migration
//   - Truncating tables
//   - Copying data row-by-row
//   - Resyncing Postgres sequences after bulk insert
//
// DumpSQLite / RestoreSQLite are schema-introspective (they read sqlite_master)
// so they do not need manual updates.
func migrationModels() []any {
	return []any{
		&model.User{},
		&model.Setting{},
		&model.HistoryOfSeeders{},
		&model.Node{},
		&model.ApiToken{},
		&model.Inbound{},
		&xray.ClientTraffic{},
		&model.OutboundTraffics{},
		&model.InboundClientIps{},
		&model.ClientRecord{},
		&model.ClientInbound{},
		&model.ClientHwid{},
		&model.ClientExternalLink{},
		&model.ClientGroup{},
		&model.InboundFallback{},
		&model.Host{},
		&model.HostTombstone{},
		&model.NodeClientTraffic{},
		&model.NodeClientIp{},
		&model.ClientGlobalTraffic{},
		&model.NodePendingReset{},
		&model.OutboundSubscription{},
		&model.SubBalancer{},
		&model.TuicTrafficReceipt{},
	}
}

// MigrateData copies every row from the configured SQLite file at srcPath into
// a fresh PostgreSQL database described by dstDSN. The destination tables are
// (re)created with AutoMigrate, truncated, and populated in one transaction, so
// a failed migration leaves both the destination schema and data unchanged.
// Source data is left untouched.
func MigrateData(srcPath, dstDSN string) error {
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("source sqlite not found at %s: %w", srcPath, err)
	}
	if dstDSN == "" {
		return errors.New("destination DSN is required")
	}

	if err := os.MkdirAll(path.Dir(srcPath), 0o755); err != nil {
		return err
	}

	srcDSN := srcPath + "?_journal_mode=WAL&_busy_timeout=10000"
	src, err := gorm.Open(sqlite.Open(srcDSN), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return fmt.Errorf("open sqlite source: %w", err)
	}
	srcSQL, err := src.DB()
	if err != nil {
		return err
	}
	defer srcSQL.Close()

	dst, err := gorm.Open(postgres.Open(dstDSN), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return fmt.Errorf("open postgres destination: %w", RedactPostgresError(err, dstDSN))
	}
	dstSQL, err := dst.DB()
	if err != nil {
		return err
	}
	defer dstSQL.Close()
	dstSQL.SetConnMaxLifetime(time.Hour)

	totalRows := 0
	txErr := dst.Transaction(func(tx *gorm.DB) error {
		log.Println("Creating destination schema...")
		for _, m := range migrationModels() {
			if err := tx.AutoMigrate(m); err != nil {
				return fmt.Errorf("AutoMigrate %T: %w", m, err)
			}
		}

		// AutoMigrate re-creates the legacy client_traffics -> inbounds foreign key,
		// but the running panel drops it (see dropLegacyForeignKeys) and tolerates
		// client_traffics rows whose inbound was deleted. Drop it here too so copying
		// such orphaned rows can't fail with an fk_inbounds_client_stats violation.
		if err := tx.Exec("ALTER TABLE client_traffics DROP CONSTRAINT IF EXISTS fk_inbounds_client_stats").Error; err != nil {
			return fmt.Errorf("drop legacy foreign key: %w", err)
		}

		// Empty the destination tables before copying: a fresh PostgreSQL DB
		// already holds an auto-seeded admin (id=1) from any prior panel start,
		// so a plain INSERT with explicit ids would collide on users_pkey. Only
		// the panel's own tables are cleared, and a failure anywhere in this
		// transaction rolls the clear back with everything else.
		if err := truncatePostgresTables(tx, migrationModels()); err != nil {
			return fmt.Errorf("clear destination tables: %w", err)
		}

		for _, m := range migrationModels() {
			n, err := copyTable(src, tx, m)
			if err != nil {
				return fmt.Errorf("copy %T: %w", m, err)
			}
			totalRows += n
			log.Printf("  %-32s %d rows", reflect.TypeOf(m).Elem().Name(), n)
		}
		return nil
	})
	if txErr != nil {
		return txErr
	}

	// setval is never rolled back by PostgreSQL, so sequences are resynced only
	// after the transaction has committed.
	if err := resetPostgresSequences(dst); err != nil {
		log.Printf("warning: failed to reset some postgres sequences: %v", err)
	}

	log.Printf("Migration complete: %d rows across %d tables.", totalRows, len(migrationModels()))
	log.Println(postgresMigrationNextStepsMessage())
	return nil
}

func postgresMigrationNextStepsMessage() string {
	return "Configure XUI_DB_TYPE=postgres and XUI_DB_DSN in RouterOS /container/envs, then recreate or replace the container with the same persistent /etc/x-ui and /root/cert mounts."
}

// ExportPostgresToSQLite copies every row from the PostgreSQL database described
// by srcDSN into a fresh SQLite file at dstPath.
func ExportPostgresToSQLite(srcDSN, dstPath string) error {
	return ExportPostgresToSQLiteContext(context.Background(), srcDSN, dstPath)
}

// ExportPostgresToSQLiteContext copies every row from the PostgreSQL database
// described by srcDSN into a newly created 0600 SQLite file at dstPath while
// respecting ctx cancellation. Existing destinations are never overwritten.
func ExportPostgresToSQLiteContext(ctx context.Context, srcDSN, dstPath string) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if srcDSN == "" {
		return errors.New("source DSN is required")
	}
	if err := os.MkdirAll(path.Dir(dstPath), 0o700); err != nil {
		return err
	}

	dstFile, openErr := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if openErr != nil {
		if errors.Is(openErr, os.ErrExist) {
			return fmt.Errorf("sqlite migration destination already exists: %s", dstPath)
		}
		return openErr
	}
	if closeErr := dstFile.Close(); closeErr != nil {
		_ = os.Remove(dstPath)
		return closeErr
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dstPath)
		}
	}()

	src, err := gorm.Open(postgres.Open(srcDSN), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return fmt.Errorf("open postgres source: %w", sanitizePostgresContextOrRedactError(ctx, err, srcDSN))
	}
	srcSQL, err := src.DB()
	if err != nil {
		return err
	}
	defer func() { _ = srcSQL.Close() }()

	if pingErr := srcSQL.PingContext(ctx); pingErr != nil {
		return fmt.Errorf("ping postgres source: %w", sanitizePostgresContextOrRedactError(ctx, pingErr, srcDSN))
	}

	// Use DELETE journal mode so the exported file is self-contained without WAL/SHM sidecars.
	dst, err := gorm.Open(sqlite.Open(dstPath+"?_busy_timeout=10000&_journal_mode=DELETE"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return fmt.Errorf("open sqlite destination: %w", err)
	}
	dstSQL, err := dst.DB()
	if err != nil {
		return err
	}
	dstClosed := false
	defer func() {
		if !dstClosed {
			_ = dstSQL.Close()
		}
	}()

	if copyErr := copyAllModelsContext(ctx, src, dst); copyErr != nil {
		return sanitizePostgresContextOrRedactError(ctx, copyErr, srcDSN)
	}

	if closeErr := dstSQL.Close(); closeErr != nil {
		dstClosed = true
		return closeErr
	}
	dstClosed = true

	if chmodErr := os.Chmod(dstPath, 0o600); chmodErr != nil {
		return chmodErr
	}
	if syncErr := syncFile(dstPath); syncErr != nil {
		return syncErr
	}
	info, statErr := os.Stat(dstPath)
	if statErr != nil {
		return statErr
	}
	if info.Size() == 0 {
		return errors.New("exported SQLite migration file is empty")
	}
	if valErr := ValidateSQLiteDB(dstPath); valErr != nil {
		return fmt.Errorf("validate exported SQLite migration: %w", valErr)
	}
	return nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if syncErr := f.Sync(); syncErr != nil {
		_ = f.Close()
		return syncErr
	}
	return f.Close()
}

func sanitizePostgresContextOrRedactError(ctx context.Context, err error, dsn string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return RedactPostgresError(err, dsn)
}

// copyAllModels (re)creates the schema on dst and copies every migrated table
// from src to dst in FK-safe order. src/dst may be any gorm backend.
func copyAllModels(src, dst *gorm.DB) error {
	return copyAllModelsContext(context.Background(), src, dst)
}

func copyAllModelsContext(ctx context.Context, src, dst *gorm.DB) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, m := range migrationModels() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := dst.WithContext(ctx).AutoMigrate(m); err != nil {
			return fmt.Errorf("AutoMigrate %T: %w", m, err)
		}
	}
	for _, m := range migrationModels() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := copyTableContext(ctx, src, dst, m); err != nil {
			return fmt.Errorf("copy %T: %w", m, err)
		}
	}
	return nil
}

func copyTable(src, dst *gorm.DB, mdl any) (int, error) {
	return copyTableContext(context.Background(), src, dst, mdl)
}

func copyTableContext(ctx context.Context, src, dst *gorm.DB, mdl any) (int, error) {
	const batchSize = 500
	if ctx == nil {
		ctx = context.Background()
	}

	sliceType := reflect.SliceOf(reflect.PointerTo(reflect.TypeOf(mdl).Elem()))

	stmt := &gorm.Statement{DB: src}
	if err := stmt.Parse(mdl); err != nil {
		return 0, err
	}
	order := strings.Join(stmt.Schema.PrimaryFieldDBNames, ", ")
	table := stmt.Schema.Table
	columns := stmt.Schema.DBNames

	total := 0
	for offset := 0; ; offset += batchSize {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		batchPtr := reflect.New(sliceType)
		q := src.WithContext(ctx).Model(mdl).Limit(batchSize).Offset(offset)
		if order != "" {
			q = q.Order(order)
		}
		if err := q.Find(batchPtr.Interface()).Error; err != nil {
			return total, err
		}
		slice := batchPtr.Elem()
		n := slice.Len()
		if n == 0 {
			break
		}

		rows := make([]map[string]any, n)
		for i := range n {
			rv := reflect.Indirect(slice.Index(i))
			row := make(map[string]any, len(columns))
			for _, name := range columns {
				value, _ := stmt.Schema.FieldsByDBName[name].ValueOf(ctx, rv)
				row[name] = value
			}
			rows[i] = row
		}

		if err := dst.WithContext(ctx).Table(table).CreateInBatches(rows, 200).Error; err != nil {
			return total, err
		}
		total += n
		if n < batchSize {
			break
		}
	}
	return total, nil
}

// truncatePostgresTables empties every migrated table on dst in a single
// statement, resetting identity sequences. CASCADE covers the inbound/client
// foreign keys regardless of insertion order. Only the panel's own tables are
// touched, never the rest of the schema.
func truncatePostgresTables(dst *gorm.DB, models []any) error {
	tables := make([]string, 0, len(models))
	for _, m := range models {
		stmt := &gorm.Statement{DB: dst}
		if err := stmt.Parse(m); err != nil {
			return err
		}
		tables = append(tables, `"`+stmt.Schema.Table+`"`)
	}
	if len(tables) == 0 {
		return nil
	}
	log.Println("Clearing destination tables...")
	return dst.Exec("TRUNCATE TABLE " + strings.Join(tables, ", ") + " RESTART IDENTITY CASCADE").Error
}

// resetPostgresSequences advances each migrated table's id sequence past MAX(id),
// otherwise the next INSERT-without-id would clash with copied rows.
func resetPostgresSequences(dst *gorm.DB) error {
	return resyncPostgresSequences(dst, migrationModels())
}

// resyncPostgresSequences sets each model's id sequence to MAX(id); idempotent. Id-less
// composite-PK tables are skipped — Postgres rejects MAX(id) at parse time and logs it (#5665).
func resyncPostgresSequences(db *gorm.DB, models []any) error {
	for _, m := range models {
		t, ok := tableWithIdColumn(db, m)
		if !ok {
			continue
		}
		// t comes from the trusted model set parsed by GORM, not user input, so
		// interpolating it as an identifier is safe. We ignore errors per-table.
		_ = db.Exec(
			`SELECT setval(pg_get_serial_sequence(?, 'id'), COALESCE((SELECT MAX(id) FROM "`+t+`"), 1), true)
			 WHERE pg_get_serial_sequence(?, 'id') IS NOT NULL`,
			t, t,
		).Error
	}
	return nil
}

// tableWithIdColumn resolves a model's table name and reports whether its GORM
// schema maps an "id" database column.
func tableWithIdColumn(db *gorm.DB, m any) (string, bool) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(m); err != nil {
		return "", false
	}
	if stmt.Schema == nil || stmt.Schema.LookUpField("id") == nil {
		return "", false
	}
	return stmt.Table, true
}

// PrepareSQLiteForMigration rejects SQLite files that are not a panel database
// before the caller causes any downtime, then AutoMigrates the panel schema
// onto the file so backups from older versions gain the newer tables and
// columns the row copy reads. Data-level upgrades are not needed here: they
// run dialect-agnostically on the destination via InitDB after the import.
func PrepareSQLiteForMigration(dbPath string) error {
	gdb, err := gorm.Open(sqlite.Open(dbPath+"?_busy_timeout=10000"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	for _, table := range []string{"users", "settings", "inbounds"} {
		if !sqliteTableExists(sqlDB, table) {
			return fmt.Errorf("not a Mikrozia panel database: required table %q is missing", table)
		}
	}
	for _, m := range migrationModels() {
		if err := gdb.AutoMigrate(m); err != nil && !isIgnorableDuplicateColumnErr(gdb, err, m) {
			return fmt.Errorf("upgrade panel schema for %T: %w", m, err)
		}
	}
	return nil
}
