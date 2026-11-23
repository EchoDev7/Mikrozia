package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/xray"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestWriteSQLiteDumpRoundTripWithQuotesUnicodeAndBinary(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	dumpPath := filepath.Join(dir, "migration.dump")
	dstPath := filepath.Join(dir, "rebuilt.db")

	src, err := gorm.Open(sqlite.Open(srcPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	for _, m := range migrationModels() {
		if err := src.AutoMigrate(m); err != nil {
			closeGorm(src)
			t.Fatalf("automigrate %T: %v", m, err)
		}
	}
	sqlDB, err := src.DB()
	if err != nil {
		closeGorm(src)
		t.Fatalf("src.DB: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS blob_items (id INTEGER PRIMARY KEY, payload BLOB)`); err != nil {
		closeGorm(src)
		t.Fatalf("create blob_items: %v", err)
	}
	rawBlob := []byte{0x00, 0x01, 0xfe, 0xff, 0x80, 0x99}
	if _, err := sqlDB.Exec(`INSERT INTO blob_items (payload) VALUES (?)`, rawBlob); err != nil {
		closeGorm(src)
		t.Fatalf("insert blob_items: %v", err)
	}

	unicodeRemark := "O'Reilly \"Mikrozia\" سلام دنیا 🌍"
	if err := src.Create(&model.Setting{Key: "quote'key", Value: unicodeRemark}).Error; err != nil {
		closeGorm(src)
		t.Fatalf("seed setting: %v", err)
	}
	if err := src.Create(&model.User{Username: "admin", Password: "hashed-pw"}).Error; err != nil {
		closeGorm(src)
		t.Fatalf("seed user: %v", err)
	}
	if err := src.Create(&model.Inbound{UserId: 1, Remark: unicodeRemark, Port: 8443, Protocol: "vless", Tag: "inbound-8443"}).Error; err != nil {
		closeGorm(src)
		t.Fatalf("seed inbound: %v", err)
	}
	if err := src.Create(&xray.ClientTraffic{InboundId: 1, Email: "user'1@example.com", Enable: true, Up: 1234, Down: 5678}).Error; err != nil {
		closeGorm(src)
		t.Fatalf("seed traffic: %v", err)
	}
	closeGorm(src)

	if err := DumpSQLiteContext(context.Background(), srcPath, dumpPath); err != nil {
		t.Fatalf("DumpSQLiteContext: %v", err)
	}
	if err := RestoreSQLite(dumpPath, dstPath); err != nil {
		t.Fatalf("RestoreSQLite: %v", err)
	}
	if err := ValidateSQLiteDB(dstPath); err != nil {
		t.Fatalf("ValidateSQLiteDB(rebuilt): %v", err)
	}

	rebuilt, err := gorm.Open(sqlite.Open(dstPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open rebuilt: %v", err)
	}
	defer closeGorm(rebuilt)

	var gotSetting model.Setting
	if err := rebuilt.Where("key = ?", "quote'key").First(&gotSetting).Error; err != nil {
		t.Fatalf("read rebuilt setting: %v", err)
	}
	if gotSetting.Value != unicodeRemark {
		t.Fatalf("setting value = %q, want %q", gotSetting.Value, unicodeRemark)
	}

	var gotInbound model.Inbound
	if err := rebuilt.Where("port = ?", 8443).First(&gotInbound).Error; err != nil {
		t.Fatalf("read rebuilt inbound: %v", err)
	}
	if gotInbound.Remark != unicodeRemark {
		t.Fatalf("inbound remark = %q, want %q", gotInbound.Remark, unicodeRemark)
	}

	var gotTraffic xray.ClientTraffic
	if err := rebuilt.Where("email = ?", "user'1@example.com").First(&gotTraffic).Error; err != nil {
		t.Fatalf("read rebuilt traffic: %v", err)
	}
	if gotTraffic.Up != 1234 || gotTraffic.Down != 5678 {
		t.Fatalf("traffic mismatch: %+v", gotTraffic)
	}

	rebuiltSQL, err := rebuilt.DB()
	if err != nil {
		t.Fatalf("rebuilt.DB: %v", err)
	}
	var gotBlob []byte
	if err := rebuiltSQL.QueryRow(`SELECT payload FROM blob_items WHERE id = 1`).Scan(&gotBlob); err != nil {
		t.Fatalf("read rebuilt blob: %v", err)
	}
	if !bytes.Equal(gotBlob, rawBlob) {
		t.Fatalf("rebuilt blob = %x, want %x", gotBlob, rawBlob)
	}
}

type failAfterNBytesWriter struct {
	limit   int
	written int
	err     error
}

func (w *failAfterNBytesWriter) Write(p []byte) (int, error) {
	if w.written >= w.limit {
		return 0, w.err
	}
	if w.written+len(p) > w.limit {
		n := w.limit - w.written
		w.written += n
		return n, w.err
	}
	w.written += len(p)
	return len(p), nil
}

func TestWriteSQLiteDumpWriterErrorStopsAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	src, err := gorm.Open(sqlite.Open(srcPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	if err := src.AutoMigrate(&model.Setting{}); err != nil {
		closeGorm(src)
		t.Fatalf("automigrate: %v", err)
	}
	for i := 0; i < 200; i++ {
		if err := src.Create(&model.Setting{Key: fmt.Sprintf("k-%d", i), Value: strings.Repeat("v", 512)}).Error; err != nil {
			closeGorm(src)
			t.Fatalf("seed: %v", err)
		}
	}
	closeGorm(src)

	diskErr := errors.New("simulated disk full")
	w := &failAfterNBytesWriter{limit: 128, err: diskErr}
	err = WriteSQLiteDump(context.Background(), srcPath, w)
	if !errors.Is(err, diskErr) {
		t.Fatalf("WriteSQLiteDump error = %v, want %v", err, diskErr)
	}
}

func TestDumpSQLiteContextCancelsMidDumpAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "large.db")
	src, err := gorm.Open(sqlite.Open(srcPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	if err := src.AutoMigrate(&model.Setting{}); err != nil {
		closeGorm(src)
		t.Fatalf("automigrate: %v", err)
	}
	sqlDB, err := src.DB()
	if err != nil {
		closeGorm(src)
		t.Fatalf("src.DB: %v", err)
	}
	tx, err := sqlDB.Begin()
	if err != nil {
		closeGorm(src)
		t.Fatalf("begin: %v", err)
	}
	if err := func() error {
		stmt, err := tx.Prepare(`INSERT INTO settings ("key", "value") VALUES (?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare: %w", err)
		}
		defer stmt.Close()
		val := strings.Repeat("X", 64*1024)
		for i := 0; i < 256; i++ {
			if _, err := stmt.Exec(fmt.Sprintf("k-%d", i), val); err != nil {
				return fmt.Errorf("insert: %w", err)
			}
		}
		return nil
	}(); err != nil {
		_ = tx.Rollback()
		closeGorm(src)
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		closeGorm(src)
		t.Fatalf("commit: %v", err)
	}
	closeGorm(src)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dumpPath := filepath.Join(dir, "canceled.dump")
	errCh := make(chan error, 1)
	go func() {
		errCh <- DumpSQLiteContext(ctx, srcPath, dumpPath)
	}()

	var sawProgress bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("DumpSQLiteContext finished (%v) before mid-dump cancel triggered", err)
		default:
		}
		info, statErr := os.Stat(dumpPath)
		if statErr == nil && info.Size() > 0 {
			sawProgress = true
			cancel()
			break
		}
		time.Sleep(100 * time.Microsecond)
	}
	if !sawProgress {
		cancel()
		<-errCh
		t.Fatal("timed out waiting for dump output progress")
	}

	err = <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DumpSQLiteContext error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(dumpPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected incomplete dump %q removed, got %v", dumpPath, statErr)
	}

	// Subsequent dump must succeed.
	nextPath := filepath.Join(dir, "ok.dump")
	if err := DumpSQLiteContext(context.Background(), srcPath, nextPath); err != nil {
		t.Fatalf("subsequent DumpSQLiteContext: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(nextPath)
		if err != nil {
			t.Fatalf("Stat(%s): %v", nextPath, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("dump file perm = %04o, want 0600", perm)
		}
	}
}

func TestExportPostgresToSQLiteContextLivePG18(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("XUI_TEST_PG_DSN"))
	if dsn == "" {
		t.Skip("set XUI_TEST_PG_DSN to run live PostgreSQL 18 migration export test")
	}

	src, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open postgres: %v", RedactPostgresError(err, dsn))
	}
	defer closeGorm(src)

	for _, m := range migrationModels() {
		if err := src.AutoMigrate(m); err != nil {
			t.Fatalf("AutoMigrate %T: %v", m, err)
		}
	}
	if err := truncatePostgresTables(src, migrationModels()); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	unicodeRemark := "PG18 'quote' سلام 🚀"
	if err := src.Create(&model.User{Username: "pg-admin", Password: "pw"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := src.Create(&model.Setting{Key: "pg_test_key", Value: unicodeRemark}).Error; err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	if err := src.Create(&model.Inbound{UserId: 1, Remark: unicodeRemark, Port: 20443, Protocol: "vless", Tag: "pg-in-20443"}).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	if err := src.Create(&xray.ClientTraffic{InboundId: 1, Email: "pg@example.com", Enable: true, Up: 777, Down: 888}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	outDir := t.TempDir()
	dstPath := filepath.Join(outDir, "exported.db")
	if err := ExportPostgresToSQLiteContext(context.Background(), dsn, dstPath); err != nil {
		t.Fatalf("ExportPostgresToSQLiteContext: %v", err)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(dstPath)
		if err != nil {
			t.Fatalf("Stat(%s): %v", dstPath, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("exported SQLite perm = %04o, want 0600", perm)
		}
	}
	if _, err := os.Stat(dstPath + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no external -wal sidecar, got err=%v", err)
	}
	if _, err := os.Stat(dstPath + "-shm"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no external -shm sidecar, got err=%v", err)
	}
	if err := ValidateSQLiteDB(dstPath); err != nil {
		t.Fatalf("ValidateSQLiteDB: %v", err)
	}

	// Verify row counts and values in exported SQLite.
	dst, err := gorm.Open(sqlite.Open(dstPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open exported sqlite: %v", err)
	}
	defer closeGorm(dst)

	var gotSetting model.Setting
	if err := dst.Where("key = ?", "pg_test_key").First(&gotSetting).Error; err != nil {
		t.Fatalf("read exported setting: %v", err)
	}
	if gotSetting.Value != unicodeRemark {
		t.Fatalf("exported setting = %q, want %q", gotSetting.Value, unicodeRemark)
	}
	var gotTraffic xray.ClientTraffic
	if err := dst.Where("email = ?", "pg@example.com").First(&gotTraffic).Error; err != nil {
		t.Fatalf("read exported traffic: %v", err)
	}
	if gotTraffic.Up != 777 || gotTraffic.Down != 888 {
		t.Fatalf("exported traffic mismatch: %+v", gotTraffic)
	}

	// Refuses existing destination without overwriting.
	existingPath := filepath.Join(outDir, "existing.db")
	if err := os.WriteFile(existingPath, []byte("keep-me"), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}
	if err := ExportPostgresToSQLiteContext(context.Background(), dsn, existingPath); err == nil {
		t.Fatal("expected ExportPostgresToSQLiteContext to refuse existing destination")
	}
	raw, _ := os.ReadFile(existingPath)
	if string(raw) != "keep-me" {
		t.Fatalf("existing destination modified: %q", string(raw))
	}

	// Deterministic mid-transfer cancel and deadline exceeded while settings is locked.
	srcSQL, err := src.DB()
	if err != nil {
		t.Fatalf("src.DB: %v", err)
	}
	assertBlockedExportContextError(t, srcSQL, dsn, filepath.Join(outDir, "canceled-mid.db"), true)
	assertBlockedExportContextError(t, srcSQL, dsn, filepath.Join(outDir, "deadline-mid.db"), false)

	// Verify PostgreSQL connection remains usable and a subsequent export succeeds cleanly.
	if err := srcSQL.PingContext(context.Background()); err != nil {
		t.Fatalf("postgres ping after cancel: %v", err)
	}
	afterCancelPath := filepath.Join(outDir, "after-cancel.db")
	if err := ExportPostgresToSQLiteContext(context.Background(), dsn, afterCancelPath); err != nil {
		t.Fatalf("subsequent ExportPostgresToSQLiteContext failed: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(afterCancelPath)
		if err != nil {
			t.Fatalf("Stat(%s): %v", afterCancelPath, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("subsequent SQLite perm = %04o, want 0600", perm)
		}
		if info.Size() == 0 {
			t.Fatal("subsequent SQLite file is empty")
		}
	}
	if _, err := os.Stat(afterCancelPath + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no -wal after subsequent export, got %v", err)
	}
	if _, err := os.Stat(afterCancelPath + "-shm"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no -shm after subsequent export, got %v", err)
	}
	if err := ValidateSQLiteDB(afterCancelPath); err != nil {
		t.Fatalf("ValidateSQLiteDB(after-cancel): %v", err)
	}

	const secret = "very-secret-pg-pass-123"
	badDSN := "postgres://baduser:" + secret + "@127.0.0.1:1/nodb?sslmode=disable&connect_timeout=1"
	err = ExportPostgresToSQLiteContext(context.Background(), badDSN, filepath.Join(outDir, "fail.db"))
	if err == nil {
		t.Fatal("expected error for unreachable postgres")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), badDSN) {
		t.Fatalf("error leaked DSN password: %q", err.Error())
	}
}

// stagedDeadlineContext reports DeadlineExceeded only when the test expires it,
// so the deadline cannot elapse before the export reaches the locked query.
type stagedDeadlineContext struct {
	done      chan struct{}
	mu        sync.Mutex
	err       error
	expireOne sync.Once
}

func newStagedDeadlineContext() *stagedDeadlineContext {
	return &stagedDeadlineContext{done: make(chan struct{})}
}

func (c *stagedDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *stagedDeadlineContext) Done() <-chan struct{} { return c.done }

func (c *stagedDeadlineContext) Value(any) any { return nil }

func (c *stagedDeadlineContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *stagedDeadlineContext) expire() {
	c.expireOne.Do(func() {
		c.mu.Lock()
		c.err = context.DeadlineExceeded
		close(c.done)
		c.mu.Unlock()
	})
}

func assertBlockedExportContextError(t *testing.T, srcSQL *sql.DB, dsn, dstPath string, useCancel bool) {
	t.Helper()
	lockTx, err := srcSQL.Begin()
	if err != nil {
		t.Fatalf("begin lock tx: %v", err)
	}
	txClosed := false
	defer func() {
		if !txClosed {
			_ = lockTx.Rollback()
		}
	}()
	if _, err := lockTx.Exec(`LOCK TABLE settings IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock settings table: %v", err)
	}

	var (
		ctx    context.Context
		expire func()
		want   error
	)
	if useCancel {
		cancelCtx, cancel := context.WithCancel(context.Background())
		ctx, expire = cancelCtx, cancel
		want = context.Canceled
	} else {
		staged := newStagedDeadlineContext()
		ctx, expire = staged, staged.expire
		want = context.DeadlineExceeded
	}
	defer expire()

	done := make(chan error, 1)
	go func() {
		done <- ExportPostgresToSQLiteContext(ctx, dsn, dstPath)
	}()

	// Wait deterministically until PostgreSQL reports a blocked lock request on settings.
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var waiting int
		err := srcSQL.QueryRow(`SELECT COUNT(*) FROM pg_locks WHERE relation = 'settings'::regclass AND NOT granted`).Scan(&waiting)
		if err == nil && waiting > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatalf("export did not enter blocked settings query within timeout")
	}
	if _, err := os.Stat(dstPath); err != nil {
		t.Fatalf("expected destination file %s created before blocking query: %v", dstPath, err)
	}

	// Cancel only after the blocked query is proven, so the outcome cannot race
	// ahead of the export reaching it.
	expire()

	select {
	case exportErr := <-done:
		if !errors.Is(exportErr, want) {
			t.Fatalf("blocked export err = %v, want errors.Is(..., %v)", exportErr, want)
		}
		if dsn != "" && strings.Contains(exportErr.Error(), dsn) {
			t.Fatalf("error leaked raw DSN: %q", exportErr.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("blocked export did not stop quickly on %v", want)
	}

	if err := lockTx.Rollback(); err != nil {
		t.Fatalf("rollback lock tx: %v", err)
	}
	txClosed = true

	for _, p := range []string{dstPath, dstPath + "-wal", dstPath + "-shm"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected %s removed after %v, got %v", p, want, err)
		}
	}
}

func TestSanitizePostgresContextOrRedactError(t *testing.T) {
	const secret = "s3cr3t-pg-pass-2b3"
	dsn := "postgres://mikrozia:" + secret + "@127.0.0.1:5432/mikrozia?sslmode=disable"

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	gotCancel := sanitizePostgresContextOrRedactError(cctx, fmt.Errorf("driver failed: %s: %w", secret, context.Canceled), dsn)
	if !errors.Is(gotCancel, context.Canceled) {
		t.Fatalf("gotCancel = %v, want context.Canceled", gotCancel)
	}
	if strings.Contains(gotCancel.Error(), secret) {
		t.Fatalf("gotCancel leaked secret: %q", gotCancel.Error())
	}

	dctx, dcancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer dcancel()
	gotDeadline := sanitizePostgresContextOrRedactError(dctx, fmt.Errorf("driver timeout: %s: %w", secret, context.DeadlineExceeded), dsn)
	if !errors.Is(gotDeadline, context.DeadlineExceeded) {
		t.Fatalf("gotDeadline = %v, want context.DeadlineExceeded", gotDeadline)
	}
	if strings.Contains(gotDeadline.Error(), secret) {
		t.Fatalf("gotDeadline leaked secret: %q", gotDeadline.Error())
	}

	gotPG := sanitizePostgresContextOrRedactError(context.Background(), fmt.Errorf("FATAL: password authentication failed for %s", dsn), dsn)
	if gotPG == nil || strings.Contains(gotPG.Error(), secret) {
		t.Fatalf("gotPG leaked secret or was nil: %v", gotPG)
	}
}
