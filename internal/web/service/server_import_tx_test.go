package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/config"
	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/logger"
)

func seedPanelDBAt(t *testing.T, dbPath, webPort, remark string) {
	t.Helper()
	if err := database.InitDB(dbPath); err != nil {
		t.Fatalf("InitDB(%q): %v", dbPath, err)
	}
	db := database.GetDB()
	for k, v := range map[string]string{
		"webPort":        webPort,
		"remarkTemplate": remark,
	} {
		if err := db.Exec(`INSERT OR REPLACE INTO settings(key, value) VALUES(?, ?)`, k, v).Error; err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}
	if err := database.CloseDB(); err != nil {
		t.Fatalf("CloseDB: %v", err)
	}
}

func assertNoLeftoverRestoreArtifacts(t *testing.T, dbDir string) {
	t.Helper()
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".mikrozia-sqlite-restore-") ||
			name == ".mikrozia-sqlite-restore-state.json" ||
			strings.HasSuffix(name, ".temp") ||
			strings.HasSuffix(name, ".backup") {
			t.Fatalf("unexpected leftover restore artifact %q in %s", name, dbDir)
		}
	}
}

func TestImportSQLiteDB_RawAndDumpAndHostSettingsAndRollback(t *testing.T) {
	t.Run("raw sqlite db import with keepHostSettings=true", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "OLD-REMARK")

		srcPath := filepath.Join(t.TempDir(), "incoming.db")
		seedPanelDBAt(t, srcPath, "9999", "NEW-REMARK")

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		stageDir := filepath.Join(dbDir, ".mikrozia-import-test1")
		if err := os.Mkdir(stageDir, 0o700); err != nil {
			t.Fatal(err)
		}
		stagedFile := filepath.Join(stageDir, "upload.staged")
		rawBytes, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stagedFile, rawBytes, 0o600); err != nil {
			t.Fatal(err)
		}

		var svc ServerService
		err = svc.importSQLiteDB(
			&ImportUploadStage{Dir: stageDir, Path: stagedFile, Kind: importKindSQLiteDB},
			true,
			func() error { return nil },
			func() error { return nil },
		)
		if err != nil {
			t.Fatalf("importSQLiteDB(raw): %v", err)
		}

		var portRow, remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "webPort").First(&portRow).Error; err != nil || portRow.Value != "8443" {
			t.Fatalf("webPort = %q err=%v, want 8443", portRow.Value, err)
		}
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "NEW-REMARK" {
			t.Fatalf("remarkTemplate = %q err=%v, want NEW-REMARK", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("sqlite sql dump import with keepHostSettings=false", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "OLD-REMARK")

		srcDB := filepath.Join(t.TempDir(), "src.db")
		seedPanelDBAt(t, srcDB, "9999", "DUMP-REMARK")
		dumpPath := filepath.Join(t.TempDir(), "src.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			false,
			func() error { return nil },
			func() error { return nil },
		)
		if err != nil {
			t.Fatalf("importSQLiteDB(dump): %v", err)
		}

		var portRow, remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "webPort").First(&portRow).Error; err != nil || portRow.Value != "9999" {
			t.Fatalf("webPort = %q err=%v, want 9999", portRow.Value, err)
		}
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "DUMP-REMARK" {
			t.Fatalf("remarkTemplate = %q err=%v, want DUMP-REMARK", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("invalid candidate rejected before stopping xray", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "SAFE-LIVE")

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		badFile := filepath.Join(t.TempDir(), "bad.db")
		if err := os.WriteFile(badFile, []byte("not-a-sqlite-db"), 0o600); err != nil {
			t.Fatal(err)
		}

		stopCalled := false
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: badFile, Kind: importKindSQLiteDB},
			true,
			func() error { stopCalled = true; return nil },
			func() error { return nil },
		)
		if err == nil {
			t.Fatal("expected error for invalid candidate")
		}
		if stopCalled {
			t.Fatal("stopXray must NOT be called when candidate validation fails")
		}
		var remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "SAFE-LIVE" {
			t.Fatalf("live DB altered: %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("restart xray failure triggers in-process rollback and restores old DB", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "BEFORE-FAIL")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-UNCOMMITTED")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		restartAttempts := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error { return nil },
			func() error {
				restartAttempts++
				if restartAttempts == 1 {
					return errors.New("xray failed to start with new db")
				}
				return nil
			},
		)
		if err == nil {
			t.Fatal("expected error when xray restart fails")
		}
		if restartAttempts != 2 {
			t.Fatalf("expected 2 restart attempts (new DB fail + rollback old DB), got %d", restartAttempts)
		}
		var remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "BEFORE-FAIL" {
			t.Fatalf("expected rollback to BEFORE-FAIL, got %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("stop xray failure aborts immediately without calling restart or touching live DB", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "LIVE-SAFE")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-IGNORED")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		stopCalls := 0
		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error {
				stopCalls++
				return errors.New("xray stop failed")
			},
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "Failed to stop Xray") {
			t.Fatalf("expected Failed to stop Xray error, got %v", err)
		}
		if stopCalls != 1 || restartCalls != 0 {
			t.Fatalf("stopCalls=%d restartCalls=%d, want 1 and 0", stopCalls, restartCalls)
		}
		var remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "LIVE-SAFE" {
			t.Fatalf("live DB altered: %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("host-bound settings restore failure triggers rollback and restores old DB", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "OLD-HOST-TEST")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-UNCOMMITTED")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		origHook := sqliteRestoreHostSettingHook
		t.Cleanup(func() { sqliteRestoreHostSettingHook = origHook })
		sqliteRestoreHostSettingHook = func(key string) error {
			if key == "webPort" {
				return errors.New("injected host setting write failure")
			}
			return nil
		}

		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error { return nil },
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "injected host setting write failure") {
			t.Fatalf("expected host setting failure, got %v", err)
		}
		if restartCalls != 1 {
			t.Fatalf("expected 1 restart call (rollback old DB), got %d", restartCalls)
		}
		var portRow, remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "webPort").First(&portRow).Error; err != nil || portRow.Value != "8443" {
			t.Fatalf("expected webPort rolled back to 8443, got %q err=%v", portRow.Value, err)
		}
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "OLD-HOST-TEST" {
			t.Fatalf("expected remarkTemplate rolled back to OLD-HOST-TEST, got %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("host-bound settings capture failure stops before fallback and restarts Xray once", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "LIVE-CAPTURE-FAIL")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-UNREACHABLE")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		origHook := sqliteCaptureHostSettingHook
		t.Cleanup(func() { sqliteCaptureHostSettingHook = origHook })
		sqliteCaptureHostSettingHook = func() error {
			return errors.New("injected host setting capture failure")
		}

		stopCalls := 0
		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error {
				stopCalls++
				return nil
			},
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "injected host setting capture failure") {
			t.Fatalf("expected host setting capture failure, got %v", err)
		}
		if stopCalls != 1 || restartCalls != 1 {
			t.Fatalf("stopCalls=%d restartCalls=%d, want 1 and 1", stopCalls, restartCalls)
		}
		var remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "LIVE-CAPTURE-FAIL" {
			t.Fatalf("live DB altered: %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("post-import MigrateDBStrict failure triggers rollback and restores old DB", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "OLD-MIGRATE-FAIL")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-BAD-MIGRATE")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		origHook := sqliteMigrateDBHook
		t.Cleanup(func() { sqliteMigrateDBHook = origHook })
		sqliteMigrateDBHook = func() error {
			return errors.New("injected post-import migration failure")
		}

		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error { return nil },
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "injected post-import migration failure") {
			t.Fatalf("expected migration failure, got %v", err)
		}
		if restartCalls != 1 {
			t.Fatalf("expected 1 restart call (rollback old DB), got %d", restartCalls)
		}
		var portRow, remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "webPort").First(&portRow).Error; err != nil || portRow.Value != "8443" {
			t.Fatalf("expected webPort rolled back to 8443, got %q err=%v", portRow.Value, err)
		}
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "OLD-MIGRATE-FAIL" {
			t.Fatalf("expected remarkTemplate rolled back to OLD-MIGRATE-FAIL, got %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("CloseLiveDB failure leaves live DB untouched and restarts Xray once", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "LIVE-CLOSE-FAIL")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-UNREACHABLE")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		origHook := sqliteCloseLiveDBHook
		t.Cleanup(func() { sqliteCloseLiveDBHook = origHook })
		sqliteCloseLiveDBHook = func(*database.SQLiteRestoreTransaction) error {
			return errors.New("injected CloseLiveDB failure")
		}

		stopCalls := 0
		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error {
				stopCalls++
				return nil
			},
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "injected CloseLiveDB failure") {
			t.Fatalf("expected CloseLiveDB failure, got %v", err)
		}
		if stopCalls != 1 || restartCalls != 1 {
			t.Fatalf("stopCalls=%d restartCalls=%d, want 1 and 1", stopCalls, restartCalls)
		}
		// Ensure DB is still usable and untouched.
		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		var remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "LIVE-CLOSE-FAIL" {
			t.Fatalf("live DB altered: %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("InitLiveDB failure after Apply triggers rollback and restores old DB", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "OLD-INIT-FAIL")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-INIT-FAIL")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		origHook := sqliteInitLiveDBHook
		t.Cleanup(func() { sqliteInitLiveDBHook = origHook })
		sqliteInitLiveDBHook = func(*database.SQLiteRestoreTransaction) error {
			return errors.New("injected InitLiveDB failure")
		}

		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error { return nil },
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "injected InitLiveDB failure") {
			t.Fatalf("expected InitLiveDB failure, got %v", err)
		}
		if restartCalls != 1 {
			t.Fatalf("expected 1 restart call (rollback old DB), got %d", restartCalls)
		}
		var portRow, remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "webPort").First(&portRow).Error; err != nil || portRow.Value != "8443" {
			t.Fatalf("expected webPort rolled back to 8443, got %q err=%v", portRow.Value, err)
		}
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "OLD-INIT-FAIL" {
			t.Fatalf("expected remarkTemplate rolled back to OLD-INIT-FAIL, got %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("Commit pre-commit failure (durable=false) triggers rollback", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "OLD-COMMIT-DURABLE-FALSE")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-COMMIT-DURABLE-FALSE")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		origHook := sqliteCommitTxHook
		t.Cleanup(func() { sqliteCommitTxHook = origHook })
		sqliteCommitTxHook = func(*database.SQLiteRestoreTransaction) (bool, error) {
			return false, errors.New("injected pre-commit marker write failure")
		}

		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error { return nil },
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "injected pre-commit marker write failure") {
			t.Fatalf("expected pre-commit failure, got %v", err)
		}
		if restartCalls != 2 {
			t.Fatalf("expected 2 restart calls (post-apply + rollback), got %d", restartCalls)
		}
		var remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "OLD-COMMIT-DURABLE-FALSE" {
			t.Fatalf("expected remarkTemplate rolled back to OLD-COMMIT-DURABLE-FALSE, got %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})

	t.Run("Commit post-commit cleanup failure (durable=true) keeps new DB active", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "OLD-COMMIT-DURABLE-TRUE")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-COMMIT-DURABLE-TRUE")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		origHook := sqliteCommitTxHook
		t.Cleanup(func() { sqliteCommitTxHook = origHook })
		sqliteCommitTxHook = func(tx *database.SQLiteRestoreTransaction) (bool, error) {
			if durable, err := tx.Commit(); err != nil {
				return durable, err
			}
			return true, errors.New("injected post-commit cleanup failure")
		}

		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error { return nil },
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "post-commit cleanup failed") {
			t.Fatalf("expected post-commit cleanup failure, got %v", err)
		}
		if restartCalls != 1 {
			t.Fatalf("expected 1 restart call (no rollback), got %d", restartCalls)
		}
		var remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "NEW-COMMIT-DURABLE-TRUE" {
			t.Fatalf("expected new DB active (NEW-COMMIT-DURABLE-TRUE), got %q err=%v", remarkRow.Value, err)
		}
		assertRestoreLockReleased(t, livePath)
		assertNoLeftoverRestoreArtifacts(t, dbDir)
	})
}

