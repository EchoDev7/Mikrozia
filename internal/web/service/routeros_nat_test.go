package service

import (
	"reflect"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/web/entity"
)

func TestProtocolsForInbound(t *testing.T) {
	remoteNodeID := 2
	cases := []struct {
		name string
		ib   *model.Inbound
		want []string
	}{
		{
			name: "nil inbound",
			ib:   nil,
			want: nil,
		},
		{
			name: "remote node skipped",
			ib:   &model.Inbound{Id: 1, Port: 443, Protocol: model.VLESS, NodeID: &remoteNodeID},
			want: nil,
		},
		{
			name: "loopback skipped",
			ib:   &model.Inbound{Id: 1, Port: 443, Protocol: model.VLESS, Listen: "127.0.0.1"},
			want: nil,
		},
		{
			name: "vless tcp default",
			ib:   &model.Inbound{Id: 1, Port: 443, Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`},
			want: []string{"tcp"},
		},
		{
			name: "vless xhttp uses tcp",
			ib:   &model.Inbound{Id: 1, Port: 443, Protocol: model.VLESS, StreamSettings: `{"network":"xhttp"}`},
			want: []string{"tcp"},
		},
		{
			name: "vless kcp uses udp",
			ib:   &model.Inbound{Id: 1, Port: 8443, Protocol: model.VLESS, StreamSettings: `{"network":"kcp"}`},
			want: []string{"udp"},
		},
		{
			name: "amneziawg uses udp",
			ib:   &model.Inbound{Id: 2, Port: 51820, Protocol: "amneziawg"},
			want: []string{"udp"},
		},
		{
			name: "hysteria2 uses udp",
			ib:   &model.Inbound{Id: 3, Port: 8443, Protocol: "hysteria2"},
			want: []string{"udp"},
		},
		{
			name: "tuic uses udp",
			ib:   &model.Inbound{Id: 4, Port: 8444, Protocol: "tuic"},
			want: []string{"udp"},
		},
		{
			name: "masque uses udp",
			ib:   &model.Inbound{Id: 5, Port: 8445, Protocol: "masque"},
			want: []string{"udp"},
		},
		{
			name: "shadowsocks tcp+udp",
			ib:   &model.Inbound{Id: 6, Port: 9000, Protocol: model.Shadowsocks, Settings: `{"network":"tcp,udp"}`},
			want: []string{"tcp", "udp"},
		},
		{
			name: "shadowsocks udp only",
			ib:   &model.Inbound{Id: 7, Port: 9001, Protocol: model.Shadowsocks, Settings: `{"network":"udp"}`},
			want: []string{"udp"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ProtocolsForInbound(tc.ib)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ProtocolsForInbound() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllSettingRouterOSNATValidation(t *testing.T) {
	base := func() *entity.AllSetting {
		return &entity.AllSetting{
			WebPort:      2053,
			SubPort:      2096,
			WebBasePath:  "/",
			SubPath:      "/sub/",
			SubJsonPath:  "/json/",
			SubClashPath: "/clash/",
			TimeLocation: "UTC",
		}
	}

	s := base()
	if err := s.CheckValid(); err != nil {
		t.Fatalf("disabled RouterOS NAT should be valid: %v", err)
	}

	s = base()
	s.RouterosNatEnable = true
	s.RouterosNatHost = "8.8.8.8"
	s.RouterosNatPort = 8728
	s.RouterosNatUsername = "mikrozia"
	s.RouterosNatPassword = "secret"
	if err := s.CheckValid(); err == nil {
		t.Fatal("expected public RouterOS host to be rejected")
	}

	s = base()
	s.RouterosNatEnable = true
	s.RouterosNatHost = "172.16.10.1"
	s.RouterosNatPort = 8728
	s.RouterosNatUsername = "mikrozia"
	s.RouterosNatPassword = "secret"
	s.RouterosNatTargetIP = "172.16.10.2"
	if err := s.CheckValid(); err != nil {
		t.Fatalf("valid private RouterOS NAT config rejected: %v", err)
	}

	s.RouterosNatTargetIP = "1.1.1.1"
	if err := s.CheckValid(); err == nil {
		t.Fatal("expected public target IP to be rejected")
	}
}
