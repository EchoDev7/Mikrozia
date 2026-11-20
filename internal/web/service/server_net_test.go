package service

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/net"
)

func TestCollectNetIO_ContainerVethAndExclusions(t *testing.T) {
	now := time.Unix(1700000010, 0)
	prev := &Status{
		T:             now.Add(-2 * time.Second),
		ResourceScope: "container",
	}
	prev.NetTraffic.Sent = 1000
	prev.NetTraffic.Recv = 2000
	prev.NetTraffic.PktSent = 10
	prev.NetTraffic.PktRecv = 20

	stats := []net.IOCountersStat{
		{Name: "lo", BytesSent: 99999, BytesRecv: 99999},
		{Name: "tun0", BytesSent: 88888, BytesRecv: 88888},
		{Name: "tap0", BytesSent: 77777, BytesRecv: 77777},
		{Name: "wg0", BytesSent: 66666, BytesRecv: 66666},
		{Name: "tailscale0", BytesSent: 55555, BytesRecv: 55555},
		{Name: "zt0", BytesSent: 44444, BytesRecv: 44444},
		{Name: "veth0", BytesSent: 3000, BytesRecv: 6000, PacketsSent: 30, PacketsRecv: 60},
	}

	cur := &Status{T: now, ResourceScope: "container"}
	collectNetIO(stats, nil, cur, prev, now)

	if !cur.MetricAvailable("netIO") {
		t.Fatalf("expected netIO available in container scope, got %v", cur.UnavailableMetrics)
	}
	if cur.NetTraffic.Sent != 3000 || cur.NetTraffic.Recv != 6000 {
		t.Fatalf("expected only veth0 traffic (3000/6000), got %+v", cur.NetTraffic)
	}
	if cur.NetIO.Up != 1000 || cur.NetIO.Down != 2000 {
		t.Fatalf("expected rates (1000/2000), got %+v", cur.NetIO)
	}
	if cur.NetIO.PktUp != 10 || cur.NetIO.PktDown != 20 {
		t.Fatalf("expected packet rates (10/20), got %+v", cur.NetIO)
	}
}

func TestCollectNetIO_HostExcludesVethAndBridges(t *testing.T) {
	now := time.Unix(1700000010, 0)

	t.Run("host scope excludes veth and reports unavailable when no physical nic", func(t *testing.T) {
		stats := []net.IOCountersStat{
			{Name: "lo", BytesSent: 100, BytesRecv: 100},
			{Name: "veth123", BytesSent: 5000, BytesRecv: 5000},
			{Name: "docker0", BytesSent: 5000, BytesRecv: 5000},
			{Name: "br-abc", BytesSent: 5000, BytesRecv: 5000},
		}
		cur := &Status{T: now, ResourceScope: ""}
		collectNetIO(stats, nil, cur, nil, now)
		if cur.MetricAvailable("netIO") {
			t.Fatalf("expected netIO unavailable when only virtual interfaces exist")
		}
	})

	t.Run("host-networked container with docker/br interface excludes veth", func(t *testing.T) {
		stats := []net.IOCountersStat{
			{Name: "eth0", BytesSent: 1000, BytesRecv: 2000},
			{Name: "docker0", BytesSent: 9000, BytesRecv: 9000},
			{Name: "veth0", BytesSent: 9000, BytesRecv: 9000},
		}
		cur := &Status{T: now, ResourceScope: "container"}
		collectNetIO(stats, nil, cur, nil, now)
		if !cur.MetricAvailable("netIO") {
			t.Fatalf("expected eth0 to keep netIO available")
		}
		if cur.NetTraffic.Sent != 1000 || cur.NetTraffic.Recv != 2000 {
			t.Fatalf("expected only eth0 counted (1000/2000), got %+v", cur.NetTraffic)
		}
	})
}

func TestCollectNetIO_UnavailableAndCounterReset(t *testing.T) {
	now := time.Unix(1700000010, 0)

	t.Run("error marks netIO unavailable", func(t *testing.T) {
		cur := &Status{T: now, ResourceScope: "container"}
		collectNetIO(nil, errors.New("boom"), cur, nil, now)
		if !slices.Contains(cur.UnavailableMetrics, "netIO") {
			t.Fatalf("expected netIO in UnavailableMetrics, got %v", cur.UnavailableMetrics)
		}
	})

	t.Run("counter reset does not underflow or spike", func(t *testing.T) {
		prev := &Status{T: now.Add(-2 * time.Second), ResourceScope: "container"}
		prev.NetTraffic.Sent = 50000
		prev.NetTraffic.Recv = 80000
		prev.NetTraffic.PktSent = 500
		prev.NetTraffic.PktRecv = 800

		stats := []net.IOCountersStat{
			{Name: "veth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 1, PacketsRecv: 2},
		}
		cur := &Status{T: now, ResourceScope: "container"}
		collectNetIO(stats, nil, cur, prev, now)

		if cur.NetIO.Up != 0 || cur.NetIO.Down != 0 || cur.NetIO.PktUp != 0 || cur.NetIO.PktDown != 0 {
			t.Fatalf("expected zero rates on counter reset (no underflow/spike), got %+v", cur.NetIO)
		}
	})

	t.Run("AppendStatusSample skips net metrics when netIO unavailable", func(t *testing.T) {
		old := systemMetrics
		systemMetrics = newMetricHistory()
		defer func() { systemMetrics = old }()

		svc := &ServerService{}
		s := &Status{UnavailableMetrics: []string{"netIO"}}
		svc.AppendStatusSample(now, s)
		if got := systemMetrics.aggregate("netUp", 2, 10); len(got) != 0 {
			t.Fatalf("expected no netUp samples when unavailable, got %v", got)
		}
		if got := systemMetrics.aggregate("netDown", 2, 10); len(got) != 0 {
			t.Fatalf("expected no netDown samples when unavailable, got %v", got)
		}
	})
}
