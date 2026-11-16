package database

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func openRawSQLiteForTest(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", dbPath, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func assertNoSQLiteSidecars(t *testing.T, dbPath string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		p := dbPath + suffix
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected %q to be absent, lstat err = %v", p, err)
		}
	}
}

func TestRestoreSQLiteContext_RichRoundTripAndSchemaObjects(t *testing.T) {
	dir := t.TempDir()
	srcDBPath := filepath.Join(dir, "src.db")
	dumpPath := filepath.Join(dir, "dump.sql")
	dstDBPath := filepath.Join(dir, "dst.db")

	srcDB := openRawSQLiteForTest(t, srcDBPath)
	schema := `
CREATE TABLE items (
	id INTEGER PRIMARY KEY,
	title TEXT NOT NULL,
	payload BLOB
);
CREATE TABLE audit (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	note TEXT NOT NULL
);
CREATE INDEX idx_items_title ON items(title);
CREATE VIEW items_view AS SELECT id, title FROM items;
CREATE TRIGGER trg_items_insert AFTER INSERT ON items BEGIN
	INSERT INTO audit(note) VALUES(
		CASE WHEN NEW.id > 1 THEN 'multi;' || NEW.title ELSE 'first;' || NEW.title END
	);
END;
`
	if _, err := srcDB.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	blobVal := []byte{0x00, 0x01, 0xFE, 0xFF, 'O', 'K'}
	if _, err := srcDB.Exec(
		`INSERT INTO items(id, title, payload) VALUES(1, ?, ?), (2, ?, ?)`,
		"سلام 'تست'؛\nخط دوم",
		blobVal,
		"line1;line2",
		[]byte("blob2"),
	); err != nil {
		t.Fatalf("insert rows: %v", err)
	}
	if err := srcDB.Close(); err != nil {
		t.Fatalf("close src: %v", err)
	}

	if err := DumpSQLite(srcDBPath, dumpPath); err != nil {
		t.Fatalf("DumpSQLite: %v", err)
	}
	if err := RestoreSQLiteContext(context.Background(), dumpPath, dstDBPath); err != nil {
		t.Fatalf("RestoreSQLiteContext: %v", err)
	}

	info, err := os.Stat(dstDBPath)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("dst permissions = %o, want 0600", perm)
	}
	for _, sidecar := range []string{dstDBPath + "-wal", dstDBPath + "-shm", dstDBPath + "-journal"} {
		if _, err := os.Lstat(sidecar); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected sidecar %q, err=%v", sidecar, err)
		}
	}
	if err := ValidateSQLiteDB(dstDBPath); err != nil {
		t.Fatalf("ValidateSQLiteDB: %v", err)
	}

	dstDB := openRawSQLiteForTest(t, dstDBPath)
	var title string
	var gotBlob []byte
	if err := dstDB.QueryRow(`SELECT title, payload FROM items WHERE id = 1`).Scan(&title, &gotBlob); err != nil {
		t.Fatalf("query item 1: %v", err)
	}
	if title != "سلام 'تست'؛\nخط دوم" || string(gotBlob) != string(blobVal) {
		t.Fatalf("item 1 mismatch: title=%q blob=%x", title, gotBlob)
	}
	var viewCount int
	if err := dstDB.QueryRow(`SELECT COUNT(*) FROM items_view`).Scan(&viewCount); err != nil || viewCount != 2 {
		t.Fatalf("view query count=%d err=%v", viewCount, err)
	}
	var auditCount int
	if err := dstDB.QueryRow(`SELECT COUNT(*) FROM audit`).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("trigger audit count=%d err=%v", auditCount, err)
	}
}

