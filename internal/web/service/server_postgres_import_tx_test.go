package service

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/shirou/gopsutil/v4/disk"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type postgresImportCallCounts struct {
	stop, restart, close, init, migrateData, migrateDB, capture, restore int
	snapshot, rollback, cleanup                                          int
}

func TestCreatePostgresSnapshot_PermissionsValidationAndFreeSpace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", root)
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dumpBin := filepath.Join(binDir, "pg_dump")
	restoreBin := filepath.Join(binDir, "pg_restore")
	if err := os.WriteFile(dumpBin, []byte("#!/bin/sh\nprintf 'PGDMP-verified-snapshot'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restoreBin, []byte("#!/bin/sh\n[ \"$1\" = \"--list\" ]\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	originalSize := postgresSchemaSizeHook
	originalUsage := postgresDiskUsageHook
	t.Cleanup(func() {
		postgresSchemaSizeHook = originalSize
		postgresDiskUsageHook = originalUsage
	})
	postgresSchemaSizeHook = func(context.Context, string, string) (int64, error) { return 4096, nil }
	postgresDiskUsageHook = func(string) (*disk.UsageStat, error) {
		return &disk.UsageStat{Free: postgresSnapshotRequiredBytes(4096)}, nil
	}
	cfg := postgresSnapshotConfig{
		dumpBin:         dumpBin,
		restoreBin:      restoreBin,
		dsn:             "postgres://user:secret@127.0.0.1:5432/xui",
		canonicalSchema: "public",
		toolEnv:         &database.PostgresToolEnv{Database: "xui", Env: os.Environ()},
	}

	snapshot, err := createPostgresSnapshot(context.Background(), cfg)
	if err != nil {
		t.Fatalf("createPostgresSnapshot: %v", err)
	}
	if filepath.Dir(snapshot.Dir) != root || !strings.HasPrefix(filepath.Base(snapshot.Dir), postgresSnapshotDirPrefix) {
		t.Fatalf("snapshot directory %q is outside persistent DB root %q", snapshot.Dir, root)
	}
	dirInfo, err := os.Stat(snapshot.Dir)
	if err != nil {
		t.Fatalf("stat snapshot directory: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("snapshot directory permissions = %#o, want 0700", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(snapshot.Path)
	if err != nil {
		t.Fatalf("stat snapshot file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 || fileInfo.Size() == 0 {
		t.Fatalf("snapshot file mode=%#o size=%d, want 0600 and non-empty", fileInfo.Mode().Perm(), fileInfo.Size())
	}
	if err := cleanupPostgresSnapshot(snapshot); err != nil {
		t.Fatalf("cleanupPostgresSnapshot: %v", err)
	}
	if _, err := os.Stat(snapshot.Dir); !os.IsNotExist(err) {
		t.Fatalf("snapshot directory remains after cleanup: %v", err)
	}

	postgresDiskUsageHook = func(string) (*disk.UsageStat, error) {
		return &disk.UsageStat{Free: postgresSnapshotRequiredBytes(4096) - 1}, nil
	}
	if _, err := createPostgresSnapshot(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "not enough free space") {
		t.Fatalf("expected deterministic free-space rejection, got %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("free-space rejection created snapshot artifacts: %v", entries)
	}
}

func newTestPostgresImportOps(counts *postgresImportCallCounts) postgresImportOps {
	ops := postgresImportOps{
		stopXray: func() error {
			counts.stop++
			return nil
		},
		restartXray: func() error {
			counts.restart++
			return nil
		},
		closeDB: func() error {
			counts.close++
			return nil
		},
		initDB: func() error {
			counts.init++
			return nil
		},
		migrateData: func(string, string) error {
			counts.migrateData++
			return nil
		},
		migrateDB: func() error {
			counts.migrateDB++
			return nil
		},
		captureSettings: func() (hostBoundSnapshot, error) {
			counts.capture++
			return hostBoundSnapshot{taken: true}, nil
		},
		restoreSettings: func(hostBoundSnapshot) error {
			counts.restore++
			return nil
		},
		createSnapshot: func(context.Context, postgresSnapshotConfig) (*postgresSnapshot, error) {
			counts.snapshot++
			return &postgresSnapshot{Dir: "/test/snapshot", Path: "/test/snapshot/pre-import.dump"}, nil
		},
		cleanupSnapshot: func(*postgresSnapshot) error {
			counts.cleanup++
			return nil
		},
	}
	ops.rollbackSnapshot = func(_ context.Context, rollbackOps postgresImportOps, _ postgresSnapshotConfig, snapshot *postgresSnapshot) error {
		counts.rollback++
		if err := rollbackOps.closeDB(); err != nil {
			return err
		}
		if err := rollbackOps.initDB(); err != nil {
			return err
		}
		if err := rollbackOps.restartXray(); err != nil {
			return err
		}
		return rollbackOps.cleanupSnapshot(snapshot)
	}
	return ops
}

func prepareFakePgRestore(t *testing.T, restoreExitCode int) (dumpPath, restoreMarker string) {
	t.Helper()
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	restoreMarker = filepath.Join(tmpDir, "restore-ran")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--list\" ]; then exit 0; fi\n" +
		"touch \"" + restoreMarker + "\"\n" +
		"exit " + strconv.Itoa(restoreExitCode) + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "pg_restore"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "pg_dump"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dumpPath = filepath.Join(tmpDir, "backup.dump")
	if err := os.WriteFile(dumpPath, []byte("PGDMP"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XUI_DB_TYPE", "postgres")
	t.Setenv("XUI_DB_DSN", "postgres://test:secret@127.0.0.1:5432/xui?sslmode=disable")
	return dumpPath, restoreMarker
}

func TestRestorePostgresDump_StrictBoundariesAndFailureOrdering(t *testing.T) {
	originalResolver := pgResolveCanonicalSchemaHook
	pgResolveCanonicalSchemaHook = func(context.Context) (string, error) { return "public", nil }
	t.Cleanup(func() { pgResolveCanonicalSchemaHook = originalResolver })

	t.Run("stop failure prevents every database operation", func(t *testing.T) {
		dumpPath, marker := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.stopXray = func() error {
			counts.stop++
			return errors.New("stop failed")
		}

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, true, ops)
		if err == nil || !strings.Contains(err.Error(), "database was not changed") {
			t.Fatalf("expected strict stop error, got %v", err)
		}
		if counts.close != 0 || counts.capture != 0 || counts.init != 0 || counts.migrateDB != 0 {
			t.Fatalf("database work ran after stop failure: %+v", counts)
		}
		if counts.restart != 1 {
			t.Fatalf("restart calls = %d, want 1", counts.restart)
		}
		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Fatalf("pg_restore ran after stop failure: %v", statErr)
		}
	})

	t.Run("host setting capture failure prevents close and restore", func(t *testing.T) {
		dumpPath, marker := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.captureSettings = func() (hostBoundSnapshot, error) {
			counts.capture++
			return hostBoundSnapshot{}, errors.New("capture failed")
		}

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, true, ops)
		if err == nil || !strings.Contains(err.Error(), "database was not changed") {
			t.Fatalf("expected strict capture error, got %v", err)
		}
		if counts.close != 0 || counts.init != 0 || counts.restore != 0 || counts.migrateDB != 0 {
			t.Fatalf("database work ran after capture failure: %+v", counts)
		}
		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Fatalf("pg_restore ran after capture failure: %v", statErr)
		}
	})

	t.Run("close failure prevents restore and reopens existing database", func(t *testing.T) {
		dumpPath, marker := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.closeDB = func() error {
			counts.close++
			return errors.New("close failed")
		}

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, false, ops)
		if err == nil || !strings.Contains(err.Error(), "database mutation was not run") {
			t.Fatalf("expected strict close error, got %v", err)
		}
		if counts.init != 1 || counts.restore != 0 || counts.migrateDB != 0 || counts.restart != 1 {
			t.Fatalf("unexpected recovery calls: %+v", counts)
		}
		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Fatalf("pg_restore ran after close failure: %v", statErr)
		}
	})

	t.Run("pg_restore failure skips host restore and semantic migrations", func(t *testing.T) {
		dumpPath, marker := prepareFakePgRestore(t, 7)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, true, ops)
		if err == nil || !strings.Contains(err.Error(), "failed before commit") {
			t.Fatalf("expected transactional pg_restore error, got %v", err)
		}
		if counts.snapshot != 1 || counts.cleanup != 1 || counts.init != 1 || counts.restore != 0 || counts.migrateDB != 0 || counts.restart != 1 {
			t.Fatalf("post-success work ran after pg_restore failure: %+v", counts)
		}
		if _, statErr := os.Stat(marker); statErr != nil {
			t.Fatalf("pg_restore restore step did not run: %v", statErr)
		}
	})

	t.Run("successful restore runs post-commit steps in order", func(t *testing.T) {
		dumpPath, _ := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)

		if err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, true, ops); err != nil {
			t.Fatalf("restorePostgresDumpWithOps: %v", err)
		}
		if counts.stop != 1 || counts.capture != 1 || counts.close != 1 || counts.snapshot != 1 || counts.init != 1 || counts.restore != 1 || counts.migrateDB != 1 || counts.restart != 1 || counts.cleanup != 1 {
			t.Fatalf("unexpected success calls: %+v", counts)
		}
	})

	t.Run("post-commit setting failure is reported honestly and stops semantic migrations", func(t *testing.T) {
		dumpPath, _ := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.restoreSettings = func(hostBoundSnapshot) error {
			counts.restore++
			return errors.New("setting restore failed")
		}

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, true, ops)
		if err == nil || !strings.Contains(err.Error(), "restore committed") {
			t.Fatalf("expected honest post-commit error, got %v", err)
		}
		if counts.restore != 1 || counts.migrateDB != 0 || counts.rollback != 1 || counts.restart != 1 || counts.cleanup != 1 {
			t.Fatalf("semantic migration ran after setting failure: %+v", counts)
		}
	})

	t.Run("restart failure rolls back the committed import", func(t *testing.T) {
		dumpPath, _ := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.restartXray = func() error {
			counts.restart++
			if counts.restart == 1 {
				return errors.New("restart failed")
			}
			return nil
		}

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, false, ops)
		if err == nil || !strings.Contains(err.Error(), "previous PostgreSQL database was restored automatically") {
			t.Fatalf("expected automatic rollback result, got %v", err)
		}
		if counts.rollback != 1 || counts.restart != 2 || counts.cleanup != 1 {
			t.Fatalf("unexpected restart rollback calls: %+v", counts)
		}
	})

	t.Run("snapshot failure is pre-mutation and restarts old database", func(t *testing.T) {
		dumpPath, marker := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.createSnapshot = func(context.Context, postgresSnapshotConfig) (*postgresSnapshot, error) {
			counts.snapshot++
			return nil, errors.New("snapshot failed")
		}

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, false, ops)
		if err == nil || !strings.Contains(err.Error(), "database was not changed") {
			t.Fatalf("expected pre-mutation snapshot error, got %v", err)
		}
		if counts.snapshot != 1 || counts.init != 1 || counts.restart != 1 || counts.rollback != 0 {
			t.Fatalf("unexpected snapshot failure calls: %+v", counts)
		}
		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Fatalf("pg_restore ran after snapshot failure: %v", statErr)
		}
	})

	t.Run("post-commit init failure rolls back automatically", func(t *testing.T) {
		dumpPath, _ := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.initDB = func() error {
			counts.init++
			if counts.init == 1 {
				return errors.New("init imported db failed")
			}
			return nil
		}

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, false, ops)
		if err == nil || !strings.Contains(err.Error(), "previous PostgreSQL database was restored automatically") {
			t.Fatalf("expected automatic rollback after init failure, got %v", err)
		}
		if counts.rollback != 1 || counts.init != 2 || counts.restart != 1 || counts.cleanup != 1 {
			t.Fatalf("unexpected init rollback calls: %+v", counts)
		}
	})

	t.Run("rollback failure retains snapshot and joins recovery guidance", func(t *testing.T) {
		dumpPath, _ := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.migrateDB = func() error {
			counts.migrateDB++
			return errors.New("semantic migration failed")
		}
		ops.rollbackSnapshot = func(context.Context, postgresImportOps, postgresSnapshotConfig, *postgresSnapshot) error {
			counts.rollback++
			return errors.New("rollback restore failed")
		}

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, false, ops)
		if err == nil || !strings.Contains(err.Error(), "recovery snapshot was retained at /test/snapshot/pre-import.dump") ||
			!strings.Contains(err.Error(), "semantic migration failed") || !strings.Contains(err.Error(), "rollback restore failed") {
			t.Fatalf("expected joined rollback failure and recovery path, got %v", err)
		}
		if counts.rollback != 1 || counts.cleanup != 0 {
			t.Fatalf("failed rollback removed its snapshot: %+v", counts)
		}
	})

	t.Run("accepted import cleanup failure never rolls back", func(t *testing.T) {
		dumpPath, _ := prepareFakePgRestore(t, 0)
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.cleanupSnapshot = func(*postgresSnapshot) error {
			counts.cleanup++
			return errors.New("cleanup failed")
		}

		err := (&ServerService{}).restorePostgresDumpWithOps(dumpPath, false, ops)
		if err == nil || !strings.Contains(err.Error(), "was accepted") {
			t.Fatalf("expected post-acceptance cleanup error, got %v", err)
		}
		if counts.rollback != 0 || counts.restart != 1 || counts.cleanup != 1 {
			t.Fatalf("cleanup after acceptance triggered rollback: %+v", counts)
		}
	})
}

