package config

import (
	"strings"
	"testing"
)

func TestRouterOSContainerCommentRejectsAmbiguousIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"empty", "", ""},
		{"valid", "[Mikrozia:container] Container", "[Mikrozia:container] Container"},
		{"leading space", " [Mikrozia:container] Container", ""},
		{"trailing space", "[Mikrozia:container] Container ", ""},
		{"newline", "[Mikrozia:container] Container\n/other", ""},
		{"carriage return", "[Mikrozia:container] Container\r/other", ""},
		{"too long", strings.Repeat("a", 129), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(routerOSContainerCommentEnv, tc.input)
			t.Setenv(routerOSContainerCommentROSEnv, "")
			if got := RouterOSContainerComment(); got != tc.want {
				t.Fatalf("comment = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRouterOSContainerCommentUsesRouterOSAliasOnlyWhenCanonicalIsUnset(t *testing.T) {
	const comment = "[Mikrozia:container] Container"
	t.Setenv(routerOSContainerCommentEnv, "")
	t.Setenv(routerOSContainerCommentROSEnv, comment)
	if got := RouterOSContainerComment(); got != comment {
		t.Fatalf("RouterOS alias comment = %q, want %q", got, comment)
	}

	t.Setenv(routerOSContainerCommentEnv, "[canonical] Container")
	if got := RouterOSContainerComment(); got != "[canonical] Container" {
		t.Fatalf("canonical comment = %q, want canonical value", got)
	}

	// An invalid canonical comment must fail closed instead of falling back to the alias.
	t.Setenv(routerOSContainerCommentEnv, " invalid-leading-space")
	if got := RouterOSContainerComment(); got != "" {
		t.Fatalf("invalid canonical with alias = %q, want empty", got)
	}
}
