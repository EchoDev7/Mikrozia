package config

import "testing"

func TestRouterOSRestartConfigRequiresSeparateCompleteCredentials(t *testing.T) {
	t.Setenv("MIKROZIA_ROUTEROS_API_HOST", "172.16.10.1")
	t.Setenv("MIKROZIA_ROUTEROS_API_USERNAME", "reader")
	t.Setenv("MIKROZIA_ROUTEROS_API_PASSWORD", "read-secret")
	t.Setenv("MIKROZIA_ROUTEROS_CONTAINER_COMMENT", "[Mikrozia:container] Container")
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_USERNAME", "")
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_PASSWORD", "")
	if got := GetRouterOSRestartConfig(); got.State != "not_configured" {
		t.Fatalf("state=%s", got.State)
	}
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_USERNAME", "operator")
	if got := GetRouterOSRestartConfig(); got.State != "invalid_configuration" {
		t.Fatalf("state=%s", got.State)
	}
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_PASSWORD", "write-secret")
	if got := GetRouterOSRestartConfig(); got.State != "ready" || got.Username != "operator" || got.Password != "write-secret" {
		t.Fatalf("config=%+v", got)
	}
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_USERNAME", "reader")
	if got := GetRouterOSRestartConfig(); got.State != "invalid_configuration" {
		t.Fatalf("reused read account state=%s", got.State)
	}
}
