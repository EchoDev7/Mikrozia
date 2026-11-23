package database

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/xray"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func seedPreMigrationDB(t *testing.T, rows []model.Inbound, trafficRows []xray.ClientTraffic) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	rawDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite seed db: %v", err)
	}
	if err := rawDB.AutoMigrate(&model.Inbound{}, &xray.ClientTraffic{}, &model.Setting{}); err != nil {
		t.Fatalf("automigrate seed db: %v", err)
	}
	for i := range rows {
		if err := rawDB.Create(&rows[i]).Error; err != nil {
			t.Fatalf("insert inbound row %d: %v", i, err)
		}
		// GORM omits zero-valued booleans/ints only if default applies; force exact column state.
		if err := rawDB.Model(&model.Inbound{}).Where("id = ?", rows[i].Id).Update("enable", rows[i].Enable).Error; err != nil {
			t.Fatalf("force enable on row %d: %v", rows[i].Id, err)
		}
	}
	for i := range trafficRows {
		if err := rawDB.Create(&trafficRows[i]).Error; err != nil {
			t.Fatalf("insert client_traffic row %d: %v", i, err)
		}
	}
	sqlDB, err := rawDB.DB()
	if err != nil {
		t.Fatalf("raw sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	return dbPath
}

func decodeJSONNumberMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("unmarshal %q: %v", raw, err)
	}
	return m
}

