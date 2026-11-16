package database

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestBackupSQLiteContextCancelsEarly(t *testing.T) {
	if err := InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	backupPath := filepath.Join(t.TempDir(), "canceled.db")
	err := BackupSQLiteContext(ctx, backupPath)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BackupSQLiteContext error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(backupPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected canceled backup file to be removed, got stat err = %v", statErr)
	}
}

func TestBackupSQLiteContextPermissionsAndMidCopyCancelInWAL(t *testing.T) {
	t.Setenv("XUI_DB_JOURNAL_MODE", "")
	if err := InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	var journalMode string
	if err := db.Raw("PRAGMA journal_mode;").Scan(&journalMode).Error; err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	// Populate ~24 MiB across multiple rows so WAL-mode BackupSQLiteContext runs
	// across many 128-page batches and we can observe the live destination file
	// mode and cancel mid-copy.
	if err := db.Exec("CREATE TABLE IF NOT EXISTS backup_payload (id INTEGER PRIMARY KEY, body BLOB)").Error; err != nil {
		t.Fatalf("create payload table: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	chunk := bytes.Repeat([]byte("M"), 128*1024)
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	stmt, err := tx.Prepare("INSERT INTO backup_payload (body) VALUES (?)")
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("prepare insert: %v", err)
	}
	defer func() { _ = stmt.Close() }()
	for i := 0; i < 192; i++ {
		if _, err := stmt.Exec(chunk); err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert payload row %d: %v", i, err)
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("close stmt: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}
	if err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE);").Error; err != nil {
		t.Fatalf("wal_checkpoint: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backupPath := filepath.Join(t.TempDir(), "midcopy.db")
	errCh := make(chan error, 1)
	go func() {
		errCh <- BackupSQLiteContext(ctx, backupPath)
	}()

	// Wait until BackupSQLiteContext has created the destination file and begun
	// writing pages into it, inspect its permissions while still in progress,
	// then cancel mid-copy.
	var sawInProgress bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("BackupSQLiteContext finished (%v) before mid-copy cancellation could trigger", err)
		default:
		}
		info, statErr := os.Stat(backupPath)
		if statErr == nil && info.Size() > 0 {
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				cancel()
				<-errCh
				t.Fatalf("in-progress backup file mode = %04o, want 0600", info.Mode().Perm())
			}
			sawInProgress = true
			cancel()
			break
		}
		time.Sleep(100 * time.Microsecond)
	}
	if !sawInProgress {
		cancel()
		<-errCh
		t.Fatal("timed out waiting for in-progress backup destination file")
	}

	var backupErr error
	select {
	case backupErr = <-errCh:
	case <-time.After(10 * time.Second):
		t.Fatal("BackupSQLiteContext did not exit promptly after mid-copy cancellation")
	}
	if !errors.Is(backupErr, context.Canceled) {
		t.Fatalf("mid-copy BackupSQLiteContext error = %v, want context.Canceled", backupErr)
	}
	if _, statErr := os.Stat(backupPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected canceled backup file %q to be removed, got stat err = %v", backupPath, statErr)
	}

	// Source DB must remain usable for queries and writes after cancellation.
	if _, err := sqlDB.Exec("INSERT INTO backup_payload (body) VALUES (?)", []byte("after-cancel")); err != nil {
		t.Fatalf("source DB write after cancel failed: %v", err)
	}

	// A subsequent backup must succeed and pass ValidateSQLiteDB.
	nextPath := filepath.Join(t.TempDir(), "after-cancel.db")
	if err := BackupSQLiteContext(context.Background(), nextPath); err != nil {
		t.Fatalf("subsequent BackupSQLiteContext: %v", err)
	}
	if err := ValidateSQLiteDB(nextPath); err != nil {
		t.Fatalf("ValidateSQLiteDB(subsequent): %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(nextPath)
		if err != nil {
			t.Fatalf("Stat(%s): %v", nextPath, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("final backup file permissions = %04o, want 0600", perm)
		}
	}
}
