package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/database"
)

func buildMultipartRequest(t *testing.T, fields []struct {
	name     string
	filename string
	payload  []byte
},
) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		var (
			w   io.Writer
			err error
		)
		if f.filename != "" {
			w, err = mw.CreateFormFile(f.name, f.filename)
		} else {
			w, err = mw.CreateFormField(f.name)
		}
		if err != nil {
			t.Fatalf("create multipart field %q: %v", f.name, err)
		}
		if _, err := w.Write(f.payload); err != nil {
			t.Fatalf("write multipart field %q: %v", f.name, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/panel/api/server/importDB", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func assertNoImportStagingDirs(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), importStagingDirPrefix) {
			t.Fatalf("leftover import staging dir: %s", e.Name())
		}
	}
}

func assertNoLegacyPgImportTemps(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "x-ui-pg-restore-") || strings.HasPrefix(name, "x-ui-pg-migrate-") {
			t.Fatalf("unexpected legacy temp entry in os.TempDir(): %s", name)
		}
	}
}

func TestStageImportMultipartFieldOrderingAndValidation(t *testing.T) {
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)

	t.Run("keepHostSettings before db", func(t *testing.T) {
		req := buildMultipartRequest(t, []struct {
			name     string
			filename string
			payload  []byte
		}{
			{name: "keepHostSettings", payload: []byte("false")},
			{name: "db", filename: "../../evil.db", payload: []byte("SQLite format 3\x00test")},
		})
		stage, err := StageImportMultipart(context.Background(), req, nil, 1024*1024)
		if err != nil {
			t.Fatalf("StageImportMultipart: %v", err)
		}
		defer stage.Cleanup()

		if stage.KeepHostSettings {
			t.Fatal("expected KeepHostSettings=false")
		}
		if stage.Kind != importKindSQLiteDB {
			t.Fatalf("Kind = %d, want importKindSQLiteDB", stage.Kind)
		}
		if filepath.Base(stage.Path) != importStagedFilename {
			t.Fatalf("staged filename = %q, want %q", filepath.Base(stage.Path), importStagedFilename)
		}
		if !strings.HasPrefix(stage.Dir, dbFolder+string(os.PathSeparator)) {
			t.Fatalf("stage.Dir = %q, want under %q", stage.Dir, dbFolder)
		}
		if runtime.GOOS != "windows" {
			dirInfo, err := os.Stat(stage.Dir)
			if err != nil {
				t.Fatalf("Stat(dir): %v", err)
			}
			if perm := dirInfo.Mode().Perm(); perm != 0o700 {
				t.Fatalf("dir perm = %04o, want 0700", perm)
			}
			fileInfo, err := os.Stat(stage.Path)
			if err != nil {
				t.Fatalf("Stat(file): %v", err)
			}
			if perm := fileInfo.Mode().Perm(); perm != 0o600 {
				t.Fatalf("file perm = %04o, want 0600", perm)
			}
		}
	})

	t.Run("keepHostSettings after db", func(t *testing.T) {
		req := buildMultipartRequest(t, []struct {
			name     string
			filename string
			payload  []byte
		}{
			{name: "db", filename: "backup.dump", payload: []byte("PGDMP\x01\x10\x04payload")},
			{name: "keepHostSettings", payload: []byte("false")},
		})
		stage, err := StageImportMultipart(context.Background(), req, nil, 1024*1024)
		if err != nil {
			t.Fatalf("StageImportMultipart: %v", err)
		}
		defer stage.Cleanup()

		if stage.KeepHostSettings {
			t.Fatal("expected KeepHostSettings=false")
		}
		if stage.Kind != importKindPgDump {
			t.Fatalf("Kind = %d, want importKindPgDump", stage.Kind)
		}
	})

	t.Run("default keepHostSettings true when omitted", func(t *testing.T) {
		req := buildMultipartRequest(t, []struct {
			name     string
			filename string
			payload  []byte
		}{
			{name: "db", filename: "mig.dump", payload: []byte("PRAGMA foreign_keys=OFF;\n")},
		})
		stage, err := StageImportMultipart(context.Background(), req, nil, 1024*1024)
		if err != nil {
			t.Fatalf("StageImportMultipart: %v", err)
		}
		defer stage.Cleanup()

		if !stage.KeepHostSettings {
			t.Fatal("expected default KeepHostSettings=true")
		}
		if stage.Kind != importKindSQLiteDump {
			t.Fatalf("Kind = %d, want importKindSQLiteDump", stage.Kind)
		}
	})

	badCases := []struct {
		name   string
		fields []struct {
			name     string
			filename string
			payload  []byte
		}
	}{
		{
			name: "missing db part",
			fields: []struct {
				name     string
				filename string
				payload  []byte
			}{
				{name: "keepHostSettings", payload: []byte("true")},
			},
		},
		{
			name: "duplicate db part",
			fields: []struct {
				name     string
				filename string
				payload  []byte
			}{
				{name: "db", filename: "a.db", payload: []byte("SQLite format 3\x00a")},
				{name: "db", filename: "b.db", payload: []byte("SQLite format 3\x00b")},
			},
		},
		{
			name: "duplicate keepHostSettings",
			fields: []struct {
				name     string
				filename string
				payload  []byte
			}{
				{name: "keepHostSettings", payload: []byte("true")},
				{name: "keepHostSettings", payload: []byte("false")},
				{name: "db", filename: "a.db", payload: []byte("SQLite format 3\x00a")},
			},
		},
		{
			name: "oversized keepHostSettings",
			fields: []struct {
				name     string
				filename string
				payload  []byte
			}{
				{name: "keepHostSettings", payload: bytes.Repeat([]byte("a"), 128)},
				{name: "db", filename: "a.db", payload: []byte("SQLite format 3\x00a")},
			},
		},
		{
			name: "invalid keepHostSettings value",
			fields: []struct {
				name     string
				filename string
				payload  []byte
			}{
				{name: "keepHostSettings", payload: []byte("yes")},
				{name: "db", filename: "a.db", payload: []byte("SQLite format 3\x00a")},
			},
		},
		{
			name: "unexpected metadata field",
			fields: []struct {
				name     string
				filename string
				payload  []byte
			}{
				{name: "extra", payload: []byte("junk")},
				{name: "db", filename: "a.db", payload: []byte("SQLite format 3\x00a")},
			},
		},
	}

	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			req := buildMultipartRequest(t, tc.fields)
			stage, err := StageImportMultipart(context.Background(), req, nil, 1024*1024)
			if stage != nil {
				stage.Cleanup()
			}
			if !errors.Is(err, ErrImportBadRequest) {
				t.Fatalf("StageImportMultipart(%s) err = %v, want ErrImportBadRequest", tc.name, err)
			}
			assertNoImportStagingDirs(t, dbFolder)
		})
	}
}

