package routeros

import (
	"bufio"
	"context"
	"net"
	"reflect"
	"testing"
)

func TestContainerSnapshotRequiresOneExactMarkedContainer(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows [][]string
		want string
	}{
		{"one", [][]string{{"!re", "=.id=*A", "=name=mikrozia:v1.0.2-mikrozia", "=comment=[Mikrozia:container] Container", "=status=running", "=interface=veth-mikrozia", "=logging=yes"}}, "connected"},
		{"none", nil, "identity_unverified"},
		{"duplicate", [][]string{{"!re", "=.id=*A", "=comment=[Mikrozia:container] Container"}, {"!re", "=.id=*B", "=comment=[Mikrozia:container] Container"}}, "identity_unverified"},
		{"wrong comment", [][]string{{"!re", "=.id=*A", "=comment=other"}}, "identity_unverified"},
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
			got := c.ContainerSnapshot(context.Background(), "192.168.88.1", "user", "password", "[Mikrozia:container] Container")
			if got.State != tc.want {
				t.Fatalf("state=%q, want %q", got.State, tc.want)
			}
			if got.State == "connected" && (got.ID != "*A" || got.Name != "mikrozia:v1.0.2-mikrozia" || got.Status != "running" || got.Interface != "veth-mikrozia" || !got.Logging) {
				t.Fatalf("snapshot=%+v", got)
			}
			wantCommand := []string{"/container/print", "=.proplist=.id,name,comment,status,interface,logging", "?comment=[Mikrozia:container] Container"}
			if command := <-seen; !reflect.DeepEqual(command, wantCommand) {
				t.Fatalf("query=%q, want %q", command, wantCommand)
			}
		})
	}
}
