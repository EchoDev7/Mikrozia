package config

import (
	"os"
	"strings"
)

type RouterOSRestartConfig struct {
	State    string `json:"state"`
	Username string `json:"-"`
	Password string `json:"-"`
}

// GetRouterOSRestartConfig requires a separate operator-supplied API account.
func GetRouterOSRestartConfig() RouterOSRestartConfig {
	user := os.Getenv("MIKROZIA_ROUTEROS_RESTART_USERNAME")
	password := os.Getenv("MIKROZIA_ROUTEROS_RESTART_PASSWORD")
	c := RouterOSRestartConfig{State: "not_configured"}
	if user == "" && password == "" {
		return c
	}
	c.State = "invalid_configuration"
	if strings.TrimSpace(user) == "" || strings.TrimSpace(password) == "" || len(user) > 256 || len(password) > 4096 || strings.ContainsAny(user+password, "\x00\r\n") {
		return c
	}
	readConfig := GetRouterOSAPIConfig()
	if readConfig.State != "connecting" || RouterOSContainerComment() == "" || user == readConfig.Username {
		return c
	}
	c.State, c.Username, c.Password = "ready", user, password
	return c
}