func makePostgresMigrationCandidate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "candidate.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Setting{}, &model.Inbound{}); err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	return path
}

func TestMigrateSQLiteIntoPostgres_FailureSkipsPostCommitMigration(t *testing.T) {
	_, _ = prepareFakePgRestore(t, 0)
	originalResolver := pgResolveCanonicalSchemaHook
	pgResolveCanonicalSchemaHook = func(context.Context) (string, error) { return "public", nil }
	t.Cleanup(func() { pgResolveCanonicalSchemaHook = originalResolver })
	srcPath := makePostgresMigrationCandidate(t)
	var counts postgresImportCallCounts
	ops := newTestPostgresImportOps(&counts)
	ops.migrateData = func(string, string) error {
		counts.migrateData++
		return errors.New("copy failed")
	}

	err := (&ServerService{}).migrateSQLiteIntoPostgresWithOps(srcPath, filepath.Dir(srcPath), false, false, ops)
	if err == nil || !strings.Contains(err.Error(), "failed before commit") {
		t.Fatalf("expected transactional migration error, got %v", err)
	}
	if counts.migrateData != 1 || counts.snapshot != 1 || counts.cleanup != 1 || counts.init != 1 || counts.migrateDB != 0 || counts.restart != 1 {
		t.Fatalf("unexpected failure calls: %+v", counts)
	}
}

