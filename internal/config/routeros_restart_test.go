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

func TestRouterOSRestartConfigUsesRouterOSAliasesOnlyWhenCanonicalPairIsUnset(t *testing.T) {
	t.Setenv("MIKROZIA_ROUTEROS_API_HOST", "172.16.10.1")
	t.Setenv("MIKROZIA_ROUTEROS_API_USERNAME", "reader")
	t.Setenv("MIKROZIA_ROUTEROS_API_PASSWORD", "read-secret")
	t.Setenv("MIKROZIA_ROUTEROS_CONTAINER_COMMENT", "[Mikrozia:container] Container")
	t.Setenv(routerOSRestartUsernameEnv, "")
	t.Setenv(routerOSRestartPasswordEnv, "")
	t.Setenv(routerOSRestartUsernameROSEnv, "operator")
	t.Setenv(routerOSRestartPasswordROSEnv, "write-secret")

	got := GetRouterOSRestartConfig()
	if got.State != "ready" || got.Username != "operator" || got.Password != "write-secret" {
		t.Fatalf("RouterOS alias config = %#v, want ready operator config", got)
	}

	t.Setenv(routerOSRestartUsernameEnv, "canonical")
	if got := GetRouterOSRestartConfig(); got.State != "invalid_configuration" {
		t.Fatalf("partial canonical username state = %q, want invalid_configuration", got.State)
	}

	t.Setenv(routerOSRestartUsernameEnv, "")
	t.Setenv(routerOSRestartPasswordEnv, "canonical-secret")
	if got := GetRouterOSRestartConfig(); got.State != "invalid_configuration" {
		t.Fatalf("partial canonical password state = %q, want invalid_configuration", got.State)
	}

	// Complete canonical pair takes priority over complete alias pair.
	t.Setenv(routerOSRestartUsernameEnv, "canonical-op")
	if got := GetRouterOSRestartConfig(); got.State != "ready" || got.Username != "canonical-op" || got.Password != "canonical-secret" {
		t.Fatalf("canonical priority config = %#v, want canonical-op", got)
	}
}

func TestRouterOSRestartConfigAliasHalfConfigAndSameUserRejected(t *testing.T) {
	t.Setenv("MIKROZIA_ROUTEROS_API_HOST", "172.16.10.1")
	t.Setenv("MIKROZIA_ROUTEROS_API_USERNAME", "reader")
	t.Setenv("MIKROZIA_ROUTEROS_API_PASSWORD", "read-secret")
	t.Setenv(routerOSContainerCommentEnv, "")
	t.Setenv(routerOSContainerCommentROSEnv, "[Mikrozia:container] Container")
	t.Setenv(routerOSRestartUsernameEnv, "")
	t.Setenv(routerOSRestartPasswordEnv, "")

	t.Run("alias username only", func(t *testing.T) {
		t.Setenv(routerOSRestartUsernameROSEnv, "operator")
		t.Setenv(routerOSRestartPasswordROSEnv, "")
		if got := GetRouterOSRestartConfig(); got.State != "invalid_configuration" {
			t.Fatalf("state=%q, want invalid_configuration", got.State)
		}
	})

	t.Run("alias password only", func(t *testing.T) {
		t.Setenv(routerOSRestartUsernameROSEnv, "")
		t.Setenv(routerOSRestartPasswordROSEnv, "write-secret")
		if got := GetRouterOSRestartConfig(); got.State != "invalid_configuration" {
			t.Fatalf("state=%q, want invalid_configuration", got.State)
		}
	})

	t.Run("alias reuses read-only username", func(t *testing.T) {
		t.Setenv(routerOSRestartUsernameROSEnv, "reader")
		t.Setenv(routerOSRestartPasswordROSEnv, "write-secret")
		if got := GetRouterOSRestartConfig(); got.State != "invalid_configuration" {
			t.Fatalf("state=%q, want invalid_configuration", got.State)
		}
	})

	t.Run("alias comment and alias restart pair ready", func(t *testing.T) {
		t.Setenv(routerOSRestartUsernameROSEnv, "operator")
		t.Setenv(routerOSRestartPasswordROSEnv, "write-secret")
		got := GetRouterOSRestartConfig()
		if got.State != "ready" || got.Username != "operator" || got.Password != "write-secret" {
			t.Fatalf("got=%#v, want ready operator", got)
		}
	})
}
