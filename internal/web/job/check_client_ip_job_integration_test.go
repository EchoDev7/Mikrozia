package job

import (
	"encoding/json"
	"log"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/op/go-logging"

	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/database/dbtest"
	"github.com/echodev7/mikrozia/v3/internal/database/model"
	xuilogger "github.com/echodev7/mikrozia/v3/internal/logger"
	"github.com/echodev7/mikrozia/v3/internal/xray"
)

// Mikrozia logger must be initialised once before any code path that can
// log a warning. otherwise log.Warningf panics on a nil logger.
var loggerInitOnce sync.Once

func setupIntegrationDB(t *testing.T) {
	t.Helper()

	loggerInitOnce.Do(func() {
		xuilogger.InitLogger(logging.ERROR)
	})

	dbDir := t.TempDir()
	logDir := t.TempDir()

	t.Setenv("XUI_DB_FOLDER", dbDir)
	t.Setenv("XUI_DB_TYPE", "sqlite")
	t.Setenv("XUI_DB_DSN", "")
	t.Setenv("XUI_LOG_FOLDER", logDir)

	// the job logs through the standard logger as well; that output is a
	// package global and would leak to other tests in the same binary.
	origLogWriter := log.Writer()
	origLogFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(origLogWriter)
		log.SetFlags(origLogFlags)
	})

	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
}

// seedInboundWithClient seeds an inbound whose settings json has a single
// client with the given email and ip limit.
func seedInboundWithClient(t *testing.T, tag, email string, limitIp int) {
	t.Helper()
	seedInboundOnlyWithClient(t, tag, email, limitIp)
}

