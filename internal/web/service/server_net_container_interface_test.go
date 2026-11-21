package service

import (
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/net"
)

// RouterOS exposes loopback plus the container's own veth in this namespace.
func containerNamespaceStats() []net.IOCountersStat {
	return []net.IOCountersStat{
		{Name: "lo", BytesSent: 99999, BytesRecv: 99999, PacketsSent: 999, PacketsRecv: 999},
		{Name: "veth-mikrozia", BytesSent: 3000, BytesRecv: 6000, PacketsSent: 30, PacketsRecv: 60},
	}
}

func TestCollectNetIO_ExplicitContainerInterface(t *testing.T) {
	now := time.Unix(1700000010, 0)

	t.Run("unknown scope with env counts exactly the configured interface", func(t *testing.T) {
		t.Setenv("MIKROZIA_CONTAINER_INTERFACE", "veth-mikrozia")

		prev := &Status{T: now.Add(-2 * time.Second)}
		prev.NetTraffic.Sent = 1000
		prev.NetTraffic.Recv = 2000
		prev.NetTraffic.PktSent = 10
		prev.NetTraffic.PktRecv = 20

		cur := &Status{T: now}
		collectNetIO(containerNamespaceStats(), nil, cur, prev, now)

		if !cur.MetricAvailable("netIO") {
			t.Fatalf("expected netIO available with explicit interface, got %v", cur.UnavailableMetrics)
		}
		if cur.NetTraffic.Sent != 3000 || cur.NetTraffic.Recv != 6000 {
			t.Fatalf("expected veth-mikrozia bytes only (3000/6000), got %+v", cur.NetTraffic)
		}
		if cur.NetTraffic.PktSent != 30 || cur.NetTraffic.PktRecv != 60 {
			t.Fatalf("expected veth-mikrozia packets only (30/60), got %+v", cur.NetTraffic)
		}
		if cur.NetIO.Up != 1000 || cur.NetIO.Down != 2000 {
			t.Fatalf("expected byte rates (1000/2000), got %+v", cur.NetIO)
		}
		if cur.NetIO.PktUp != 10 || cur.NetIO.PktDown != 20 {
			t.Fatalf("expected packet rates (10/20), got %+v", cur.NetIO)
		}
	})

	t.Run("same namespace without env stays unavailable", func(t *testing.T) {
		t.Setenv("MIKROZIA_CONTAINER_INTERFACE", "")

		cur := &Status{T: now}
		collectNetIO(containerNamespaceStats(), nil, cur, nil, now)

		if cur.MetricAvailable("netIO") {
			t.Fatalf("expected netIO unavailable without env")
		}
		if cur.NetTraffic.Sent != 0 || cur.NetTraffic.Recv != 0 || cur.NetIO.Up != 0 || cur.NetIO.Down != 0 {
			t.Fatalf("expected no fabricated traffic, got netTraffic=%+v netIO=%+v", cur.NetTraffic, cur.NetIO)
		}
	})

	t.Run("env naming a missing interface does not fall back", func(t *testing.T) {
		t.Setenv("MIKROZIA_CONTAINER_INTERFACE", "veth-missing")

		cur := &Status{T: now}
		collectNetIO(containerNamespaceStats(), nil, cur, nil, now)

		if cur.MetricAvailable("netIO") {
			t.Fatalf("expected netIO unavailable when the configured interface is absent")
		}
		if cur.NetTraffic.Sent != 0 || cur.NetTraffic.Recv != 0 {
			t.Fatalf("expected no fallback traffic, got %+v", cur.NetTraffic)
		}
	})

	t.Run("invalid env value behaves like unset", func(t *testing.T) {
		t.Setenv("MIKROZIA_CONTAINER_INTERFACE", "veth*")

		cur := &Status{T: now}
		collectNetIO(containerNamespaceStats(), nil, cur, nil, now)

		if cur.MetricAvailable("netIO") {
			t.Fatalf("expected netIO unavailable for a glob value")
		}
	})

	t.Run("host with a physical nic keeps current behavior and never double counts", func(t *testing.T) {
		t.Setenv("MIKROZIA_CONTAINER_INTERFACE", "veth123")

		stats := []net.IOCountersStat{
			{Name: "lo", BytesSent: 100, BytesRecv: 100},
			{Name: "eth0", BytesSent: 1000, BytesRecv: 2000, PacketsSent: 10, PacketsRecv: 20},
			{Name: "docker0", BytesSent: 9000, BytesRecv: 9000},
			{Name: "veth123", BytesSent: 8000, BytesRecv: 8000},
		}
		cur := &Status{T: now}
		collectNetIO(stats, nil, cur, nil, now)

		if !cur.MetricAvailable("netIO") {
			t.Fatalf("expected host behavior to stay available")
		}
		if cur.NetTraffic.Sent != 1000 || cur.NetTraffic.Recv != 2000 {
			t.Fatalf("expected only eth0 counted (1000/2000), got %+v", cur.NetTraffic)
		}
	})

	t.Run("proven container scope keeps its existing rules and ignores env", func(t *testing.T) {
		t.Setenv("MIKROZIA_CONTAINER_INTERFACE", "does-not-exist")

		stats := []net.IOCountersStat{
			{Name: "lo", BytesSent: 99999, BytesRecv: 99999},
			{Name: "veth0", BytesSent: 3000, BytesRecv: 6000, PacketsSent: 30, PacketsRecv: 60},
		}
		cur := &Status{T: now, ResourceScope: "container"}
		collectNetIO(stats, nil, cur, nil, now)

		if !cur.MetricAvailable("netIO") {
			t.Fatalf("expected proven container scope to keep counting veth")
		}
		if cur.NetTraffic.Sent != 3000 || cur.NetTraffic.Recv != 6000 {
			t.Fatalf("expected veth0 traffic, got %+v", cur.NetTraffic)
		}
	})
}

