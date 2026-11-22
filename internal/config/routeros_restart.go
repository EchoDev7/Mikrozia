package config

import (
	"os"
	"strings"
)

const (
	routerOSRestartUsernameEnv    = "MIKROZIA_ROUTEROS_RESTART_USERNAME"
	routerOSRestartPasswordEnv    = "MIKROZIA_ROUTEROS_RESTART_PASSWORD"
	routerOSRestartUsernameROSEnv = "MIKROZIA_ROS_RESTART_USERNAME"
	routerOSRestartPasswordROSEnv = "MIKROZIA_ROS_RESTART_PASSWORD"
)

type RouterOSRestartConfig struct {
	State    string `json:"state"`
	Username string `json:"-"`
	Password string `json:"-"`
}

// GetRouterOSRestartConfig requires a separate operator-supplied API account.
func GetRouterOSRestartConfig() RouterOSRestartConfig {
	user := os.Getenv(routerOSRestartUsernameEnv)
	password := os.Getenv(routerOSRestartPasswordEnv)
	if user == "" && password == "" {
		user = os.Getenv(routerOSRestartUsernameROSEnv)
		password = os.Getenv(routerOSRestartPasswordROSEnv)
	}
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
