package panel

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
)

// initEmptyDB opens a fresh panel database without an administrator account.
func initEmptyDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x-ui.db")
	if err := database.InitDB(path); err != nil {
		t.Fatalf("init database: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })
}

// setAdminEnvironment sets the container environment values for a test.
func setAdminEnvironment(t *testing.T, username string, password string) {
	t.Helper()
	t.Setenv(config.AdminUsernameEnv, username)
	t.Setenv(config.AdminPasswordEnv, password)
}

// storedUser returns the first stored account.
func storedUser(t *testing.T) *model.User {
	t.Helper()
	user, err := (&UserService{}).GetFirstUser()
	if err != nil {
		t.Fatalf("read the stored account: %v", err)
	}
	return user
}

func TestAHalfConfiguredEnvironmentPairFailsClosed(t *testing.T) {
	initEmptyDB(t)
	setAdminEnvironment(t, "env-admin", "")

	state, err := (&UserService{}).ReconcileAdminAccount()
	if !errors.Is(err, ErrAdminCredentialPairIncomplete) {
		t.Fatalf("expected the incomplete-pair error, got %v", err)
	}
	if state.Source != "" {
		t.Errorf("a refused reconciliation must not report a source, got %q", state.Source)
	}
	if _, err := (&UserService{}).GetFirstUser(); !database.IsNotFound(err) {
		t.Error("a refused reconciliation must not create an account")
	}
}

func TestACleanFirstBootUsesTheEnvironmentCredential(t *testing.T) {
	initEmptyDB(t)
	setAdminEnvironment(t, "env-admin", "env-admin-password")

	state, err := (&UserService{}).ReconcileAdminAccount()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if state.Source != AdminCredentialFromEnvironment || !state.Managed || !state.Changed {
		t.Fatalf("unexpected state %+v", state)
	}
	user := storedUser(t)
	if user.Username != "env-admin" {
		t.Errorf("unexpected username %q", user.Username)
	}
	if !crypto.CheckPasswordHash(user.Password, "env-admin-password") {
		t.Error("the stored password does not match the environment value")
	}
}

func TestALegacyAccountSurvivesWithoutTheEnvironment(t *testing.T) {
	initEmptyDB(t)
	setAdminEnvironment(t, "", "")
	if err := database.EnsureFirstUser("legacy-admin", "legacy-admin-password"); err != nil {
		t.Fatalf("seed the legacy account: %v", err)
	}
	before := storedUser(t)

	state, err := (&UserService{}).ReconcileAdminAccount()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if state.Source != AdminCredentialPreserved {
		t.Fatalf("unexpected state %+v", state)
	}
	if state.Changed {
		t.Error("a preserved account must not be reported as changed")
	}
	after := storedUser(t)
	if after.Username != before.Username {
		t.Errorf("the username changed from %q to %q", before.Username, after.Username)
	}
	if after.Password != before.Password {
		t.Error("the password hash of a legacy account must not change")
	}
	if !crypto.CheckPasswordHash(after.Password, "legacy-admin-password") {
		t.Error("the legacy password must keep working")
	}
}

func TestAnExistingAccountIsReconciledWithTheEnvironment(t *testing.T) {
	initEmptyDB(t)
	setAdminEnvironment(t, "", "")
	if err := database.EnsureFirstUser("legacy-admin", "legacy-admin-password"); err != nil {
		t.Fatalf("seed the legacy account: %v", err)
	}
	setAdminEnvironment(t, "env-admin", "env-admin-password")

	state, err := (&UserService{}).ReconcileAdminAccount()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if state.Source != AdminCredentialFromEnvironment || !state.Changed {
		t.Fatalf("unexpected state %+v", state)
	}
	user := storedUser(t)
	if user.Username != "env-admin" {
		t.Errorf("unexpected username %q", user.Username)
	}
	if !crypto.CheckPasswordHash(user.Password, "env-admin-password") {
		t.Error("the stored password does not match the environment value")
	}
}

func TestTheReconciliationIsIdempotent(t *testing.T) {
	initEmptyDB(t)
	setAdminEnvironment(t, "env-admin", "env-admin-password")

	if _, err := (&UserService{}).ReconcileAdminAccount(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	state, err := (&UserService{}).ReconcileAdminAccount()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if state.Changed {
		t.Error("a second reconciliation must not change the stored account")
	}
}

func TestCredentialChangesInvalidateSessionsAndMatchingEnvironmentDoesNotWrite(t *testing.T) {
	initEmptyDB(t)
	setAdminEnvironment(t, "ephemeral-admin", "ephemeral-password")
	svc := &UserService{}
	if _, err := svc.ReconcileAdminAccount(); err != nil {
		t.Fatal(err)
	}
	before := *storedUser(t)
	state, err := svc.ReconcileAdminAccount()
	after := *storedUser(t)
	if err != nil || state.Changed || before != after {
		t.Fatal("matching environment changed persisted account")
	}
	t.Setenv(config.AdminUsernameEnv, "ephemeral-renamed-admin")
	if _, err := svc.ReconcileAdminAccount(); err != nil {
		t.Fatal(err)
	}
	renamed := *storedUser(t)
	if renamed.LoginEpoch != before.LoginEpoch+1 || renamed.Password != before.Password {
		t.Fatal("username rotation did not invalidate sessions while preserving password hash")
	}
	t.Setenv(config.AdminPasswordEnv, "ephemeral-replacement-password")
	if _, err := svc.ReconcileAdminAccount(); err != nil {
		t.Fatal(err)
	}
	rotated := storedUser(t)
	if rotated.LoginEpoch != renamed.LoginEpoch+1 || !crypto.CheckPasswordHash(rotated.Password, "ephemeral-replacement-password") {
		t.Fatal("password rotation did not invalidate prior sessions")
	}
}

func TestTheLegacyDefaultAccountIsReportedAndKept(t *testing.T) {
	initEmptyDB(t)
	setAdminEnvironment(t, "", "")
	if err := database.EnsureFirstUser("admin", "admin"); err != nil {
		t.Fatalf("seed the legacy default account: %v", err)
	}

	state, err := (&UserService{}).ReconcileAdminAccount()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if state.Source != AdminCredentialLegacyDefault {
		t.Fatalf("unexpected state %+v", state)
	}
	user := storedUser(t)
	if user.Username != "admin" {
		t.Errorf("the legacy account must not be renamed, got %q", user.Username)
	}
}

func TestAMissingAccountWithoutTheEnvironmentIsReported(t *testing.T) {
	initEmptyDB(t)
	setAdminEnvironment(t, "", "")

	state, err := (&UserService{}).ReconcileAdminAccount()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if state.Source != AdminCredentialMissing {
		t.Fatalf("unexpected state %+v", state)
	}
	if state.Managed {
		t.Error("a missing account is not managed by the environment")
	}
}

func TestTheInspectionNeverChangesTheStoredAccount(t *testing.T) {
	initEmptyDB(t)
	setAdminEnvironment(t, "env-admin", "env-admin-password")
	if err := database.EnsureFirstUser("legacy-admin", "legacy-admin-password"); err != nil {
		t.Fatalf("seed the legacy account: %v", err)
	}
	before := storedUser(t)

	state, err := (&UserService{}).InspectAdminAccount()
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !state.Managed || state.Source != AdminCredentialFromEnvironment {
		t.Fatalf("unexpected state %+v", state)
	}
	after := storedUser(t)
	if after.Username != before.Username || after.Password != before.Password {
		t.Error("the inspection must not change the stored account")
	}
}
