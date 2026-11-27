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
	services    []map[string]string
	conntrack   []map[string]string
	health      []map[string]string
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
		case "/ip/service/print":
			for _, row := range f.services {
				out := []string{"!re"}
				for k, v := range row {
					out = append(out, "="+k+"="+v)
				}
				_ = writeSentence(conn, out)
			}
			_ = writeSentence(conn, []string{"!done"})
		case "/ip/firewall/connection/tracking/print":
			for _, row := range f.conntrack {
				out := []string{"!re"}
				for k, v := range row {
					out = append(out, "="+k+"="+v)
				}
				_ = writeSentence(conn, out)
			}
			_ = writeSentence(conn, []string{"!done"})
		case "/system/health/print":
			for _, row := range f.health {
				out := []string{"!re"}
				for k, v := range row {
					out = append(out, "="+k+"="+v)
				}
				_ = writeSentence(conn, out)
			}
			_ = writeSentence(conn, []string{"!done"})
		case "/interface/monitor-traffic":
			_ = writeSentence(conn, []string{
				"!re",
				"=rx-bits-per-second=3200000",
				"=tx-bits-per-second=2400000",
				"=rx-packets-per-second=200",
				"=tx-packets-per-second=150",
			})
			_ = writeSentence(conn, []string{"!done"})
		case "/interface/print":
			_ = writeSentence(conn, []string{
				"!re",
				"=rx-byte=4420410450",
				"=tx-byte=4115375411",
				"=rx-packet=4317313",
				"=tx-packet=5242580",
			})
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

