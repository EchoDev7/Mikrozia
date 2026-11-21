package config

import (
	"strings"
	"testing"
)

func TestGetMikroziaContainerInterface(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "unset keeps default behavior", value: "", want: ""},
		{name: "blank keeps default behavior", value: "   ", want: ""},
		{name: "valid routeros veth name", value: "veth-mikrozia", want: "veth-mikrozia"},
		{name: "surrounding spaces are rejected", value: "  veth-mikrozia  ", want: ""},
		{name: "valid short name", value: "eth0", want: "eth0"},
		{name: "loopback is rejected", value: "lo", want: ""},
		{name: "loopback0 is rejected", value: "lo0", want: ""},
		{name: "case-sensitive non-loopback name is valid", value: "LO", want: "LO"},
		{name: "glob is rejected", value: "veth*", want: ""},
		{name: "list is rejected", value: "eth0,eth1", want: ""},
		{name: "space inside is rejected", value: "veth mikrozia", want: ""},
		{name: "slash is rejected", value: "../veth0", want: ""},
		{name: "command is rejected", value: "$(id)", want: ""},
		{name: "too long is rejected", value: "veth0123456789012345678901234567890123456789012345678901234567890123", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ContainerInterfaceEnv, tc.value)
			if got := GetMikroziaContainerInterface(); got != tc.want {
				t.Fatalf("GetMikroziaContainerInterface() with %q = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestGetMikroziaContainerInterfaceMaxLengthAccepted(t *testing.T) {
	name := "veth" + strings.Repeat("a", 59)
	if len(name) != 63 {
		t.Fatalf("test fixture length = %d, want 63", len(name))
	}
	t.Setenv(ContainerInterfaceEnv, name)
	if got := GetMikroziaContainerInterface(); got != name {
		t.Fatalf("63-char interface name should be accepted, got %q", got)
	}
}
