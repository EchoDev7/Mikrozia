package main

import (
	"os"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
)

func TestContainerCredentialRefusalPrecedesDatabaseAccess(t *testing.T) {
	t.Setenv("XUI_IN_DOCKER", "true")
	t.Setenv(config.AdminUsernameEnv, "")
	t.Setenv(config.AdminPasswordEnv, "")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	t.Setenv("XUI_DB_FOLDER", dir)
	err := updateSetting(0, "ephemeral-request", "", "", "", false)
	if err == nil || err.Error() != "administrator credential mutation is disabled in container mode" {
		t.Fatalf("refusal=%v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("refused credential request touched database storage")
	}
}

// The administrator credential must never reach a log line, an API response,
// or the console. The startup path reconciles the stored account from the
// container environment and reports only which source it came from.
func TestTheStartupPathNeverPrintsTheAdministratorCredential(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(source)

	forbidden := []string{
		"Password: %s",
		"Username: %s",
		"password: %s",
		"username: %s",
		"EnsureFirstUser",
	}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Errorf("main.go must not contain %q: the administrator credential may never be printed", needle)
		}
	}
	if !strings.Contains(text, "ReconcileAdminAccount") {
		t.Error("main.go must reconcile the administrator account at startup")
	}
}

// The environment values are only read by the reconciliation and by the
// refusal message of the mutating paths. They are never formatted into a
// message that also carries their value.
func TestTheEnvironmentValuesAreNeverFormattedIntoAMessage(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	lines := strings.Split(string(source), "\n")
	for i, line := range lines {
		if !strings.Contains(line, "GetAdminUsername()") && !strings.Contains(line, "GetAdminPassword()") {
			continue
		}
		if strings.Contains(line, "Print") || strings.Contains(line, "Fatal") || strings.Contains(line, "logger.") {
			t.Errorf("main.go:%d reads the environment value inside a log statement: %s", i+1, strings.TrimSpace(line))
		}
	}
}

// The mutating paths refuse in the environment-managed mode instead of writing
// a competing credential.
func TestTheMutatingPathsRefuseInTheEnvironmentManagedMode(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "AdminCredentialMutationForbidden()") {
		t.Error("main.go must refuse the credential write when the environment owns the account")
	}
	if !strings.Contains(text, "Refusing to change the administrator credential") {
		t.Error("main.go must explain the refusal")
	}
}