func TestCollectNetIO_ExplicitContainerInterfaceCounterEdges(t *testing.T) {
	t.Setenv("MIKROZIA_CONTAINER_INTERFACE", "veth-mikrozia")
	now := time.Unix(1700000010, 0)

	t.Run("64-bit counters keep exact deltas without overflow", func(t *testing.T) {
		const base = uint64(1) << 40
		prev := &Status{T: now.Add(-2 * time.Second)}
		prev.NetTraffic.Sent = base
		prev.NetTraffic.Recv = base
		prev.NetTraffic.PktSent = 1 << 30
		prev.NetTraffic.PktRecv = 1 << 30

		stats := []net.IOCountersStat{
			{Name: "lo", BytesSent: 1, BytesRecv: 1},
			{Name: "veth-mikrozia", BytesSent: base + 4096, BytesRecv: base + 8192, PacketsSent: 1<<30 + 100, PacketsRecv: 1<<30 + 200},
		}
		cur := &Status{T: now}
		collectNetIO(stats, nil, cur, prev, now)

		if !cur.MetricAvailable("netIO") {
			t.Fatalf("expected netIO available, got %v", cur.UnavailableMetrics)
		}
		if cur.NetTraffic.Sent != base+4096 || cur.NetTraffic.Recv != base+8192 {
			t.Fatalf("expected exact cumulative counters, got %+v", cur.NetTraffic)
		}
		if cur.NetIO.Up != 2048 || cur.NetIO.Down != 4096 {
			t.Fatalf("expected rates (2048/4096), got %+v", cur.NetIO)
		}
		if cur.NetIO.PktUp != 50 || cur.NetIO.PktDown != 100 {
			t.Fatalf("expected packet rates (50/100), got %+v", cur.NetIO)
		}
	})

	t.Run("counter reset yields zero rates and a fresh baseline", func(t *testing.T) {
		prev := &Status{T: now.Add(-2 * time.Second)}
		prev.NetTraffic.Sent = 50000
		prev.NetTraffic.Recv = 80000
		prev.NetTraffic.PktSent = 500
		prev.NetTraffic.PktRecv = 800

		stats := []net.IOCountersStat{
			{Name: "veth-mikrozia", BytesSent: 100, BytesRecv: 200, PacketsSent: 1, PacketsRecv: 2},
		}
		cur := &Status{T: now}
		collectNetIO(stats, nil, cur, prev, now)

		if cur.NetIO.Up != 0 || cur.NetIO.Down != 0 || cur.NetIO.PktUp != 0 || cur.NetIO.PktDown != 0 {
			t.Fatalf("expected zero rates on reset, got %+v", cur.NetIO)
		}

		// The next sample must use the post-reset counters as its baseline.
		next := &Status{T: now.Add(2 * time.Second)}
		stats[0].BytesSent = 1100
		stats[0].BytesRecv = 2200
		stats[0].PacketsSent = 11
		stats[0].PacketsRecv = 22
		collectNetIO(stats, nil, next, cur, next.T)

		if next.NetIO.Up != 500 || next.NetIO.Down != 1000 {
			t.Fatalf("expected rates from the new baseline (500/1000), got %+v", next.NetIO)
		}
		if next.NetIO.PktUp != 5 || next.NetIO.PktDown != 10 {
			t.Fatalf("expected packet rates (5/10), got %+v", next.NetIO)
		}
	})

	t.Run("history receives all four net series once available", func(t *testing.T) {
		old := systemMetrics
		systemMetrics = newMetricHistory()
		defer func() { systemMetrics = old }()

		// aggregate() windows on time.Now(), so use wall-clock timestamps here.
		live := time.Now()
		stats := []net.IOCountersStat{
			{Name: "veth-mikrozia", BytesSent: 3000, BytesRecv: 6000, PacketsSent: 30, PacketsRecv: 60},
		}

		t.Setenv("MIKROZIA_CONTAINER_INTERFACE", "")
		first := &Status{T: live.Add(-2 * time.Second)}
		collectNetIO(stats, nil, first, nil, first.T)
		svc := &ServerService{}
		svc.AppendStatusSample(first.T, first)
		if got := systemMetrics.aggregate("netUp", 2, 10); len(got) != 0 {
			t.Fatalf("expected no history without env, got %v", got)
		}

		t.Setenv("MIKROZIA_CONTAINER_INTERFACE", "veth-mikrozia")
		second := &Status{T: live}
		collectNetIO(stats, nil, second, first, live)
		if !second.MetricAvailable("netIO") {
			t.Fatalf("expected netIO available once the interface is configured")
		}
		svc.AppendStatusSample(second.T, second)

		for _, metric := range []string{"netUp", "netDown", "pktUp", "pktDown"} {
			if got := systemMetrics.aggregate(metric, 2, 10); len(got) == 0 {
				t.Fatalf("expected %s history after netIO became available", metric)
			}
		}
	})
}