func TestStageImportMultipartExactLimitAndOverflow(t *testing.T) {
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)

	const limit = int64(256 * 1024)
	exactPayload := make([]byte, limit)
	copy(exactPayload, "SQLite format 3\x00")

	t.Run("exact limit accepted", func(t *testing.T) {
		req := buildMultipartRequest(t, []struct {
			name     string
			filename string
			payload  []byte
		}{
			{name: "db", filename: "exact.db", payload: exactPayload},
		})
		stage, err := StageImportMultipart(context.Background(), req, nil, limit)
		if err != nil {
			t.Fatalf("expected exact limit accepted, got %v", err)
		}
		if stage.Size != limit {
			stage.Cleanup()
			t.Fatalf("stage.Size = %d, want %d", stage.Size, limit)
		}
		stage.Cleanup()
		assertNoImportStagingDirs(t, dbFolder)
	})

	t.Run("max+1 rejected", func(t *testing.T) {
		overPayload := make([]byte, limit+1)
		copy(overPayload, "SQLite format 3\x00")
		req := buildMultipartRequest(t, []struct {
			name     string
			filename string
			payload  []byte
		}{
			{name: "db", filename: "over.db", payload: overPayload},
		})
		stage, err := StageImportMultipart(context.Background(), req, nil, limit)
		if stage != nil {
			stage.Cleanup()
		}
		if !errors.Is(err, ErrImportTooLarge) {
			t.Fatalf("err = %v, want ErrImportTooLarge", err)
		}
		var tooLarge *ImportTooLargeError
		if !errors.As(err, &tooLarge) {
			t.Fatalf("expected errors.As(*ImportTooLargeError), got %T", err)
		}
		assertNoImportStagingDirs(t, dbFolder)
	})

	t.Run("chunked unknown ContentLength rejected mid-stream", func(t *testing.T) {
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		go func() {
			part, err := mw.CreateFormFile("db", "chunked.db")
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			chunk := bytes.Repeat([]byte("A"), 32*1024)
			for written := int64(0); written <= limit+64*1024; written += int64(len(chunk)) {
				if _, err := part.Write(chunk); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
			}
			_ = mw.Close()
			_ = pw.Close()
		}()

		req := httptest.NewRequest(http.MethodPost, "/panel/api/server/importDB", pr)
		req.ContentLength = -1
		req.Header.Set("Content-Type", mw.FormDataContentType())

		stage, err := StageImportMultipart(context.Background(), req, nil, limit)
		if stage != nil {
			stage.Cleanup()
		}
		if !errors.Is(err, ErrImportTooLarge) {
			t.Fatalf("chunked overflow err = %v, want ErrImportTooLarge", err)
		}
		assertNoImportStagingDirs(t, dbFolder)
	})
}

