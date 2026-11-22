package config

import (
	"os"
	"strings"
)

const (
	routerOSContainerCommentEnv    = "MIKROZIA_ROUTEROS_CONTAINER_COMMENT"
	routerOSContainerCommentROSEnv = "MIKROZIA_ROS_CONTAINER_COMMENT"
)

// RouterOSContainerComment is an exact, operator-configured container marker.
func RouterOSContainerComment() string {
	comment := os.Getenv(routerOSContainerCommentEnv)
	if comment == "" {
		comment = os.Getenv(routerOSContainerCommentROSEnv)
	}
	if len(comment) == 0 || len(comment) > 128 || strings.TrimSpace(comment) != comment || strings.ContainsAny(comment, "\x00\r\n") {
		return ""
	}
	return comment
}