func TestMigrateAmneziaWGKernelMode(t *testing.T) {
	nodeTwo := 2

	kernelTrueSettings := `{
  "server": {
    "secretKey": "srv-priv-key-1",
    "publicKey": "srv-pub-key-1",
    "ip": "10.66.0.1/24",
    "ipv6": "fd00:66::1/64",
    "mtu": 1280,
    "dns": "1.1.1.1,9.9.9.9",
    "routeThroughXray": true,
    "externalInterface": "eth0",
    "kernelMode": true,
    "unknownServerExt": {"nested": 9007199254740993}
  },
  "obfuscation": {
    "jc": 4,
    "jmin": 40,
    "jmax": 70,
    "s1": 15,
    "s2": 25,
    "h1": 100001,
    "h2": 100002,
    "h3": 100003,
    "h4": 100004,
    "i1": "<b 0xdeadbeef>",
    "rekeyAfterTime": 120
  },
  "peers": [
    {
      "id": "peer-uuid-1",
      "email": "alice@example",
      "publicKey": "cli-pub-1",
      "privateKey": "cli-priv-1",
      "preSharedKey": "psk-1",
      "allowedIPs": ["10.66.0.2/32", "fd00:66::2/128"],
      "keepAlive": 25,
      "forwardedPorts": "80,443,8000-8010",
      "enable": true,
      "totalGB": 10737418240,
      "expiryTime": 1893456000000,
      "extraPeerField": "keep-me"
    }
  ],
  "topLevelUnknown": 42
}`

	kernelFalseDisabledSettings := `{
  "server": {
    "secretKey": "srv-priv-key-2",
    "ip": "10.67.0.1/24",
    "mtu": 1420,
    "kernelMode": false
  },
  "obfuscation": {"jc": 3},
  "peers": [{"email": "bob@example", "publicKey": "cli-pub-2", "enable": false}]
}`

	withoutKernelModeSettings := `{
  "server": {
    "secretKey": "srv-priv-key-3",
    "ip": "10.68.0.1/24",
    "mtu": 1380
  },
  "obfuscation": {"jc": 5},
  "peers": [{"email": "carol@example", "publicKey": "cli-pub-3", "enable": true}]
}`

	vlessWithKernelModeLikeKey := `{"clients":[{"id":"u1","email":"vless@example"}],"server":{"kernelMode":true}}`

	currentSchemaWithKernelModeSettings := `{
  "server": {
    "privateKey": "cur-srv-priv-key",
    "publicKey": "cur-srv-pub-key",
    "subnetIp": "10.8.1.0",
    "subnetCidr": 24,
    "mtu": 1420,
    "primaryDns": "8.8.8.8",
    "secondaryDns": "1.1.1.1",
    "externalInterface": "eth0",
    "ipv6Enabled": true,
    "ipv6Subnet": "fd00:8:1::/64",
    "ipv6ExternalInterface": "eth0",
    "routeThroughXray": false,
    "kernelMode": true,
    "jc": 4,
    "jmin": 65,
    "jmax": 220,
    "s1": 87,
    "s2": 44,
    "s3": 21,
    "s4": 9,
    "h1": "463065432",
    "h2": "912345678",
    "h3": "1345678901",
    "h4": "1987654321",
    "i1": "<r 148>",
    "headerProtectionKey": "8Iu83eHDA3fMKKSGaEsVW9Ycd2lYYzc0MYlk1jJTvE4=",
    "contentPaddingAddition": "17-49",
    "rekeyAfterTime": "111-139",
    "rekeyTimeout": "4-7",
    "rejectAfterTime": "187-251",
    "keepaliveTimeout": "9-14",
    "maxHandshakeAttempts": "19-36",
    "randomTrailers": true,
    "disableCookies": true,
    "unknownCurrentServerProp": {"precisionId": 9007199254740993, "note": "keep-server-ext"}
  },
  "clients": [
    {
      "id": "cur-cli-1",
      "email": "current-alice@example",
      "privateKey": "cur-cli-priv-1",
      "publicKey": "cur-cli-pub-1",
      "preSharedKey": "cur-cli-psk-1",
      "allowedIPs": ["10.8.1.2/32", "fd00:8:1::2/128"],
      "keepAlive": "15-25",
      "forwardedPorts": "80,443,8000-8100",
      "enable": true,
      "totalGB": 53687091200,
      "expiryTime": 1893456000000,
      "unknownClientProp": {"largePrecisionInt": 9007199254740995, "label": "keep-client-ext"}
    },
    {
      "id": "cur-cli-2",
      "email": "current-bob@example",
      "privateKey": "cur-cli-priv-2",
      "publicKey": "cur-cli-pub-2",
      "allowedIPs": ["10.8.1.3/32"],
      "forwardedPorts": "",
      "enable": false,
      "totalGB": 0,
      "expiryTime": 0
    }
  ]
}`

	dbPath := seedPreMigrationDB(t, []model.Inbound{
		{
			Id:         1,
			UserId:     1,
			Up:         123456,
			Down:       654321,
			Total:      999999999,
			Remark:     "awg-kernel-true-active",
			Enable:     true,
			ExpiryTime: 1893456000000,
			Listen:     "0.0.0.0",
			Port:       51820,
			Protocol:   model.AmneziaWG,
			Settings:   kernelTrueSettings,
			Tag:        "awg-1",
			NodeID:     &nodeTwo,
		},
		{
			Id:         2,
			UserId:     1,
			Up:         555,
			Down:       777,
			Total:      8888,
			Remark:     "awg-kernel-false-disabled",
			Enable:     false,
			ExpiryTime: 1800000000000,
			Listen:     "127.0.0.1",
			Port:       51821,
			Protocol:   model.AmneziaWG,
			Settings:   kernelFalseDisabledSettings,
			Tag:        "awg-2",
		},
		{
			Id:       3,
			UserId:   1,
			Remark:   "awg-already-userspace",
			Enable:   true,
			Port:     51822,
			Protocol: model.AmneziaWG,
			Settings: withoutKernelModeSettings,
			Tag:      "awg-3",
		},
		{
			Id:       4,
			UserId:   1,
			Remark:   "vless-untouched",
			Enable:   true,
			Port:     443,
			Protocol: model.VLESS,
			Settings: vlessWithKernelModeLikeKey,
			Tag:      "vless-4",
		},
		{
			Id:         5,
			UserId:     1,
			Up:         987654321,
			Down:       123456789,
			Total:      53687091200,
			Remark:     "awg-current-schema-kernel-true",
			Enable:     true,
			ExpiryTime: 1893456000000,
			Listen:     "203.0.113.10",
			Port:       51825,
			Protocol:   model.AmneziaWG,
			Settings:   currentSchemaWithKernelModeSettings,
			Tag:        "in-51825-udp",
			NodeID:     &nodeTwo,
		},
	}, []xray.ClientTraffic{
		{
			InboundId:  1,
			Enable:     true,
			Email:      "alice@example",
			UUID:       "peer-uuid-1",
			Up:         1000,
			Down:       2000,
			LastOnline: 1735680000000,
			Total:      10737418240,
			ExpiryTime: 1893456000000,
		},
		{
			InboundId:  5,
			Enable:     true,
			Email:      "current-alice@example",
			UUID:       "cur-cli-1",
			Up:         4444,
			Down:       5555,
			LastOnline: 1735690000000,
			Total:      53687091200,
			ExpiryTime: 1893456000000,
		},
	})

	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	var got1, got2, got3, got4, got5 model.Inbound
	if err := GetDB().First(&got1, 1).Error; err != nil {
		t.Fatalf("load inbound 1: %v", err)
	}
	if err := GetDB().First(&got2, 2).Error; err != nil {
		t.Fatalf("load inbound 2: %v", err)
	}
	if err := GetDB().First(&got3, 3).Error; err != nil {
		t.Fatalf("load inbound 3: %v", err)
	}
	if err := GetDB().First(&got4, 4).Error; err != nil {
		t.Fatalf("load inbound 4: %v", err)
	}
	if err := GetDB().First(&got5, 5).Error; err != nil {
		t.Fatalf("load inbound 5: %v", err)
	}

	// Row 1: kernelMode removed, everything else preserved losslessly including 64-bit unknown fields & metadata.
	m1 := decodeJSONNumberMap(t, got1.Settings)
	srv1 := m1["server"].(map[string]any)
	if _, exists := srv1["kernelMode"]; exists {
		t.Fatalf("expected server.kernelMode removed from inbound 1, got %s", got1.Settings)
	}
	if srv1["secretKey"] != "srv-priv-key-1" || srv1["publicKey"] != "srv-pub-key-1" ||
		srv1["ip"] != "10.66.0.1/24" || srv1["ipv6"] != "fd00:66::1/64" ||
		srv1["dns"] != "1.1.1.1,9.9.9.9" || srv1["routeThroughXray"] != true ||
		srv1["externalInterface"] != "eth0" {
		t.Fatalf("server fields altered on inbound 1: %#v", srv1)
	}
	var rawRoot map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got1.Settings), &rawRoot); err != nil {
		t.Fatalf("unmarshal raw root: %v", err)
	}
	var rawSrv map[string]json.RawMessage
	if err := json.Unmarshal(rawRoot["server"], &rawSrv); err != nil {
		t.Fatalf("unmarshal raw server: %v", err)
	}
	if string(rawSrv["unknownServerExt"]) != `{"nested": 9007199254740993}` &&
		string(rawSrv["unknownServerExt"]) != "{\n      \"nested\": 9007199254740993\n    }" {
		t.Fatalf("64-bit integer lost precision or dropped: %s", string(rawSrv["unknownServerExt"]))
	}
	if string(rawRoot["topLevelUnknown"]) != "42" {
		t.Fatalf("topLevelUnknown lost: %s", string(rawRoot["topLevelUnknown"]))
	}
	peers1 := m1["peers"].([]any)
	peer1 := peers1[0].(map[string]any)
	if peer1["email"] != "alice@example" || peer1["publicKey"] != "cli-pub-1" ||
		peer1["privateKey"] != "cli-priv-1" || peer1["preSharedKey"] != "psk-1" ||
		peer1["forwardedPorts"] != "80,443,8000-8010" || peer1["extraPeerField"] != "keep-me" {
		t.Fatalf("peer fields altered: %#v", peer1)
	}
	if got1.Up != 123456 || got1.Down != 654321 || got1.Total != 999999999 ||
		got1.ExpiryTime != 1893456000000 || !got1.Enable || got1.Port != 51820 ||
		got1.Listen != "0.0.0.0" || got1.NodeID == nil || *got1.NodeID != 2 {
		t.Fatalf("inbound 1 columns altered: %+v", got1)
	}

	var ct xray.ClientTraffic
	if err := GetDB().Where("inbound_id = ? AND email = ?", 1, "alice@example").First(&ct).Error; err != nil {
		t.Fatalf("client traffic lost: %v", err)
	}
	if ct.Up != 1000 || ct.Down != 2000 || ct.LastOnline != 1735680000000 || ct.Total != 10737418240 || ct.ExpiryTime != 1893456000000 {
		t.Fatalf("client traffic altered: %+v", ct)
	}

	// Row 2: kernelMode:false removed, disabled state stays false.
	m2 := decodeJSONNumberMap(t, got2.Settings)
	srv2 := m2["server"].(map[string]any)
	if _, exists := srv2["kernelMode"]; exists {
		t.Fatalf("expected server.kernelMode removed from inbound 2, got %s", got2.Settings)
	}
	if got2.Enable {
		t.Fatal("disabled inbound 2 must remain disabled")
	}

	// Row 3: no kernelMode -> untouched verbatim.
	if got3.Settings != withoutKernelModeSettings {
		t.Fatalf("inbound without kernelMode was unexpectedly modified:\ngot:  %s\nwant: %s", got3.Settings, withoutKernelModeSettings)
	}

	// Row 4: non-AmneziaWG protocol -> untouched verbatim.
	if got4.Settings != vlessWithKernelModeLikeKey {
		t.Fatalf("non-AmneziaWG inbound was unexpectedly modified: %s", got4.Settings)
	}

	// Row 5: current AmneziaWG schema (ServerSettings flat obfuscation + clients[]) preserves every field except server.kernelMode.
	m5 := decodeJSONNumberMap(t, got5.Settings)
	srv5 := m5["server"].(map[string]any)
	if _, exists := srv5["kernelMode"]; exists {
		t.Fatalf("expected server.kernelMode removed from current-schema inbound 5, got %s", got5.Settings)
	}
	if srv5["privateKey"] != "cur-srv-priv-key" || srv5["publicKey"] != "cur-srv-pub-key" ||
		srv5["subnetIp"] != "10.8.1.0" || srv5["subnetCidr"] != json.Number("24") ||
		srv5["mtu"] != json.Number("1420") || srv5["primaryDns"] != "8.8.8.8" ||
		srv5["secondaryDns"] != "1.1.1.1" || srv5["externalInterface"] != "eth0" ||
		srv5["ipv6Enabled"] != true || srv5["ipv6Subnet"] != "fd00:8:1::/64" ||
		srv5["ipv6ExternalInterface"] != "eth0" || srv5["routeThroughXray"] != false {
		t.Fatalf("current-schema server core fields altered: %#v", srv5)
	}
	if srv5["jc"] != json.Number("4") || srv5["jmin"] != json.Number("65") || srv5["jmax"] != json.Number("220") ||
		srv5["s1"] != json.Number("87") || srv5["s2"] != json.Number("44") ||
		srv5["s3"] != json.Number("21") || srv5["s4"] != json.Number("9") ||
		srv5["h1"] != "463065432" || srv5["h2"] != "912345678" ||
		srv5["h3"] != "1345678901" || srv5["h4"] != "1987654321" ||
		srv5["i1"] != "<r 148>" ||
		srv5["headerProtectionKey"] != "8Iu83eHDA3fMKKSGaEsVW9Ycd2lYYzc0MYlk1jJTvE4=" ||
		srv5["contentPaddingAddition"] != "17-49" || srv5["rekeyAfterTime"] != "111-139" ||
		srv5["rekeyTimeout"] != "4-7" || srv5["rejectAfterTime"] != "187-251" ||
		srv5["keepaliveTimeout"] != "9-14" || srv5["maxHandshakeAttempts"] != "19-36" ||
		srv5["randomTrailers"] != true || srv5["disableCookies"] != true {
		t.Fatalf("current-schema flat obfuscation fields altered: %#v", srv5)
	}
	srvExt5 := srv5["unknownCurrentServerProp"].(map[string]any)
	if srvExt5["precisionId"] != json.Number("9007199254740993") || srvExt5["note"] != "keep-server-ext" {
		t.Fatalf("unknownCurrentServerProp or 64-bit precision altered: %#v", srvExt5)
	}
	clients5 := m5["clients"].([]any)
	if len(clients5) != 2 {
		t.Fatalf("expected 2 clients preserved, got %d", len(clients5))
	}
	c1 := clients5[0].(map[string]any)
	if c1["id"] != "cur-cli-1" || c1["email"] != "current-alice@example" ||
		c1["privateKey"] != "cur-cli-priv-1" || c1["publicKey"] != "cur-cli-pub-1" ||
		c1["preSharedKey"] != "cur-cli-psk-1" || c1["keepAlive"] != "15-25" ||
		c1["forwardedPorts"] != "80,443,8000-8100" || c1["enable"] != true ||
		c1["totalGB"] != json.Number("53687091200") || c1["expiryTime"] != json.Number("1893456000000") ||
		!reflect.DeepEqual(c1["allowedIPs"], []any{"10.8.1.2/32", "fd00:8:1::2/128"}) {
		t.Fatalf("current-schema client 1 altered: %#v", c1)
	}
	cliExt := c1["unknownClientProp"].(map[string]any)
	if cliExt["largePrecisionInt"] != json.Number("9007199254740995") || cliExt["label"] != "keep-client-ext" {
		t.Fatalf("unknownClientProp or 64-bit precision altered: %#v", cliExt)
	}
	c2 := clients5[1].(map[string]any)
	if c2["email"] != "current-bob@example" || c2["enable"] != false ||
		c2["totalGB"] != json.Number("0") || c2["expiryTime"] != json.Number("0") ||
		!reflect.DeepEqual(c2["allowedIPs"], []any{"10.8.1.3/32"}) {
		t.Fatalf("current-schema client 2 altered: %#v", c2)
	}
	if got5.Up != 987654321 || got5.Down != 123456789 || got5.Total != 53687091200 ||
		got5.ExpiryTime != 1893456000000 || !got5.Enable || got5.Port != 51825 ||
		got5.Listen != "203.0.113.10" || got5.Tag != "in-51825-udp" || got5.NodeID == nil || *got5.NodeID != 2 {
		t.Fatalf("inbound 5 columns altered: %+v", got5)
	}
	var ct5 xray.ClientTraffic
	if err := GetDB().Where("inbound_id = ? AND email = ?", 5, "current-alice@example").First(&ct5).Error; err != nil {
		t.Fatalf("client traffic lost on inbound 5: %v", err)
	}
	if ct5.Up != 4444 || ct5.Down != 5555 || ct5.LastOnline != 1735690000000 || ct5.Total != 53687091200 || ct5.ExpiryTime != 1893456000000 {
		t.Fatalf("client traffic altered on inbound 5: %+v", ct5)
	}

	// Idempotency: running migration a second time succeeds and changes nothing across legacy and current schemas.
	afterFirstPass := got1.Settings
	afterFirstPass5 := got5.Settings
	if err := migrateAmneziaWGKernelMode(); err != nil {
		t.Fatalf("second migrateAmneziaWGKernelMode call failed: %v", err)
	}
	var got1Again, got5Again model.Inbound
	if err := GetDB().First(&got1Again, 1).Error; err != nil {
		t.Fatalf("reload inbound 1: %v", err)
	}
	if err := GetDB().First(&got5Again, 5).Error; err != nil {
		t.Fatalf("reload inbound 5: %v", err)
	}
	if !reflect.DeepEqual(got1Again.Settings, afterFirstPass) {
		t.Fatalf("migration is not idempotent on legacy inbound:\nfirst:  %s\nsecond: %s", afterFirstPass, got1Again.Settings)
	}
	if !reflect.DeepEqual(got5Again.Settings, afterFirstPass5) {
		t.Fatalf("migration is not idempotent on current-schema inbound:\nfirst:  %s\nsecond: %s", afterFirstPass5, got5Again.Settings)
	}
}