func TestRestoreSQLiteContext_EdgeCasesAndCleanup(t *testing.T) {
	t.Run("CRLF and UTF8 BOM accepted", func(t *testing.T) {
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "bom_crlf.sql")
		dstPath := filepath.Join(dir, "dst.db")
		content := "\xef\xbb\xbfBEGIN TRANSACTION;\r\nCREATE TABLE t(id INT PRIMARY KEY, v TEXT);\r\nINSERT INTO t VALUES(1, 'ok;1');\r\nCOMMIT;\r\n"
		if err := os.WriteFile(dumpPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath); err != nil {
			t.Fatalf("RestoreSQLiteContext failed: %v", err)
		}
		db := openRawSQLiteForTest(t, dstPath)
		var v string
		if err := db.QueryRow(`SELECT v FROM t WHERE id = 1`).Scan(&v); err != nil || v != "ok;1" {
			t.Fatalf("got (%q, %v), want ok;1", v, err)
		}
	})

	t.Run("existing destination is untouched", func(t *testing.T) {
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "d.sql")
		dstPath := filepath.Join(dir, "existing.db")
		if err := os.WriteFile(dumpPath, []byte("CREATE TABLE t(x INT);"), 0o600); err != nil {
			t.Fatal(err)
		}
		sentinel := []byte("ORIGINAL-DB-CONTENT")
		if err := os.WriteFile(dstPath, sentinel, 0o600); err != nil {
			t.Fatal(err)
		}
		err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
		if err == nil || !strings.Contains(err.Error(), "destination already exists") {
			t.Fatalf("expected destination already exists error, got %v", err)
		}
		got, readErr := os.ReadFile(dstPath)
		if readErr != nil || string(got) != string(sentinel) {
			t.Fatalf("existing file was modified! content=%q err=%v", got, readErr)
		}
	})

	t.Run("invalid SQL cleans up dst and sidecars", func(t *testing.T) {
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "bad.sql")
		dstPath := filepath.Join(dir, "bad.db")
		if err := os.WriteFile(dumpPath, []byte("CREATE TABLE t(id INT);\nINSERT INTO nonexistent VALUES(1);"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
		if err == nil {
			t.Fatal("expected error for invalid SQL")
		}
		assertNoSQLiteSidecars(t, dstPath)
	})

	t.Run("incomplete SQL cleans up", func(t *testing.T) {
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "incomplete.sql")
		dstPath := filepath.Join(dir, "incomplete.db")
		if err := os.WriteFile(dumpPath, []byte("CREATE TABLE t(id INT);\nINSERT INTO t VALUES(1)"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
		if !errors.Is(err, ErrIncompleteSQL) {
			t.Fatalf("got err = %v, want ErrIncompleteSQL", err)
		}
		assertNoSQLiteSidecars(t, dstPath)
	})

	t.Run("empty or comment-only dump rejected", func(t *testing.T) {
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "empty.sql")
		dstPath := filepath.Join(dir, "empty.db")
		if err := os.WriteFile(dumpPath, []byte("-- comment only\n/* block */\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
		if !errors.Is(err, ErrEmptySQLiteDump) {
			t.Fatalf("got err = %v, want ErrEmptySQLiteDump", err)
		}
		assertNoSQLiteSidecars(t, dstPath)
	})

	t.Run("open transaction rejected", func(t *testing.T) {
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "opentx.sql")
		dstPath := filepath.Join(dir, "opentx.db")
		if err := os.WriteFile(dumpPath, []byte("BEGIN TRANSACTION;\nCREATE TABLE t(id INT);"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
		if err == nil || !strings.Contains(err.Error(), "open transaction") {
			t.Fatalf("expected open transaction error, got %v", err)
		}
		assertNoSQLiteSidecars(t, dstPath)
	})

	t.Run("ATTACH and VACUUM INTO blocked", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			sql  string
		}{
			{
				name: "ATTACH",
				sql:  fmt.Sprintf("ATTACH DATABASE '%s' AS ext;", filepath.Join(t.TempDir(), "ext.db")),
			},
			{
				name: "VACUUM INTO",
				sql:  fmt.Sprintf("CREATE TABLE t(x INT); VACUUM INTO '%s';", filepath.Join(t.TempDir(), "out.db")),
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				dumpPath := filepath.Join(dir, "sec.sql")
				dstPath := filepath.Join(dir, "sec.db")
				if err := os.WriteFile(dumpPath, []byte(tc.sql), 0o600); err != nil {
					t.Fatal(err)
				}
				err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
				if err == nil {
					t.Fatalf("expected %s to be blocked", tc.name)
				}
				assertNoSQLiteSidecars(t, dstPath)
			})
		}
	})

	t.Run("statement exceeding XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB rejected", func(t *testing.T) {
		t.Setenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB", "1")
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "huge_stmt.sql")
		dstPath := filepath.Join(dir, "huge.db")

		f, err := os.Create(dumpPath)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString("CREATE TABLE t(v TEXT);\nINSERT INTO t VALUES('")
		chunk := strings.Repeat("Z", 64*1024)
		for i := 0; i < 18; i++ { // > 1 MiB single statement
			_, _ = f.WriteString(chunk)
		}
		_, _ = f.WriteString("');\n")
		_ = f.Close()

		err = RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
		if !errors.Is(err, ErrStatementTooLarge) {
			t.Fatalf("got err = %v, want ErrStatementTooLarge", err)
		}
		assertNoSQLiteSidecars(t, dstPath)
	})

	t.Run("canceled context stops restore and cleans up", func(t *testing.T) {
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "cancel.sql")
		dstPath := filepath.Join(dir, "cancel.db")
		if err := os.WriteFile(dumpPath, []byte("CREATE TABLE t(id INT);\nINSERT INTO t VALUES(1);\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := RestoreSQLiteContext(ctx, dumpPath, dstPath)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got err = %v, want context.Canceled", err)
		}
		assertNoSQLiteSidecars(t, dstPath)
	})
}