func assertRestoreLockReleased(t *testing.T, livePath string) {
	t.Helper()
	tx2, err := database.BeginSQLiteRestore(livePath)
	if err != nil {
		if strings.Contains(err.Error(), "already in progress") {
			t.Fatalf("restore mutex leaked: %v", err)
		}
		if strings.Contains(err.Error(), "pending SQLite restore marker exists") {
			t.Fatalf("unexpected pending marker blocking next restore: %v", err)
		}
		t.Fatalf("BeginSQLiteRestore after previous outcome failed: %v", err)
	}
	if abortErr := tx2.AbortBeforeApply(); abortErr != nil {
		t.Fatalf("AbortBeforeApply on second restore tx: %v", abortErr)
	}
}

func TestImportSQLiteDB_RealMalformedDBAndPanicAndLockRelease(t *testing.T) {
	t.Run("real malformed candidate DB triggers strict migration rollback without panic", func(t *testing.T) {
		cases := []struct {
			name          string
			seedCandidate func(t *testing.T, srcDB string)
			wantErrSubstr string
		}{
			{
				name: "scenario1_invalid_clients_element",
				seedCandidate: func(t *testing.T, srcDB string) {
					t.Helper()
					seedPanelDBAt(t, srcDB, "9999", "BAD-CLIENTS")
					if err := database.InitDB(srcDB); err != nil {
						t.Fatal(err)
					}
					if err := database.GetDB().Create(&model.Inbound{
						UserId:         1,
						Tag:            "bad-clients",
						Enable:         true,
						Port:           34001,
						Protocol:       model.VLESS,
						Settings:       `{"clients":[1]}`,
						StreamSettings: `{}`,
					}).Error; err != nil {
						t.Fatal(err)
					}
				},
				wantErrSubstr: "field settings.clients[0]: expected JSON object",
			},
			{
				name: "scenario2_invalid_settings_json",
				seedCandidate: func(t *testing.T, srcDB string) {
					t.Helper()
					seedPanelDBAt(t, srcDB, "9999", "BAD-JSON")
					if err := database.InitDB(srcDB); err != nil {
						t.Fatal(err)
					}
					if err := database.GetDB().Create(&model.Inbound{
						UserId:         1,
						Tag:            "bad-json",
						Enable:         true,
						Port:           34002,
						Protocol:       model.VLESS,
						Settings:       `{bad-json`,
						StreamSettings: `{}`,
					}).Error; err != nil {
						t.Fatal(err)
					}
				},
				wantErrSubstr: "field settings: invalid JSON",
			},
			{
				name: "scenario3_external_proxy_invalid_domain",
				seedCandidate: func(t *testing.T, srcDB string) {
					t.Helper()
					seedPanelDBAt(t, srcDB, "9999", "BAD-DOMAIN")
					if err := database.InitDB(srcDB); err != nil {
						t.Fatal(err)
					}
					if err := database.GetDB().Create(&model.Inbound{
						UserId:         1,
						Tag:            "bad-domain",
						Enable:         true,
						Port:           34003,
						Protocol:       model.VLESS,
						Settings:       `{"clients":[]}`,
						StreamSettings: `{"security":"tls","tlsSettings":{"settings":{"domains":[{"domain":123}]}}}`,
					}).Error; err != nil {
						t.Fatal(err)
					}
				},
				wantErrSubstr: "domains[0].domain: expected non-empty string",
			},
			{
				name: "scenario3b_malformed_stream_settings_json",
				seedCandidate: func(t *testing.T, srcDB string) {
					t.Helper()
					seedPanelDBAt(t, srcDB, "9999", "BAD-STREAM-JSON")
					if err := database.InitDB(srcDB); err != nil {
						t.Fatal(err)
					}
					if err := database.GetDB().Create(&model.Inbound{
						UserId:         1,
						Tag:            "bad-stream-json",
						Enable:         true,
						Port:           34005,
						Protocol:       model.VLESS,
						Settings:       `{"clients":[]}`,
						StreamSettings: `{malformed-stream-json`,
					}).Error; err != nil {
						t.Fatal(err)
					}
				},
				wantErrSubstr: "field streamSettings: invalid JSON",
			},
			{
				name: "scenario4_real_downstream_db_error_in_migration",
				seedCandidate: func(t *testing.T, srcDB string) {
					t.Helper()
					seedPanelDBAt(t, srcDB, "9999", "BAD-TRIGGER")
					if err := database.InitDB(srcDB); err != nil {
						t.Fatal(err)
					}
					db := database.GetDB()
					if err := db.Create(&model.Inbound{
						UserId:         1,
						Tag:            "trigger-inbound",
						Enable:         true,
						Port:           34004,
						Protocol:       model.VLESS,
						Settings:       `{"clients":[{"email":"trig@example.com","id":"trig-uid-1","enable":true}]}`,
						StreamSettings: `{}`,
					}).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Exec(`CREATE TRIGGER fail_inbounds_update BEFORE UPDATE ON inbounds BEGIN SELECT RAISE(ABORT, 'simulated downstream inbounds update error'); END;`).Error; err != nil {
						t.Fatal(err)
					}
				},
				wantErrSubstr: "simulated downstream inbounds update error",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dbDir := t.TempDir()
				t.Setenv("XUI_DB_FOLDER", dbDir)
				livePath := config.GetDBPath()
				seedPanelDBAt(t, livePath, "8443", "OLD-REAL-MIGRATION-SAFE")
				_ = database.CloseDB()

				srcDB := filepath.Join(t.TempDir(), "bad.db")
				tc.seedCandidate(t, srcDB)
				_ = database.CloseDB()

				if err := database.InitDB(livePath); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = database.CloseDB() })

				restartCalls := 0
				var svc ServerService
				err := svc.importSQLiteDB(
					&ImportUploadStage{Path: srcDB, Kind: importKindSQLiteDB},
					true,
					func() error { return nil },
					func() error {
						restartCalls++
						return nil
					},
				)
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErrSubstr, err)
				}
				if restartCalls != 1 {
					t.Fatalf("expected 1 restart call on rollback, got %d", restartCalls)
				}
				var remarkRow model.Setting
				if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "OLD-REAL-MIGRATION-SAFE" {
					t.Fatalf("expected OLD-REAL-MIGRATION-SAFE restored, got %q err=%v", remarkRow.Value, err)
				}
				assertNoLeftoverRestoreArtifacts(t, dbDir)
				assertRestoreLockReleased(t, livePath)
			})
		}
	})

	t.Run("panic after apply triggers rollback, single Xray restart, sanitized error, and lock release", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "OLD-PANIC-SAFE")

		srcDB := filepath.Join(t.TempDir(), "new.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-PANIC")
		dumpPath := filepath.Join(t.TempDir(), "new.dump")
		if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
			t.Fatal(err)
		}

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		origHook := sqliteMigrateDBHook
		t.Cleanup(func() { sqliteMigrateDBHook = origHook })
		sqliteMigrateDBHook = func() error {
			panic("secret-dsn-or-internal-panic-detail")
		}

		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
			true,
			func() error { return nil },
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "Unexpected internal error while applying imported DB") {
			t.Fatalf("expected sanitized panic boundary error, got %v", err)
		}
		if strings.Contains(err.Error(), "secret-dsn-or-internal-panic-detail") || strings.Contains(err.Error(), "goroutine") {
			t.Fatalf("user-facing error leaked panic detail or stack: %v", err)
		}
		logs := logger.GetLogs(50, "error")
		foundBoundaryLog := false
		for _, entry := range logs {
			if strings.Contains(entry, "secret-dsn-or-internal-panic-detail") {
				t.Fatalf("error log leaked raw panic value: %s", entry)
			}
			if strings.Contains(entry, "Import panic (applied=true, durable=false, type=string)") && strings.Contains(entry, "runtime/debug.Stack") {
				foundBoundaryLog = true
			}
		}
		if !foundBoundaryLog {
			t.Fatalf("expected sanitized panic boundary log with stack trace in error logs, got %v", logs)
		}
		if restartCalls != 1 {
			t.Fatalf("expected exactly 1 Xray restart from rollback, got %d", restartCalls)
		}
		var remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "OLD-PANIC-SAFE" {
			t.Fatalf("expected OLD-PANIC-SAFE restored, got %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
		assertRestoreLockReleased(t, livePath)
	})

	t.Run("panic inside MigrationRequirements tx rolls back internal tx, triggers service rollback, and releases lock", func(t *testing.T) {
		dbDir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dbDir)
		livePath := config.GetDBPath()
		seedPanelDBAt(t, livePath, "8443", "OLD-INTX-PANIC")

		srcDB := filepath.Join(t.TempDir(), "new-intx-panic.db")
		seedPanelDBAt(t, srcDB, "9999", "NEW-INTX-PANIC")
		if err := database.InitDB(srcDB); err != nil {
			t.Fatal(err)
		}
		if err := database.GetDB().Create(&model.Inbound{
			UserId:         1,
			Tag:            "intx-panic",
			Enable:         true,
			Port:           36002,
			Protocol:       model.VLESS,
			Settings:       `{"clients":[{"email":"intx@example.com","id":"intx-uid-1","enable":true}]}`,
			StreamSettings: `{}`,
		}).Error; err != nil {
			t.Fatal(err)
		}
		_ = database.CloseDB()

		if err := database.InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })

		origHook := sqliteMigrationInTxHook
		t.Cleanup(func() { sqliteMigrationInTxHook = origHook })
		sqliteMigrationInTxHook = func() {
			panic("SECRET_INTX_PANIC_VALUE")
		}

		restartCalls := 0
		var svc ServerService
		err := svc.importSQLiteDB(
			&ImportUploadStage{Path: srcDB, Kind: importKindSQLiteDB},
			true,
			func() error { return nil },
			func() error {
				restartCalls++
				return nil
			},
		)
		if err == nil || !strings.Contains(err.Error(), "Unexpected internal error while applying imported DB") {
			t.Fatalf("expected sanitized panic boundary error, got %v", err)
		}
		if strings.Contains(err.Error(), "SECRET_INTX_PANIC_VALUE") {
			t.Fatalf("user-facing error leaked secret panic value: %v", err)
		}
		if restartCalls != 1 {
			t.Fatalf("expected 1 Xray restart from rollback, got %d", restartCalls)
		}
		var remarkRow model.Setting
		if err := database.GetDB().Where("key = ?", "remarkTemplate").First(&remarkRow).Error; err != nil || remarkRow.Value != "OLD-INTX-PANIC" {
			t.Fatalf("expected OLD-INTX-PANIC restored, got %q err=%v", remarkRow.Value, err)
		}
		assertNoLeftoverRestoreArtifacts(t, dbDir)
		assertRestoreLockReleased(t, livePath)
	})

	t.Run("restore lock is released across all remaining failure and success outcomes", func(t *testing.T) {
		runOutcome := func(t *testing.T, setupHook func(t *testing.T), wantErr string) {
			t.Helper()
			dbDir := t.TempDir()
			t.Setenv("XUI_DB_FOLDER", dbDir)
			livePath := config.GetDBPath()
			seedPanelDBAt(t, livePath, "8443", "OLD-LOCK-CHECK")

			srcDB := filepath.Join(t.TempDir(), "new.db")
			seedPanelDBAt(t, srcDB, "9999", "NEW-LOCK-CHECK")
			dumpPath := filepath.Join(t.TempDir(), "new.dump")
			if err := database.DumpSQLite(srcDB, dumpPath); err != nil {
				t.Fatal(err)
			}

			if err := database.InitDB(livePath); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.CloseDB() })

			if setupHook != nil {
				setupHook(t)
			}

			var svc ServerService
			err := svc.importSQLiteDB(
				&ImportUploadStage{Path: dumpPath, Kind: importKindSQLiteDump},
				true,
				func() error { return nil },
				func() error { return nil },
			)
			if wantErr == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("expected error %q, got %v", wantErr, err)
			}
			assertRestoreLockReleased(t, livePath)
			assertNoLeftoverRestoreArtifacts(t, dbDir)
		}

		t.Run("capture_host_settings_failure", func(t *testing.T) {
			runOutcome(t, func(t *testing.T) {
				orig := sqliteCaptureHostSettingHook
				t.Cleanup(func() { sqliteCaptureHostSettingHook = orig })
				sqliteCaptureHostSettingHook = func() error { return errors.New("lock-test capture fail") }
			}, "lock-test capture fail")
		})
		t.Run("close_live_db_failure", func(t *testing.T) {
			runOutcome(t, func(t *testing.T) {
				orig := sqliteCloseLiveDBHook
				t.Cleanup(func() { sqliteCloseLiveDBHook = orig })
				sqliteCloseLiveDBHook = func(*database.SQLiteRestoreTransaction) error { return errors.New("lock-test close fail") }
			}, "lock-test close fail")
		})
		t.Run("init_live_db_failure", func(t *testing.T) {
			runOutcome(t, func(t *testing.T) {
				orig := sqliteInitLiveDBHook
				t.Cleanup(func() { sqliteInitLiveDBHook = orig })
				sqliteInitLiveDBHook = func(*database.SQLiteRestoreTransaction) error { return errors.New("lock-test init fail") }
			}, "lock-test init fail")
		})
		t.Run("migrate_db_strict_failure", func(t *testing.T) {
			runOutcome(t, func(t *testing.T) {
				orig := sqliteMigrateDBHook
				t.Cleanup(func() { sqliteMigrateDBHook = orig })
				sqliteMigrateDBHook = func() error { return errors.New("lock-test migrate fail") }
			}, "lock-test migrate fail")
		})
		t.Run("pre_commit_durable_false_failure", func(t *testing.T) {
			runOutcome(t, func(t *testing.T) {
				orig := sqliteCommitTxHook
				t.Cleanup(func() { sqliteCommitTxHook = orig })
				sqliteCommitTxHook = func(*database.SQLiteRestoreTransaction) (bool, error) {
					return false, errors.New("lock-test pre-commit fail")
				}
			}, "lock-test pre-commit fail")
		})
		t.Run("commit_success", func(t *testing.T) {
			runOutcome(t, nil, "")
		})
	})
}
