package routeros

import (
	"bufio"
	"context"
	"net"
	"reflect"
	"testing"
)

func TestRestartContainerRechecksIdentityBeforeSingleCommand(t *testing.T) {
	for _, tc := range []struct {
		name      string
		id        string
		status    string
		wantState string
		wantWrite bool
	}{
		{"verified", "*A", "running", "accepted", true},
		{"changed id", "*B", "running", "identity_unverified", false},
		{"stopped", "*A", "stopped", "not_running", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			writes := make(chan []string, 1)
			go func() {
				r := bufio.NewReader(server)
				_, _ = readSentence(r)
				_ = writeSentence(server, []string{"!done"})
				_, _ = readSentence(r)
				_ = writeSentence(server, []string{"!re", "=.id=" + tc.id, "=name=mikrozia:v1", "=comment=[Mikrozia:container] Container", "=status=" + tc.status})
				_ = writeSentence(server, []string{"!done"})
				if tc.wantWrite {
					command, _ := readSentence(r)
					writes <- command
					_ = writeSentence(server, []string{"!done"})
				}
			}()
			c := Client{Dial: func(context.Context, string, string) (net.Conn, error) { return client, nil }}
			got := c.RestartContainer(context.Background(), "192.168.88.1", "operator", "password", "[Mikrozia:container] Container", "*A", "mikrozia:v1")
			if got.State != tc.wantState {
				t.Fatalf("state=%s, want %s", got.State, tc.wantState)
			}
			if tc.wantWrite {
				want := []string{"/container/restart", "=.id=*A"}
				if command := <-writes; !reflect.DeepEqual(command, want) {
					t.Fatalf("command=%q, want %q", command, want)
				}
			} else if len(writes) != 0 {
				t.Fatal("unexpected restart command")
			}
		})
	}
}

func TestRestartContainerLostReplyIsUncertainWithoutRetry(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	writes := make(chan []string, 1)
	go func() {
		r := bufio.NewReader(server)
		_, _ = readSentence(r)
		_ = writeSentence(server, []string{"!done"})
		_, _ = readSentence(r)
		_ = writeSentence(server, []string{"!re", "=.id=*A", "=name=mikrozia:v1", "=comment=[Mikrozia:container] Container", "=status=running"})
		_ = writeSentence(server, []string{"!done"})
		command, _ := readSentence(r)
		writes <- command
		_ = server.Close() // RouterOS may restart the panel before its reply arrives.
	}()
	dials := 0
	c := Client{Dial: func(context.Context, string, string) (net.Conn, error) {
		dials++
		return client, nil
	}}
	got := c.RestartContainer(context.Background(), "192.168.88.1", "operator", "password", "[Mikrozia:container] Container", "*A", "mikrozia:v1")
	if got.State != "uncertain" {
		t.Fatalf("state=%s, want uncertain", got.State)
	}
	if dials != 1 {
		t.Fatalf("dials=%d, want one", dials)
	}
	if command := <-writes; !reflect.DeepEqual(command, []string{"/container/restart", "=.id=*A"}) {
		t.Fatalf("command=%q, want one exact restart", command)
	}
}
