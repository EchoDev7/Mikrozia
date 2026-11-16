package amneziawg

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRejectLegacyKernelMode(t *testing.T) {
	t.Run("rejects kernelMode true", func(t *testing.T) {
		err := RejectLegacyKernelMode(`{"server":{"ip":"10.66.0.1/24","kernelMode":true}}`)
		if err == nil {
			t.Fatal("expected error for kernelMode=true")
		}
		if !strings.Contains(err.Error(), "kernel mode is not supported in Mikrozia/RouterOS; use userspace") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("accepts kernelMode false and drops it on marshal", func(t *testing.T) {
		raw := `{"server":{"secretKey":"abc","ip":"10.66.0.1/24","mtu":1420,"kernelMode":false}}`
		if err := RejectLegacyKernelMode(raw); err != nil {
			t.Fatalf("unexpected error for kernelMode=false: %v", err)
		}
		var parsed InboundSettings
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			t.Fatalf("unmarshal InboundSettings: %v", err)
		}
		out, err := json.Marshal(parsed)
		if err != nil {
			t.Fatalf("marshal InboundSettings: %v", err)
		}
		if strings.Contains(string(out), "kernelMode") {
			t.Fatalf("marshaled InboundSettings unexpectedly retained kernelMode: %s", string(out))
		}
	})

	t.Run("accepts normal payload without kernelMode", func(t *testing.T) {
		if err := RejectLegacyKernelMode(`{"server":{"ip":"10.66.0.1/24"}}`); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := RejectLegacyKernelMode(""); err != nil {
			t.Fatalf("unexpected error on empty payload: %v", err)
		}
	})
}
