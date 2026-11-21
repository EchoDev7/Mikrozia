package service

import (
	"context"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/routeros"
)

func TestRouterOSContainerLogsRequiresVerifiedIdentityAndLogging(t *testing.T) {
	t.Setenv("MIKROZIA_ROUTEROS_API_HOST", "172.16.10.1")
	t.Setenv("MIKROZIA_ROUTEROS_API_USERNAME", "reader")
	t.Setenv("MIKROZIA_ROUTEROS_API_PASSWORD", "fixture-secret")
	t.Setenv("MIKROZIA_ROUTEROS_CONTAINER_COMMENT", "[Mikrozia:container] Container")
	for _, tc := range []struct {
		name      string
		snapshot  routeros.ContainerSnapshot
		wantState string
		wantReads int
	}{
		{"verified", routeros.ContainerSnapshot{State: "connected", Name: "mikrozia:v1", Logging: true}, "connected", 1},
		{"duplicate", routeros.ContainerSnapshot{State: "identity_unverified"}, "identity_unverified", 0},
		{"logging disabled", routeros.ContainerSnapshot{State: "connected", Name: "mikrozia:v1", Logging: false}, "logging_disabled", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			s := RouterOSContainerService{
				lookup: func(context.Context, string, string, string, string) routeros.ContainerSnapshot {
					return tc.snapshot
				},
				readLogs: func(_ context.Context, _, _, _, name string, count int) routeros.ContainerLogsSnapshot {
					reads++
					if name != "mikrozia:v1" || count != 20 {
						t.Fatalf("name=%q count=%d", name, count)
					}
					return routeros.ContainerLogsSnapshot{State: "connected", Entries: []routeros.ContainerLogEntry{{Time: "now", Message: "safe"}}}
				},
			}
			got := s.Logs(context.Background(), 20)
			if got.State != tc.wantState || reads != tc.wantReads {
				t.Fatalf("result=%+v reads=%d", got, reads)
			}
		})
	}
}

func TestRouterOSContainerLogsWithoutCommentDoesNotProbe(t *testing.T) {
	t.Setenv("MIKROZIA_ROUTEROS_API_HOST", "172.16.10.1")
	t.Setenv("MIKROZIA_ROUTEROS_API_USERNAME", "reader")
	t.Setenv("MIKROZIA_ROUTEROS_API_PASSWORD", "fixture-secret")
	t.Setenv("MIKROZIA_ROUTEROS_CONTAINER_COMMENT", "")
	s := RouterOSContainerService{lookup: func(context.Context, string, string, string, string) routeros.ContainerSnapshot {
		t.Fatal("unexpected RouterOS request")
		return routeros.ContainerSnapshot{}
	}}
	got := s.Logs(context.Background(), 20)
	if got.State != "not_configured" || len(got.Entries) != 0 {
		t.Fatalf("result=%+v", got)
	}
}

func TestRouterOSContainerRestartRequiresOperationalAccountAndVerifiedTarget(t *testing.T) {
	t.Setenv("MIKROZIA_ROUTEROS_API_HOST", "172.16.10.1")
	t.Setenv("MIKROZIA_ROUTEROS_API_USERNAME", "reader")
	t.Setenv("MIKROZIA_ROUTEROS_API_PASSWORD", "read-secret")
	t.Setenv("MIKROZIA_ROUTEROS_CONTAINER_COMMENT", "[Mikrozia:container] Container")
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_USERNAME", "")
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_PASSWORD", "")
	s := RouterOSContainerService{lookup: func(context.Context, string, string, string, string) routeros.ContainerSnapshot {
		return routeros.ContainerSnapshot{State: "connected", ID: "*A", Name: "mikrozia:v1", Status: "running"}
	}}
	if got := s.Restart(context.Background()); got.State != "not_configured" {
		t.Fatalf("without account: %+v", got)
	}
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_USERNAME", "operator")
	t.Setenv("MIKROZIA_ROUTEROS_RESTART_PASSWORD", "write-secret")
	called := 0
	s.restart = func(_ context.Context, host, user, password, comment, id, name string) routeros.ContainerRestartResult {
		called++
		if host != "172.16.10.1" || user != "operator" || password != "write-secret" || comment != "[Mikrozia:container] Container" || id != "*A" || name != "mikrozia:v1" {
			t.Fatalf("unexpected target or credentials")
		}
		return routeros.ContainerRestartResult{State: "accepted"}
	}
	if got := s.Restart(context.Background()); got.State != "accepted" || called != 1 {
		t.Fatalf("verified: %+v called=%d", got, called)
	}
	s.lookup = func(context.Context, string, string, string, string) routeros.ContainerSnapshot {
		return routeros.ContainerSnapshot{State: "identity_unverified"}
	}
	if got := s.Restart(context.Background()); got.State != "identity_unverified" || called != 1 {
		t.Fatalf("ambiguous: %+v called=%d", got, called)
	}
}
