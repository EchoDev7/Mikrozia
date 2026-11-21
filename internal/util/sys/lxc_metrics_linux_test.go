//go:build linux

package sys

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const lxcMountFixture = "1 0 0:92 /proc/meminfo /proc/meminfo rw - fuse.lxcfs lxcfs rw\n2 0 0:92 /proc/cpuinfo /proc/cpuinfo rw - fuse.lxcfs lxcfs rw\n3 0 0:92 /proc/uptime /proc/uptime rw - fuse.lxcfs lxcfs rw\n"

func lxcFixture(t *testing.T) *LXCReader {
	t.Helper()
	root := t.TempDir()
	r := &LXCReader{ProcRoot: filepath.Join(root, "proc"), CgroupRoot: filepath.Join(root, "cgroup")}
	fixtures := map[string]string{
		filepath.Join(r.ProcRoot, "self/mountinfo"):          lxcMountFixture + "4 0 0:28 / " + r.CgroupRoot + " rw - cgroup2 cgroup rw\n",
		filepath.Join(r.ProcRoot, "1/cgroup"):                "0::/init.scope\n",
		filepath.Join(r.ProcRoot, "meminfo"):                 "MemTotal: 2097152 kB\nMemAvailable: 1966080 kB\nSwapTotal: 524288 kB\nSwapFree: 505384 kB\n",
		filepath.Join(r.ProcRoot, "cpuinfo"):                 "processor : 0\ncpu cores : 16\n\nprocessor : 1\ncpu cores : 16\n",
		filepath.Join(r.ProcRoot, "uptime"):                  "52624.93 52624.93\n",
		filepath.Join(r.CgroupRoot, "cpu.stat"):              "usage_usec 222772076\nuser_usec 101515374\n",
		filepath.Join(r.CgroupRoot, "cpu.max"):               "max 100000\n",
		filepath.Join(r.CgroupRoot, "cpuset.cpus.effective"): "0-1\n",
		filepath.Join(r.CgroupRoot, "memory.current"):        "75497472\n",
		filepath.Join(r.CgroupRoot, "memory.max"):            "1073741824\n",
	}
	for path, content := range fixtures {
		fixtureWrite(t, path, content)
	}
	return r
}

func fixtureWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLXCResourcesIgnoreHostTopologyAndInheritedMax(t *testing.T) {
	r := lxcFixture(t)
	got, err := r.Read(time.Unix(100, 0))
	if err != nil || got == nil {
		t.Fatalf("sample=%+v err=%v", got, err)
	}
	if got.CPUs != 2 || got.SwapTotal != 512<<20 || got.SwapUsed != 19357696 || got.Uptime != 52624 {
		t.Fatalf("wrong container resources: %+v", got)
	}
	if !slices.Equal(got.Unavailable, []string{"cpu"}) {
		t.Fatalf("first sample=%+v", got)
	}
}

