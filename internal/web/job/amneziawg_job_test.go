package job

import (
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/amneziawgnet"
	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/util/wireguard"
)

func TestAmneziaWGJobReconcilesUserspaceInbounds(t *testing.T) {
	setupIntegrationDB(t)
	srvPriv, srvPub, err := wireguard.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("generate server keypair: %v", err)
	}
	_, peerPub, err := wireguard.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("generate peer keypair: %v", err)
	}
	inbound := &model.Inbound{
		UserId:   1,
		Tag:      "awg-userspace-job",
		Enable:   true,
		Port:     58841,
		Protocol: model.AmneziaWG,
		Settings: `{"server":{"privateKey":"` + srvPriv + `","publicKey":"` + srvPub + `","subnetIp":"10.210.0.0","subnetCidr":24,"mtu":1420},"clients":[{"email":"job@example.test","enable":true,"publicKey":"` + peerPub + `","allowedIPs":["10.210.0.2/32"]}]}`,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	t.Cleanup(func() { amneziawgnet.GetManager().Remove(inbound.Id) })

	NewAmneziaWGJob().Run()

	if _, _, ok := amneziawgnet.GetManager().Lookup(inbound.Id); !ok {
		t.Fatal("expected AmneziaWGJob.Run() to reconcile the enabled userspace inbound")
	}
}
