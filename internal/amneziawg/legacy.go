package amneziawg

import (
	"encoding/json"
	"errors"
	"strings"
)

// ErrKernelModeUnsupported is returned when a caller explicitly requests the
// retired kernel-module AmneziaWG datapath ("kernelMode": true).
var ErrKernelModeUnsupported = errors.New("amneziawg: kernel mode is not supported in Mikrozia/RouterOS; use userspace")

// RejectLegacyKernelMode checks a raw AmneziaWG inbound settings JSON payload
// without re-exposing KernelMode on any public or OpenAPI-scanned struct.
// Payloads requesting server.kernelMode=true are rejected; server.kernelMode=false
// or omission is allowed so legacy false payloads are silently stripped on save.
func RejectLegacyKernelMode(rawSettings string) error {
	if strings.TrimSpace(rawSettings) == "" {
		return nil
	}
	var legacy struct {
		Server *struct {
			KernelMode *bool `json:"kernelMode"`
		} `json:"server"`
	}
	if err := json.Unmarshal([]byte(rawSettings), &legacy); err != nil {
		// Let normal InboundSettings decoding report syntax/type errors.
		return nil
	}
	if legacy.Server != nil && legacy.Server.KernelMode != nil && *legacy.Server.KernelMode {
		return ErrKernelModeUnsupported
	}
	return nil
}
