package database

import (
	"path/filepath"
	"testing"
)

// A replaced pool that stays open keeps its database file open; Windows then
// cannot delete or replace that file.
func TestInitDBClosesThePoolItReplaces(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("first InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	replaced, err := GetDB().DB()
	if err != nil {
		t.Fatalf("first pool: %v", err)
	}

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("second InitDB: %v", err)
	}
	if err := replaced.Ping(); err == nil || err.Error() != "sql: database is closed" {
		t.Fatalf("replaced pool Ping() = %v, want sql: database is closed", err)
	}
}

// A closed pool must not remain the active dialect; IsPostgres and Dialect have
// to fall back to configuration instead of reporting the stale driver name.
func TestCloseDBDropsTheClosedHandle(t *testing.T) {
	t.Setenv("XUI_DB_TYPE", "sqlite")
	if err := InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	if err := CloseDB(); err != nil {
		t.Fatalf("CloseDB: %v", err)
	}
	if GetDB() != nil {
		t.Fatal("CloseDB kept the closed pool as the active handle")
	}
	if got := Dialect(); got != "" {
		t.Fatalf("Dialect() after CloseDB = %q, want empty", got)
	}
}
