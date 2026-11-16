package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
)

// Geo data is immutable image content on RouterOS. A stored Xray-native
// `geodata` block would download assets at runtime, so it must be preserved in
// the user's template (never deleted) but omitted from the runtime config.
func TestGetXrayConfig_OmitsStoredGeodataWithoutMutatingTemplate(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	if err := database.InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	template := `{
		"log": {"loglevel": "warning"},
		"inbounds": [],
		"outbounds": [{"tag": "direct", "protocol": "freedom"}],
		"geodata": {
			"cron": "0 4 * * *",
			"outbound": "direct",
			"assets": [
				{"url": "https://example.com/geosite_custom.dat", "file": "geosite_custom.dat"}
			]
		}
	}`
	settingSvc := &SettingService{}
	if err := settingSvc.saveSetting("xrayTemplateConfig", template); err != nil {
		t.Fatalf("saveSetting: %v", err)
	}

	xraySvc := &XrayService{}
	cfg, err := xraySvc.GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}
	if len(cfg.Geodata) != 0 {
		t.Fatalf("runtime config must omit stored geodata, got %s", cfg.Geodata)
	}

	runtimeJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal runtime config: %v", err)
	}
	if strings.Contains(string(runtimeJSON), `"geodata"`) {
		t.Fatalf("runtime config JSON must not contain a geodata block: %s", runtimeJSON)
	}

	stored, err := settingSvc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatalf("GetXrayConfigTemplate: %v", err)
	}
	if stored != template {
		t.Fatalf("stored xrayTemplateConfig must remain untouched:\nwant %s\ngot  %s", template, stored)
	}
}

func TestGetXrayConfig_KeepsTemplateWithoutGeodata(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	if err := database.InitDB(dbPath); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	template := `{
		"log": {"loglevel": "warning"},
		"inbounds": [],
		"outbounds": [{"tag": "direct", "protocol": "freedom"}]
	}`
	settingSvc := &SettingService{}
	if err := settingSvc.saveSetting("xrayTemplateConfig", template); err != nil {
		t.Fatalf("saveSetting: %v", err)
	}

	cfg, err := (&XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}
	if len(cfg.Geodata) != 0 {
		t.Fatalf("unexpected geodata in runtime config: %s", cfg.Geodata)
	}
	if !strings.Contains(string(cfg.OutboundConfigs), `"direct"`) {
		t.Fatalf("template outbounds must survive: %s", cfg.OutboundConfigs)
	}
}
