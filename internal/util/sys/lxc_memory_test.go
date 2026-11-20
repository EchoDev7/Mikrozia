package sys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadContainerMemory_CgroupV2(t *testing.T) {
	t.Run("finite limit", func(t *testing.T) {
		cg := t.TempDir()
		writeFixture(t, filepath.Join(cg, "memory.current"), "134217728\n")
		writeFixture(t, filepath.Join(cg, "memory.max"), "536870912\n")
		got := readContainerMemory(t.TempDir(), cg, true, false, true)
		if got == nil || got.Used != 134217728 || got.Limit == nil || *got.Limit != 536870912 {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("unlimited max", func(t *testing.T) {
		cg := t.TempDir()
		writeFixture(t, filepath.Join(cg, "memory.current"), "67108864\n")
		writeFixture(t, filepath.Join(cg, "memory.max"), "max\n")
		got := readContainerMemory(t.TempDir(), cg, true, false, true)
		if got == nil || got.Used != 67108864 || got.Limit != nil {
			t.Fatalf("got = %+v", got)
		}
		raw, _ := json.Marshal(got)
		if string(raw) != `{"used":67108864,"limit":null}` {
			t.Fatalf("json = %s", raw)
		}
	})

	t.Run("malformed or missing returns nil", func(t *testing.T) {
		cg := t.TempDir()
		writeFixture(t, filepath.Join(cg, "memory.current"), "not-a-num\n")
		writeFixture(t, filepath.Join(cg, "memory.max"), "536870912\n")
		if got := readContainerMemory(t.TempDir(), cg, true, false, true); got != nil {
			t.Fatalf("expected nil on malformed usage, got %+v", got)
		}

		cg2 := t.TempDir()
		writeFixture(t, filepath.Join(cg2, "memory.current"), "1024\n")
		if got := readContainerMemory(t.TempDir(), cg2, true, false, true); got != nil {
			t.Fatalf("expected nil on missing memory.max, got %+v", got)
		}

		writeFixture(t, filepath.Join(cg2, "memory.max"), "0\n")
		if got := readContainerMemory(t.TempDir(), cg2, true, false, true); got != nil {
			t.Fatalf("expected nil on zero limit, got %+v", got)
		}
	})
}

func TestReadContainerMemory_CgroupV1AndNonContainerHost(t *testing.T) {
	t.Run("cgroup v1 finite limit", func(t *testing.T) {
		proc, cg := t.TempDir(), t.TempDir()
		writeFixture(t, filepath.Join(proc, "1/cgroup"), "4:memory:/\n")
		writeFixture(t, filepath.Join(cg, "memory/memory.usage_in_bytes"), "104857600\n")
		writeFixture(t, filepath.Join(cg, "memory/memory.limit_in_bytes"), "268435456\n")
		got := readContainerMemory(proc, cg, false, true, false)
		if got == nil || got.Used != 104857600 || got.Limit == nil || *got.Limit != 268435456 {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("cgroup v1 unlimited sentinel", func(t *testing.T) {
		proc, cg := t.TempDir(), t.TempDir()
		writeFixture(t, filepath.Join(proc, "1/cgroup"), "4:memory:/\n")
		writeFixture(t, filepath.Join(cg, "memory/memory.usage_in_bytes"), "104857600\n")
		writeFixture(t, filepath.Join(cg, "memory/memory.limit_in_bytes"), "9223372036854771712\n")
		got := readContainerMemory(proc, cg, false, true, false)
		if got == nil || got.Used != 104857600 || got.Limit != nil {
			t.Fatalf("got = %+v", got)
		}
	})

	t.Run("cgroup v1 malformed or missing returns nil", func(t *testing.T) {
		proc, cg := t.TempDir(), t.TempDir()
		writeFixture(t, filepath.Join(proc, "1/cgroup"), "4:memory:/\n")
		writeFixture(t, filepath.Join(cg, "memory/memory.usage_in_bytes"), "abc\n")
		writeFixture(t, filepath.Join(cg, "memory/memory.limit_in_bytes"), "268435456\n")
		if got := readContainerMemory(proc, cg, false, true, false); got != nil {
			t.Fatalf("expected nil on malformed v1 usage, got %+v", got)
		}
		if got := readContainerMemory(proc, t.TempDir(), false, true, false); got != nil {
			t.Fatalf("expected nil on missing v1 files, got %+v", got)
		}
	})

	t.Run("non-container host never exposes host memory", func(t *testing.T) {
		proc, cg := t.TempDir(), t.TempDir()
		writeFixture(t, filepath.Join(proc, "1/cgroup"), "4:memory:/system.slice/docker.service\n")
		writeFixture(t, filepath.Join(cg, "memory.current"), "999999999\n")
		writeFixture(t, filepath.Join(cg, "memory.max"), "1999999999\n")
		writeFixture(t, filepath.Join(cg, "memory/memory.usage_in_bytes"), "999999999\n")
		writeFixture(t, filepath.Join(cg, "memory/memory.limit_in_bytes"), "1999999999\n")
		if got := readContainerMemory(proc, cg, false, false, false); got != nil {
			t.Fatalf("expected nil on non-container host, got %+v", got)
		}
		if got := readContainerMemory(proc, cg, true, true, false); got != nil {
			t.Fatalf("expected nil when PID 1 is not at container root, got %+v", got)
		}
	})
}
