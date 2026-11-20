package service

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestSummaryCounts_ZeroClients(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	var clientSvc ClientService
	var inboundSvc InboundService
	var settingSvc SettingService

	got, err := clientSvc.SummaryCounts(&inboundSvc, &settingSvc)
	if err != nil {
		t.Fatalf("SummaryCounts on empty DB failed: %v", err)
	}
	want := ClientsSummaryCounts{}
	if *got != want {
		t.Fatalf("got %+v, want %+v", *got, want)
	}
}

func TestSummaryCounts_ParityAndBoundaries(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	var clientSvc ClientService
	var inboundSvc InboundService
	var settingSvc SettingService

	db := database.GetDB()
	if err := db.Create(&model.Setting{Key: "expireDiff", Value: "7"}).Error; err != nil {
		t.Fatalf("seed expireDiff: %v", err)
	}
	if err := db.Create(&model.Setting{Key: "trafficDiff", Value: "2"}).Error; err != nil {
		t.Fatalf("seed trafficDiff: %v", err)
	}

	seedPagingClients(t)

	paged, err := clientSvc.ListPaged(&inboundSvc, &settingSvc, ClientPageParams{Page: 1, PageSize: 25})
	if err != nil {
		t.Fatalf("ListPaged: %v", err)
	}
	got, err := clientSvc.SummaryCounts(&inboundSvc, &settingSvc)
	if err != nil {
		t.Fatalf("SummaryCounts: %v", err)
	}

	if got.Total != paged.Summary.Total ||
		got.Online != paged.Summary.OnlineCount ||
		got.Ended != paged.Summary.DepletedCount ||
		got.Depleting != paged.Summary.ExpiringCount ||
		got.Disabled != paged.Summary.DeactiveCount ||
		got.Active != paged.Summary.Active {
		t.Fatalf("SummaryCounts = %+v, ListPaged.Summary = %+v", *got, paged.Summary)
	}
	if got.Online != 0 {
		t.Fatalf("expected Online = 0 when xray is stopped, got %d", got.Online)
	}
	if got.Total == 0 || got.Ended == 0 || got.Depleting == 0 || got.Disabled == 0 || got.Active == 0 {
		t.Fatalf("expected non-zero counts across all DB buckets, got %+v", *got)
	}
}

func TestSummaryCounts_Exceeds200CapAndDeduplicatesOnline(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	db := database.GetDB()

	now := time.Now().UnixMilli()
	records := make([]model.ClientRecord, 0, 230)
	onlineInput := make([]string, 0, 260)
	for i := range 230 {
		email := fmt.Sprintf("user-%03d@example.com", i)
		enable := i < 220 // 220 enabled, 10 disabled
		records = append(records, model.ClientRecord{
			Email:      email,
			Enable:     enable,
			ExpiryTime: now + 30*pagingDay,
		})
		if i < 215 {
			onlineInput = append(onlineInput, email)
			if i%5 == 0 {
				// Duplicate online identifier must only count once.
				onlineInput = append(onlineInput, email)
			}
		}
	}
	// Disabled client reported in online list must not be counted as online.
	onlineInput = append(onlineInput, "user-225@example.com")

	if err := db.Create(&records).Error; err != nil {
		t.Fatalf("Create clients: %v", err)
	}
	if err := db.Model(&model.ClientRecord{}).Where("email >= ?", "user-220@example.com").Update("enable", false).Error; err != nil {
		t.Fatalf("disable tail clients: %v", err)
	}

	q := newClientQuery(db, now, 0, 0)
	online, err := q.onlineCount(onlineInput)
	if err != nil {
		t.Fatalf("onlineCount: %v", err)
	}
	if online != 215 {
		t.Fatalf("onlineCount = %d, want 215 (no 200-entry cap, deduplicated, enabled-only)", online)
	}

	var clientSvc ClientService
	var inboundSvc InboundService
	var settingSvc SettingService
	got, err := clientSvc.SummaryCounts(&inboundSvc, &settingSvc)
	if err != nil {
		t.Fatalf("SummaryCounts: %v", err)
	}
	if got.Total != 230 || got.Active != 220 || got.Disabled != 10 {
		t.Fatalf("unexpected counts for >200 clients: %+v", *got)
	}
}

func TestSummaryCounts_XrayStoppedReturnsZeroOnlineWithValidDatabaseCounts(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	db := database.GetDB()
	if err := db.Create(&model.Setting{Key: "expireDiff", Value: "7"}).Error; err != nil {
		t.Fatalf("seed expireDiff: %v", err)
	}
	if err := db.Create(&model.Setting{Key: "trafficDiff", Value: "2"}).Error; err != nil {
		t.Fatalf("seed trafficDiff: %v", err)
	}

	seedPagingClients(t)

	var clientSvc ClientService
	var inboundSvc InboundService
	var settingSvc SettingService

	// With Xray stopped, GetOnlineClients() returns an empty slice.
	if onlines := inboundSvc.GetOnlineClients(); len(onlines) != 0 {
		t.Fatalf("expected empty GetOnlineClients when xray is stopped, got %v", onlines)
	}

	got, err := clientSvc.SummaryCounts(&inboundSvc, &settingSvc)
	if err != nil {
		t.Fatalf("SummaryCounts with stopped xray: %v", err)
	}

	if got.Online != 0 {
		t.Fatalf("Online = %d, want 0 when xray is stopped", got.Online)
	}
	if got.Active < 1 || got.Ended == 0 || got.Depleting == 0 || got.Disabled == 0 {
		t.Fatalf("expected non-zero DB-derived buckets with active >= 1, got %+v", *got)
	}
	if got.Total != got.Active+got.Ended+got.Depleting+got.Disabled {
		t.Fatalf("Total = %d, want sum of buckets %d", got.Total, got.Active+got.Ended+got.Depleting+got.Disabled)
	}

	// Verify returned struct exposes only the 6 integer count fields and no PII.
	rt := reflect.TypeOf(*got)
	if rt.NumField() != 6 {
		t.Fatalf("ClientsSummaryCounts has %d fields, want 6", rt.NumField())
	}
	for i := range rt.NumField() {
		if rt.Field(i).Type.Kind() != reflect.Int {
			t.Fatalf("field %s has kind %s, want int", rt.Field(i).Name, rt.Field(i).Type.Kind())
		}
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var m map[string]int
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("json.Unmarshal into map[string]int: %v", err)
	}
	if len(m) != 6 {
		t.Fatalf("JSON object has %d keys (%v), want 6", len(m), m)
	}
}

func TestSummaryCounts_DatabaseUnavailableReturnsError(t *testing.T) {
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "test.db"))
	if err := database.CloseDB(); err != nil {
		t.Fatalf("CloseDB: %v", err)
	}

	var clientSvc ClientService
	var inboundSvc InboundService
	var settingSvc SettingService
	if _, err := clientSvc.SummaryCounts(&inboundSvc, &settingSvc); err == nil {
		t.Fatal("expected error when database is closed, got nil")
	}
}
