package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestCopyAllModelsIntoSQLite exercises the same AutoMigrate + copyTable
// machinery that ExportPostgresToSQLite relies on, but with a SQLite source so
// it needs no external database. The Postgres source path uses identical gorm
// reads (see MigrateData), so this validates the destination-side copy.
func TestCopyAllModelsIntoSQLite(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	dstPath := filepath.Join(dir, "dst.db")

	src, err := gorm.Open(sqlite.Open(srcPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer closeGorm(src)
	for _, m := range migrationModels() {
		if err := src.AutoMigrate(m); err != nil {
			t.Fatalf("automigrate src %T: %v", m, err)
		}
	}

	// Seed a few rows across parent/child tables and a composite-PK table.
	if err := src.Create(&model.User{Username: "admin", Password: "x"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := src.Create(&model.Inbound{UserId: 1, Remark: "in", Port: 443, Protocol: "vless", Tag: "inbound-443"}).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	if err := src.Create(&xray.ClientTraffic{InboundId: 1, Email: "a@b.c", Enable: true, Up: 10, Down: 20}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	dst, err := gorm.Open(sqlite.Open(dstPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer closeGorm(dst)
	if err := copyAllModels(src, dst); err != nil {
		t.Fatalf("copyAllModels: %v", err)
	}

	for _, tc := range []struct {
		model any
		want  int64
	}{
		{&model.User{}, 1},
		{&model.Inbound{}, 1},
		{&xray.ClientTraffic{}, 1},
	} {
		var got int64
		if err := dst.Model(tc.model).Count(&got).Error; err != nil {
			t.Fatalf("count %T: %v", tc.model, err)
		}
		if got != tc.want {
			t.Errorf("%T: got %d rows, want %d", tc.model, got, tc.want)
		}
	}

	// Spot-check a copied value survived the round-trip.
	var ct xray.ClientTraffic
	if err := dst.Where("email = ?", "a@b.c").First(&ct).Error; err != nil {
		t.Fatalf("read back traffic: %v", err)
	}
	if ct.Up != 10 || ct.Down != 20 || !ct.Enable {
		t.Errorf("traffic mismatch: %+v", ct)
	}
}

// TestDumpAndRestoreSQLiteRoundTrip dumps a seeded SQLite db to .dump text and
// rebuilds it, asserting the row survives.
func TestDumpAndRestoreSQLiteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.db")
	dumpPath := filepath.Join(dir, "out.dump")
	dstPath := filepath.Join(dir, "rebuilt.db")

	src, err := gorm.Open(sqlite.Open(srcPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	if err := src.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := src.Create(&model.Setting{Key: "secret", Value: "o'brien \"quote\""}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if sqlDB, _ := src.DB(); sqlDB != nil {
		sqlDB.Close()
	}

	if err := DumpSQLite(srcPath, dumpPath); err != nil {
		t.Fatalf("DumpSQLite: %v", err)
	}
	if fi, err := os.Stat(dumpPath); err != nil || fi.Size() == 0 {
		t.Fatalf("dump missing/empty: %v", err)
	}
	if err := RestoreSQLite(dumpPath, dstPath); err != nil {
		t.Fatalf("RestoreSQLite: %v", err)
	}

	dst, err := gorm.Open(sqlite.Open(dstPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer closeGorm(dst)
	var s model.Setting
	if err := dst.Where("key = ?", "secret").First(&s).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if s.Value != "o'brien \"quote\"" {
		t.Errorf("value mismatch after round-trip: %q", s.Value)
	}
}

// TestDumpSQLiteUsesConsistentSnapshotDuringWrites proves a logical dump of a
// live WAL database cannot mix values from different committed transactions.
func TestDumpSQLiteUsesConsistentSnapshotDuringWrites(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "live.db")
	writer, err := sql.Open("sqlite3", srcPath+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writer.Close()
	writer.SetMaxOpenConns(1)

	payload := strings.Repeat("x", 8<<20)
	for _, statement := range []string{
		`CREATE TABLE pair_a (v INTEGER NOT NULL)`,
		`CREATE TABLE payload (body TEXT NOT NULL)`,
		`CREATE TABLE pair_b (v INTEGER NOT NULL)`,
		`INSERT INTO pair_a VALUES (0)`,
		`INSERT INTO pair_b VALUES (0)`,
	} {
		if _, err := writer.Exec(statement); err != nil {
			t.Fatalf("seed %q: %v", statement, err)
		}
	}
	if _, err := writer.Exec(`INSERT INTO payload VALUES (?)`, payload); err != nil {
		t.Fatalf("seed payload: %v", err)
	}

	stop := make(chan struct{})
	writerErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				writerErr <- nil
				return
			default:
			}
			tx, err := writer.Begin()
			if err != nil {
				writerErr <- err
				return
			}
			if _, err := tx.Exec(`UPDATE pair_a SET v = v + 1`); err != nil {
				_ = tx.Rollback()
				writerErr <- err
				return
			}
			if _, err := tx.Exec(`UPDATE pair_b SET v = v + 1`); err != nil {
				_ = tx.Rollback()
				writerErr <- err
				return
			}
			if err := tx.Commit(); err != nil {
				writerErr <- err
				return
			}
		}
	}()

	defer func() {
		close(stop)
		if err := <-writerErr; err != nil {
			t.Errorf("writer failed: %v", err)
		}
	}()

	for i := 0; i < 10; i++ {
		dumpPath := filepath.Join(dir, fmt.Sprintf("out-%d.dump", i))
		dstPath := filepath.Join(dir, fmt.Sprintf("rebuilt-%d.db", i))
		if err := DumpSQLite(srcPath, dumpPath); err != nil {
			t.Fatalf("DumpSQLite iteration %d: %v", i, err)
		}
		if err := RestoreSQLite(dumpPath, dstPath); err != nil {
			t.Fatalf("RestoreSQLite iteration %d: %v", i, err)
		}

		dst, err := sql.Open("sqlite3", dstPath)
		if err != nil {
			t.Fatalf("open restored %d: %v", i, err)
		}
		var a, b int64
		if err := dst.QueryRow(`SELECT v FROM pair_a`).Scan(&a); err != nil {
			_ = dst.Close()
			t.Fatalf("read pair_a %d: %v", i, err)
		}
		if err := dst.QueryRow(`SELECT v FROM pair_b`).Scan(&b); err != nil {
			_ = dst.Close()
			t.Fatalf("read pair_b %d: %v", i, err)
		}
		_ = dst.Close()
		if a != b {
			t.Fatalf("dump %d mixed committed states: pair_a=%d pair_b=%d", i, a, b)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// closeGorm closes the underlying *sql.DB so Windows can delete the temp file.
func closeGorm(db *gorm.DB) {
	if db == nil {
		return
	}
	if s, err := db.DB(); err == nil {
		s.Close()
	}
}

func TestRestoreSQLiteWritesNoFileOutsideDestination(t *testing.T) {
	cases := []struct {
		name      string
		statement string
	}{
		{"attach", "ATTACH DATABASE '%s' AS x; CREATE TABLE x.t (a); INSERT INTO x.t VALUES (1);"},
		{"vacuum into", "CREATE TABLE t (a); VACUUM INTO '%s';"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			outside := filepath.Join(t.TempDir(), "outside.db")
			dumpPath := filepath.Join(dir, "in.dump")
			script := "PRAGMA foreign_keys=OFF;\n" + fmt.Sprintf(tc.statement, outside) + "\n"
			if err := os.WriteFile(dumpPath, []byte(script), 0o600); err != nil {
				t.Fatalf("write dump: %v", err)
			}

			err := RestoreSQLite(dumpPath, filepath.Join(dir, "rebuilt.db"))
			if err == nil {
				t.Fatal("RestoreSQLite accepted a dump that writes another database file")
			}
			if !strings.Contains(err.Error(), "too many attached databases") {
				t.Fatalf("restore failed for another reason: %v", err)
			}
			if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
				t.Fatalf("restore created %s (stat err %v)", outside, statErr)
			}
		})
	}
}
