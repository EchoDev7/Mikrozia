package config

import "testing"

func TestRouterOSContainerCommentRejectsAmbiguousIdentity(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{"", ""},
		{"[Mikrozia:container] Container", "[Mikrozia:container] Container"},
		{" [Mikrozia:container] Container", ""},
		{"[Mikrozia:container] Container\n/other", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Setenv("MIKROZIA_ROUTEROS_CONTAINER_COMMENT", tc.input)
			if got := RouterOSContainerComment(); got != tc.want {
				t.Fatalf("comment = %q, want %q", got, tc.want)
			}
		})
	}
}