func TestLXCCPUUsesWholeContainerDeltaAndCapacity(t *testing.T) {
	for _, tc := range []struct {
		name, quota string
		delta       uint64
		want        float64
	}{
		{"one CPU of two", "max 100000\n", 1000000, 50},
		{"idle is a valid zero", "max 100000\n", 0, 0},
		{"half CPU quota", "50000 100000\n", 125000, 25},
		{"quota above visible CPUs", "800000 100000\n", 1000000, 50},
		{"saturated interval bounded", "max 100000\n", 2100000, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := lxcFixture(t)
			fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.max"), tc.quota)
			fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec 1000000\n")
			r.Read(time.Unix(100, 0))
			fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec "+strconv.FormatUint(1000000+tc.delta, 10)+"\n")
			got, err := r.Read(time.Unix(101, 0))
			if err != nil || got.CPU != tc.want || slices.Contains(got.Unavailable, "cpu") {
				t.Fatalf("sample=%+v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestLXCCounterClockAndCapacityChangesResetBaseline(t *testing.T) {
	for _, scenario := range []string{"counter reset", "zero interval", "backward interval", "capacity change", "missing counter", "scope change"} {
		t.Run(scenario, func(t *testing.T) {
			r := lxcFixture(t)
			r.Read(time.Unix(100, 0))
			at := time.Unix(101, 0)
			switch scenario {
			case "counter reset":
				fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec 1\n")
			case "zero interval":
				at = time.Unix(100, 0)
			case "backward interval":
				at = time.Unix(99, 0)
			case "capacity change":
				fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.max"), "100000 100000\n")
			case "missing counter":
				os.Remove(filepath.Join(r.CgroupRoot, "cpu.stat"))
				r.Read(at)
				fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec 222772076\n")
			case "scope change":
				fixtureWrite(t, filepath.Join(r.ProcRoot, "self/mountinfo"), "1 0 0:1 / / rw - ext4 /dev/root rw\n")
				r.Read(at)
				fixtureWrite(t, filepath.Join(r.ProcRoot, "self/mountinfo"), lxcMountFixture+"4 0 0:28 / "+r.CgroupRoot+" rw - cgroup2 cgroup rw\n")
			}
			got, err := r.Read(at)
			if err != nil || !slices.Contains(got.Unavailable, "cpu") {
				t.Fatalf("invalid interval=%+v err=%v", got, err)
			}
			usage, _ := readUsage(filepath.Join(r.CgroupRoot, "cpu.stat"))
			fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec "+strconv.FormatUint(usage+500000, 10)+"\n")
			got, err = r.Read(at.Add(time.Second))
			want := 25.0
			if scenario == "capacity change" {
				want = 50
			}
			if err != nil || got.CPU != want || slices.Contains(got.Unavailable, "cpu") {
				t.Fatalf("recovery=%+v err=%v", got, err)
			}
		})
	}
}

func TestLXCInvalidMetricsNeverUseHostFallback(t *testing.T) {
	for _, tc := range []struct{ path, content, metric string }{
		{"meminfo", "SwapTotal: 1 kB\nSwapFree: 2 kB\n", "swap"},
		{"meminfo", "SwapTotal: 18446744073709551615 kB\nSwapFree: 0 kB\n", "swap"},
		{"meminfo", "SwapTotal: 1 MB\nSwapFree: 0 kB\n", "swap"},
		{"meminfo", "SwapTotal: 1 kB\nSwapTotal: 1 kB\nSwapFree: 0 kB\n", "swap"},
		{"meminfo", "SwapTotal: 1 kB\n", "swap"},
		{"uptime", "NaN 0\n", "uptime"},
		{"uptime", "+Inf 0\n", "uptime"},
		{"uptime", "-1 0\n", "uptime"},
		{"uptime", "18446744073709551616 0\n", "uptime"},
		{"uptime", "", "uptime"},
		{"cpuset.cpus.effective", "", "cpu"},
		{"cpuset.cpus.effective", "0-1,1\n", "cpu"},
	} {
		t.Run(tc.metric+"/"+tc.content, func(t *testing.T) {
			r := lxcFixture(t)
			path := filepath.Join(r.ProcRoot, tc.path)
			if tc.path == "cpuset.cpus.effective" {
				path = filepath.Join(r.CgroupRoot, tc.path)
			}
			fixtureWrite(t, path, tc.content)
			got, err := r.Read(time.Unix(100, 0))
			if err != nil || got == nil || !slices.Contains(got.Unavailable, tc.metric) {
				t.Fatalf("invalid sample=%+v err=%v", got, err)
			}
			for _, v := range got.Unavailable {
				if v != "cpu" && v != "swap" && v != "uptime" {
					t.Fatalf("unrelated metric changed: %s", v)
				}
			}
		})
	}
}

func TestLXCCPURejectsMalformedCgroupData(t *testing.T) {
	for _, tc := range []struct{ path, value string }{
		{"cpu.max", "max 0"},
		{"cpu.max", "0 100000"},
		{"cpu.max", "NaN 100000"},
		{"cpu.max", "max"},
		{"cpu.stat", "user_usec 123"},
		{"cpu.stat", "usage_usec -1"},
		{"cpu.stat", "usage_usec 1\nusage_usec 2"},
	} {
		t.Run(tc.path+"/"+tc.value, func(t *testing.T) {
			r := lxcFixture(t)
			r.Read(time.Unix(100, 0))
			fixtureWrite(t, filepath.Join(r.CgroupRoot, tc.path), tc.value)
			got, err := r.Read(time.Unix(101, 0))
			if err != nil || !slices.Contains(got.Unavailable, "cpu") || math.IsNaN(got.CPU) || math.IsInf(got.CPU, 0) {
				t.Fatalf("bad CPU=%+v err=%v", got, err)
			}
		})
	}
}

func TestLXCZeroSwapIsValidAndRAMIsNotParsed(t *testing.T) {
	r := lxcFixture(t)
	fixtureWrite(t, filepath.Join(r.ProcRoot, "meminfo"), "MemTotal: not-a-number kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")
	got, err := r.Read(time.Unix(100, 0))
	if err != nil || got.SwapTotal != 0 || got.SwapUsed != 0 || slices.Contains(got.Unavailable, "swap") {
		t.Fatalf("disabled swap=%+v err=%v", got, err)
	}
}

func TestLXCEachProcSourceRequiresItsOwnMount(t *testing.T) {
	for _, tc := range []struct{ path, metric string }{{"meminfo", "swap"}, {"cpuinfo", "cpu"}, {"uptime", "uptime"}} {
		t.Run(tc.path, func(t *testing.T) {
			r := lxcFixture(t)
			var lines []string
			for _, line := range strings.Split(lxcMountFixture, "\n") {
				if !strings.Contains(line, "/proc/"+tc.path) {
					lines = append(lines, line)
				}
			}
			fixtureWrite(t, filepath.Join(r.ProcRoot, "self/mountinfo"), strings.Join(lines, "\n"))
			got, err := r.Read(time.Unix(100, 0))
			if err != nil || got == nil || !slices.Contains(got.Unavailable, tc.metric) {
				t.Fatalf("unverified mount=%+v err=%v", got, err)
			}
		})
	}
}

func TestNativeMetricsKeepOriginalPath(t *testing.T) {
	r := lxcFixture(t)
	fixtureWrite(t, filepath.Join(r.ProcRoot, "self/mountinfo"), "1 0 0:1 / / rw - ext4 /dev/root rw\n")
	os.RemoveAll(r.CgroupRoot)
	got, err := r.Read(time.Unix(100, 0))
	if err != nil || got != nil || r.UseScopedHistory() {
		t.Fatalf("native=%+v err=%v", got, err)
	}
}

func TestHostCgroupV2NeverClaimsContainerScopeWithoutRouterOSIdentity(t *testing.T) {
	r := routerOSContainerFixture(t)
	fixtureWrite(t, filepath.Join(r.ProcRoot, "1/cgroup"), "0::/\n")
	got, err := r.Read(time.Unix(100, 0))
	if err != nil || got != nil || r.UseScopedHistory() {
		t.Fatalf("host cgroup became container scope: sample=%+v err=%v", got, err)
	}
}

func TestRouterOSContainerRequiresMatchingSelfMembership(t *testing.T) {
	r := routerOSContainerFixture(t)
	fixtureWrite(t, filepath.Join(r.ProcRoot, "self/cgroup"), "0::/system.slice\n")
	got, err := r.Read(time.Unix(100, 0))
	if err != nil || got != nil || r.UseScopedHistory() {
		t.Fatalf("unverified process became container scope: %+v err=%v", got, err)
	}
}

func TestLXCFSFallsBackToItsVerifiedCPUInfoWhenCPUSetIsUnavailable(t *testing.T) {
	r := lxcFixture(t)
	os.Remove(filepath.Join(r.CgroupRoot, "cpuset.cpus.effective"))
	r.Read(time.Unix(100, 0))
	fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec 223772076\n")
	got, err := r.Read(time.Unix(101, 0))
	if err != nil || got.CPU != 50 || got.CPUs != 2 || slices.Contains(got.Unavailable, "cpu") {
		t.Fatalf("LXCFS fallback=%+v err=%v", got, err)
	}
}

func TestUnreadableScopeNeverSelectsNativeHostMetrics(t *testing.T) {
	for _, bad := range []string{"missing", "malformed"} {
		t.Run(bad, func(t *testing.T) {
			r := lxcFixture(t)
			if bad == "missing" {
				os.Remove(filepath.Join(r.ProcRoot, "self/mountinfo"))
			} else {
				fixtureWrite(t, filepath.Join(r.ProcRoot, "self/mountinfo"), "invalid")
			}
			got, err := r.Read(time.Unix(100, 0))
			if err == nil || got == nil || got.Scope != "unknown" || !slices.Equal(got.Unavailable, []string{"cpu", "swap", "uptime"}) || !r.UseScopedHistory() {
				t.Fatalf("unknown=%+v err=%v", got, err)
			}
		})
	}
}

func TestLXCCPURejectsUnverifiedCgroupRoot(t *testing.T) {
	for _, scenario := range []string{"missing mount", "subtree mount", "legacy controller", "host namespace", "missing init membership"} {
		t.Run(scenario, func(t *testing.T) {
			r := lxcFixture(t)
			r.Read(time.Unix(100, 0))
			mounts := lxcMountFixture + "4 0 0:28 / " + r.CgroupRoot + " rw - cgroup2 cgroup rw\n"
			switch scenario {
			case "missing mount":
				mounts = lxcMountFixture
			case "subtree mount":
				mounts = lxcMountFixture + "4 0 0:28 /system.slice " + r.CgroupRoot + " rw - cgroup2 cgroup rw\n"
			case "legacy controller":
				mounts = lxcMountFixture + "4 0 0:28 / " + r.CgroupRoot + " rw - cgroup cgroup rw\n"
			case "host namespace":
				fixtureWrite(t, filepath.Join(r.ProcRoot, "1/cgroup"), "0::/lxc/example/init.scope\n")
			case "missing init membership":
				os.Remove(filepath.Join(r.ProcRoot, "1/cgroup"))
			}
			fixtureWrite(t, filepath.Join(r.ProcRoot, "self/mountinfo"), mounts)
			fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec 223772076\n")
			got, err := r.Read(time.Unix(101, 0))
			if err != nil || got == nil || !slices.Contains(got.Unavailable, "cpu") || got.CPU != 0 || got.SwapTotal != 512<<20 || got.Uptime != 52624 {
				t.Fatalf("unverified cgroup leaked CPU or changed proc metrics: %+v err=%v", got, err)
			}
		})
	}
}

func TestLXCCPUAcceptsContainerInitAtNamespaceRoot(t *testing.T) {
	r := lxcFixture(t)
	fixtureWrite(t, filepath.Join(r.ProcRoot, "1/cgroup"), "0::/\n")
	r.Read(time.Unix(100, 0))
	fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec 223772076\n")
	got, err := r.Read(time.Unix(101, 0))
	if err != nil || got.CPU != 50 || slices.Contains(got.Unavailable, "cpu") {
		t.Fatalf("root init sample=%+v err=%v", got, err)
	}
}

func routerOSContainerFixture(t *testing.T) *LXCReader {
	t.Helper()
	root := t.TempDir()
	r := &LXCReader{ProcRoot: filepath.Join(root, "proc"), CgroupRoot: filepath.Join(root, "cgroup")}
	fixtures := map[string]string{
		filepath.Join(r.ProcRoot, "self/mountinfo"):          "36 35 0:13 / " + r.CgroupRoot + " rw,nosuid,nodev,noexec,relatime - cgroup2 cgroup2 rw\n",
		filepath.Join(r.ProcRoot, "1/cgroup"):                "0::/init\n",
		filepath.Join(r.ProcRoot, "self/cgroup"):             "0::/init\n",
		filepath.Join(r.CgroupRoot, "cpu.stat"):              "usage_usec 1000000\n",
		filepath.Join(r.CgroupRoot, "cpu.max"):               "max 100000\n",
		filepath.Join(r.CgroupRoot, "cpuset.cpus.effective"): "0-1\n",
		filepath.Join(r.CgroupRoot, "memory.current"):        "75497472\n",
		filepath.Join(r.CgroupRoot, "memory.max"):            "1073741824\n",
	}
	for path, content := range fixtures {
		fixtureWrite(t, path, content)
	}
	return r
}

func TestRouterOSContainerCgroupV2ProvidesCPUAndMemoryWithoutLXCFS(t *testing.T) {
	r := routerOSContainerFixture(t)
	first, err := r.Read(time.Unix(100, 0))
	if err != nil || first == nil || first.Scope != "container" || !slices.Contains(first.Unavailable, "cpu") || !slices.Contains(first.Unavailable, "swap") || !slices.Contains(first.Unavailable, "uptime") {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if first.ContainerMem == nil || first.ContainerMem.Used != 75497472 || first.ContainerMem.Limit == nil || *first.ContainerMem.Limit != 1073741824 {
		t.Fatalf("memory=%+v", first.ContainerMem)
	}
	fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec 2000000\n")
	second, err := r.Read(time.Unix(101, 0))
	if err != nil || second.CPU != 50 || second.CPUs != 2 || slices.Contains(second.Unavailable, "cpu") {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestRouterOSContainerCgroupV2QuotaAndInvalidFilesFailSafe(t *testing.T) {
	r := routerOSContainerFixture(t)
	fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.max"), "50000 100000\n")
	r.Read(time.Unix(100, 0))
	fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpu.stat"), "usage_usec 1250000\n")
	got, err := r.Read(time.Unix(101, 0))
	if err != nil || got.CPU != 50 || slices.Contains(got.Unavailable, "cpu") {
		t.Fatalf("quota=%+v err=%v", got, err)
	}
	fixtureWrite(t, filepath.Join(r.CgroupRoot, "cpuset.cpus.effective"), "0-1,1\n")
	got, err = r.Read(time.Unix(102, 0))
	if err != nil || !slices.Contains(got.Unavailable, "cpu") || got.CPU != 0 {
		t.Fatalf("invalid cpuset=%+v err=%v", got, err)
	}
	fixtureWrite(t, filepath.Join(r.CgroupRoot, "memory.current"), "invalid\n")
	got, err = r.Read(time.Unix(103, 0))
	if err != nil || got.ContainerMem != nil || !slices.Contains(got.Unavailable, "containerMem") {
		t.Fatalf("invalid memory=%+v err=%v", got, err)
	}
}

func TestParseCPUSetCount(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
		ok    bool
	}{
		{value: "0-1,4,6-7", want: 5, ok: true},
		{value: "0", want: 1, ok: true},
		{value: "", ok: false},
		{value: "1-0", ok: false},
		{value: "0-1,1", ok: false},
		{value: "0,,1", ok: false},
		{value: "0-18446744073709551614", ok: false},
		{value: "0-18446744073709551615", ok: false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := parseCPUSetCount(tc.value)
			if (err == nil) != tc.ok || got != tc.want {
				t.Fatalf("parseCPUSetCount(%q) = %d, %v", tc.value, got, err)
			}
		})
	}
}

func TestRouterOSContainerRejectsHostDataOnMissingOrInvalidMetrics(t *testing.T) {
	for _, name := range []string{"cpu.stat", "cpu.max", "cpuset.cpus.effective", "memory.current", "memory.max"} {
		for _, missing := range []bool{false, true} {
			t.Run(name+"/missing="+strconv.FormatBool(missing), func(t *testing.T) {
				r := routerOSContainerFixture(t)
				fixtureWrite(t, filepath.Join(r.ProcRoot, "stat"), "cpu 999 0 0 1\n")
				fixtureWrite(t, filepath.Join(r.ProcRoot, "meminfo"), "MemTotal: 999999 kB\nSwapTotal: 999999 kB\nSwapFree: 0 kB\n")
				r.Read(time.Unix(100, 0))
				path := filepath.Join(r.CgroupRoot, name)
				if missing {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else {
					fixtureWrite(t, path, "invalid\n")
				}
				got, err := r.Read(time.Unix(101, 0))
				if err != nil || got == nil || got.Scope != "container" || got.SwapTotal != 0 || !slices.Contains(got.Unavailable, "swap") || !slices.Contains(got.Unavailable, "uptime") {
					t.Fatalf("unexpected scope or host data: %+v, %v", got, err)
				}
				if strings.HasPrefix(name, "memory") {
					if got.ContainerMem != nil || !slices.Contains(got.Unavailable, "containerMem") {
						t.Fatalf("invalid memory became available: %+v", got)
					}
				} else if got.CPU != 0 || !slices.Contains(got.Unavailable, "cpu") {
					t.Fatalf("invalid CPU became available: %+v", got)
				}
			})
		}
	}
}
