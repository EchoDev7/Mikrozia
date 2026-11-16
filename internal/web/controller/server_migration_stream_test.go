package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"github.com/gin-gonic/gin"
)

func seedLargeSQLiteForControllerMigration(t *testing.T, rowCount, valueBytes int) string {
	t.Helper()
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "sqlite")
	if err := database.InitDB(filepath.Join(dbFolder, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	db := database.GetDB()
	if db == nil {
		t.Fatal("expected initialized database")
	}
	payload := strings.Repeat("c", valueBytes)
	rows := make([]model.Setting, 0, rowCount)
	for i := range rowCount {
		rows = append(rows, model.Setting{
			Key:   fmt.Sprintf("ctrl-mig-%05d", i),
			Value: payload,
		})
	}
	if err := db.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	return dbFolder
}

func assertNoMigrationDirs(t *testing.T, dbFolder string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := os.ReadDir(dbFolder)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		var leftover string
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".mikrozia-migration-") {
				leftover = entry.Name()
				break
			}
		}
		if leftover == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("leftover migration temp dir: %s", leftover)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestGetMigrationStreamsLargeSQLiteWithoutFullMemoryBuffer(t *testing.T) {
	dbFolder := seedLargeSQLiteForControllerMigration(t, 6000, 4096)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	ctrl := &ServerController{}
	engine.GET("/panel/api/server/getMigration", ctrl.getMigration)

	srv := httptest.NewServer(engine)
	defer srv.Close()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	resp, err := http.Get(srv.URL + "/panel/api/server/getMigration")
	if err != nil {
		t.Fatalf("GET /panel/api/server/getMigration: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, string(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "attachment; filename=x-ui.dump" {
		t.Fatalf("Content-Disposition = %q, want attachment; filename=x-ui.dump", cd)
	}
	if resp.ContentLength <= 20*1024*1024 {
		t.Fatalf("Content-Length = %d, want > 20 MiB", resp.ContentLength)
	}

	written, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	if written != resp.ContentLength {
		t.Fatalf("streamed bytes = %d, Content-Length = %d", written, resp.ContentLength)
	}

	runtime.GC()
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+16*1024*1024 {
		t.Fatalf("HeapAlloc grew by %d bytes (> 16 MiB) for %d-byte migration stream",
			after.HeapAlloc-before.HeapAlloc, written)
	}

	assertNoMigrationDirs(t, dbFolder)
}

func TestGetMigrationClientDisconnectCleansUpAndDoesNotLockNext(t *testing.T) {
	dbFolder := seedLargeSQLiteForControllerMigration(t, 2000, 4096)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	ctrl := &ServerController{}
	engine.GET("/panel/api/server/getMigration", ctrl.getMigration)

	srv := httptest.NewServer(engine)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/panel/api/server/getMigration")
	if err != nil {
		t.Fatalf("first GET /panel/api/server/getMigration: %v", err)
	}
	buf := make([]byte, 4096)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		resp.Body.Close()
		t.Fatalf("ReadFull(4096): %v", err)
	}
	resp.Body.Close()

	assertNoMigrationDirs(t, dbFolder)

	resp2, err := http.Get(srv.URL + "/panel/api/server/getMigration")
	if err != nil {
		t.Fatalf("second GET /panel/api/server/getMigration: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp2.Body, 4096))
		t.Fatalf("second status = %d, want 200 (body: %s)", resp2.StatusCode, string(body))
	}
	written, err := io.Copy(io.Discard, resp2.Body)
	if err != nil {
		t.Fatalf("second io.Copy: %v", err)
	}
	if written != resp2.ContentLength || written == 0 {
		t.Fatalf("second streamed bytes = %d, Content-Length = %d", written, resp2.ContentLength)
	}

	assertNoMigrationDirs(t, dbFolder)
}
