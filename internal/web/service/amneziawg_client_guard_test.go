package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/database/model"
)

func userspaceAWGSettings(clients []model.Client) string {
	b, _ := json.Marshal(map[string]any{
		"server":  map[string]any{"subnetIp": "10.8.1.0", "subnetCidr": 24},
		"clients": clients,
	})
	return string(b)
}

func mustAWGClients(t *testing.T, ib *model.Inbound) []model.Client {
	t.Helper()
	clients, err := (&InboundService{}).GetClients(ib)
	if err != nil {
		t.Fatal(err)
	}
	return clients
}

func TestAWGClientWritesRejectUnsafeValues(t *testing.T) {
	cases := []struct {
		name, want string
		client     model.Client
	}{
		{"public key hook injection", "control characters", model.Client{Email: "bad-key@awg", Enable: true, PublicKey: "peer-key\n[Interface]\nPostDown = touch /tmp/pwned", AllowedIPs: []string{"10.8.1.2/32"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupBulkDB(t)
			inboundSvc := &InboundService{}
			svc := &ClientService{}
			ib := mkInbound(t, 52000, model.AmneziaWG, userspaceAWGSettings(nil))
			tc.client.AllowedIPsByInbound = map[int][]string{ib.Id: tc.client.AllowedIPs}
			_, err := svc.CreateOne(inboundSvc, ib.Id, tc.client)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CreateOne error = %v, want an error containing %q", err, tc.want)
			}
			var records int64
			if dbErr := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", tc.client.Email).Count(&records).Error; dbErr != nil {
				t.Fatal(dbErr)
			}
			if records != 0 {
				t.Fatalf("rejected create persisted %d client records", records)
			}
			after, getErr := inboundSvc.GetInbound(ib.Id)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if len(mustAWGClients(t, after)) != 0 {
				t.Fatal("rejected create changed inbound settings")
			}
		})
	}
}

func TestAWGClientUpdateRejectsUnsafeValues(t *testing.T) {
	setupBulkDB(t)
	inboundSvc := &InboundService{}
	svc := &ClientService{}
	seed := model.Client{Email: "kept@awg", Enable: true, PublicKey: "valid-key", PrivateKey: "valid-private", AllowedIPs: []string{"10.8.1.2/32"}}
	ib := mkInbound(t, 52001, model.AmneziaWG, userspaceAWGSettings(nil))
	if _, err := svc.CreateOne(inboundSvc, ib.Id, seed); err != nil {
		t.Fatalf("seed client: %v", err)
	}
	rec := lookupClientRecord(t, seed.Email)
	before, getErr := inboundSvc.GetInbound(ib.Id)
	if getErr != nil {
		t.Fatal(getErr)
	}
	_, err := svc.Update(inboundSvc, rec.Id, model.Client{Email: "kept@awg", PreSharedKey: "psk\n[Interface]\nPreDown = touch /tmp/pwned"}, 0)
	if err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("ClientService.Update error = %v, want control characters rejection", err)
	}
	after, err := inboundSvc.GetInbound(ib.Id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Settings != before.Settings {
		t.Fatal("rejected update changed settings")
	}
}

func TestAWGUserspaceSupportsIPv6AndForwardedPorts(t *testing.T) {
	setupBulkDB(t)
	inboundSvc := &InboundService{}
	svc := &ClientService{}
	ib := mkInbound(t, 52003, model.AmneziaWG, amneziawgClientTestSettings)
	client := model.Client{
		Email:          "userspace@awg",
		Enable:         true,
		PublicKey:      "valid-key",
		AllowedIPs:     []string{"10.8.1.3/32", "2001:db8::3/128"},
		ForwardedPorts: "54321",
	}
	client.AllowedIPsByInbound = map[int][]string{ib.Id: client.AllowedIPs}
	if _, err := svc.CreateOne(inboundSvc, ib.Id, client); err != nil {
		t.Fatalf("userspace AWG IPv6 + forwardedPorts was rejected: %v", err)
	}
}
