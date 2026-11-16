package database

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// PostgreSQL variant of the legacy-limit preservation guarantee: a database
// written by an earlier release keeps its non-zero limit_ip and settings
// limitIp across migration and reopen, even though no enforcement consumes them.
func TestLegacyIpLimitsPreservedPostgresMigration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("XUI_TEST_PG_DSN"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("XUI_DB_DSN"))
	}
	if dsn == "" {
		t.Skip("set XUI_TEST_PG_DSN or XUI_DB_DSN to run the PostgreSQL limit preservation test")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL admin connection: %v", err)
	}
	adminHandle, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminHandle.Close() })

	schema := fmt.Sprintf("legacy_limit_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = CloseDB()
		if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})

	scoped := dsn + " search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		scoped = parsed.String()
	}
	t.Setenv("XUI_DB_TYPE", "postgres")
	t.Setenv("XUI_DB_DSN", scoped)

	legacy, err := gorm.Open(postgres.Open(scoped), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy schema: %v", err)
	}
	if err := legacy.Exec("CREATE TABLE clients (id BIGSERIAL PRIMARY KEY, email TEXT NOT NULL, limit_ip BIGINT DEFAULT 0)").Error; err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec("INSERT INTO clients (email, limit_ip) VALUES ('legacy-limited', 3), ('legacy-unlimited', 0)").Error; err != nil {
		t.Fatal(err)
	}
	legacyHandle, err := legacy.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := legacyHandle.Close(); err != nil {
		t.Fatal(err)
	}

	if err := InitDB(""); err != nil {
		t.Fatalf("InitDB legacy schema: %v", err)
	}
	for _, tc := range []struct {
		email string
		want  int
	}{{"legacy-limited", 3}, {"legacy-unlimited", 0}} {
		var record model.ClientRecord
		if err := GetDB().Where("email = ?", tc.email).First(&record).Error; err != nil {
			t.Fatalf("read %s after migration: %v", tc.email, err)
		}
		if record.LimitIP != tc.want {
			t.Fatalf("%s limit_ip after migration = %d, want %d", tc.email, record.LimitIP, tc.want)
		}
	}

	inbound := &model.Inbound{
		UserId:   1,
		Port:     43555,
		Protocol: model.VLESS,
		Tag:      "legacy-limit-pg",
		Settings: `{"clients":[{"email":"legacy-limited","limitIp":3,"enable":true}]}`,
	}
	if err := GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	if err := CloseDB(); err != nil {
		t.Fatal(err)
	}
	if err := InitDB(""); err != nil {
		t.Fatalf("reopen migrated PostgreSQL database: %v", err)
	}

	var record model.ClientRecord
	if err := GetDB().Where("email = ?", "legacy-limited").First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.LimitIP != 3 {
		t.Fatalf("limit_ip after reopen = %d, want 3", record.LimitIP)
	}

	var reopened model.Inbound
	if err := GetDB().First(&reopened, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Clients []model.Client `json:"clients"`
	}
	if err := json.Unmarshal([]byte(reopened.Settings), &payload); err != nil {
		t.Fatalf("decode reopened settings: %v", err)
	}
	if len(payload.Clients) != 1 || payload.Clients[0].LimitIP != 3 {
		t.Fatalf("reopened settings clients = %#v, want limitIp 3", payload.Clients)
	}
}
