package routeros

import (
	"bufio"
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHealthRequiresAuthenticatedReadOnlyReply(t *testing.T) {
	for _, tc := range []struct{ name, login, query, want string }{
		{"success", "!done", "!re|=uptime=1h|=cpu-load=14|=cpu-count=4|=cpu-frequency=2400\n!done", "connected"},
		{"empty health", "!done", "!done", "invalid_reply"},
		{"missing cpu-load", "!done", "!re|=uptime=1h\n!done", "invalid_reply"},
		{"authentication", "!trap|=message=invalid credentials", "", "authentication_failed"},
		{"permission", "!done", "!trap|=category=9|=message=not enough permissions\n!done", "permission_denied"},
		{"trap", "!done", "!trap|=category=1|=message=failed\n!done", "query_failed"},
		{"malformed", "!done", "unexpected|=message=failed", "invalid_reply"},
		{"legacy challenge", "!done|=ret=challenge", "", "authentication_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			commands := make(chan [][]string, 1)
			go func() {
				defer server.Close()
				r := bufio.NewReader(server)
				login, _ := readSentence(r)
				seen := [][]string{login}
				for _, s := range strings.Split(tc.login, "\n") {
					_ = writeSentence(server, strings.Split(s, "|"))
				}
				if tc.query != "" {
					q, _ := readSentence(r)
					seen = append(seen, q)
					for _, s := range strings.Split(tc.query, "\n") {
						if writeSentence(server, strings.Split(s, "|")) != nil {
							break
						}
					}
				}
				commands <- seen
			}()
			c := Client{Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if network != "tcp" || address != "192.168.88.1:8728" {
					t.Errorf("unsafe dial: %s %s", network, address)
				}
				return client, nil
			}}
			got := c.Health(context.Background(), "192.168.88.1", "fixture-user", "fixture-password")
			if got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
			seen := <-commands
			if !reflect.DeepEqual(seen[0], []string{"/login", "=name=fixture-user", "=password=fixture-password"}) {
				t.Fatal("unexpected login sentence")
			}
			if len(seen) == 2 && !reflect.DeepEqual(seen[1], []string{"/system/resource/print", "=.proplist=uptime,cpu-load,cpu-count,cpu-frequency,total-memory,free-memory"}) {
				t.Fatalf("non-allowlisted query: %q", seen[1])
			}
		})
	}
}

