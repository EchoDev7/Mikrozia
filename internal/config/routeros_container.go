package config

import (
	"os"
	"strings"
)

// RouterOSContainerComment is an exact, operator-configured container marker.
func RouterOSContainerComment() string {
	comment := os.Getenv("MIKROZIA_ROUTEROS_CONTAINER_COMMENT")
	if len(comment) == 0 || len(comment) > 128 || strings.TrimSpace(comment) != comment || strings.ContainsAny(comment, "\x00\r\n") {
		return ""
	}
	return comment
}
