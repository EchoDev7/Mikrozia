package database

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
)

// TestLegacyNodeAdminTokenMigratedToNodeSync verifies that existing stored tokens
// with legacy "node-admin" scope are cleanly migrated to "node-sync" on InitDB
// without losing their token hash, name, expiry, creation time, or enabled status.
func TestLegacyNodeAdminTokenMigratedToNodeSync(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "x-ui.db")
	t.Setenv("XUI_DB_FOLDER", dbDir)

	// Pre-populate raw SQLite database with a legacy node-admin token
	rawDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	if err := rawDB.AutoMigrate(&model.ApiToken{}); err != nil {
		t.Fatalf("raw automigrate: %v", err)
	}

	rawHash := crypto.HashTokenSHA256("secret-plaintext-token")
	legacyToken := model.ApiToken{
		Id:        42,
		Name:      "legacy-node-admin-token",
		Token:     rawHash,
		Enabled:   true,
		CreatedAt: 1735000000,
		Scope:     "node-admin",
		ExpiresAt: 1890000000000,
	}
	if err := rawDB.Create(&legacyToken).Error; err != nil {
		t.Fatalf("seed legacy token: %v", err)
	}
	rawSQL, _ := rawDB.DB()
	_ = rawSQL.Close()

	// Run InitDB which triggers migrations
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	// Verify migrated token
	var migrated model.ApiToken
	if err := GetDB().First(&migrated, 42).Error; err != nil {
		t.Fatalf("find migrated token: %v", err)
	}

	if migrated.Scope != model.ApiScopeNodeSync {
		t.Errorf("migrated Scope = %q, want %q", migrated.Scope, model.ApiScopeNodeSync)
	}
	if migrated.Name != "legacy-node-admin-token" {
		t.Errorf("migrated Name = %q, want %q", migrated.Name, "legacy-node-admin-token")
	}
	if migrated.Token != rawHash {
		t.Errorf("migrated Token hash altered: got %q, want %q", migrated.Token, rawHash)
	}
	if !migrated.Enabled {
		t.Errorf("migrated Enabled altered: got false, want true")
	}
	if migrated.ExpiresAt != 1890000000000 {
		t.Errorf("migrated ExpiresAt altered: got %d, want %d", migrated.ExpiresAt, 1890000000000)
	}
	if migrated.CreatedAt != 1735000000 {
		t.Errorf("migrated CreatedAt altered: got %d, want %d", migrated.CreatedAt, 1735000000)
	}
}
