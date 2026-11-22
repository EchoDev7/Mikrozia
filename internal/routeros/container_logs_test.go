package routeros

import (
	"bufio"
	"context"
	"net"
	"reflect"
	"testing"
)

func TestContainerLogsExactNameAndBoundedTail(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rows  [][]string
		state string
		count int
	}{
		{"valid", [][]string{{"!re", "=time=2026-11-21 03:00:00", "=container=mikrozia:v1", "=message=first"}, {"!re", "=time=2026-11-21 03:00:01", "=container=mikrozia:v1", "=message=second"}}, "connected", 1},
		{"wrong container", [][]string{{"!re", "=time=now", "=container=other", "=message=secret"}}, "identity_unverified", 0},
		{"missing time", [][]string{{"!re", "=container=mikrozia:v1", "=message=message"}}, "identity_unverified", 0},
		{"permission denied", [][]string{{"!trap", "=category=9", "=message=not enough permissions"}}, "permission_denied", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			seen := make(chan []string, 1)
			go func() {
				r := bufio.NewReader(server)
				_, _ = readSentence(r)
				_ = writeSentence(server, []string{"!done"})
				command, _ := readSentence(r)
				seen <- command
				for _, row := range tc.rows {
					_ = writeSentence(server, row)
				}
				_ = writeSentence(server, []string{"!done"})
			}()
			c := Client{Dial: func(context.Context, string, string) (net.Conn, error) { return client, nil }}
			got := c.ContainerLogs(context.Background(), "192.168.88.1", "user", "password", "mikrozia:v1", 1)
			if got.State != tc.state || len(got.Entries) != tc.count {
				t.Fatalf("result=%+v, want state=%s count=%d", got, tc.state, tc.count)
			}
			if tc.name == "valid" && got.Entries[0].Message != "second" {
				t.Fatalf("tail=%+v", got.Entries)
			}
			want := []string{"/container/log/print", "=.proplist=time,container,message", "?container=mikrozia:v1"}
			if command := <-seen; !reflect.DeepEqual(command, want) {
				t.Fatalf("query=%q, want %q", command, want)
			}
		})
	}
}
