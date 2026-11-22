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
		rows      [][]string
		wantState string
		wantWrite bool
	}{
		{
			name:      "verified",
			rows:      [][]string{{"!re", "=.id=*A", "=name=mikrozia:v1", "=comment=[Mikrozia:container] Container", "=status=running"}},
			wantState: "accepted",
			wantWrite: true,
		},
		{
			name:      "changed id",
			rows:      [][]string{{"!re", "=.id=*B", "=name=mikrozia:v1", "=comment=[Mikrozia:container] Container", "=status=running"}},
			wantState: "identity_unverified",
			wantWrite: false,
		},
		{
			name:      "changed name",
			rows:      [][]string{{"!re", "=.id=*A", "=name=mikrozia:v2", "=comment=[Mikrozia:container] Container", "=status=running"}},
			wantState: "identity_unverified",
			wantWrite: false,
		},
		{
			name: "duplicate comment",
			rows: [][]string{
				{"!re", "=.id=*A", "=name=mikrozia:v1", "=comment=[Mikrozia:container] Container", "=status=running"},
				{"!re", "=.id=*B", "=name=mikrozia:v1-copy", "=comment=[Mikrozia:container] Container", "=status=running"},
			},
			wantState: "identity_unverified",
			wantWrite: false,
		},
		{
			name:      "stopped",
			rows:      [][]string{{"!re", "=.id=*A", "=name=mikrozia:v1", "=comment=[Mikrozia:container] Container", "=status=stopped"}},
			wantState: "not_running",
			wantWrite: false,
		},
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
				for _, row := range tc.rows {
					_ = writeSentence(server, row)
				}
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