func TestResourceSnapshotStrictParsingAndNullableFields(t *testing.T) {
	runMock := func(t *testing.T, reply string) ResourceSnapshot {
		t.Helper()
		client, server := net.Pipe()
		t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
		go func() {
			defer server.Close()
			r := bufio.NewReader(server)
			_, _ = readSentence(r)
			_ = writeSentence(server, []string{"!done"})
			_, _ = readSentence(r)
			for _, s := range strings.Split(reply, "\n") {
				if writeSentence(server, strings.Split(s, "|")) != nil {
					break
				}
			}
		}()
		c := Client{Dial: func(context.Context, string, string) (net.Conn, error) { return client, nil }}
		return c.ResourceSnapshot(context.Background(), "192.168.88.1", "fixture-user", "fixture-password")
	}

	t.Run("valid zero cpu-load with valid count and frequency", func(t *testing.T) {
		snap := runMock(t, "!re|=uptime=2d3h4m5s|=cpu-load=0|=cpu-count=2|=cpu-frequency=2400\n!done")
		if snap.State != "connected" || snap.CPULoad == nil || *snap.CPULoad != 0 {
			t.Fatalf("unexpected snap: %+v", snap)
		}
		if snap.CPUCount == nil || *snap.CPUCount != 2 {
			t.Fatalf("cpuCount = %v, want 2", snap.CPUCount)
		}
		if snap.CPUFrequencyMhz == nil || *snap.CPUFrequencyMhz != 2400 {
			t.Fatalf("cpuFrequencyMhz = %v, want 2400", snap.CPUFrequencyMhz)
		}
		if snap.UptimeSeconds == nil || *snap.UptimeSeconds != 2*86400+3*3600+4*60+5 {
			t.Fatalf("uptimeSeconds = %v", snap.UptimeSeconds)
		}
	})

	t.Run("zero or missing frequency and invalid cpu-count become nil", func(t *testing.T) {
		snap := runMock(t, "!re|=uptime=1h|=cpu-load=42|=cpu-count=0|=cpu-frequency=0\n!done")
		if snap.State != "connected" || snap.CPULoad == nil || *snap.CPULoad != 42 {
			t.Fatalf("unexpected snap: %+v", snap)
		}
		if snap.CPUCount != nil {
			t.Fatalf("expected nil cpuCount for 0, got %d", *snap.CPUCount)
		}
		if snap.CPUFrequencyMhz != nil {
			t.Fatalf("expected nil cpuFrequencyMhz for 0, got %d", *snap.CPUFrequencyMhz)
		}
	})

	for _, bad := range []struct{ name, reply string }{
		{"missing cpu-load", "!re|=uptime=1h|=cpu-count=4\n!done"},
		{"non-numeric cpu-load", "!re|=uptime=1h|=cpu-load=abc\n!done"},
		{"negative cpu-load", "!re|=uptime=1h|=cpu-load=-1\n!done"},
		{"out-of-range cpu-load", "!re|=uptime=1h|=cpu-load=101\n!done"},
	} {
		t.Run(bad.name, func(t *testing.T) {
			snap := runMock(t, bad.reply)
			if snap.State != "invalid_reply" || snap.CPULoad != nil {
				t.Fatalf("expected invalid_reply with nil CPULoad, got %+v", snap)
			}
		})
	}

	t.Run("valid zero-used and normal RouterOS memory", func(t *testing.T) {
		zeroUsed := runMock(t, "!re|=uptime=1h|=cpu-load=5|=total-memory=1073741824|=free-memory=1073741824\n!done")
		if zeroUsed.MemUsed == nil || *zeroUsed.MemUsed != 0 || zeroUsed.MemTotal == nil || *zeroUsed.MemTotal != 1073741824 {
			t.Fatalf("zero-used memory mismatch: %+v", zeroUsed)
		}

		normal := runMock(t, "!re|=uptime=1h|=cpu-load=14|=total-memory=1000|=free-memory=250\n!done")
		if normal.MemUsed == nil || *normal.MemUsed != 750 || normal.MemTotal == nil || *normal.MemTotal != 1000 {
			t.Fatalf("normal memory mismatch: %+v", normal)
		}
	})

	for _, badMem := range []struct{ name, reply string }{
		{"zero total-memory", "!re|=uptime=1h|=cpu-load=5|=total-memory=0|=free-memory=0\n!done"},
		{"free greater than total", "!re|=uptime=1h|=cpu-load=5|=total-memory=1000|=free-memory=1200\n!done"},
		{"missing free-memory", "!re|=uptime=1h|=cpu-load=5|=total-memory=1000\n!done"},
		{"malformed free-memory", "!re|=uptime=1h|=cpu-load=5|=total-memory=1000|=free-memory=abc\n!done"},
	} {
		t.Run(badMem.name, func(t *testing.T) {
			snap := runMock(t, badMem.reply)
			if snap.MemUsed != nil || snap.MemTotal != nil {
				t.Fatalf("expected nil memory on %s, got %+v", badMem.name, snap)
			}
		})
	}
}

func TestHealthCancellationAndNetworkErrorsAreSafe(t *testing.T) {
	c := Client{Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("redacted fixture error")
	}}
	if got := c.Health(context.Background(), "192.168.88.1", "fixture-user", "fixture-password"); got != "unreachable" {
		t.Fatalf("state = %q", got)
	}
	client, server := net.Pipe()
	defer server.Close()
	c.Dial = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got := c.Health(ctx, "192.168.88.1", "fixture-user", "fixture-password"); got != "unreachable" {
		t.Fatalf("timeout state = %q", got)
	}
}

func TestSentenceLengthBoundariesAndLimits(t *testing.T) {
	for _, n := range []int{1, 127, 128, 16383, 16384, maxWord} {
		var b strings.Builder
		want := []string{strings.Repeat("a", n)}
		if err := writeSentence(&b, want); err != nil {
			t.Fatal(err)
		}
		got, err := readSentence(bufio.NewReader(strings.NewReader(b.String())))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("length %d round trip failed: %v", n, err)
		}
	}
	for _, wire := range []string{"\xf8", "\xf0\xff\xff\xff\xff", "\x04a"} {
		if _, err := readSentence(bufio.NewReader(strings.NewReader(wire))); err == nil {
			t.Fatal("accepted malformed or oversized frame")
		}
	}
}
