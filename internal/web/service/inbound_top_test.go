package service

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestGetTopInbounds_EmptyDatabase(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	svc := &InboundService{}

	got, err := svc.GetTopInbounds(1)
	if err != nil {
		t.Fatalf("GetTopInbounds on empty DB failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice so JSON marshals as []")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestGetTopInbounds_RankingLimitTieBreakIsolationDisabledAndReset(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	db := database.GetDB()
	svc := &InboundService{}

	// Seed 7 inbounds for user 1 (including disabled and tied traffic) + 1 high-traffic inbound for user 2.
	// Note: GORM ignores Enable=false on Create when column has no default-override unless Updates/Select is used,
	// so we explicitly set Enable=false via map/Updates after insert where needed.
	seeds := []model.Inbound{
		{
			UserId: 1, Remark: "low-1", Protocol: model.VLESS, Port: 10001, Tag: "in-10001",
			Up: 100, Down: 200, Total: 5000, Enable: true,
			Settings:       `{"clients":[{"id":"11111111-1111-1111-1111-111111111111","email":"alice@example.com"}]}`,
			StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"privateKey":"priv-key-fixture"}}`,
			Sniffing:       `{"enabled":true}`,
		},
		{
			UserId: 1, Remark: "tie-first", Protocol: model.VMESS, Port: 10002, Tag: "in-10002",
			Up: 400, Down: 600, Total: 9000, Enable: true,
			Settings: `{"clients":[{"id":"22222222-2222-2222-2222-222222222222","email":"bob@example.com"}]}`,
		},
		{
			UserId: 1, Remark: "tie-second", Protocol: model.Trojan, Port: 10003, Tag: "in-10003",
			Up: 500, Down: 500, Total: 0, Enable: true,
			Settings: `{"clients":[{"password":"client-pass-fixture","email":"carol@example.com"}]}`,
		},
		{
			UserId: 1, Remark: "disabled-high", Protocol: model.Shadowsocks, Port: 10004, Tag: "in-10004",
			Up: 1500, Down: 1500, Total: 10000, Enable: true,
			Settings: `{"password":"ss-pass-fixture"}`,
		},
		{
			UserId: 1, Remark: "top-1", Protocol: model.VLESS, Port: 10005, Tag: "in-10005",
			Up: 2000, Down: 3000, Total: 20000, Enable: true,
		},
		{
			UserId: 1, Remark: "top-2", Protocol: model.TUIC, Port: 10006, Tag: "in-10006",
			Up: 1200, Down: 2800, Total: 0, Enable: true,
		},
		{
			UserId: 1, Remark: "lowest-excluded", Protocol: model.WireGuard, Port: 10007, Tag: "in-10007",
			Up: 10, Down: 10, Total: 0, Enable: true,
		},
		{
			UserId: 2, Remark: "other-user-huge", Protocol: model.VLESS, Port: 20001, Tag: "in-20001",
			Up: 999999, Down: 999999, Total: 0, Enable: true,
		},
	}

	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed inbound %d: %v", i, err)
		}
	}
	if err := db.Model(&model.Inbound{}).Where("id = ?", seeds[3].Id).Update("enable", false).Error; err != nil {
		t.Fatalf("disable inbound: %v", err)
	}

	got, err := svc.GetTopInbounds(1)
	if err != nil {
		t.Fatalf("GetTopInbounds(1): %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}

	// Expected ranking for user 1:
	// 1. top-1 (5000, enabled=true)
	// 2. top-2 (4000, enabled=true)
	// 3. disabled-high (3000, enabled=false)
	// 4. tie-first (1000, smaller id)
	// 5. tie-second (1000, larger id)
	wantRemarks := []string{"top-1", "top-2", "disabled-high", "tie-first", "tie-second"}
	wantTraffics := []int64{5000, 4000, 3000, 1000, 1000}
	wantEnabled := []bool{true, true, false, true, true}
	for i := range got {
		if got[i].Remark != wantRemarks[i] || got[i].Traffic != wantTraffics[i] || got[i].Enabled != wantEnabled[i] {
			t.Fatalf("row[%d] = %+v, want remark=%s traffic=%d enabled=%v", i, got[i], wantRemarks[i], wantTraffics[i], wantEnabled[i])
		}
		if got[i].Remark == "other-user-huge" {
			t.Fatalf("user 2 inbound leaked into user 1 result: %+v", got[i])
		}
	}
	if got[3].Id >= got[4].Id {
		t.Fatalf("tie-break expected id ASC: got[3].Id=%d >= got[4].Id=%d", got[3].Id, got[4].Id)
	}

	// Verify exact JSON struct fields (6 fields) and absence of forbidden fields/PII.
	rt := reflect.TypeOf(TopInbound{})
	if rt.NumField() != 6 {
		t.Fatalf("TopInbound has %d fields, want 6", rt.NumField())
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal TopInbound: %v", err)
	}
	body := string(raw)
	for _, forbidden := range []string{
		`"up"`, `"down"`, `"total"`, `"settings"`, `"streamSettings"`, `"sniffing"`, `"clientStats"`,
		"alice@example.com", "bob@example.com", "carol@example.com",
		"11111111-1111-1111-1111-111111111111", "priv-key-fixture", "client-pass-fixture", "ss-pass-fixture",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("forbidden field/PII %q leaked in %s", forbidden, body)
		}
	}

	// Verify ResetAllTraffics zeroes counters and orders ties by id ASC.
	if err := svc.ResetAllTraffics(); err != nil {
		t.Fatalf("ResetAllTraffics: %v", err)
	}
	afterReset, err := svc.GetTopInbounds(1)
	if err != nil {
		t.Fatalf("GetTopInbounds after reset: %v", err)
	}
	if len(afterReset) != 5 {
		t.Fatalf("afterReset len = %d, want 5", len(afterReset))
	}
	for i := range afterReset {
		if afterReset[i].Traffic != 0 {
			t.Fatalf("afterReset[%d].Traffic = %d, want 0", i, afterReset[i].Traffic)
		}
		if i > 0 && afterReset[i-1].Id >= afterReset[i].Id {
			t.Fatalf("afterReset id ASC broken at %d: %d >= %d", i, afterReset[i-1].Id, afterReset[i].Id)
		}
	}
}

func TestGetTopInbounds_DatabaseUnavailableReturnsError(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	sqlDB, err := database.GetDB().DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}

	svc := &InboundService{}
	if _, err := svc.GetTopInbounds(1); err == nil {
		t.Fatal("expected error when database is closed, got nil")
	}
}
