package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/amneziawgnet"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/wireguard"
)

func datapathInbound(t *testing.T, id, port int, withLegacyKernelFlag *bool) *model.Inbound {
	t.Helper()
	srvPriv, srvPub, err := wireguard.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("generate server keypair: %v", err)
	}
	_, peerPub, err := wireguard.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("generate peer keypair: %v", err)
	}
	kernelJSON := ""
	if withLegacyKernelFlag != nil {
		kernelJSON = fmt.Sprintf(`,"kernelMode":%t`, *withLegacyKernelFlag)
	}
	settings := fmt.Sprintf(
		`{"server":{"privateKey":%q,"publicKey":%q,"subnetIp":"10.209.0.0","subnetCidr":24,"mtu":1420,`+
			`"jc":4,"jmin":40,"jmax":70,"s1":20,"s2":30,"s3":20,"s4":20%s},`+
			`"clients":[{"email":"peer@example.test","enable":true,"publicKey":%q,"allowedIPs":["10.209.0.2/32"]}]}`,
		srvPriv, srvPub, kernelJSON, peerPub)
	return &model.Inbound{
		Id:       id,
		Enable:   true,
		Protocol: model.AmneziaWG,
		Port:     port,
		Tag:      fmt.Sprintf("inbound-%d", id),
		Settings: settings,
	}
}

func newTestLocal() *Local {
	return NewLocal(LocalDeps{APIPort: func() int { return 0 }, SetNeedRestart: func() {}})
}

func TestAddInboundRejectsLegacyKernelMode(t *testing.T) {
	trueFlag := true
	ib := datapathInbound(t, 4181, 58731, &trueFlag)
	t.Cleanup(func() { amneziawgnet.GetManager().Remove(ib.Id) })

	err := newTestLocal().AddInbound(context.Background(), ib)
	if err == nil || !strings.Contains(err.Error(), "kernel mode is not supported") {
		t.Fatalf("AddInbound error = %v, want kernel mode unsupported rejection", err)
	}
	if _, _, ok := amneziawgnet.GetManager().Lookup(ib.Id); ok {
		t.Fatal("rejected kernelMode=true inbound must not start an embedded device")
	}
}

func TestAddInboundUsesUserspaceDatapath(t *testing.T) {
	falseFlag := false
	for _, tc := range []struct {
		name string
		id   int
		port int
		flag *bool
	}{
		{name: "without kernelMode", id: 4182, port: 58732, flag: nil},
		{name: "with legacy kernelMode false", id: 4183, port: 58733, flag: &falseFlag},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ib := datapathInbound(t, tc.id, tc.port, tc.flag)
			t.Cleanup(func() { amneziawgnet.GetManager().Remove(ib.Id) })

			if err := newTestLocal().AddInbound(context.Background(), ib); err != nil {
				t.Fatalf("AddInbound: %v", err)
			}
			if _, _, ok := amneziawgnet.GetManager().Lookup(ib.Id); !ok {
				t.Fatal("userspace inbound did not create an embedded device")
			}
		})
	}
}
