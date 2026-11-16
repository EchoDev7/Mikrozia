package database

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Mikrozia cannot enforce IP limits inside a RouterOS container, but the stored
// values are user data: an upgrade must never clear, reset, or reinterpret a
// non-zero limitIp/limit_ip written by an earlier release. These tests build a
// database that predates this change and run the real startup migration over it.

// setupLegacyLimitDB creates an existing database containing an inbound with
// limitIp and a client record with limit_ip, mimicking a real database prior to
// upgrade.
func setupLegacyLimitDB(t *testing.T, dbPath string, limit int, withResetSeeder bool) (int, int) {
	t.Helper()
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("setupLegacyLimitDB InitDB: %v", err)
	}

	// Insert an admin user so the database is recognized as an existing user database.
	user := model.User{
		Username: "admin",
		Password: "hashed_test_password",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	settings, err := json.Marshal(map[string]any{
		"clients": []any{
			map[string]any{
				"email":   "user@example.test",
				"limitIp": limit,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	inbound := model.Inbound{
		UserId:   user.Id,
		Port:     12345,
		Protocol: "vless",
		Remark:   "test-inbound",
		Settings: string(settings),
	}
	if err := db.Create(&inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	record := model.ClientRecord{
		Email:   "user@example.test",
		LimitIP: limit,
		Enable:  true,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("create client record: %v", err)
	}

	// Older builds recorded a seeder named ResetIpLimitNoFail2ban; a database
	// that has that row must upgrade like any other.
	if withResetSeeder {
		if err := db.Create(&model.HistoryOfSeeders{SeederName: "ResetIpLimitNoFail2ban"}).Error; err != nil {
			t.Fatalf("create seeder: %v", err)
		}
	} else {
		if err := db.Where("seeder_name = ?", "ResetIpLimitNoFail2ban").Delete(&model.HistoryOfSeeders{}).Error; err != nil {
			t.Fatalf("delete seeder: %v", err)
		}
	}

	if err := CloseDB(); err != nil {
		t.Fatalf("close legacy setup db: %v", err)
	}

	return inbound.Id, record.Id
}

func verifyLimitsPreserved(t *testing.T, inboundId, recordId, expectedLimit int) {
	t.Helper()

	var inbound model.Inbound
	if err := db.First(&inbound, inboundId).Error; err != nil {
		t.Fatalf("reload inbound: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}

	clients, ok := settings["clients"].([]any)
	if !ok || len(clients) == 0 {
		t.Fatalf("missing clients array in settings: %s", inbound.Settings)
	}

	clientMap, ok := clients[0].(map[string]any)
	if !ok {
		t.Fatalf("invalid client shape: %v", clients[0])
	}

	limitVal, ok := clientMap["limitIp"]
	if !ok {
		t.Fatal("limitIp key missing from client in inbound settings")
	}

	num, ok := limitVal.(float64)
	if !ok || int(num) != expectedLimit {
		t.Fatalf("inbound client limitIp = %v, want %d", limitVal, expectedLimit)
	}

	var record model.ClientRecord
	if err := db.First(&record, recordId).Error; err != nil {
		t.Fatalf("reload client record: %v", err)
	}

	if record.LimitIP != expectedLimit {
		t.Fatalf("client record LimitIP = %d, want %d", record.LimitIP, expectedLimit)
	}
}

// Scenario 1: startup with no Fail2ban binary on PATH at all. A non-zero
// limitIp/limit_ip must survive untouched.
func TestLegacyIpLimitsPreservedWithoutFail2banBinary(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "x-ui.db")
	t.Setenv("XUI_DB_FOLDER", dbDir)
	t.Setenv("PATH", t.TempDir()) // no fail2ban-client, as on RouterOS

	inboundId, recordId := setupLegacyLimitDB(t, dbPath, 3, false)

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB migration failed: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	verifyLimitsPreserved(t, inboundId, recordId, 3)
}

// Scenario 2: the database still carries the legacy ResetIpLimitNoFail2ban
// seeder row. InitDB must succeed and limits must remain intact.
func TestLegacyIpLimitsPreservedWithExistingResetIpLimitSeeder(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "x-ui.db")
	t.Setenv("XUI_DB_FOLDER", dbDir)
	t.Setenv("PATH", t.TempDir())

	inboundId, recordId := setupLegacyLimitDB(t, dbPath, 3, true)

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB failed with existing seeder: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	verifyLimitsPreserved(t, inboundId, recordId, 3)
}

// Scenario 3: multiple InitDB/migration runs must be idempotent and preserve
// limits.
func TestLegacyIpLimitsPreservationIdempotentAcrossMultipleInitDBRuns(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "x-ui.db")
	t.Setenv("XUI_DB_FOLDER", dbDir)
	t.Setenv("PATH", t.TempDir())

	inboundId, recordId := setupLegacyLimitDB(t, dbPath, 3, false)

	for run := 1; run <= 3; run++ {
		if err := InitDB(dbPath); err != nil {
			t.Fatalf("InitDB run %d failed: %v", run, err)
		}
		verifyLimitsPreserved(t, inboundId, recordId, 3)
		if err := CloseDB(); err != nil {
			t.Fatalf("CloseDB run %d failed: %v", run, err)
		}
	}
}
