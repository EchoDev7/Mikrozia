package job

import (
	"reflect"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/database/model"
)

func TestMergeClientIps_EvictsStaleOldEntries(t *testing.T) {
	// #4077: a long-disconnected IP used to stay in the DB with an ancient
	// timestamp and keep "protecting" itself against eviction. Guard against
	// that regression here.
	old := []model.ClientIpEntry{
		{IP: "1.1.1.1", Timestamp: 100},  // stale — client disconnected long ago
		{IP: "2.2.2.2", Timestamp: 1900}, // fresh — still connecting
	}
	new := []model.ClientIpEntry{
		{IP: "2.2.2.2", Timestamp: 2000}, // same IP, newer log line
	}

	got := mergeClientIps(old, new, 1000, false)

	want := map[string]int64{"2.2.2.2": 2000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale 1.1.1.1 should have been dropped\ngot:  %v\nwant: %v", got, want)
	}
}

func TestMergeClientIps_KeepsFreshOldEntriesUnchanged(t *testing.T) {
	// Entries that aren't stale are still carried forward, so a client's IP
	// history survives scans in which that client is not connecting.
	old := []model.ClientIpEntry{
		{IP: "1.1.1.1", Timestamp: 1500},
	}
	got := mergeClientIps(old, nil, 1000, false)

	want := map[string]int64{"1.1.1.1": 1500}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh old IP should have been retained\ngot:  %v\nwant: %v", got, want)
	}
}

func TestMergeClientIps_PrefersLaterTimestampForSameIp(t *testing.T) {
	old := []model.ClientIpEntry{{IP: "1.1.1.1", Timestamp: 1500}}
	new := []model.ClientIpEntry{{IP: "1.1.1.1", Timestamp: 1700}}

	got := mergeClientIps(old, new, 1000, false)

	if got["1.1.1.1"] != 1700 {
		t.Fatalf("expected latest timestamp 1700, got %d", got["1.1.1.1"])
	}
}

func TestMergeClientIps_DropsStaleNewEntries(t *testing.T) {
	// A clock-skewed old timestamp must not resurrect a stale IP past the cutoff.
	new := []model.ClientIpEntry{{IP: "1.1.1.1", Timestamp: 500}}
	got := mergeClientIps(nil, new, 1000, false)

	if len(got) != 0 {
		t.Fatalf("stale new IP should have been dropped, got %v", got)
	}
}

func TestMergeClientIps_NoStaleCutoffStillWorks(t *testing.T) {
	// Defensive: a zero cutoff (e.g. during very first run on a fresh
	// install) must not over-evict.
	old := []model.ClientIpEntry{{IP: "1.1.1.1", Timestamp: 100}}
	new := []model.ClientIpEntry{{IP: "2.2.2.2", Timestamp: 200}}

	got := mergeClientIps(old, new, 0, false)

	want := map[string]int64{"1.1.1.1": 100, "2.2.2.2": 200}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zero cutoff should keep everything\ngot:  %v\nwant: %v", got, want)
	}
}

func TestMergeClientIps_LiveObservationsBypassStaleCutoff(t *testing.T) {
	// online-API mode: lastSeen is set when the connection was dispatched, so
	// a connection held open for hours has an "old" timestamp while being live
	// by definition. It must survive the stale cutoff.
	new := []model.ClientIpEntry{{IP: "1.1.1.1", Timestamp: 500}} // opened long ago, still connected
	got := mergeClientIps(nil, new, 1000, true)

	want := map[string]int64{"1.1.1.1": 500}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("live observation must bypass the stale cutoff\ngot:  %v\nwant: %v", got, want)
	}
}

func TestMergeClientIps_LiveModeStillEvictsStaleOldEntries(t *testing.T) {
	// the bypass applies only to this scan's observations — persisted entries
	// from past scans still age out as before.
	old := []model.ClientIpEntry{{IP: "2.2.2.2", Timestamp: 100}}
	new := []model.ClientIpEntry{{IP: "1.1.1.1", Timestamp: 2000}}
	got := mergeClientIps(old, new, 1000, true)

	want := map[string]int64{"1.1.1.1": 2000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale db entry must still be evicted in live mode\ngot:  %v\nwant: %v", got, want)
	}
}