func TestMigrateSQLiteIntoPostgres_HostSettingsParityAndPanicRollback(t *testing.T) {
	_, _ = prepareFakePgRestore(t, 0)
	originalResolver := pgResolveCanonicalSchemaHook
	pgResolveCanonicalSchemaHook = func(context.Context) (string, error) { return "public", nil }
	t.Cleanup(func() { pgResolveCanonicalSchemaHook = originalResolver })
	srcPath := makePostgresMigrationCandidate(t)

	t.Run("keep host settings true captures and restores", func(t *testing.T) {
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		if err := (&ServerService{}).migrateSQLiteIntoPostgresWithOps(srcPath, filepath.Dir(srcPath), false, true, ops); err != nil {
			t.Fatalf("migrateSQLiteIntoPostgresWithOps: %v", err)
		}
		if counts.capture != 1 || counts.restore != 1 || counts.rollback != 0 || counts.cleanup != 1 {
			t.Fatalf("keepHostSettings=true calls = %+v", counts)
		}
	})

	t.Run("keep host settings false does not capture or restore", func(t *testing.T) {
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		if err := (&ServerService{}).migrateSQLiteIntoPostgresWithOps(srcPath, filepath.Dir(srcPath), false, false, ops); err != nil {
			t.Fatalf("migrateSQLiteIntoPostgresWithOps: %v", err)
		}
		if counts.capture != 0 || counts.restore != 0 || counts.rollback != 0 || counts.cleanup != 1 {
			t.Fatalf("keepHostSettings=false calls = %+v", counts)
		}
	})

	t.Run("panic after mutation starts is sanitized and rolled back", func(t *testing.T) {
		var counts postgresImportCallCounts
		ops := newTestPostgresImportOps(&counts)
		ops.migrateData = func(string, string) error {
			counts.migrateData++
			panic("SECRET_DSN_PANIC_VALUE")
		}

		err := (&ServerService{}).migrateSQLiteIntoPostgresWithOps(srcPath, filepath.Dir(srcPath), false, false, ops)
		if err == nil || !strings.Contains(err.Error(), "Unexpected internal error while applying PostgreSQL import") ||
			!strings.Contains(err.Error(), "previous PostgreSQL database was restored automatically") {
			t.Fatalf("expected sanitized panic rollback error, got %v", err)
		}
		if strings.Contains(err.Error(), "SECRET_DSN_PANIC_VALUE") {
			t.Fatalf("panic secret leaked to caller: %v", err)
		}
		if counts.rollback != 1 || counts.cleanup != 1 || counts.restart != 1 {
			t.Fatalf("panic did not rollback exactly once: %+v", counts)
		}
	})
}

