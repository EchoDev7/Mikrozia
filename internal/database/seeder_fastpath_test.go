package database

import (
	"path/filepath"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/database/model"
)

func TestFreshInstallFastPathDoesNotCreateResetIpLimitSeeder(t *testing.T) {
	if err := InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	if err := db.Where("1 = 1").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
		t.Fatalf("reset seeder history: %v", err)
	}
	if err := db.Where("1 = 1").Delete(&model.User{}).Error; err != nil {
		t.Fatalf("reset users: %v", err)
	}

	if err := runSeeders(true); err != nil {
		t.Fatalf("runSeeders: %v", err)
	}

	var cnt int64
	if err := db.Model(&model.HistoryOfSeeders{}).
		Where("seeder_name = ?", "ResetIpLimitNoFail2ban").
		Count(&cnt).Error; err != nil {
		t.Fatalf("count seeder history: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("fresh-install fast path must not create ResetIpLimitNoFail2ban seeder, got %d", cnt)
	}
}
