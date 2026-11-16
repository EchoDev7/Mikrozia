package config

import (
	"os"
	"strings"
	"testing"
)

func readCanonicalMikroziaVersion(t *testing.T) string {
	t.Helper()
	bytes, err := os.ReadFile("../../MIKROZIA_VERSION")
	if err != nil {
		t.Fatalf("could not read MIKROZIA_VERSION file: %v", err)
	}
	return string(bytes)
}

func TestGetPanelVersion(t *testing.T) {
	orig := mikroziaVersion
	t.Cleanup(func() { mikroziaVersion = orig })

	if got := GetBaseVersion(); got != "3.11.0" {
		t.Fatalf("GetBaseVersion = %q, want %q", got, "3.11.0")
	}
	if got := GetUpstreamVersion(); got != "3.11.0" {
		t.Fatalf("GetUpstreamVersion = %q, want %q", got, "3.11.0")
	}

	canonical := readCanonicalMikroziaVersion(t)
	if !releaseVersionPattern.MatchString(canonical) {
		t.Fatalf("canonical MIKROZIA_VERSION %q is not a valid release version", canonical)
	}

	tests := []struct {
		name     string
		injected string
		want     string
	}{
		{name: "missing linker value", injected: "", want: devVersion},
		{name: "whitespace linker value", injected: "  \n", want: devVersion},
		{name: "explicit dev", injected: "dev", want: devVersion},
		{name: "malformed version", injected: "v3.11", want: devVersion},
		{name: "missing v prefix", injected: strings.TrimPrefix(canonical, "v"), want: devVersion},
		{name: "empty suffix", injected: "v3.11.0-", want: devVersion},
		{name: "release version", injected: canonical, want: canonical},
		{name: "independent version", injected: "v1.0.0", want: "v1.0.0"},
		{name: "leading space", injected: " v1.0.0", want: devVersion},
		{name: "trailing space", injected: "v1.0.0 ", want: devVersion},
		{name: "trailing newline", injected: "v1.0.0\n", want: devVersion},
		{name: "trailing tab", injected: "v1.0.0\t", want: devVersion},
		{name: "embedded space", injected: "v1. 0.0", want: devVersion},
		{name: "multiline", injected: "v1.0.0\nv1.0.0", want: devVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mikroziaVersion = tt.injected
			if got := GetMikroziaVersion(); got != tt.want {
				t.Fatalf("GetMikroziaVersion = %q, want %q", got, tt.want)
			}
			if got := GetPanelVersion(); got != tt.want {
				t.Fatalf("GetPanelVersion = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMissingInjectionNeverFallsBackToCanonicalRelease(t *testing.T) {
	orig := mikroziaVersion
	t.Cleanup(func() { mikroziaVersion = orig })

	canonical := readCanonicalMikroziaVersion(t)
	if !releaseVersionPattern.MatchString(canonical) {
		t.Fatalf("canonical MIKROZIA_VERSION %q is not a valid release version", canonical)
	}

	for _, injected := range []string{
		"", " ", "\t", "\n", "dev", "v3.11", "v3.11.0-",
		" " + canonical,
		canonical + " ",
		canonical + "\t",
		canonical + "\n",
		strings.Replace(canonical, "-", "- ", 1),
		canonical + "\n" + canonical,
	} {
		mikroziaVersion = injected
		if got := GetPanelVersion(); got != devVersion {
			t.Fatalf("injected %q produced %q, want %q", injected, got, devVersion)
		}
	}
}

func TestUpstreamVersionStaysSeparateFromMikroziaIdentity(t *testing.T) {
	upstream := GetUpstreamVersion()
	if strings.Contains(strings.ToLower(upstream), "mikrozia") {
		t.Fatalf("upstream version %q must not carry Mikrozia identity", upstream)
	}
	if upstream == GetMikroziaVersion() {
		t.Fatalf("upstream version %q must stay separate from the Mikrozia release version", upstream)
	}
}

func TestGetPortOverride(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		set        bool
		wantPort   int
		configured bool
		wantErr    bool
	}{
		{name: "unset"},
		{name: "empty", value: "", set: true},
		{name: "whitespace", value: "   ", set: true},
		{name: "minimum", value: "1", set: true, wantPort: 1, configured: true},
		{name: "default panel port", value: "2053", set: true, wantPort: 2053, configured: true},
		{name: "surrounding whitespace", value: " 8080 ", set: true, wantPort: 8080, configured: true},
		{name: "maximum", value: "65535", set: true, wantPort: 65535, configured: true},
		{name: "zero", value: "0", set: true, configured: true, wantErr: true},
		{name: "above maximum", value: "65536", set: true, configured: true, wantErr: true},
		{name: "negative", value: "-1", set: true, configured: true, wantErr: true},
		{name: "non-numeric", value: "abc", set: true, configured: true, wantErr: true},
		{name: "decimal", value: "8080.0", set: true, configured: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("XUI_PORT", tt.value)
			} else {
				original, existed := os.LookupEnv("XUI_PORT")
				if err := os.Unsetenv("XUI_PORT"); err != nil {
					t.Fatalf("unset XUI_PORT: %v", err)
				}
				t.Cleanup(func() {
					if existed {
						_ = os.Setenv("XUI_PORT", original)
					} else {
						_ = os.Unsetenv("XUI_PORT")
					}
				})
			}

			port, configured, err := GetPortOverride()
			if port != tt.wantPort {
				t.Errorf("port = %d, want %d", port, tt.wantPort)
			}
			if configured != tt.configured {
				t.Errorf("configured = %t, want %t", configured, tt.configured)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
