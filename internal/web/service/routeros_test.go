package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/routeros"
)

func intPtr(v int) *int          { return &v }
func int64Ptr(v int64) *int64    { return &v }
func uint64Ptr(v uint64) *uint64 { return &v }

func TestRouterOSStatusNeverConnectedBeforeHealthAndCoalescesViewers(t *testing.T) {
	setTestRouterOSConfig(t, RouterOSAPIConfig{
		State:    "connecting",
		Host:     "172.16.10.1",
		Port:     8728,
		Username: "test-user",
		Password: "secret-canary",
	})
	start, release := make(chan struct{}, 2), make(chan struct{})
	s := &RouterOSAPIService{probe: func(context.Context, string, string, string) routeros.ResourceSnapshot {
		start <- struct{}{}
		<-release
		return routeros.ResourceSnapshot{
			State:           "connected",
			UptimeSeconds:   int64Ptr(3600),
			CPULoad:         intPtr(0),
			CPUCount:        intPtr(4),
			CPUFrequencyMhz: intPtr(2400),
			MemUsed:         uint64Ptr(0),
			MemTotal:        uint64Ptr(1073741824),
		}
	}}
	for range 20 {
		if got := s.Status(); got.State != "connecting" {
			t.Fatalf("premature status: %+v", got)
		}
	}
	<-start
	select {
	case <-start:
		t.Fatal("duplicate health probe")
	default:
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		got := s.Status()
		if got.State == "connected" {
			if got.CheckedAt == 0 || got.Stale || got.CPULoad == nil || *got.CPULoad != 0 {
				t.Fatalf("bad zero-cpu success: %+v", got)
			}
			if got.CpuAvg == nil || *got.CpuAvg != 0 || got.CpuPeak == nil || *got.CpuPeak != 0 || len(got.CpuHistory) != 1 {
				t.Fatalf("bad zero-cpu history: %+v", got)
			}
			if got.MemUsed == nil || *got.MemUsed != 0 || got.MemTotal == nil || *got.MemTotal != 1073741824 || got.MemPercent == nil || *got.MemPercent != 0 {
				t.Fatalf("bad zero-mem success: %+v", got)
			}
			if got.MemAvg == nil || *got.MemAvg != 0 || got.MemPeak == nil || *got.MemPeak != 0 || len(got.MemHistory) != 1 {
				t.Fatalf("bad zero-mem history: %+v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("health result was not published")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRouterOSStatusHistoryWindowStaleRebootAndConfigRemoval(t *testing.T) {
	activeCfg := RouterOSAPIConfig{
		State:    "connecting",
		Host:     "172.16.10.1",
		Port:     8728,
		Username: "test-user",
		Password: "secret-canary",
	}
	prev := routerOSConfigOverride
	routerOSConfigOverride = func() RouterOSAPIConfig { return activeCfg }
	t.Cleanup(func() { routerOSConfigOverride = prev })

	now := time.Unix(1_700_000_000, 0)
	nextSnap := routeros.ResourceSnapshot{
		State:         "connected",
		UptimeSeconds: int64Ptr(100),
		CPULoad:       intPtr(20),
		CPUCount:      intPtr(2),
		MemUsed:       uint64Ptr(250),
		MemTotal:      uint64Ptr(1000),
	}
	done := make(chan struct{}, 16)
	s := &RouterOSAPIService{
		now: func() time.Time { return now },
		probe: func(context.Context, string, string, string) routeros.ResourceSnapshot {
			snap := nextSnap
			done <- struct{}{}
			return snap
		},
	}

	waitProbe := func() {
		t.Helper()
		s.Status()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("probe did not run")
		}
		deadline := time.Now().Add(time.Second)
		for {
			s.mu.Lock()
			busy := s.checking
			s.mu.Unlock()
			if !busy {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("probe did not settle")
			}
			time.Sleep(time.Millisecond)
		}
	}

	// Sample 1: 20% CPU, 25% Mem
	waitProbe()
	// Advance 20s -> Sample 2: 40% CPU, 55% Mem
	now = now.Add(20 * time.Second)
	nextSnap = routeros.ResourceSnapshot{State: "connected", UptimeSeconds: int64Ptr(120), CPULoad: intPtr(40), CPUCount: intPtr(2), MemUsed: uint64Ptr(550), MemTotal: uint64Ptr(1000)}
	waitProbe()

	got := s.Status()
	if *got.CPULoad != 40 || *got.CpuAvg != 30 || *got.CpuPeak != 40 || len(got.CpuHistory) != 2 {
		t.Fatalf("unexpected 2-sample summary: %+v", got)
	}
	if *got.MemPercent != 55 || *got.MemAvg != 40 || *got.MemPeak != 55 || len(got.MemHistory) != 2 {
		t.Fatalf("unexpected 2-sample mem summary: %+v", got)
	}

	// Stale check (>30s without a new sample): Status() marks stale and does NOT append duplicate sample.
	now = now.Add(35 * time.Second)
	s.mu.Lock()
	s.next = now.Add(time.Minute) // suppress new probe while testing stale read
	s.mu.Unlock()
	staleGot := s.Status()
	if !staleGot.Stale || len(staleGot.CpuHistory) != 2 || len(staleGot.MemHistory) != 2 {
		t.Fatalf("expected stale without duplicate append: %+v", staleGot)
	}

	// Reboot detected (uptime dropped from 120 to 5): clears prior history.
	s.mu.Lock()
	s.next = now
	s.mu.Unlock()
	nextSnap = routeros.ResourceSnapshot{State: "connected", UptimeSeconds: int64Ptr(5), CPULoad: intPtr(10), CPUCount: intPtr(2), MemUsed: uint64Ptr(100), MemTotal: uint64Ptr(1000)}
	waitProbe()
	afterReboot := s.Status()
	if len(afterReboot.CpuHistory) != 1 || afterReboot.CpuHistory[0] != 10 || *afterReboot.CpuPeak != 10 {
		t.Fatalf("expected reboot to reset history, got %+v", afterReboot)
	}
	if len(afterReboot.MemHistory) != 1 || afterReboot.MemHistory[0] != 10 || *afterReboot.MemPeak != 10 {
		t.Fatalf("expected reboot to reset mem history, got %+v", afterReboot)
	}

	// Unreachable probe sets cpuLoad=nil and memUsed=nil (never 0%), serialized as JSON null.
	now = now.Add(20 * time.Second)
	nextSnap = routeros.ResourceSnapshot{State: "unreachable"}
	waitProbe()
	unavail := s.Status()
	if unavail.CPULoad != nil || unavail.MemUsed != nil || unavail.MemTotal != nil || unavail.MemPercent != nil {
		t.Fatalf("expected nil cpuLoad/mem when unreachable, got %+v", unavail)
	}
	blob, err := json.Marshal(unavail)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if val, exists := decoded["cpuLoad"]; !exists || val != nil {
		t.Fatalf("expected explicit json null for cpuLoad, got exists=%v val=%v", exists, val)
	}
	if val, exists := decoded["memUsed"]; !exists || val != nil {
		t.Fatalf("expected explicit json null for memUsed, got exists=%v val=%v", exists, val)
	}

	// Config removal clears history completely.
	activeCfg = RouterOSAPIConfig{State: "not_configured"}
	removed := s.Status()
	if removed.State != "not_configured" || len(removed.CpuHistory) != 0 || removed.CpuAvg != nil || removed.CpuPeak != nil || len(removed.MemHistory) != 0 || removed.MemAvg != nil || removed.MemPeak != nil {
		t.Fatalf("expected cleared history on config removal, got %+v", removed)
	}
}

func TestRouterOSStatusHistoryCapAndSlidingWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := &RouterOSAPIService{}
	for i := 1; i <= 70; i++ {
		s.samples = append(s.samples, routerOSCPUSample{
			at:   now.Add(time.Duration(i) * 10 * time.Second),
			load: i,
		})
	}
	// At t + 700s (all 70 samples within 15m=900s), cap at 60 keeps samples 11..70.
	avg, peak, hist := s.pruneAndSummarizeLocked(now.Add(700 * time.Second))
	if len(hist) != 60 || hist[0] != 11 || hist[59] != 70 || *peak != 70 || *avg != 40.5 {
		t.Fatalf("cap 60 failed: len=%d first=%d last=%d peak=%v avg=%v", len(hist), hist[0], hist[len(hist)-1], peak, avg)
	}
	// Advance to t + 1400s (cutoff = t + 500s -> keeps samples 50..70, 21 samples).
	_, _, pruned := s.pruneAndSummarizeLocked(now.Add(1400 * time.Second))
	if len(pruned) != 21 || pruned[0] != 50 || pruned[20] != 70 {
		t.Fatalf("15m sliding window failed: len=%d first=%d last=%d", len(pruned), pruned[0], pruned[len(pruned)-1])
	}
}

func TestEvaluateManagedNATHealth(t *testing.T) {
	inbounds := []*model.Inbound{
		{Id: 1, Enable: true, Protocol: model.VLESS, Port: 443},
		{Id: 2, Enable: true, Protocol: model.Protocol("hysteria2"), Port: 8443},
		{Id: 3, Enable: false, Protocol: model.VLESS, Port: 9443},
	}

	t.Run("in_sync with active inbounds and panel service rule", func(t *testing.T) {
		rules := []routeros.ManagedNATRule{
			{InboundID: 1, Protocol: "tcp", DstPort: "443", Disabled: false},
			{InboundID: 2, Protocol: "udp", DstPort: "8443", Disabled: false},
			{InboundID: 3, Protocol: "tcp", DstPort: "9443", Disabled: true},
			{Service: "panel", Protocol: "tcp", DstPort: "2053", Disabled: false},
		}
		got := EvaluateManagedNATHealth(rules, inbounds, true, 2053, false, 2096)
		if !got.Enabled || got.State != "in_sync" || got.RuleCount != 4 || got.MissingCount != 0 || got.OrphanCount != 0 {
			t.Fatalf("unexpected health: %+v", got)
		}
	})

	t.Run("drift detects missing and orphan rules", func(t *testing.T) {
		rules := []routeros.ManagedNATRule{
			{InboundID: 1, Protocol: "tcp", DstPort: "443", Disabled: false},
			{InboundID: 99, Protocol: "tcp", DstPort: "1234", Disabled: false},
		}
		got := EvaluateManagedNATHealth(rules, inbounds, true, 2053, false, 2096)
		if !got.Enabled || got.State != "drift" || got.RuleCount != 2 || got.MissingCount != 2 || got.OrphanCount != 1 {
			t.Fatalf("unexpected drift health: %+v", got)
		}
	})
}