func TestStageImportMultipartStreamingMemoryAndCancelCleanup(t *testing.T) {
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)

	t.Run("streaming 40 MiB via io.Pipe stays bounded", func(t *testing.T) {
		const totalSize = int64(40 * 1024 * 1024)
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		go func() {
			part, err := mw.CreateFormFile("db", "large.db")
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			if _, err := part.Write([]byte("SQLite format 3\x00")); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			chunk := bytes.Repeat([]byte("Z"), 64*1024)
			rem := totalSize - 16
			for rem > 0 {
				n := min(int64(len(chunk)), rem)
				if _, err := part.Write(chunk[:n]); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
				rem -= n
			}
			_ = mw.Close()
			_ = pw.Close()
		}()

		req := httptest.NewRequest(http.MethodPost, "/panel/api/server/importDB", pr)
		req.ContentLength = -1
		req.Header.Set("Content-Type", mw.FormDataContentType())

		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)

		stage, err := StageImportMultipart(context.Background(), req, nil, 64*1024*1024)
		if err != nil {
			t.Fatalf("StageImportMultipart(40 MiB): %v", err)
		}
		defer stage.Cleanup()

		runtime.GC()
		runtime.ReadMemStats(&after)

		if stage.Size != totalSize {
			t.Fatalf("stage.Size = %d, want %d", stage.Size, totalSize)
		}
		if after.HeapAlloc > before.HeapAlloc+8*1024*1024 {
			t.Fatalf("HeapAlloc grew by %d bytes (> 8 MiB) while streaming 40 MiB upload",
				after.HeapAlloc-before.HeapAlloc)
		}
		stage.Cleanup()
		assertNoImportStagingDirs(t, dbFolder)
	})

	t.Run("cancel mid-stream cleans up staging dir", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		go func() {
			part, err := mw.CreateFormFile("db", "cancel.db")
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			_, _ = part.Write([]byte("SQLite format 3\x00"))
			time.Sleep(30 * time.Millisecond)
			cancel()
			_ = pw.CloseWithError(context.Canceled)
		}()

		req := httptest.NewRequest(http.MethodPost, "/panel/api/server/importDB", pr)
		req.ContentLength = -1
		req.Header.Set("Content-Type", mw.FormDataContentType())

		stage, err := StageImportMultipart(ctx, req, nil, 64*1024*1024)
		if stage != nil {
			stage.Cleanup()
		}
		if err == nil {
			t.Fatal("expected error on canceled upload")
		}
		assertNoImportStagingDirs(t, dbFolder)
	})
}

func TestPostgresImportUsesStagedPathWithoutTempDirCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake pg_restore uses POSIX shell script")
	}
	dbFolder := ensurePostgresTestProcess(t, "postgres://mikrozia:supersecret@127.0.0.1:5432/mikrozia?sslmode=disable")
	if dbFolder == "" {
		return
	}

	binDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "pg_restore_args.txt")
	pgRestorePath := filepath.Join(binDir, "pg_restore")
	script := fmt.Sprintf(`#!/bin/sh
printf "%%s\n" "$@" >> %q
exit 1
`, argsFile)
	if err := os.WriteFile(pgRestorePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pg_restore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "pg_dump"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	req := buildMultipartRequest(t, []struct {
		name     string
		filename string
		payload  []byte
	}{
		{name: "db", filename: "pg.dump", payload: []byte("PGDMP\x01\x10\x04fake-archive")},
	})

	stage, err := StageImportMultipart(context.Background(), req, nil, 1024*1024)
	if err != nil {
		t.Fatalf("StageImportMultipart: %v", err)
	}
	defer stage.Cleanup()

	var svc ServerService
	if err := svc.ImportDB(stage, stage.KeepHostSettings); err == nil {
		t.Fatal("expected error from fake pg_restore --list")
	}
	stage.Cleanup()

	rawArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read pg_restore args: %v", err)
	}
	if !strings.Contains(string(rawArgs), stage.Path) {
		t.Fatalf("pg_restore did not receive staged path %q directly; args=%q", stage.Path, string(rawArgs))
	}
	assertNoImportStagingDirs(t, dbFolder)
	assertNoLegacyPgImportTemps(t)
}

