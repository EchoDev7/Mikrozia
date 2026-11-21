package service

import (
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/sys"
)

func TestContainerHistoryNeverUsesHostCPUOrRAMFallback(t *testing.T) {
	old := systemMetrics
	systemMetrics = newMetricHistory()
	t.Cleanup(func() { systemMetrics = old })
	svc := &ServerService{}
	now := time.Now()
	host := &Status{Cpu: 75}
	host.Mem.Current, host.Mem.Total = 750, 1000
	svc.AppendStatusSample(now, host)
	if cpu := svc.AggregateSystemMetric("containerCpu", 2, 60); len(cpu) != 0 {
		t.Fatalf("host CPU leaked into container history: %v", cpu)
	}
	if mem := svc.AggregateSystemMetric("containerMem", 2, 60); len(mem) != 0 {
		t.Fatalf("host RAM leaked into container history: %v", mem)
	}
	limit := uint64(1000)
	container := &Status{
		ResourceScope: "container", Cpu: 12.5,
		ContainerMem: &sys.ContainerMemory{Used: 250, Limit: &limit},
	}
	container.Mem.Current, container.Mem.Total = 900, 1000
	svc.AppendStatusSample(now.Add(2*time.Second), container)
	cpu := svc.AggregateSystemMetric("containerCpu", 2, 60)
	mem := svc.AggregateSystemMetric("containerMem", 2, 60)
	if len(cpu) != 1 || cpu[0]["v"] != 12.5 || len(mem) != 1 || mem[0]["v"] != 25.0 {
		t.Fatalf("container series cpu=%v mem=%v", cpu, mem)
	}
	svc.AppendStatusSample(now.Add(4*time.Second), &Status{
		ResourceScope: "container", UnavailableMetrics: []string{"cpu"},
		ContainerMem: &sys.ContainerMemory{Used: 250},
	})
	if got := svc.AggregateSystemMetric("containerCpu", 2, 60); len(got) != 1 {
		t.Fatalf("unavailable CPU added sample: %v", got)
	}
	if got := svc.AggregateSystemMetric("containerMem", 2, 60); len(got) != 1 {
		t.Fatalf("unlimited RAM invented percentage: %v", got)
	}
}