func TestLivePostgres_PreSnapshotAndExactPostCommitRollback(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("XUI_TEST_PG_DSN"))
	if dsn == "" {
		t.Skip("set XUI_TEST_PG_DSN to run live PostgreSQL snapshot rollback test")
	}
	dumpBin, err := exec.LookPath("pg_dump")
	if err != nil {
		t.Skip("pg_dump is required for live PostgreSQL snapshot rollback test")
	}
	restoreBin, err := exec.LookPath("pg_restore")
	if err != nil {
		t.Skip("pg_restore is required for live PostgreSQL snapshot rollback test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	schema := "snapshot_rollback_" + runID
	otherSchema := "snapshot_other_" + runID
	qSchema := pgx.Identifier{schema}.Sanitize()
	qOtherSchema := pgx.Identifier{otherSchema}.Sanitize()
	for _, statement := range []string{
		"CREATE SCHEMA " + qSchema,
		"CREATE SCHEMA " + qOtherSchema,
		"CREATE TABLE " + pgx.Identifier{schema, "users"}.Sanitize() + " (id integer primary key, value text not null)",
		"INSERT INTO " + pgx.Identifier{schema, "users"}.Sanitize() + " VALUES (1, 'before-import')",
		"CREATE TABLE " + pgx.Identifier{otherSchema, "untouched"}.Sanitize() + " (id integer primary key)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare live rollback fixture with %q: %v", statement, err)
		}
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanCancel()
		_, _ = db.ExecContext(cleanCtx, "DROP SCHEMA IF EXISTS "+qSchema+" CASCADE")
		_, _ = db.ExecContext(cleanCtx, "DROP SCHEMA IF EXISTS "+qOtherSchema+" CASCADE")
	})

	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	toolEnv, err := database.PreparePostgresToolEnv(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = toolEnv.Cleanup() }()
	cfg := postgresSnapshotConfig{
		dumpBin:         dumpBin,
		restoreBin:      restoreBin,
		dsn:             dsn,
		canonicalSchema: schema,
		toolEnv:         toolEnv,
	}
	var restartCalls int
	ops := postgresImportOps{
		stopXray: func() error { return nil },
		restartXray: func() error {
			restartCalls++
			return nil
		},
		closeDB:         func() error { return nil },
		initDB:          func() error { return nil },
		migrateDB:       func() error { return errors.New("forced post-commit validation failure") },
		captureSettings: func() (hostBoundSnapshot, error) { return hostBoundSnapshot{}, nil },
		restoreSettings: func(hostBoundSnapshot) error { return nil },
		createSnapshot:  createPostgresSnapshot,
		cleanupSnapshot: cleanupPostgresSnapshot,
	}
	ops.rollbackSnapshot = rollbackPostgresSnapshot

	err = runPostgresImportTransaction(ops, cfg, false, func() error {
		if _, err := db.ExecContext(ctx, "UPDATE "+pgx.Identifier{schema, "users"}.Sanitize()+" SET value='after-import' WHERE id=1"); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, "CREATE TABLE "+pgx.Identifier{schema, "created_after_snapshot"}.Sanitize()+" (id integer)")
		return err
	}, "live rollback test")
	if err == nil || !strings.Contains(err.Error(), "previous PostgreSQL database was restored automatically") {
		t.Fatalf("expected successful automatic rollback, got %v", err)
	}
	if restartCalls != 1 {
		t.Fatalf("restart calls=%d, want 1 after rollback", restartCalls)
	}
	var value string
	if err := db.QueryRowContext(ctx, "SELECT value FROM "+pgx.Identifier{schema, "users"}.Sanitize()+" WHERE id=1").Scan(&value); err != nil {
		t.Fatalf("read restored row: %v", err)
	}
	if value != "before-import" {
		t.Fatalf("restored value=%q, want before-import", value)
	}
	var createdAfterSnapshot, unrelatedTable sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT pg_catalog.to_regclass($1)::text`, schema+".created_after_snapshot").Scan(&createdAfterSnapshot); err != nil {
		t.Fatal(err)
	}
	if createdAfterSnapshot.Valid {
		t.Fatalf("exact rollback left post-snapshot table %q", createdAfterSnapshot.String)
	}
	if err := db.QueryRowContext(ctx, `SELECT pg_catalog.to_regclass($1)::text`, otherSchema+".untouched").Scan(&unrelatedTable); err != nil {
		t.Fatal(err)
	}
	if !unrelatedTable.Valid {
		t.Fatal("exact rollback removed table from unrelated schema")
	}
	entries, err := os.ReadDir(config.GetDBFolderPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("successful rollback left snapshot artifacts: %v", entries)
	}
}

func postgresTestDSNWithSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema
}

func TestLivePostgres_SQLiteMigrationHostSettingsParity(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("XUI_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("set XUI_TEST_PG_DSN to run live PostgreSQL host-settings parity test")
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump is required for live PostgreSQL host-settings parity test")
	}
	if _, err := exec.LookPath("pg_restore"); err != nil {
		t.Skip("pg_restore is required for live PostgreSQL host-settings parity test")
	}

	adminDB, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adminDB.Close() }()

	for _, keepHostSettings := range []bool{true, false} {
		name := "replace_host_settings"
		if keepHostSettings {
			name = "preserve_host_settings"
		}
		t.Run(name, func(t *testing.T) {
			runID := strconv.FormatInt(time.Now().UnixNano(), 36)
			schema := "host_parity_" + runID
			qSchema := pgx.Identifier{schema}.Sanitize()
			if _, err := adminDB.Exec("CREATE SCHEMA " + qSchema); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = database.CloseDB()
				_, _ = adminDB.Exec("DROP SCHEMA IF EXISTS " + qSchema + " CASCADE")
			})

			candidateDir := t.TempDir()
			candidatePath := filepath.Join(candidateDir, "candidate.db")
			t.Setenv("XUI_DB_TYPE", "sqlite")
			t.Setenv("XUI_DB_FOLDER", candidateDir)
			t.Setenv("XUI_DB_DSN", "")
			if err := database.InitDB(candidatePath); err != nil {
				t.Fatalf("initialize SQLite candidate: %v", err)
			}
			candidateDB := database.GetDB()
			candidateSettings := map[string]string{
				"webPort":           "29999",
				"webBasePath":       "/candidate/",
				"webCertFile":       "/candidate/cert.pem",
				"subCertFile":       "/candidate/sub-cert.pem",
				"panelGuid":         "candidate-guid",
				"nodeMtlsCaCertPem": "candidate-ca",
				"remarkTemplate":    "candidate-remark",
			}
			for key, value := range candidateSettings {
				if err := candidateDB.Where("key = ?", key).Assign(&model.Setting{Key: key, Value: value}).FirstOrCreate(&model.Setting{}).Error; err != nil {
					t.Fatalf("seed candidate setting %s: %v", key, err)
				}
			}
			if err := candidateDB.Create(&model.User{Id: 1, Username: "candidate-admin", Password: "candidate-password"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := database.CloseDB(); err != nil {
				t.Fatal(err)
			}

			postgresFolder := t.TempDir()
			t.Setenv("XUI_DB_TYPE", "postgres")
			t.Setenv("XUI_DB_FOLDER", postgresFolder)
			t.Setenv("XUI_DB_DSN", postgresTestDSNWithSearchPath(t, baseDSN, schema))
			if err := database.InitDB(config.GetDBPath()); err != nil {
				t.Fatalf("initialize PostgreSQL target: %v", err)
			}
			targetDB := database.GetDB()
			targetSettings := map[string]string{
				"webPort":           "24443",
				"webBasePath":       "/router/",
				"webCertFile":       "/router/cert.pem",
				"subCertFile":       "/router/sub-cert.pem",
				"panelGuid":         "router-guid",
				"nodeMtlsCaCertPem": "router-ca",
				"remarkTemplate":    "router-old-remark",
			}
			for key, value := range targetSettings {
				if err := targetDB.Where("key = ?", key).Assign(&model.Setting{Key: key, Value: value}).FirstOrCreate(&model.Setting{}).Error; err != nil {
					t.Fatalf("seed target setting %s: %v", key, err)
				}
			}

			svc := &ServerService{}
			ops := svc.defaultPostgresImportOps()
			ops.stopXray = func() error { return nil }
			ops.restartXray = func() error { return nil }
			if err := svc.migrateSQLiteIntoPostgresWithOps(candidatePath, candidateDir, false, keepHostSettings, ops); err != nil {
				t.Fatalf("migrate SQLite into PostgreSQL: %v", err)
			}

			wantHost := candidateSettings
			if keepHostSettings {
				wantHost = targetSettings
			}
			for _, key := range []string{"webPort", "webBasePath", "webCertFile", "subCertFile", "panelGuid", "nodeMtlsCaCertPem"} {
				var row model.Setting
				if err := database.GetDB().Where("key = ?", key).First(&row).Error; err != nil {
					t.Fatalf("read migrated setting %s: %v", key, err)
				}
				if row.Value != wantHost[key] {
					t.Fatalf("%s=%q, want %q (keepHostSettings=%t)", key, row.Value, wantHost[key], keepHostSettings)
				}
			}
			var remark model.Setting
			if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remark).Error; err != nil || remark.Value != "candidate-remark" {
				t.Fatalf("portable setting was not imported: value=%q err=%v", remark.Value, err)
			}
			var user model.User
			if err := database.GetDB().First(&user, 1).Error; err != nil || user.Username != "candidate-admin" {
				t.Fatalf("administrator data should follow imported DB: username=%q err=%v", user.Username, err)
			}
			entries, err := os.ReadDir(postgresFolder)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), postgresSnapshotDirPrefix) {
					t.Fatalf("successful migration left snapshot directory %q", entry.Name())
				}
			}
		})
	}
}