func TestMigrateAmneziaWGKernelMode_MalformedJSONRollsBack(t *testing.T) {
	originalValid := `{"server":{"secretKey":"k1","ip":"10.66.0.1/24","kernelMode":true},"peers":[]}`
	malformed := `{"server":{"secretKey":"k2","kernelMode":true` // truncated JSON

	dbPath := seedPreMigrationDB(t, []model.Inbound{
		{
			Id:       1,
			UserId:   1,
			Remark:   "valid-before-bad",
			Enable:   true,
			Port:     51830,
			Protocol: model.AmneziaWG,
			Settings: originalValid,
			Tag:      "awg-valid",
		},
		{
			Id:       2,
			UserId:   1,
			Remark:   "corrupted-json",
			Enable:   true,
			Port:     51831,
			Protocol: model.AmneziaWG,
			Settings: malformed,
			Tag:      "awg-corrupt",
		},
	}, nil)

	err := InitDB(dbPath)
	t.Cleanup(func() { _ = CloseDB() })
	if err == nil {
		t.Fatal("expected InitDB to fail on malformed AmneziaWG JSON")
	}

	// Inspect database directly to verify transaction rolled back row 1 cleanly.
	rawDB, openErr := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if openErr != nil {
		t.Fatalf("reopen sqlite db: %v", openErr)
	}
	sqlDB, _ := rawDB.DB()
	defer sqlDB.Close()

	var row1, row2 model.Inbound
	if err := rawDB.First(&row1, 1).Error; err != nil {
		t.Fatalf("query row 1 after rollback: %v", err)
	}
	if err := rawDB.First(&row2, 2).Error; err != nil {
		t.Fatalf("query row 2 after rollback: %v", err)
	}
	if row1.Settings != originalValid {
		t.Fatalf("expected row 1 rolled back to original settings:\ngot:  %s\nwant: %s", row1.Settings, originalValid)
	}
	if row2.Settings != malformed {
		t.Fatalf("expected row 2 unchanged, got %s", row2.Settings)
	}
}
