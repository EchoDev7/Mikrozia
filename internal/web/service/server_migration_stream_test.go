package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/database/model"
)

func seedLargeSQLiteSettingsForMigration(t *testing.T, rowCount, valueBytes int) {
	t.Helper()
	db := database.GetDB()
	if db == nil {
		t.Fatal("expected initialized database")
	}
	payload := strings.Repeat("m", valueBytes)
	rows := make([]model.Setting, 0, rowCount)
	for i := range rowCount {
		rows = append(rows, model.Setting{
			Key:   fmt.Sprintf("mig-test-%05d", i),
			Value: payload,
		})
	}
	if err := db.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatalf("seed settings: %v", err)
	}
}

func TestPrepareMigrationSQLiteLargeAndCleanup(t *testing.T) {
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "sqlite")
	if err := database.InitDB(filepath.Join(dbFolder, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	// Seed ~24 MiB of settings rows.
	seedLargeSQLiteSettingsForMigration(t, 6000, 4096)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	var s ServerService
	artifact, err := s.PrepareMigration(context.Background())
	if err != nil {
		t.Fatalf("PrepareMigration: %v", err)
	}
	defer artifact.Cleanup()

	runtime.GC()
	runtime.ReadMemStats(&after)

	if artifact.Backend != "sqlite" {
		t.Fatalf("artifact.Backend = %q, want sqlite", artifact.Backend)
	}
	if artifact.Filename != "x-ui.dump" {
		t.Fatalf("artifact.Filename = %q, want x-ui.dump", artifact.Filename)
	}
	if artifact.Size <= 20*1024*1024 {
		t.Fatalf("artifact.Size = %d, want > 20 MiB", artifact.Size)
	}
	if !strings.HasPrefix(artifact.Path, dbFolder+string(os.PathSeparator)) {
		t.Fatalf("artifact.Path = %q, want inside %q", artifact.Path, dbFolder)
	}

	parentDir := filepath.Dir(artifact.Path)
	if !strings.HasPrefix(filepath.Base(parentDir), ".mikrozia-migration-") {
		t.Fatalf("artifact parent dir = %q, want .mikrozia-migration-* prefix", filepath.Base(parentDir))
	}

	dirInfo, err := os.Stat(parentDir)
	if err != nil {
		t.Fatalf("Stat(dir): %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("migration dir perm = %04o, want 0700", perm)
	}

	fileInfo, err := os.Stat(artifact.Path)
	if err != nil {
		t.Fatalf("Stat(file): %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("migration file perm = %04o, want 0600", perm)
	}
	if fileInfo.Size() != artifact.Size {
		t.Fatalf("fileInfo.Size() = %d, artifact.Size = %d", fileInfo.Size(), artifact.Size)
	}

	// Intermediate snapshot.db must already be deleted before PrepareMigration returns.
	if _, statErr := os.Stat(filepath.Join(parentDir, "snapshot.db")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected snapshot.db deleted, got %v", statErr)
	}

	if after.HeapAlloc > before.HeapAlloc+16*1024*1024 {
		t.Fatalf("HeapAlloc grew by %d bytes (> 16 MiB) for %d-byte migration dump",
			after.HeapAlloc-before.HeapAlloc, artifact.Size)
	}

	artifact.Cleanup()
	artifact.Cleanup()
	if _, statErr := os.Stat(parentDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected migration dir %q removed after Cleanup, got %v", parentDir, statErr)
	}
}

func TestPrepareMigrationConcurrencyLockAndCancel(t *testing.T) {
	if ensurePostgresTestProcess(t, "postgres://mikrozia:supersecret@127.0.0.1:5432/mikrozia?sslmode=disable") == "" {
		return
	}
	startedFile := filepath.Join(t.TempDir(), "started")
	releaseFile := filepath.Join(t.TempDir(), "release")
	setupFakePgDump(t, fmt.Sprintf(`touch %q
while [ ! -f %q ]; do sleep 0.02; done
printf "PGDMP-first"`, startedFile, releaseFile))

	firstDone := make(chan error, 1)
	go func() {
		var s ServerService
		artifact, err := s.PrepareBackup(context.Background(), "panel.example.com")
		if artifact != nil {
			artifact.Cleanup()
		}
		firstDone <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(startedFile); err != nil {
		t.Fatal("first backup did not start in time")
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer waitCancel()
	var s2 ServerService
	if artifact, err := s2.PrepareMigration(waitCtx); err == nil {
		artifact.Cleanup()
		t.Fatal("expected PrepareMigration to fail while PrepareBackup holds lock")
	} else if !strings.Contains(err.Error(), "database export request canceled while another backup or migration is running") {
		t.Fatalf("unexpected concurrent cancel error: %v", err)
	}

	if err := os.WriteFile(releaseFile, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write releaseFile: %v", err)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first PrepareBackup failed: %v", err)
	}

	// Lock is released; subsequent PrepareBackup succeeds.
	artifact2, err := s2.PrepareBackup(context.Background(), "panel.example.com")
	if err != nil {
		t.Fatalf("subsequent PrepareBackup failed: %v", err)
	}
	artifact2.Cleanup()
}
