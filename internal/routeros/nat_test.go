package routeros

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

type fakeNATServer struct {
	rules       []map[string]string
	nextID      int
	failOnCmd   string
	failAttempt int
	commands    [][]string
}

func (f *fakeNATServer) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	c1, c2 := net.Pipe()
	go f.serve(c2)
	return c1, nil
}

func (f *fakeNATServer) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	cmdCount := map[string]int{}
	for {
		words, err := readSentence(r)
		if err != nil || len(words) == 0 {
			return
		}
		f.commands = append(f.commands, words)
		cmd := words[0]
		cmdCount[cmd]++
		attrs := map[string]string{}
		for _, w := range words[1:] {
			if strings.HasPrefix(w, "=") {
				k, v, _ := strings.Cut(w[1:], "=")
				attrs[k] = v
			}
		}
		if f.failOnCmd != "" && cmd == f.failOnCmd && (f.failAttempt == 0 || cmdCount[cmd] == f.failAttempt) {
			_ = writeSentence(conn, []string{"!trap", "=message=simulated failure"})
			_ = writeSentence(conn, []string{"!done"})
			continue
		}
		switch cmd {
		case "/login":
			_ = writeSentence(conn, []string{"!done"})
		case "/ip/firewall/nat/print":
			for _, row := range f.rules {
				out := []string{"!re"}
				for k, v := range row {
					out = append(out, "="+k+"="+v)
				}
				_ = writeSentence(conn, out)
			}
			_ = writeSentence(conn, []string{"!done"})
		case "/ip/firewall/nat/add":
			f.nextID++
			id := "*" + string(rune('0'+f.nextID))
			attrs[".id"] = id
			f.rules = append(f.rules, attrs)
			_ = writeSentence(conn, []string{"!done", "=ret=" + id})
		case "/ip/firewall/nat/set":
			for i, row := range f.rules {
				if row[".id"] == attrs[".id"] {
					for k, v := range attrs {
						f.rules[i][k] = v
					}
				}
			}
			_ = writeSentence(conn, []string{"!done"})
		case "/ip/firewall/nat/remove":
			var kept []map[string]string
			for _, row := range f.rules {
				if row[".id"] != attrs[".id"] {
					kept = append(kept, row)
				}
			}
			f.rules = kept
			_ = writeSentence(conn, []string{"!done"})
		default:
			_ = writeSentence(conn, []string{"!done"})
		}
	}
}

func TestReconcileInboundNAT_LifecycleAndOwnership(t *testing.T) {
	srv := &fakeNATServer{
		rules: []map[string]string{
			{".id": "*99", "chain": "dstnat", "action": "dst-nat", "protocol": "tcp", "dst-port": "2222", "comment": "operator-manual-rule", "disabled": "false"},
		},
	}
	client := Client{Dial: srv.dial}
	cfg := NATConnConfig{Host: "172.16.10.1", Port: 8728, Username: "mikrozia", Password: "secret"}

	// 1. Add inbound 5 (tcp/443)
	res := client.ReconcileInboundNAT(context.Background(), cfg, 5, []NATRuleSpec{
		{InboundID: 5, Protocol: "tcp", DstPort: 443, ToAddress: "172.16.10.2", ToPort: 443, InInterfaceList: "WAN"},
	})
	if res.State != "ok" || res.Added != 1 || res.Updated != 0 || res.Removed != 0 {
		t.Fatalf("add reconcile = %+v", res)
	}
	if len(srv.rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(srv.rules))
	}

	// 2. Disable inbound 5 -> sets disabled=yes instead of deleting
	res = client.ReconcileInboundNAT(context.Background(), cfg, 5, []NATRuleSpec{
		{InboundID: 5, Protocol: "tcp", DstPort: 443, ToAddress: "172.16.10.2", ToPort: 443, InInterfaceList: "WAN", Disabled: true},
	})
	if res.State != "ok" || res.Updated != 1 || res.Added != 0 {
		t.Fatalf("disable reconcile = %+v", res)
	}

	// 3. Delete inbound 5 -> removes only inbound 5 rule, preserves operator manual rule
	res = client.DeleteInboundNAT(context.Background(), cfg, 5)
	if res.State != "ok" || res.Removed != 1 {
		t.Fatalf("delete reconcile = %+v", res)
	}
	if len(srv.rules) != 1 || srv.rules[0]["comment"] != "operator-manual-rule" {
		t.Fatalf("operator manual rule was disturbed: %+v", srv.rules)
	}
}

func TestReconcileInboundNAT_DetectsConflictAndFailsClosed(t *testing.T) {
	srv := &fakeNATServer{
		rules: []map[string]string{
			{".id": "*10", "chain": "dstnat", "action": "dst-nat", "protocol": "tcp", "dst-port": "443", "comment": "manual-https", "disabled": "false"},
		},
	}
	client := Client{Dial: srv.dial}
	cfg := NATConnConfig{Host: "172.16.10.1", Port: 8728, Username: "mikrozia", Password: "secret"}

	res := client.ReconcileInboundNAT(context.Background(), cfg, 7, []NATRuleSpec{
		{InboundID: 7, Protocol: "tcp", DstPort: 443, ToAddress: "172.16.10.2", ToPort: 443},
	})
	if res.State != "conflict" {
		t.Fatalf("expected conflict, got %+v", res)
	}
	if len(srv.rules) != 1 {
		t.Fatalf("conflict must not mutate rules: %+v", srv.rules)
	}
}

func TestReconcileInboundNAT_RollsBackOnPartialFailure(t *testing.T) {
	srv := &fakeNATServer{failOnCmd: "/ip/firewall/nat/add", failAttempt: 2}
	client := Client{Dial: srv.dial}
	cfg := NATConnConfig{Host: "172.16.10.1", Port: 8728, Username: "mikrozia", Password: "secret"}

	res := client.ReconcileInboundNAT(context.Background(), cfg, 9, []NATRuleSpec{
		{InboundID: 9, Protocol: "tcp", DstPort: 8443, ToAddress: "172.16.10.2", ToPort: 8443},
		{InboundID: 9, Protocol: "udp", DstPort: 8443, ToAddress: "172.16.10.2", ToPort: 8443},
	})
	if res.State != "write_failed" || !res.RolledBack {
		t.Fatalf("expected write_failed with rollback, got %+v", res)
	}
	if len(srv.rules) != 0 {
		t.Fatalf("expected rollback to remove first rule, left with %+v", srv.rules)
	}
}
