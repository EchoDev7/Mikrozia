package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRouterOSEnvironmentFailsClosedAndNeverSerializesSecrets(t *testing.T) {
	for _, tc := range []struct{ host, user, password, state string }{
		{"", "", "", "not_configured"},
		{"172.16.10.1", "", "", "invalid_configuration"},
		{"192.168.88.1", "fixture-user", "fixture-password", "connecting"},
		{"10.2.3.1", "fixture-user", "fixture-password", "connecting"},
		{"fd00::1", "fixture-user", "fixture-password", "connecting"},
		{"127.0.0.1", "fixture-user", "fixture-password", "invalid_configuration"},
		{"169.254.169.254", "fixture-user", "fixture-password", "invalid_configuration"},
		{"8.8.8.8", "fixture-user", "fixture-password", "invalid_configuration"},
		{"router.local", "fixture-user", "fixture-password", "invalid_configuration"},
		{"192.168.88.1:8729", "fixture-user", "fixture-password", "invalid_configuration"},
		{"http://192.168.88.1", "fixture-user", "fixture-password", "invalid_configuration"},
		{"192.168.88.1", "", "fixture-password", "invalid_configuration"},
		{"192.168.88.1", "fixture-user", "", "invalid_configuration"},
	} {
		t.Run(tc.host+tc.state, func(t *testing.T) {
			t.Setenv("MIKROZIA_ROUTEROS_API_HOST", tc.host)
			t.Setenv("MIKROZIA_ROUTEROS_API_USERNAME", tc.user)
			t.Setenv("MIKROZIA_ROUTEROS_API_PASSWORD", tc.password)
			cfg := GetRouterOSAPIConfig()
			if cfg.State != tc.state {
				t.Fatalf("state = %q, want %q", cfg.State, tc.state)
			}
			b, err := json.Marshal(cfg)
			if err != nil || strings.Contains(string(b), "fixture-password") || strings.Contains(string(b), "fixture-user") || strings.Contains(string(b), tc.host) && tc.host != "" {
				t.Fatal("configuration serialization leaked endpoint or credentials")
			}
		})
	}
}