func TestRestoreSQLiteContext_BoundedMemory64MiBStream(t *testing.T) {
	t.Setenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB", "1")

	dir := t.TempDir()
	dumpPath := filepath.Join(dir, "large_64mib.sql")
	dstPath := filepath.Join(dir, "large_64mib.db")

	f, err := os.Create(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	bw := bufio.NewWriterSize(f, 256*1024)
	_, _ = bw.WriteString("PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\nCREATE TABLE big_rows(id INTEGER PRIMARY KEY, body TEXT);\n")

	// ~66 MiB dump composed of ~68,000 small 1 KiB statements.
	payload := strings.Repeat("A", 980)
	const totalRows = 68000
	for i := 1; i <= totalRows; i++ {
		if _, wErr := fmt.Fprintf(bw, "INSERT INTO big_rows(id,body) VALUES(%d,'%s');\n", i, payload); wErr != nil {
			t.Fatal(wErr)
		}
	}
	_, _ = bw.WriteString("COMMIT;\n")
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() < 64*1024*1024 {
		t.Fatalf("expected dump >= 64 MiB, got %d bytes", st.Size())
	}

	runtime.GC()
	var baseMem runtime.MemStats
	runtime.ReadMemStats(&baseMem)

	var peakHeapAlloc atomic.Uint64
	peakHeapAlloc.Store(baseMem.HeapAlloc)

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				for {
					cur := peakHeapAlloc.Load()
					if m.HeapAlloc <= cur || peakHeapAlloc.CompareAndSwap(cur, m.HeapAlloc) {
						break
					}
				}
			}
		}
	}()

	err = RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
	close(done)
	if err != nil {
		t.Fatalf("RestoreSQLiteContext 64MiB failed: %v", err)
	}

	peakDelta := int64(peakHeapAlloc.Load()) - int64(baseMem.HeapAlloc)
	const maxAllowedDelta = 32 * 1024 * 1024 // < half of the 66 MiB dump size
	t.Logf("64MiB+ dump size=%d MiB, baselineHeap=%d MiB, peakHeap=%d MiB, delta=%d MiB",
		st.Size()/(1024*1024),
		baseMem.HeapAlloc/(1024*1024),
		peakHeapAlloc.Load()/(1024*1024),
		peakDelta/(1024*1024),
	)
	if peakDelta > maxAllowedDelta {
		t.Fatalf("peak heap delta %d bytes exceeded bounded streaming budget %d bytes", peakDelta, maxAllowedDelta)
	}

	db := openRawSQLiteForTest(t, dstPath)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM big_rows`).Scan(&count); err != nil {
		t.Fatalf("count big_rows: %v", err)
	}
	if count != totalRows {
		t.Fatalf("restored row count = %d, want %d", count, totalRows)
	}
}

func TestRestoreSQLiteContext_PreExistingArtifactsAndOwnership(t *testing.T) {
	validDump := "BEGIN TRANSACTION;\nCREATE TABLE t(id INTEGER PRIMARY KEY, v TEXT);\nINSERT INTO t VALUES(1, 'ok');\nCOMMIT;\n"

	t.Run("pre-existing dstPath preserves content permission and creates no sidecars", func(t *testing.T) {
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "d.sql")
		dstPath := filepath.Join(dir, "existing.db")
		if err := os.WriteFile(dumpPath, []byte(validDump), 0o600); err != nil {
			t.Fatal(err)
		}
		sentinel := []byte("SENTINEL-MAIN-DB")
		if err := os.WriteFile(dstPath, sentinel, 0o640); err != nil {
			t.Fatal(err)
		}
		err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
		if err == nil || !strings.Contains(err.Error(), "destination") {
			t.Fatalf("expected destination error, got %v", err)
		}
		got, readErr := os.ReadFile(dstPath)
		if readErr != nil || string(got) != string(sentinel) {
			t.Fatalf("dstPath modified: got=%q err=%v", got, readErr)
		}
		info, statErr := os.Lstat(dstPath)
		if statErr != nil || info.Mode().Perm() != 0o640 {
			t.Fatalf("dstPath perm changed: mode=%v err=%v", info.Mode().Perm(), statErr)
		}
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if _, err := os.Lstat(dstPath + suffix); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected sidecar %s created: %v", suffix, err)
			}
		}
	})

	for _, tc := range []struct {
		name     string
		suffix   string
		wantKind string
	}{
		{name: "pre-existing WAL", suffix: "-wal", wantKind: "WAL sidecar"},
		{name: "pre-existing SHM", suffix: "-shm", wantKind: "SHM sidecar"},
		{name: "pre-existing journal", suffix: "-journal", wantKind: "journal sidecar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dumpPath := filepath.Join(dir, "d.sql")
			dstPath := filepath.Join(dir, "target.db")
			sidecarPath := dstPath + tc.suffix
			if err := os.WriteFile(dumpPath, []byte(validDump), 0o600); err != nil {
				t.Fatal(err)
			}
			sentinel := []byte("SENTINEL-" + tc.suffix)
			if err := os.WriteFile(sidecarPath, sentinel, 0o640); err != nil {
				t.Fatal(err)
			}

			err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
			if err == nil || !strings.Contains(err.Error(), tc.wantKind) {
				t.Fatalf("expected %q in error, got %v", tc.wantKind, err)
			}
			if _, statErr := os.Lstat(dstPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("dstPath must not be created when %s exists, lstat err=%v", tc.suffix, statErr)
			}
			got, readErr := os.ReadFile(sidecarPath)
			if readErr != nil || string(got) != string(sentinel) {
				t.Fatalf("%s modified: got=%q err=%v", sidecarPath, got, readErr)
			}
			info, statErr := os.Lstat(sidecarPath)
			if statErr != nil || info.Mode().Perm() != 0o640 {
				t.Fatalf("%s perm changed: mode=%v err=%v", sidecarPath, info.Mode().Perm(), statErr)
			}
		})
	}

	t.Run("dangling symlink on sidecar is detected and preserved", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX symlink test")
		}
		for _, tc := range []struct {
			suffix   string
			wantKind string
		}{
			{suffix: "-wal", wantKind: "WAL sidecar"},
			{suffix: "-shm", wantKind: "SHM sidecar"},
			{suffix: "-journal", wantKind: "journal sidecar"},
		} {
			t.Run(tc.suffix, func(t *testing.T) {
				dir := t.TempDir()
				dumpPath := filepath.Join(dir, "d.sql")
				dstPath := filepath.Join(dir, "symlink.db")
				if err := os.WriteFile(dumpPath, []byte(validDump), 0o600); err != nil {
					t.Fatal(err)
				}
				danglingTarget := filepath.Join(dir, "does-not-exist")
				sidecarPath := dstPath + tc.suffix
				if err := os.Symlink(danglingTarget, sidecarPath); err != nil {
					t.Fatal(err)
				}

				err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath)
				if err == nil || !strings.Contains(err.Error(), tc.wantKind) {
					t.Fatalf("expected %q in error, got %v", tc.wantKind, err)
				}
				if _, statErr := os.Lstat(dstPath); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("dstPath must not be created, lstat err=%v", statErr)
				}
				info, lstatErr := os.Lstat(sidecarPath)
				if lstatErr != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("dangling symlink %s must remain intact, lstat=(%v, %v)", sidecarPath, info, lstatErr)
				}
				if _, targetErr := os.Lstat(danglingTarget); !errors.Is(targetErr, os.ErrNotExist) {
					t.Fatalf("dangling symlink target must not be created, err=%v", targetErr)
				}
			})
		}
	})

	t.Run("mid-stream cancellation cleans new artifacts and keeps unrelated files", func(t *testing.T) {
		dir := t.TempDir()
		dumpPath := filepath.Join(dir, "multi.sql")
		dstPath := filepath.Join(dir, "canceled.db")
		unrelated := filepath.Join(dir, "unrelated.db")
		if err := os.WriteFile(unrelated, []byte("KEEP-ME"), 0o600); err != nil {
			t.Fatal(err)
		}

		var sb strings.Builder
		sb.WriteString("CREATE TABLE t(id INTEGER PRIMARY KEY, v TEXT);\n")
		for i := 1; i <= 5000; i++ {
			fmt.Fprintf(&sb, "INSERT INTO t VALUES(%d, 'row-%d');\n", i, i)
		}
		if err := os.WriteFile(dumpPath, []byte(sb.String()), 0o600); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(2 * time.Millisecond)
			cancel()
		}()
		err := RestoreSQLiteContext(ctx, dumpPath, dstPath)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		assertNoSQLiteSidecars(t, dstPath)
		got, readErr := os.ReadFile(unrelated)
		if readErr != nil || string(got) != "KEEP-ME" {
			t.Fatalf("unrelated file touched: got=%q err=%v", got, readErr)
		}
	})
}
