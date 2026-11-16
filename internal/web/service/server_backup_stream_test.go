package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
)

func setupFakePgDump(t *testing.T, scriptBody string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake pg_dump test uses POSIX shell script")
	}
	binDir := t.TempDir()
	pgDumpPath := filepath.Join(binDir, "pg_dump")
	if err := os.WriteFile(pgDumpPath, []byte("#!/bin/sh\n"+scriptBody+"\n"), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	origHook := pgResolveCanonicalSchemaHook
	pgResolveCanonicalSchemaHook = func(context.Context) (string, error) {
		return "public", nil
	}
	t.Cleanup(func() {
		pgResolveCanonicalSchemaHook = origHook
	})
	return binDir
}

func ensurePostgresTestProcess(t *testing.T, dsn string) string {
	t.Helper()
	if os.Getenv("MIKROZIA_PG_SUBPROCESS") != "1" && database.GetDB() != nil {
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
		cmd.Env = append(os.Environ(), "MIKROZIA_PG_SUBPROCESS=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("subprocess %s failed: %v\n%s", t.Name(), err, string(out))
		}
		return ""
	}
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "postgres")
	t.Setenv("XUI_DB_DSN", dsn)
	if !database.IsPostgres() {
		t.Fatal("expected database.IsPostgres() to be true")
	}
	return dbFolder
}

func TestPrepareBackupPostgresLargeStreamFileBacked(t *testing.T) {
	const wantSize = int64(64 * 1024 * 1024)
	dbFolder := ensurePostgresTestProcess(t, "postgres://mikrozia:supersecret@127.0.0.1:5432/mikrozia?sslmode=disable")
	if dbFolder == "" {
		return
	}
	setupFakePgDump(t, `exec dd if=/dev/zero bs=1048576 count=64 2>/dev/null`)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	var s ServerService
	artifact, err := s.PrepareBackup(context.Background(), "panel.example.com")
	if err != nil {
		t.Fatalf("PrepareBackup: %v", err)
	}
	defer artifact.Cleanup()

	runtime.ReadMemStats(&after)

	if artifact.Backend != "postgres" {
		t.Fatalf("artifact.Backend = %q, want postgres", artifact.Backend)
	}
	if artifact.Size != wantSize {
		t.Fatalf("artifact.Size = %d, want %d", artifact.Size, wantSize)
	}
	if !strings.HasSuffix(artifact.Filename, ".dump") {
		t.Fatalf("artifact.Filename = %q, want .dump suffix", artifact.Filename)
	}
	if !strings.HasPrefix(artifact.Path, dbFolder+string(os.PathSeparator)) {
		t.Fatalf("artifact.Path = %q, want inside %q", artifact.Path, dbFolder)
	}
	parentBase := filepath.Base(filepath.Dir(artifact.Path))
	if !strings.HasPrefix(parentBase, ".mikrozia-backup-") {
		t.Fatalf("artifact parent dir = %q, want .mikrozia-backup-* prefix", parentBase)
	}

	dirInfo, err := os.Stat(filepath.Dir(artifact.Path))
	if err != nil {
		t.Fatalf("Stat(dir): %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("backup dir perm = %04o, want 0700", perm)
	}
	fileInfo, err := os.Stat(artifact.Path)
	if err != nil {
		t.Fatalf("Stat(file): %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("backup file perm = %04o, want 0600", perm)
	}
	if fileInfo.Size() != wantSize {
		t.Fatalf("on-disk size = %d, want %d", fileInfo.Size(), wantSize)
	}

	// File-backed pg_dump must not buffer the 64 MiB payload on the Go heap.
	if after.TotalAlloc > before.TotalAlloc && after.TotalAlloc-before.TotalAlloc > 32*1024*1024 {
		t.Fatalf("TotalAlloc grew by %d bytes during 64 MiB pg_dump; expected file-backed streaming without full RAM buffer", after.TotalAlloc-before.TotalAlloc)
	}

	parentDir := filepath.Dir(artifact.Path)
	artifact.Cleanup()
	if _, statErr := os.Stat(parentDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected backup dir %q to be removed after Cleanup, got err=%v", parentDir, statErr)
	}
}

func TestPrepareBackupPostgresCancelKillsProcessGroupAndCleansUp(t *testing.T) {
	dbFolder := ensurePostgresTestProcess(t, "postgres://mikrozia:supersecret@127.0.0.1:5432/mikrozia?sslmode=disable")
	if dbFolder == "" {
		return
	}
	pidFile := filepath.Join(t.TempDir(), "pg_dump.pid")
	setupFakePgDump(t, fmt.Sprintf(`echo $$ > %q
printf "PGDMP-partial"
exec sleep 30`, pidFile))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		var s ServerService
		artifact, err := s.PrepareBackup(ctx, "panel.example.com")
		if artifact != nil {
			artifact.Cleanup()
		}
		errCh <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidFile)
		if err == nil {
			if parsed, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && parsed > 0 {
				pid = parsed
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("fake pg_dump did not start in time")
	}

	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected canceled PrepareBackup to return error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PrepareBackup did not return promptly after context cancel")
	}

	// Process group should be terminated.
	waitDead := time.Now().Add(5 * time.Second)
	for time.Now().Before(waitDead) {
		if err := syscall.Kill(-pid, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(-pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("expected pg_dump process group -%d to be dead, got err=%v", pid, err)
	}

	// No .mikrozia-backup-* directories should remain in dbFolder.
	entries, err := os.ReadDir(dbFolder)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dbFolder, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".mikrozia-backup-") {
			t.Fatalf("leftover backup temp dir after cancel: %s", entry.Name())
		}
	}

	// Lock must be released so a subsequent backup can succeed.
	setupFakePgDump(t, `printf "PGDMP-ok"`)
	var s ServerService
	artifact, err := s.PrepareBackup(context.Background(), "panel.example.com")
	if err != nil {
		t.Fatalf("subsequent PrepareBackup after cancel failed: %v", err)
	}
	artifact.Cleanup()
}

func TestPrepareBackupPostgresEmptyOutputFails(t *testing.T) {
	dbFolder := ensurePostgresTestProcess(t, "postgres://mikrozia:supersecret@127.0.0.1:5432/mikrozia?sslmode=disable")
	if dbFolder == "" {
		return
	}
	setupFakePgDump(t, `exit 0`)

	var s ServerService
	artifact, err := s.PrepareBackup(context.Background(), "panel.example.com")
	if artifact != nil {
		artifact.Cleanup()
	}
	if err == nil {
		t.Fatal("expected error when pg_dump produces 0 bytes")
	}
	entries, readErr := os.ReadDir(dbFolder)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".mikrozia-backup-") {
			t.Fatalf("leftover backup temp dir after empty output: %s", entry.Name())
		}
	}
}

