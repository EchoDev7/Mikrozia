package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestSanitizeRuntimeOutboundsTcpCongestion(t *testing.T) {
	raw := json_util.RawMessage(`[
		{
			"tag": "out-stream-bbr",
			"protocol": "vless",
			"streamSettings": {
				"network": "tcp",
				"sockopt": {
					"tcpcongestion": "bbr",
					"tcpUserTimeout": 10000
				},
				"tlsSettings": {
					"echSockopt": {
						"tcpCongestion": "BBR",
						"dialerProxy": "relay"
					}
				},
				"finalmask": {
					"quicParams": {
						"congestion": "bbr"
					}
				}
			}
		},
		{
			"tag": "out-nonstream-cubic",
			"protocol": "freedom",
			"streamSettings": {
				"sockopt": {
					"tcpcongestion": "cubic",
					"domainStrategy": "UseIP"
				}
			}
		},
		{
			"tag": "out-no-sockopt",
			"protocol": "blackhole"
		}
	]`)

	sanitized, warnings := sanitizeRuntimeOutboundsTcpCongestion(raw)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 combined warning for outbound with both paths, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "sockopt.tcpcongestion, tlsSettings.echSockopt.tcpcongestion") {
		t.Fatalf("expected full multi-path string in warning, got: %s", warnings[0])
	}

	var outbounds []map[string]any
	if err := json.Unmarshal(sanitized, &outbounds); err != nil {
		t.Fatalf("unmarshal sanitized outbounds: %v", err)
	}

	first := outbounds[0]["streamSettings"].(map[string]any)
	sockopt := first["sockopt"].(map[string]any)
	if _, has := sockopt["tcpcongestion"]; has {
		t.Fatalf("outbound sockopt.tcpcongestion=bbr must be omitted: %#v", sockopt)
	}
	if sockopt["tcpUserTimeout"] != float64(10000) {
		t.Fatalf("tcpUserTimeout must remain: %#v", sockopt)
	}

	ech := first["tlsSettings"].(map[string]any)["echSockopt"].(map[string]any)
	if _, has := ech["tcpCongestion"]; has {
		t.Fatalf("outbound echSockopt.tcpCongestion=BBR must be omitted: %#v", ech)
	}
	if ech["dialerProxy"] != "relay" {
		t.Fatalf("dialerProxy must remain: %#v", ech)
	}

	fm := first["finalmask"].(map[string]any)["quicParams"].(map[string]any)
	if fm["congestion"] != "bbr" {
		t.Fatalf("finalmask.quicParams.congestion=bbr must be preserved: %#v", fm)
	}

	secondSock := outbounds[1]["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
	if secondSock["tcpcongestion"] != "cubic" {
		t.Fatalf("cubic must be preserved: %#v", secondSock)
	}

	// Ensure original raw slice was not mutated.
	if !strings.Contains(string(raw), `"tcpcongestion": "bbr"`) {
		t.Fatalf("original raw template bytes must remain untouched")
	}
}

func TestGetXrayConfig_OmitsOutboundTemplateBbrWithoutMutatingStoredSetting(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	if err := database.InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	template := `{
		"log": {"loglevel": "warning"},
		"inbounds": [],
		"outbounds": [
			{
				"tag": "direct-bbr",
				"protocol": "freedom",
				"streamSettings": {
					"sockopt": {
						"tcpcongestion": "bbr",
						"tcpMaxSeg": 1400
					}
				}
			}
		]
	}`
	settingSvc := &SettingService{}
	if err := settingSvc.saveSetting("xrayTemplateConfig", template); err != nil {
		t.Fatalf("saveSetting: %v", err)
	}

	// Also insert an inbound with legacy bbr.
	ib := &model.Inbound{
		UserId:         1,
		Enable:         true,
		Tag:            "vless-bbr-in",
		Protocol:       model.VLESS,
		Port:           10443,
		Listen:         "0.0.0.0",
		Settings:       `{"clients":[],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","sockopt":{"tcpcongestion":"bbr","tcpUserTimeout":5000}}`,
		Sniffing:       `{"enabled":false}`,
	}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	xraySvc := &XrayService{}
	cfg, err := xraySvc.GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}

	if strings.Contains(strings.ToLower(string(cfg.OutboundConfigs)), "tcpcongestion") {
		t.Fatalf("runtime OutboundConfigs must omit bbr: %s", cfg.OutboundConfigs)
	}
	if !strings.Contains(string(cfg.OutboundConfigs), "1400") {
		t.Fatalf("tcpMaxSeg must survive in runtime OutboundConfigs: %s", cfg.OutboundConfigs)
	}

	// Verify stored template and stored inbound in DB are untouched.
	storedTemplate, err := settingSvc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	if !strings.Contains(storedTemplate, `"tcpcongestion": "bbr"`) {
		t.Fatalf("stored xrayTemplateConfig must remain untouched: %s", storedTemplate)
	}
	var dbInbound model.Inbound
	if err := database.GetDB().First(&dbInbound, ib.Id).Error; err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if !strings.Contains(dbInbound.StreamSettings, `"tcpcongestion":"bbr"`) {
		t.Fatalf("stored inbound StreamSettings must remain untouched: %s", dbInbound.StreamSettings)
	}

	// Also verify mergeSubscriptionOutbounds + runtime sanitization on subscription outbounds.
	subCfg := &xray.Config{OutboundConfigs: json_util.RawMessage(`[]`)}
	subOutbound := map[string]any{
		"tag":      "sub-bbr",
		"protocol": "vless",
		"streamSettings": map[string]any{
			"sockopt": map[string]any{"tcpcongestion": "bbr", "tcpFastOpen": true},
		},
	}
	mergeSubscriptionOutbounds(subCfg, nil, []any{subOutbound})
	sanitizedSub, _ := sanitizeRuntimeOutboundsTcpCongestion(subCfg.OutboundConfigs)
	if strings.Contains(strings.ToLower(string(sanitizedSub)), "tcpcongestion") {
		t.Fatalf("subscription outbound bbr must be omitted at runtime: %s", sanitizedSub)
	}
}
