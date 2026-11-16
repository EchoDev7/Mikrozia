package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
)

func TestStageSQLiteUploadRebuildsFromDump(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	dbtest.InitDB(t, dbPath)
	dump, err := database.DumpSQLiteToBytes(dbPath)
	if err != nil {
		t.Fatalf("DumpSQLiteToBytes: %v", err)
	}

	uploadPath := filepath.Join(dir, "upload.dump")
	if err := os.WriteFile(uploadPath, dump, 0o600); err != nil {
		t.Fatalf("write upload: %v", err)
	}

	staged := filepath.Join(dir, "x-ui.db.temp")
	if err := stageSQLiteUpload(uploadPath, importKindSQLiteDump, staged); err != nil {
		t.Fatalf("stageSQLiteUpload: %v", err)
	}
	if err := database.ValidateSQLiteDB(staged); err != nil {
		t.Errorf("staged database fails integrity check: %v", err)
	}
	if err := database.PrepareSQLiteForMigration(staged); err != nil {
		t.Errorf("staged database fails the panel pre-flight: %v", err)
	}
}
