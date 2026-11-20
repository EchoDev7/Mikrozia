package config

import (
	"net/netip"
	"os"
	"strings"
)

type RouterOSAPIConfig struct {
	State    string `json:"state"`
	Host     string `json:"-"`
	Username string `json:"-"`
	Password string `json:"-"`
}

// GetRouterOSAPIConfig permits literal private addresses only, avoiding DNS rebinding and URL/port injection.
func GetRouterOSAPIConfig() RouterOSAPIConfig {
	host := os.Getenv("MIKROZIA_ROUTEROS_API_HOST")
	user := os.Getenv("MIKROZIA_ROUTEROS_API_USERNAME")
	password := os.Getenv("MIKROZIA_ROUTEROS_API_PASSWORD")
	c := RouterOSAPIConfig{State: "not_configured"}
	if host == "" && user == "" && password == "" {
		return c
	}
	c.State = "invalid_configuration"
	if strings.TrimSpace(host) == "" || strings.TrimSpace(user) == "" || strings.TrimSpace(password) == "" {
		return c
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || !ip.IsPrivate() || ip.Is4In6() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return c
	}
	if len(user) > 256 || len(password) > 4096 || strings.ContainsAny(user+password, "\x00\r\n") {
		return c
	}
	c.State, c.Host, c.Username, c.Password = "connecting", ip.String(), user, password
	return c
}
