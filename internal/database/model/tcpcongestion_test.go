package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStripUnsupportedRouterOSTcpCongestion(t *testing.T) {
	t.Run("removes sockopt.tcpcongestion=bbr and preserves other fields", func(t *testing.T) {
		stream := `{
			"network": "tcp",
			"sockopt": {
				"tcpcongestion": "bbr",
				"tcpUserTimeout": 10000,
				"tcpMaxSeg": 1440,
				"tproxy": "tproxy",
				"customSockopt": [{"level":"6","opt":"1","value":"1","type":"int"}]
			}
		}`
		out, paths, changed := StripUnsupportedRouterOSTcpCongestion(stream)
		if !changed {
			t.Fatal("expected changed=true")
		}
		if len(paths) != 1 || paths[0] != "sockopt.tcpcongestion" {
			t.Fatalf("expected paths=[sockopt.tcpcongestion], got %v", paths)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(out), &parsed); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		sockopt := parsed["sockopt"].(map[string]any)
		if _, has := sockopt["tcpcongestion"]; has {
			t.Fatalf("tcpcongestion must be omitted: %s", out)
		}
		if sockopt["tcpUserTimeout"] != float64(10000) {
			t.Fatalf("tcpUserTimeout must remain, got %#v", sockopt["tcpUserTimeout"])
		}
		if sockopt["tcpMaxSeg"] != float64(1440) {
			t.Fatalf("tcpMaxSeg must remain, got %#v", sockopt["tcpMaxSeg"])
		}
		if sockopt["tproxy"] != "tproxy" {
			t.Fatalf("tproxy must remain, got %#v", sockopt["tproxy"])
		}
		if _, has := sockopt["customSockopt"]; !has {
			t.Fatalf("customSockopt must remain: %s", out)
		}
	})

	t.Run("removes case-insensitive key tcpCongestion and mixed-case BBR", func(t *testing.T) {
		stream := `{"network":"tcp","sockopt":{"tcpCongestion":" BBR ","tcpFastOpen":true}}`
		out, paths, changed := StripUnsupportedRouterOSTcpCongestion(stream)
		if !changed {
			t.Fatal("expected changed=true")
		}
		if len(paths) != 1 || paths[0] != "sockopt.tcpcongestion" {
			t.Fatalf("expected paths=[sockopt.tcpcongestion], got %v", paths)
		}
		if strings.Contains(strings.ToLower(out), "tcpcongestion") {
			t.Fatalf("tcpCongestion must be removed: %s", out)
		}
		if !strings.Contains(out, `"tcpFastOpen": true`) && !strings.Contains(out, `"tcpFastOpen":true`) {
			t.Fatalf("tcpFastOpen must remain: %s", out)
		}
	})

	t.Run("preserves cubic and reno", func(t *testing.T) {
		for _, algo := range []string{"cubic", "reno"} {
			stream := `{"network":"tcp","sockopt":{"tcpcongestion":"` + algo + `"}}`
			out, paths, changed := StripUnsupportedRouterOSTcpCongestion(stream)
			if changed || len(paths) != 0 || out != stream {
				t.Fatalf("%s must be preserved unchanged: changed=%v paths=%v out=%s", algo, changed, paths, out)
			}
		}
	})

	t.Run("preserves finalmask.quicParams.congestion=bbr", func(t *testing.T) {
		stream := `{"network":"raw","finalmask":{"quicParams":{"congestion":"bbr"}}}`
		out, paths, changed := StripUnsupportedRouterOSTcpCongestion(stream)
		if changed || len(paths) != 0 || out != stream {
			t.Fatalf("finalmask quicParams congestion must not be touched: changed=%v out=%s", changed, out)
		}
	})

	t.Run("invalid JSON returns unchanged without panic", func(t *testing.T) {
		stream := `{not valid json`
		out, paths, changed := StripUnsupportedRouterOSTcpCongestion(stream)
		if changed || len(paths) != 0 || out != stream {
			t.Fatalf("invalid JSON must return unchanged: changed=%v out=%s", changed, out)
		}
	})

	t.Run("missing sockopt produces no change", func(t *testing.T) {
		stream := `{"network":"ws","wsSettings":{"path":"/"}}`
		out, paths, changed := StripUnsupportedRouterOSTcpCongestion(stream)
		if changed || len(paths) != 0 || out != stream {
			t.Fatalf("missing sockopt must return unchanged: changed=%v out=%s", changed, out)
		}
	})

	t.Run("strips echSockopt.tcpcongestion=bbr and keeps other echSockopt fields", func(t *testing.T) {
		stream := `{
			"network": "tcp",
			"security": "tls",
			"tlsSettings": {
				"echSockopt": {
					"tcpcongestion": "bbr",
					"dialerProxy": "proxy-out",
					"domainStrategy": "UseIPv4",
					"tcpFastOpen": true,
					"tcpMptcp": true
				}
			}
		}`
		out, paths, changed := StripUnsupportedRouterOSTcpCongestion(stream)
		if !changed {
			t.Fatal("expected changed=true for echSockopt")
		}
		if len(paths) != 1 || paths[0] != "tlsSettings.echSockopt.tcpcongestion" {
			t.Fatalf("expected paths=[tlsSettings.echSockopt.tcpcongestion], got %v", paths)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(out), &parsed); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		ech := parsed["tlsSettings"].(map[string]any)["echSockopt"].(map[string]any)
		if _, has := ech["tcpcongestion"]; has {
			t.Fatalf("echSockopt.tcpcongestion must be removed: %s", out)
		}
		if ech["dialerProxy"] != "proxy-out" || ech["domainStrategy"] != "UseIPv4" || ech["tcpFastOpen"] != true || ech["tcpMptcp"] != true {
			t.Fatalf("echSockopt sibling fields must remain: %#v", ech)
		}
	})

	t.Run("preserves echSockopt cubic and reno", func(t *testing.T) {
		stream := `{"tlsSettings":{"echSockopt":{"tcpcongestion":"cubic","dialerProxy":"out"}}}`
		out, paths, changed := StripUnsupportedRouterOSTcpCongestion(stream)
		if changed || len(paths) != 0 || out != stream {
			t.Fatalf("echSockopt cubic must remain unchanged: changed=%v out=%s", changed, out)
		}
	})
}

func TestGenXrayInboundConfig_OmitsRouterOSKernelBbrButPreservesDBRow(t *testing.T) {
	original := `{"network":"tcp","sockopt":{"tcpcongestion":"bbr","tcpUserTimeout":10000}}`
	in := Inbound{
		Protocol:       VLESS,
		Port:           443,
		Listen:         "0.0.0.0",
		Tag:            "in-bbr-test",
		Settings:       `{"clients":[],"decryption":"none"}`,
		StreamSettings: original,
	}

	cfg := in.GenXrayInboundConfig()
	if strings.Contains(strings.ToLower(string(cfg.StreamSettings)), "tcpcongestion") {
		t.Fatalf("GenXrayInboundConfig must omit tcpcongestion=bbr from runtime: %s", cfg.StreamSettings)
	}
	if !strings.Contains(string(cfg.StreamSettings), `"tcpUserTimeout"`) {
		t.Fatalf("GenXrayInboundConfig must preserve tcpUserTimeout: %s", cfg.StreamSettings)
	}
	if in.StreamSettings != original {
		t.Fatalf("Inbound.StreamSettings must remain untouched: got %s want %s", in.StreamSettings, original)
	}
}
