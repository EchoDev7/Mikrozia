package controller

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/testpg"

	"github.com/gin-gonic/gin"
)

func TestGetDbStreamsPostgres64MiBWithoutFullMemoryBuffer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake pg_dump test uses POSIX shell script")
	}
	baseDSN := strings.TrimSpace(os.Getenv("XUI_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("set XUI_TEST_PG_DSN to run the live PostgreSQL backup stream test")
	}
	if os.Getenv("MIKROZIA_PG_SUBPROCESS") != "1" && database.GetDB() != nil {
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
		cmd.Env = append(os.Environ(), "MIKROZIA_PG_SUBPROCESS=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("subprocess %s failed: %v\n%s", t.Name(), err, string(out))
		}
		return
	}

	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "postgres")
	t.Setenv("XUI_DB_DSN", baseDSN)
	// The backup path resolves the canonical schema from the live pool, so a
	// real PostgreSQL is required; the dump itself stays fake for exact sizes.
	cleanupSchema, err := testpg.IsolatePackage("controller_backup_stream")
	if err != nil {
		t.Fatalf("isolate PostgreSQL schema: %v", err)
	}
	t.Cleanup(cleanupSchema)
	if err := database.InitDB(filepath.Join(dbFolder, "x-ui.db")); err != nil {
		t.Fatalf("InitDB(postgres): %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })
	if !database.IsPostgres() {
		t.Fatal("expected database.IsPostgres() to be true")
	}

	binDir := t.TempDir()
	pgDumpPath := filepath.Join(binDir, "pg_dump")
	if err := os.WriteFile(pgDumpPath, []byte("#!/bin/sh\nexec dd if=/dev/zero bs=1048576 count=64 2>/dev/null\n"), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	ctrl := &ServerController{}
	engine.GET("/panel/api/server/getDb", ctrl.getDb)

	srv := httptest.NewServer(engine)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/panel/api/server/getDb")
	if err != nil {
		t.Fatalf("GET /panel/api/server/getDb: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment; filename=") || !strings.HasSuffix(cd, ".dump") {
		t.Fatalf("Content-Disposition = %q, want attachment; filename=*.dump", cd)
	}
	const wantSize = int64(64 * 1024 * 1024)
	if resp.ContentLength != wantSize {
		t.Fatalf("Content-Length = %d, want %d", resp.ContentLength, wantSize)
	}

	written, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	if written != wantSize {
		t.Fatalf("streamed bytes = %d, want %d", written, wantSize)
	}

	assertNoBackupDirs(t, dbFolder)
}

func assertNoBackupDirs(t *testing.T, dbFolder string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := os.ReadDir(dbFolder)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		var leftover string
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".mikrozia-backup-") {
				leftover = entry.Name()
				break
			}
		}
		if leftover == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("leftover backup temp dir after HTTP stream: %s", leftover)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGetDbStreamsSQLiteSnapshot(t *testing.T) {
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "sqlite")
	if err := database.InitDB(filepath.Join(dbFolder, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	ctrl := &ServerController{}
	engine.GET("/panel/api/server/getDb", ctrl.getDb)

	srv := httptest.NewServer(engine)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/panel/api/server/getDb")
	if err != nil {
		t.Fatalf("GET /panel/api/server/getDb: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment; filename=") || !strings.HasSuffix(cd, ".db") {
		t.Fatalf("Content-Disposition = %q, want attachment; filename=*.db", cd)
	}
	if resp.ContentLength <= 0 {
		t.Fatalf("Content-Length = %d, want > 0", resp.ContentLength)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if int64(len(body)) != resp.ContentLength {
		t.Fatalf("body length = %d, Content-Length = %d", len(body), resp.ContentLength)
	}
	if !bytes.HasPrefix(body, []byte("SQLite format 3\x00")) {
		t.Fatalf("response does not start with SQLite header")
	}

	// The handler removes the artifact after it returns from streaming, so poll
	// briefly instead of racing the deferred cleanup.
	assertNoBackupDirs(t, dbFolder)
}