func seedInboundOnlyWithClient(t *testing.T, tag, email string, limitIp int) *model.Inbound {
	t.Helper()
	settings := map[string]any{
		"clients": []map[string]any{
			{
				"email":   email,
				"limitIp": limitIp,
				"enable":  true,
			},
		},
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	inbound := &model.Inbound{
		Tag:      tag,
		Enable:   true,
		Protocol: model.VLESS,
		Port:     4321,
		Settings: string(settingsJSON),
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	return inbound
}

func seedLinkedInboundWithClient(t *testing.T, tag, email string, limitIp int) *model.Inbound {
	t.Helper()
	inbound := seedInboundOnlyWithClient(t, tag, email, limitIp)
	client := &model.ClientRecord{Email: email, LimitIP: limitIp}
	if err := database.GetDB().Create(client).Error; err != nil {
		t.Fatalf("seed client record: %v", err)
	}
	link := &model.ClientInbound{ClientId: client.Id, InboundId: inbound.Id}
	if err := database.GetDB().Create(link).Error; err != nil {
		t.Fatalf("seed client inbound link: %v", err)
	}
	return inbound
}

// seed an InboundClientIps row with the given blob.
func seedClientIps(t *testing.T, email string, ips []model.ClientIpEntry) *model.InboundClientIps {
	t.Helper()
	blob, err := model.MarshalClientIpEntries(ips)
	if err != nil {
		t.Fatalf("marshal ips: %v", err)
	}
	row := &model.InboundClientIps{
		ClientEmail: email,
		Ips:         blob,
	}
	if err := database.GetDB().Create(row).Error; err != nil {
		t.Fatalf("seed InboundClientIps: %v", err)
	}
	return row
}

// read the persisted blob and parse it back.
func readClientIps(t *testing.T, email string) []model.ClientIpEntry {
	t.Helper()
	row := &model.InboundClientIps{}
	if err := database.GetDB().Where("client_email = ?", email).First(row).Error; err != nil {
		t.Fatalf("read InboundClientIps for %s: %v", email, err)
	}
	if row.Ips == "" {
		return nil
	}
	var out []model.ClientIpEntry
	if err := json.Unmarshal([]byte(row.Ips), &out); err != nil {
		t.Fatalf("unmarshal Ips blob %q: %v", row.Ips, err)
	}
	return out
}

// make a lookup map so asserts don't depend on slice order.
func ipSet(entries []model.ClientIpEntry) map[string]int64 {
	out := make(map[string]int64, len(entries))
	for _, e := range entries {
		out[e.IP] = e.Timestamp
	}
	return out
}

// With the access-log fallback removed, an unavailable online-stats API (xray
// down, as in this unit test) must make Run a clean no-op: no inbound_client_ips
// rows — never a crash or partial work.
func TestRun_NoOpWhenOnlineApiUnavailable(t *testing.T) {
	setupIntegrationDB(t)

	const email = "no-api-user"
	seedInboundWithClient(t, "inbound-no-api", email, 1)

	NewCheckClientIpJob().Run()

	var count int64
	if err := database.GetDB().Model(&model.InboundClientIps{}).Where("client_email = ?", email).Count(&count).Error; err != nil {
		t.Fatalf("count InboundClientIps: %v", err)
	}
	if count != 0 {
		t.Fatalf("no IP-tracking rows should be persisted when Run no-ops, got %d", count)
	}
}

// #4091 repro: client has limit=3, db still holds 3 idle ips from a
// few minutes ago, only one live ip is actually connecting. The scan must keep
// every address visible and stamp the live one with the scan time; with
// enforcement retired there is nothing to disconnect or ban.
func TestUpdateInboundClientIps_KeepsLiveAndFreshHistoricalIps(t *testing.T) {
	setupIntegrationDB(t)

	const email = "pr4091-repro"
	seedInboundWithClient(t, "inbound-pr4091", email, 3)

	now := time.Now().Unix()
	// idle but still within the 30min staleness window.
	row := seedClientIps(t, email, []model.ClientIpEntry{
		{IP: "10.0.0.1", Timestamp: now - 20*60},
		{IP: "10.0.0.2", Timestamp: now - 15*60},
		{IP: "10.0.0.3", Timestamp: now - 10*60},
	})

	j := NewCheckClientIpJob()
	// the one that's actually connecting (user's 128.71.x.x).
	live := []model.ClientIpEntry{
		{IP: "128.71.1.1", Timestamp: now},
	}

	j.updateInboundClientIps(database.GetDB(), row, live, false)

	persisted := ipSet(readClientIps(t, email))
	for _, want := range []string{"128.71.1.1", "10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		if _, ok := persisted[want]; !ok {
			t.Errorf("expected %s to be persisted in inbound_client_ips.ips; got %v", want, persisted)
		}
	}
	if got := persisted["128.71.1.1"]; got != now {
		t.Errorf("live ip timestamp should match the scan timestamp %d, got %d", now, got)
	}
}

// #4800: per-client IP tracking must populate even when no client has an IP
// limit. processObserved records observed IPs for the panel regardless of any
// stored limit value.
func TestProcessObserved_CollectsIpsWithoutLimit(t *testing.T) {
	setupIntegrationDB(t)

	const email = "no-limit-user"
	seedInboundWithClient(t, "inbound-no-limit", email, 0) // limitIp = 0

	observed := map[string]map[string]int64{
		email: {"203.0.113.10": time.Now().Unix()},
	}
	NewCheckClientIpJob().processObserved(observed, true)

	ips := readClientIps(t, email)
	if len(ips) != 1 || ips[0].IP != "203.0.113.10" {
		t.Fatalf("expected the observed IP to be collected without a limit, got %v", ips)
	}
}

// #6722: a non-live run keeps the enforcing run's 30-minute window; the old
// branch overwrote the blob, dropping IPs seen minutes earlier.
func TestProcessObserved_NonLiveRunKeepsRecentWindow(t *testing.T) {
	setupIntegrationDB(t)

	const email = "window-user"
	seedInboundWithClient(t, "inbound-window", email, 0)
	fiveMinAgo := time.Now().Unix() - 5*60
	seedClientIps(t, email, []model.ClientIpEntry{{IP: "198.51.100.1", Timestamp: fiveMinAgo}})

	observed := map[string]map[string]int64{
		email: {"203.0.113.30": time.Now().Unix()},
	}
	NewCheckClientIpJob().processObserved(observed, false)

	got := ipSet(readClientIps(t, email))
	if _, ok := got["198.51.100.1"]; !ok {
		t.Fatalf("non-live run dropped the recent IP 198.51.100.1, got %v", got)
	}
	if _, ok := got["203.0.113.30"]; !ok {
		t.Fatalf("non-live run missed the observed IP 203.0.113.30, got %v", got)
	}
}

// staticOnlineUsers pins the job's online-stats source so Run can be driven
// end to end without a live xray core.
type staticOnlineUsers []xray.OnlineUser

func (s staticOnlineUsers) GetOnlineUsers() ([]xray.OnlineUser, bool, error) {
	return s, true, nil
}

// End-to-end tracking: Run records every address a client connects from, and an
// over-limit observation is recorded exactly like any other — the retired
// enforcement path must not disconnect the client or rewrite its stored limit.
func TestRun_RecordsObservedIpsAndLeavesStoredLimitUntouched(t *testing.T) {
	setupIntegrationDB(t)

	const email = "track-only-user"
	inbound := seedLinkedInboundWithClient(t, "inbound-track-only", email, 1)

	j := NewCheckClientIpJob()
	j.onlineUsers = staticOnlineUsers{{Email: email, IPs: []xray.OnlineIP{
		{IP: "203.0.113.20"}, {IP: "203.0.113.21"},
	}}}
	j.Run()
	j.Run()

	got := ipSet(readClientIps(t, email))
	if len(got) != 2 {
		t.Fatalf("expected both observed IPs recorded, got %v", got)
	}

	var record model.ClientRecord
	if err := database.GetDB().Where("email = ?", email).First(&record).Error; err != nil {
		t.Fatalf("read client record: %v", err)
	}
	if record.LimitIP != 1 {
		t.Fatalf("stored clients.limit_ip = %d, want the pre-existing 1 preserved", record.LimitIP)
	}

	var stored model.Inbound
	if err := database.GetDB().First(&stored, inbound.Id).Error; err != nil {
		t.Fatalf("read inbound: %v", err)
	}
	clients, err := parseTestInboundClients(stored.Settings)
	if err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	if len(clients) != 1 || clients[0]["limitIp"] != float64(1) {
		t.Fatalf("inbound settings limitIp changed: %v", clients)
	}
}

func parseTestInboundClients(settings string) ([]map[string]any, error) {
	var parsed struct {
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return nil, err
	}
	return parsed.Clients, nil
}

// #4963: an observed IP for a renamed/deleted client (its email no longer maps
// to any inbound) must not create or resurrect an inbound_client_ips row, and
// must drop any orphan left behind — instead of erroring every run.
func TestProcessObserved_StaleEmailIsSkippedAndOrphanDropped(t *testing.T) {
	setupIntegrationDB(t)

	const staleEmail = "renamed-away"
	// No inbound references staleEmail. Pre-seed an orphan tracking row to
	// confirm the job removes it rather than leaving it to error forever.
	seedClientIps(t, staleEmail, []model.ClientIpEntry{{IP: "203.0.113.5", Timestamp: time.Now().Unix()}})

	observed := map[string]map[string]int64{
		staleEmail: {"203.0.113.5": time.Now().Unix()},
	}
	NewCheckClientIpJob().processObserved(observed, true)

	var count int64
	if err := database.GetDB().Model(&model.InboundClientIps{}).Where("client_email = ?", staleEmail).Count(&count).Error; err != nil {
		t.Fatalf("count InboundClientIps: %v", err)
	}
	if count != 0 {
		t.Fatalf("stale-email orphan row should be deleted, got %d row(s)", count)
	}
}

// the exact clients/client_inbounds relation must win over the substring scan,
// so a client is resolved to its own inbound even when another inbound holds a
// superstring email.
func TestGetInboundByEmailUsesClientInboundLink(t *testing.T) {
	setupIntegrationDB(t)

	want := seedLinkedInboundWithClient(t, "linked-inbound", "exact@example.com", 1)
	seedInboundOnlyWithClient(t, "other-inbound", "not-exact@example.com", 1)

	got, err := (&CheckClientIpJob{}).getInboundByEmail("exact@example.com")
	if err != nil {
		t.Fatalf("getInboundByEmail returned error: %v", err)
	}
	if got.Id != want.Id {
		t.Fatalf("getInboundByEmail returned inbound %d, want %d", got.Id, want.Id)
	}
}

// the substring fallback must still verify the exact email inside settings, so
// "ann@example.com" does not match an inbound holding "joann@example.com".
func TestGetInboundByEmailRejectsSubstringFallbackMatch(t *testing.T) {
	setupIntegrationDB(t)

	seedInboundOnlyWithClient(t, "substring-only", "joann@example.com", 1)

	if got, err := (&CheckClientIpJob{}).getInboundByEmail("ann@example.com"); err == nil {
		t.Fatalf("substring email matched inbound %d; want no exact match", got.Id)
	}
}