func TestPrepareBackupPostgresBoundedStderrAndRedaction(t *testing.T) {
	const secret = "s3cr3t-pg-pass-98765"
	if ensurePostgresTestProcess(t, "postgres://mikrozia:"+secret+"@127.0.0.1:5432/mikrozia?sslmode=disable") == "" {
		return
	}
	setupFakePgDump(t, fmt.Sprintf(`i=0
while [ "$i" -lt 2000 ]; do
  printf "pg_dump error %s padding-line-0123456789abcdef0123456789abcdef\n" >&2
  i=$((i + 1))
done
exit 1`, secret))

	var s ServerService
	artifact, err := s.PrepareBackup(context.Background(), "panel.example.com")
	if artifact != nil {
		artifact.Cleanup()
	}
	if err == nil {
		t.Fatal("expected error when pg_dump exits 1")
	}
	msg := err.Error()
	if strings.Contains(msg, secret) {
		t.Fatalf("error message leaked DSN password: %q", msg)
	}
	if len(msg) > 80*1024 {
		t.Fatalf("error message length = %d, expected bounded stderr", len(msg))
	}
}

func TestPrepareBackupConcurrencyLockAndCancel(t *testing.T) {
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
	if artifact, err := s2.PrepareBackup(waitCtx, "panel.example.com"); err == nil {
		artifact.Cleanup()
		t.Fatal("expected second concurrent backup to fail while first holds lock")
	} else if !strings.Contains(err.Error(), "database export request canceled while another backup or migration is running") {
		t.Fatalf("unexpected concurrent cancel error: %v", err)
	}

	if err := os.WriteFile(releaseFile, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write releaseFile: %v", err)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first PrepareBackup failed: %v", err)
	}

	// Lock is released; third request succeeds.
	artifact3, err := s2.PrepareBackup(context.Background(), "panel.example.com")
	if err != nil {
		t.Fatalf("third PrepareBackup failed: %v", err)
	}
	artifact3.Cleanup()
}

func TestPrepareBackupSQLiteSnapshot(t *testing.T) {
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "sqlite")
	liveDBPath := filepath.Join(dbFolder, "x-ui.db")
	if err := database.InitDB(liveDBPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	var s ServerService
	artifact, err := s.PrepareBackup(context.Background(), "127.0.0.1:2053")
	if err != nil {
		t.Fatalf("PrepareBackup(sqlite): %v", err)
	}
	if artifact.Backend != "sqlite" {
		artifact.Cleanup()
		t.Fatalf("artifact.Backend = %q, want sqlite", artifact.Backend)
	}
	if artifact.Path == liveDBPath {
		artifact.Cleanup()
		t.Fatal("artifact.Path must be a snapshot, not the live x-ui.db file")
	}
	if !strings.HasSuffix(artifact.Filename, ".db") {
		artifact.Cleanup()
		t.Fatalf("artifact.Filename = %q, want .db", artifact.Filename)
	}
	if err := database.ValidateSQLiteDB(artifact.Path); err != nil {
		artifact.Cleanup()
		t.Fatalf("ValidateSQLiteDB(snapshot): %v", err)
	}
	dirInfo, err := os.Stat(filepath.Dir(artifact.Path))
	if err != nil {
		artifact.Cleanup()
		t.Fatalf("Stat(dir): %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		artifact.Cleanup()
		t.Fatalf("sqlite backup dir perm = %04o, want 0700", perm)
	}
	fileInfo, err := os.Stat(artifact.Path)
	if err != nil {
		artifact.Cleanup()
		t.Fatalf("Stat(file): %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		artifact.Cleanup()
		t.Fatalf("sqlite backup file perm = %04o, want 0600", perm)
	}
	parentDir := filepath.Dir(artifact.Path)
	artifact.Cleanup()
	// Repeated Cleanup calls must be idempotent and safe.
	artifact.Cleanup()
	if _, statErr := os.Stat(parentDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected sqlite backup dir %q removed after Cleanup, got %v", parentDir, statErr)
	}
}
