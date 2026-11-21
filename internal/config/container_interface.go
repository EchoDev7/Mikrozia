package config

import (
	"os"
	"regexp"
)

const ContainerInterfaceEnv = "MIKROZIA_CONTAINER_INTERFACE"

// Invalid values behave as unset, preserving the existing collection rules.
var containerInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,63}$`)

// GetMikroziaContainerInterface returns one exact, non-loopback interface name.
func GetMikroziaContainerInterface() string {
	name := os.Getenv(ContainerInterfaceEnv)
	if name == "" || !containerInterfacePattern.MatchString(name) {
		return ""
	}
	switch name {
	case "lo", "lo0":
		return ""
	}
	return name
}