func TestMigrateSQLiteIntoPostgresUsesStageDirNotTempDir(t *testing.T) {
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)

	req := buildMultipartRequest(t, []struct {
		name     string
		filename string
		payload  []byte
	}{
		{name: "db", filename: "invalid.dump", payload: []byte("PRAGMA foreign_keys=OFF;\nCREATE TABLE not_a_panel(id INT);\n")},
	})

	stage, err := StageImportMultipart(context.Background(), req, nil, 1024*1024)
	if err != nil {
		t.Fatalf("StageImportMultipart: %v", err)
	}
	defer stage.Cleanup()

	var svc ServerService
	err = svc.migrateSQLiteIntoPostgres(stage.Path, stage.Dir, true, false)
	if err == nil {
		t.Fatal("expected error for invalid panel SQLite dump")
	}
	// Verify restored.db was created inside stage.Dir (under XUI_DB_FOLDER), not os.TempDir().
	if _, statErr := os.Stat(filepath.Join(stage.Dir, "restored.db")); statErr != nil {
		t.Fatalf("expected restored.db inside stage.Dir %q, got %v", stage.Dir, statErr)
	}
	stage.Cleanup()
	assertNoImportStagingDirs(t, dbFolder)
	assertNoLegacyPgImportTemps(t)
}

func TestInvalidSQLiteRestoreLimitDoesNotBreakRawDBOrPGStaging(t *testing.T) {
	t.Setenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB", "invalid-value")

	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)

	// 1. Raw SQLite .db staging (non-SQL-dump) must not consult XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB.
	// Create a valid empty SQLite DB file.
	rawDBPath := filepath.Join(t.TempDir(), "raw.db")
	rawDump := filepath.Join(t.TempDir(), "seed.sql")
	if err := os.WriteFile(rawDump, []byte("CREATE TABLE users(id INT); CREATE TABLE inbounds(id INT); CREATE TABLE client_traffics(id INT); CREATE TABLE settings(id INT);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Temporarily unset for seeding raw.db, then set invalid again.
	t.Setenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB", "16")
	if err := database.RestoreSQLite(rawDump, rawDBPath); err != nil {
		t.Fatalf("seed rawDB: %v", err)
	}
	t.Setenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB", "invalid-value")

	rawBytes, err := os.ReadFile(rawDBPath)
	if err != nil {
		t.Fatal(err)
	}
	reqRaw := buildMultipartRequest(t, []struct {
		name     string
		filename string
		payload  []byte
	}{
		{name: "db", filename: "backup.db", payload: rawBytes},
	})
	stageRaw, err := StageImportMultipart(context.Background(), reqRaw, nil, 1024*1024)
	if err != nil {
		t.Fatalf("StageImportMultipart raw .db: %v", err)
	}
	defer stageRaw.Cleanup()

	if err := stageSQLiteUpload(stageRaw.Path, importKindSQLiteDB, filepath.Join(stageRaw.Dir, "restored.db")); err != nil {
		t.Fatalf("stageSQLiteUpload raw .db should succeed even when XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB is invalid, got: %v", err)
	}

	// 2. PG dump staging must also not consult XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB.
	reqPG := buildMultipartRequest(t, []struct {
		name     string
		filename string
		payload  []byte
	}{
		{name: "db", filename: "pg.sql", payload: []byte("-- PostgreSQL database dump\nSELECT 1;\n")},
	})
	stagePG, err := StageImportMultipart(context.Background(), reqPG, nil, 1024*1024)
	if err != nil {
		t.Fatalf("StageImportMultipart pg.sql with invalid SQLITE_RESTORE_MAX_STATEMENT_MIB: %v", err)
	}
	stagePG.Cleanup()
}