func TestExtractWANInterfaceFromGateway(t *testing.T) {
	cases := map[string]string{
		"172.31.0.1%ether1":  "ether1",
		"10.0.0.1%vlan-100":  "vlan-100",
		"pppoe-out1":         "pppoe-out1",
		"lte1":               "lte1",
		"172.31.0.1":         "",
		"":                   "",
		"bad;rm -rf /":       "",
		"172.31.0.1%bad;cmd": "",
	}
	for in, want := range cases {
		if got := ExtractWANInterfaceFromGateway(in); got != want {
			t.Errorf("ExtractWANInterfaceFromGateway(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseManagedInboundID(t *testing.T) {
	if id, ok := parseManagedInboundID("mikrozia:managed:inbound:42:tcp"); !ok || id != 42 {
		t.Fatalf("got (%d, %v), want (42, true)", id, ok)
	}
	if _, ok := parseManagedInboundID("manual rule"); ok {
		t.Fatal("manual rule should not parse as managed inbound")
	}
	if _, ok := parseManagedInboundID("mikrozia:managed:inbound:0:udp"); ok {
		t.Fatal("inbound id 0 should be rejected")
	}
}

func TestReconcileServiceNAT_LifecycleAndConflict(t *testing.T) {
	srv := &fakeNATServer{
		rules: []map[string]string{
			{".id": "*50", "chain": "dstnat", "action": "dst-nat", "protocol": "tcp", "dst-port": "9999", "comment": "manual-service", "disabled": "false"},
		},
	}
	client := Client{Dial: srv.dial}
	cfg := NATConnConfig{Host: "172.16.10.1", Port: 8728, Username: "mikrozia", Password: "secret"}

	if got := ManagedServiceNATComment("panel"); got != "mikrozia:managed:service:panel" {
		t.Fatalf("ManagedServiceNATComment(panel) = %q", got)
	}

	// 1. Add panel service rule on port 2053
	res := client.ReconcileServiceNAT(context.Background(), cfg, "panel", 2053, "172.16.10.2", "", false)
	if res.State != "ok" || res.Added != 1 || res.Updated != 0 {
		t.Fatalf("add service rule = %+v", res)
	}

	// 2. Update panel service port to 2083 in-place
	res = client.ReconcileServiceNAT(context.Background(), cfg, "panel", 2083, "172.16.10.2", "ether1", false)
	if res.State != "ok" || res.Updated != 1 || res.Added != 0 {
		t.Fatalf("update service rule = %+v", res)
	}

	// 3. Conflict with manual rule on port 9999 fails closed
	res = client.ReconcileServiceNAT(context.Background(), cfg, "sub", 9999, "172.16.10.2", "", false)
	if res.State != "conflict" {
		t.Fatalf("expected conflict, got %+v", res)
	}
}

func TestRunDiagnostics_ReadOnlyChecks(t *testing.T) {
	srv := &fakeNATServer{
		rules: []map[string]string{
			{".id": "*1", "chain": "srcnat", "action": "masquerade", "disabled": "false"},
			{".id": "*2", "chain": "dstnat", "action": "dst-nat", "protocol": "tcp", "dst-port": "443", "comment": "manual-https", "disabled": "false"},
		},
		services: []map[string]string{
			{"name": "www", "port": "80", "disabled": "false"},
			{"name": "xray-linux-amd6", "port": "443", "disabled": "false", "dynamic": "true"},
			{"name": "x-ui", "port": "11879", "disabled": "false"},
		},
		conntrack: []map[string]string{
			{"enabled": "auto", "total-entries": "8000", "max-entries": "10000"},
		},
		health: []map[string]string{
			{"name": "cpu-temperature", "value": "52", "type": "C"},
			{"name": "voltage", "value": "24.1", "type": "V"},
		},
	}
	client := Client{Dial: srv.dial}
	cfg := NATConnConfig{Host: "172.16.10.1", Port: 8728, Username: "mikrozia", Password: "secret"}

	res := client.RunDiagnostics(context.Background(), cfg, "172.16.10.2", []InboundPortCheck{
		{InboundID: 3, Protocol: "tcp", Port: 443},
	})
	if res.State != "ok" {
		t.Fatalf("RunDiagnostics state = %q, want ok", res.State)
	}
	byKey := map[string]DiagnosticCheck{}
	for _, c := range res.Checks {
		byKey[c.Key] = c
	}
	if byKey["masquerade"].Status != "ok" {
		t.Fatalf("masquerade check = %+v, want ok", byKey["masquerade"])
	}
	if byKey["conflicts"].Status != "warn" {
		t.Fatalf("conflicts check = %+v, want warn", byKey["conflicts"])
	}
	if byKey["services"].Status != "warn" {
		t.Fatalf("services check = %+v, want warn", byKey["services"])
	}
	if byKey["conntrack"].Status != "warn" {
		t.Fatalf("conntrack check = %+v, want warn (80%%)", byKey["conntrack"])
	}
	if byKey["hardware"].Status != "ok" || !strings.Contains(byKey["hardware"].Detail, "cpu-temperature: 52C") {
		t.Fatalf("hardware check = %+v, want ok with cpu-temperature", byKey["hardware"])
	}
}

func TestCheckPort80Status(t *testing.T) {
	cfg := NATConnConfig{Host: "172.16.10.1", Port: 8728, Username: "mikrozia", Password: "secret"}

	// Enabled port 80 dst-nat + enabled www:80 service -> open + conflict
	srvOpen := &fakeNATServer{
		rules: []map[string]string{
			{".id": "*1", "chain": "dstnat", "action": "dst-nat", "protocol": "tcp", "dst-port": "80", "disabled": "false"},
		},
		services: []map[string]string{
			{"name": "www", "port": "80", "disabled": "false"},
			{"name": "www-ssl", "port": "443", "disabled": "true"},
		},
	}
	snap := (Client{Dial: srvOpen.dial}).CheckPort80AndServices(context.Background(), cfg)
	if snap.Port80Status != "open" || snap.ServiceConflict != "www:80" {
		t.Fatalf("CheckPort80AndServices(open) = %+v, want open + www:80", snap)
	}

	// Disabled port 80 dst-nat + disabled services -> closed + no conflict
	srvClosed := &fakeNATServer{
		rules: []map[string]string{
			{".id": "*1", "chain": "dstnat", "action": "dst-nat", "protocol": "tcp", "dst-port": "80", "disabled": "true"},
			{".id": "*2", "chain": "dstnat", "action": "dst-nat", "protocol": "tcp", "dst-port": "443", "disabled": "false"},
		},
		services: []map[string]string{
			{"name": "www", "port": "80", "disabled": "true"},
			{"name": "www-ssl", "port": "443", "disabled": "true"},
		},
	}
	snap = (Client{Dial: srvClosed.dial}).CheckPort80AndServices(context.Background(), cfg)
	if snap.Port80Status != "closed" || snap.ServiceConflict != "" {
		t.Fatalf("CheckPort80AndServices(closed) = %+v, want closed + empty conflict", snap)
	}
}

func TestReadInterfaceTraffic(t *testing.T) {
	srv := &fakeNATServer{}
	cfg := NATConnConfig{Host: "172.16.10.1", Port: 8728, Username: "mikrozia", Password: "secret"}
	got := (Client{Dial: srv.dial}).ReadInterfaceTraffic(context.Background(), cfg, "veth-mikrozia", "[Mikrozia:container] Container")
	if !got.OK {
		t.Fatalf("expected OK=true, got %+v", got)
	}
	if got.UpBytesPS != 400000 || got.DownBytesPS != 300000 {
		t.Fatalf("rates = (up=%d, down=%d), want (400000, 300000)", got.UpBytesPS, got.DownBytesPS)
	}
	if got.SentBytes != 4420410450 || got.RecvBytes != 4115375411 {
		t.Fatalf("totals = (sent=%d, recv=%d), want (4420410450, 4115375411)", got.SentBytes, got.RecvBytes)
	}
}
