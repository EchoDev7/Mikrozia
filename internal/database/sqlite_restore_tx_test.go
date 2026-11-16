package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func seedPanelSQLiteDB(t *testing.T, dbPath, markerValue string) {
	t.Helper()
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB(%q): %v", dbPath, err)
	}
	if err := GetDB().Exec(`INSERT OR REPLACE INTO settings(key, value) VALUES('remarkTemplate', ?)`, markerValue).Error; err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	if err := CloseDB(); err != nil {
		t.Fatalf("CloseDB: %v", err)
	}
}

func readSettingFromSQLiteFile(t *testing.T, dbPath, key string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", dbPath, err)
	}
	defer db.Close()
	var val string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val); err != nil {
		t.Fatalf("query %s from %s: %v", key, dbPath, err)
	}
	return val
}

func TestSQLiteRestoreTransaction_WALFallbackConsistencyAndPermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	livePath := filepath.Join(dir, "x-ui.db")
	candSrcPath := filepath.Join(dir, "cand-src.db")

	seedPanelSQLiteDB(t, candSrcPath, "NEW-DATA")
	seedPanelSQLiteDB(t, livePath, "OLD-BASE")

	// Keep live DB open in WAL mode and commit a row that lives in -wal without checkpointing.
	if err := InitDB(livePath); err != nil {
		t.Fatalf("InitDB(live): %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	if err := GetDB().Exec(`PRAGMA journal_mode=WAL;`).Error; err != nil {
		t.Fatalf("enable WAL: %v", err)
	}
	if err := GetDB().Exec(`UPDATE settings SET value = 'OLD-WAL-COMMITTED' WHERE key = 'remarkTemplate'`).Error; err != nil {
		t.Fatalf("update in WAL: %v", err)
	}

	tx, err := BeginSQLiteRestore(livePath)
	if err != nil {
		t.Fatalf("BeginSQLiteRestore: %v", err)
	}
	defer func() { _ = tx.AbortBeforeApply() }()

	opInfo, err := os.Lstat(tx.OpDir())
	if err != nil || opInfo.Mode().Perm() != 0o700 {
		t.Fatalf("opDir perm = %v err = %v, want 0700", opInfo.Mode().Perm(), err)
	}

	if err := tx.PrepareCandidate(candSrcPath, false); err != nil {
		t.Fatalf("PrepareCandidate: %v", err)
	}
	if err := tx.CreateFallbackSnapshot(); err != nil {
		t.Fatalf("CreateFallbackSnapshot: %v", err)
	}

	candInfo, err := os.Lstat(tx.CandidatePath())
	if err != nil || candInfo.Mode().Perm() != 0o600 {
		t.Fatalf("candidate perm = %v err = %v, want 0600", candInfo.Mode().Perm(), err)
	}
	fbInfo, err := os.Lstat(tx.FallbackPath())
	if err != nil || fbInfo.Mode().Perm() != 0o600 {
		t.Fatalf("fallback perm = %v err = %v, want 0600", fbInfo.Mode().Perm(), err)
	}

	// Verify fallback snapshot captured the WAL-committed value!
	if got := readSettingFromSQLiteFile(t, tx.FallbackPath(), "remarkTemplate"); got != "OLD-WAL-COMMITTED" {
		t.Fatalf("fallback value = %q, want OLD-WAL-COMMITTED", got)
	}

	if err := tx.WritePreparedState(); err != nil {
		t.Fatalf("WritePreparedState: %v", err)
	}
	statePath := filepath.Join(dir, sqliteRestoreStateFilename)
	stInfo, err := os.Lstat(statePath)
	if err != nil || stInfo.Mode().Perm() != 0o600 {
		t.Fatalf("state perm = %v err = %v, want 0600", stInfo.Mode().Perm(), err)
	}
	rawState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	lowerState := strings.ToLower(string(rawState))
	for _, forbidden := range []string{"password", "postgres://", "token", "secret", "webport", "webcertfile"} {
		if strings.Contains(lowerState, forbidden) {
			t.Fatalf("state marker leaked %q: %s", forbidden, string(rawState))
		}
	}

	// Concurrent second transaction must be rejected.
	if _, err := BeginSQLiteRestore(livePath); err == nil {
		t.Fatal("expected second concurrent BeginSQLiteRestore to fail")
	}

	_ = CloseDB()
}

func TestRecoverPendingSQLiteRestore_ValidationAndPhases(t *testing.T) {
	t.Run("invalid markers are preserved and halt startup", func(t *testing.T) {
		cases := []struct {
			name    string
			payload string
			setup   func(t *testing.T, dir, statePath string)
		}{
			{
				name:    "unknown version",
				payload: `{"version":99,"phase":"prepared","target_db":"x-ui.db","op_dir":".mikrozia-sqlite-restore-1","candidate":"candidate.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"op1","created_at":"2026-11-13T12:00:00Z"}`,
			},
			{
				name:    "corrupt json",
				payload: `{not-valid-json`,
			},
			{
				name:    "dotdot traversal",
				payload: `{"version":1,"phase":"applying","target_db":"x-ui.db","op_dir":"../escape","candidate":"candidate.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"op1","created_at":"2026-11-13T12:00:00Z"}`,
			},
			{
				name:    "absolute path",
				payload: `{"version":1,"phase":"applying","target_db":"x-ui.db","op_dir":"/tmp/evil","candidate":"candidate.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"op1","created_at":"2026-11-13T12:00:00Z"}`,
			},
			{
				name:    "candidate and fallback swapped",
				payload: `{"version":1,"phase":"applying","target_db":"x-ui.db","op_dir":".mikrozia-sqlite-restore-1","candidate":"fallback.db","fallback":"candidate.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"op1","created_at":"2026-11-13T12:00:00Z"}`,
			},
			{
				name:    "unexpected relative candidate name",
				payload: `{"version":1,"phase":"applying","target_db":"x-ui.db","op_dir":".mikrozia-sqlite-restore-1","candidate":"other.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"op1","created_at":"2026-11-13T12:00:00Z"}`,
			},
			{
				name:    "empty operation_id",
				payload: `{"version":1,"phase":"applying","target_db":"x-ui.db","op_dir":".mikrozia-sqlite-restore-1","candidate":"candidate.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"","created_at":"2026-11-13T12:00:00Z"}`,
			},
			{
				name:    "invalid operation_id",
				payload: `{"version":1,"phase":"applying","target_db":"x-ui.db","op_dir":".mikrozia-sqlite-restore-1","candidate":"candidate.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"bad/id","created_at":"2026-11-13T12:00:00Z"}`,
			},
			{
				name:    "invalid created_at",
				payload: `{"version":1,"phase":"applying","target_db":"x-ui.db","op_dir":".mikrozia-sqlite-restore-1","candidate":"candidate.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"op1","created_at":"not-a-timestamp"}`,
			},
			{
				name:    "target_db mismatch",
				payload: `{"version":1,"phase":"applying","target_db":"other.db","op_dir":".mikrozia-sqlite-restore-1","candidate":"candidate.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"op1","created_at":"2026-11-13T12:00:00Z"}`,
			},
			{
				name:    "symlink op_dir",
				payload: `{"version":1,"phase":"applying","target_db":"x-ui.db","op_dir":".mikrozia-sqlite-restore-sym","candidate":"candidate.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"op1","created_at":"2026-11-13T12:00:00Z"}`,
				setup: func(t *testing.T, dir, _ string) {
					if runtime.GOOS == "windows" {
						t.Skip("POSIX symlink test")
					}
					realDir := filepath.Join(dir, "real")
					if err := os.Mkdir(realDir, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(realDir, filepath.Join(dir, ".mikrozia-sqlite-restore-sym")); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name:    "insecure marker permissions 0644",
				payload: `{"version":1,"phase":"applying","target_db":"x-ui.db","op_dir":".mikrozia-sqlite-restore-1","candidate":"candidate.db","fallback":"fallback.db","prev_live":"previous-live.db","recovery_copy":"recovery.db","failed_live":"failed-live.db","operation_id":"op1","created_at":"2026-11-13T12:00:00Z"}`,
				setup: func(t *testing.T, _, statePath string) {
					if runtime.GOOS == "windows" {
						t.Skip("POSIX permission test")
					}
					t.Cleanup(func() { _ = os.Chmod(statePath, 0o600) })
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				livePath := filepath.Join(dir, "x-ui.db")
				statePath := filepath.Join(dir, sqliteRestoreStateFilename)
				if tc.setup != nil {
					tc.setup(t, dir, statePath)
				}
				if err := os.WriteFile(statePath, []byte(tc.payload), 0o600); err != nil {
					t.Fatal(err)
				}
				if tc.name == "insecure marker permissions 0644" {
					if err := os.Chmod(statePath, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if err := RecoverPendingSQLiteRestore(livePath); err == nil {
					t.Fatalf("expected RecoverPendingSQLiteRestore to fail for %s", tc.name)
				}
				got, readErr := os.ReadFile(statePath)
				if readErr != nil || string(got) != tc.payload {
					t.Fatalf("invalid state marker was modified or deleted! got=%q err=%v", got, readErr)
				}
			})
		}
	})

	t.Run("unix insecure opDir or artifact permissions and symlinks halt recovery", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission/symlink test")
		}
		validState := func(opDirName string) sqliteRestoreState {
			return sqliteRestoreState{
				Version:      sqliteRestoreStateVersion,
				Phase:        restorePhaseApplying,
				TargetDB:     "x-ui.db",
				OpDir:        opDirName,
				Candidate:    sqliteRestoreCandidateFile,
				Fallback:     sqliteRestoreFallbackFile,
				PrevLive:     sqliteRestorePrevLiveFile,
				RecoveryCopy: sqliteRestoreRecoveryFile,
				FailedLive:   sqliteRestoreFailedLiveFile,
				OperationID:  "unit-perm",
				CreatedAt:    "2026-11-13T12:00:00Z",
			}
		}

		t.Run("insecure opDir 0755", func(t *testing.T) {
			dir := t.TempDir()
			opDirName := ".mikrozia-sqlite-restore-perm"
			opDir := filepath.Join(dir, opDirName)
			if err := os.Mkdir(opDir, 0o755); err != nil {
				t.Fatal(err)
			}
			_ = os.Chmod(opDir, 0o755)
			statePath := filepath.Join(dir, sqliteRestoreStateFilename)
			if err := writeRestoreStateDurable(dir, statePath, validState(opDirName)); err != nil {
				t.Fatal(err)
			}
			if err := RecoverPendingSQLiteRestore(filepath.Join(dir, "x-ui.db")); err == nil {
				t.Fatal("expected error for insecure opDir permissions")
			}
			if _, err := os.Lstat(statePath); err != nil {
				t.Fatalf("marker should be preserved: %v", err)
			}
			if _, err := os.Lstat(opDir); err != nil {
				t.Fatalf("opDir should be preserved: %v", err)
			}
		})

		t.Run("insecure fallback 0644", func(t *testing.T) {
			dir := t.TempDir()
			opDirName := ".mikrozia-sqlite-restore-fbperm"
			opDir := filepath.Join(dir, opDirName)
			if err := os.Mkdir(opDir, 0o700); err != nil {
				t.Fatal(err)
			}
			fbPath := filepath.Join(opDir, sqliteRestoreFallbackFile)
			if err := os.WriteFile(fbPath, []byte("fb"), 0o644); err != nil {
				t.Fatal(err)
			}
			_ = os.Chmod(fbPath, 0o644)
			statePath := filepath.Join(dir, sqliteRestoreStateFilename)
			if err := writeRestoreStateDurable(dir, statePath, validState(opDirName)); err != nil {
				t.Fatal(err)
			}
			if err := RecoverPendingSQLiteRestore(filepath.Join(dir, "x-ui.db")); err == nil {
				t.Fatal("expected error for insecure fallback permissions")
			}
			if _, err := os.Lstat(fbPath); err != nil {
				t.Fatalf("fallback should be preserved: %v", err)
			}
		})

		t.Run("symlinked fallback or candidate", func(t *testing.T) {
			for _, targetName := range []string{sqliteRestoreFallbackFile, sqliteRestoreCandidateFile} {
				t.Run(targetName, func(t *testing.T) {
					dir := t.TempDir()
					opDirName := ".mikrozia-sqlite-restore-symart"
					opDir := filepath.Join(dir, opDirName)
					if err := os.Mkdir(opDir, 0o700); err != nil {
						t.Fatal(err)
					}
					realFile := filepath.Join(dir, "real.db")
					if err := os.WriteFile(realFile, []byte("x"), 0o600); err != nil {
						t.Fatal(err)
					}
					artPath := filepath.Join(opDir, targetName)
					if err := os.Symlink(realFile, artPath); err != nil {
						t.Fatal(err)
					}
					statePath := filepath.Join(dir, sqliteRestoreStateFilename)
					if err := writeRestoreStateDurable(dir, statePath, validState(opDirName)); err != nil {
						t.Fatal(err)
					}
					if err := RecoverPendingSQLiteRestore(filepath.Join(dir, "x-ui.db")); err == nil {
						t.Fatalf("expected error for symlinked %s", targetName)
					}
					if _, err := os.Lstat(artPath); err != nil {
						t.Fatalf("artifact should be preserved: %v", err)
					}
				})
			}
		})
	})

	t.Run("all phases recover missing or corrupt live DB and are idempotent", func(t *testing.T) {
		phases := []sqliteRestorePhase{
			restorePhasePrepared,
			restorePhaseApplying,
			restorePhaseVerifying,
			restorePhaseCommitted,
			restorePhaseRollingBack,
		}
		for _, mode := range []string{"missing_live", "corrupt_live"} {
			for _, phase := range phases {
				t.Run(mode+"_"+string(phase), func(t *testing.T) {
					dir := t.TempDir()
					t.Setenv("XUI_DB_FOLDER", dir)
					livePath := filepath.Join(dir, "x-ui.db")
					opDirName := ".mikrozia-sqlite-restore-unit"
					opDir := filepath.Join(dir, opDirName)
					if err := os.Mkdir(opDir, 0o700); err != nil {
						t.Fatal(err)
					}
					fbPath := filepath.Join(opDir, sqliteRestoreFallbackFile)
					seedPanelSQLiteDB(t, fbPath, "RECOVERED-VALUE")

					if mode == "corrupt_live" {
						if err := os.WriteFile(livePath, []byte("corrupt-bytes"), 0o600); err != nil {
							t.Fatal(err)
						}
					}

					state := sqliteRestoreState{
						Version:      sqliteRestoreStateVersion,
						Phase:        phase,
						TargetDB:     "x-ui.db",
						OpDir:        opDirName,
						Candidate:    sqliteRestoreCandidateFile,
						Fallback:     sqliteRestoreFallbackFile,
						PrevLive:     sqliteRestorePrevLiveFile,
						RecoveryCopy: sqliteRestoreRecoveryFile,
						FailedLive:   sqliteRestoreFailedLiveFile,
						OperationID:  "unit-op",
						CreatedAt:    "2026-11-13T12:00:00Z",
					}
					statePath := filepath.Join(dir, sqliteRestoreStateFilename)
					if err := writeRestoreStateDurable(dir, statePath, state); err != nil {
						t.Fatal(err)
					}

					if err := RecoverPendingSQLiteRestore(livePath); err != nil {
						t.Fatalf("RecoverPendingSQLiteRestore(%s, %s): %v", mode, phase, err)
					}
					if err := ValidateSQLiteDB(livePath); err != nil {
						t.Fatalf("recovered live DB invalid: %v", err)
					}
					if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "RECOVERED-VALUE" {
						t.Fatalf("recovered value = %q, want RECOVERED-VALUE", got)
					}
					for _, suffix := range []string{"-wal", "-shm", "-journal"} {
						if _, err := os.Lstat(livePath + suffix); !errors.Is(err, os.ErrNotExist) {
							t.Fatalf("unexpected sidecar %s after recovery", suffix)
						}
					}
					if _, err := os.Lstat(statePath); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("state marker should be removed after recovery")
					}
					if _, err := os.Lstat(opDir); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("opDir should be removed after recovery")
					}
					// Idempotent second call
					if err := RecoverPendingSQLiteRestore(livePath); err != nil {
						t.Fatalf("second RecoverPendingSQLiteRestore should be no-op, got: %v", err)
					}
				})
			}
		}
	})

	t.Run("prepared with healthy live DB keeps live DB untouched", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		seedPanelSQLiteDB(t, livePath, "LIVE-ORIGINAL")

		opDirName := ".mikrozia-sqlite-restore-prep"
		opDir := filepath.Join(dir, opDirName)
		if err := os.Mkdir(opDir, 0o700); err != nil {
			t.Fatal(err)
		}
		seedPanelSQLiteDB(t, filepath.Join(opDir, sqliteRestoreFallbackFile), "FALLBACK-OLD")

		state := sqliteRestoreState{
			Version:      sqliteRestoreStateVersion,
			Phase:        restorePhasePrepared,
			TargetDB:     "x-ui.db",
			OpDir:        opDirName,
			Candidate:    sqliteRestoreCandidateFile,
			Fallback:     sqliteRestoreFallbackFile,
			PrevLive:     sqliteRestorePrevLiveFile,
			RecoveryCopy: sqliteRestoreRecoveryFile,
			FailedLive:   sqliteRestoreFailedLiveFile,
			OperationID:  "unit-prep",
			CreatedAt:    "2026-11-13T12:00:00Z",
		}
		statePath := filepath.Join(dir, sqliteRestoreStateFilename)
		if err := writeRestoreStateDurable(dir, statePath, state); err != nil {
			t.Fatal(err)
		}

		if err := RecoverPendingSQLiteRestore(livePath); err != nil {
			t.Fatalf("RecoverPendingSQLiteRestore: %v", err)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "LIVE-ORIGINAL" {
			t.Fatalf("got %q, want LIVE-ORIGINAL", got)
		}
	})

	t.Run("committed with healthy live DB completes cleanup and keeps new data", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		seedPanelSQLiteDB(t, livePath, "COMMITTED-NEW")

		opDirName := ".mikrozia-sqlite-restore-comm"
		opDir := filepath.Join(dir, opDirName)
		if err := os.Mkdir(opDir, 0o700); err != nil {
			t.Fatal(err)
		}
		seedPanelSQLiteDB(t, filepath.Join(opDir, sqliteRestoreFallbackFile), "FALLBACK-OLD")

		state := sqliteRestoreState{
			Version:      sqliteRestoreStateVersion,
			Phase:        restorePhaseCommitted,
			TargetDB:     "x-ui.db",
			OpDir:        opDirName,
			Candidate:    sqliteRestoreCandidateFile,
			Fallback:     sqliteRestoreFallbackFile,
			PrevLive:     sqliteRestorePrevLiveFile,
			RecoveryCopy: sqliteRestoreRecoveryFile,
			FailedLive:   sqliteRestoreFailedLiveFile,
			OperationID:  "unit-comm",
			CreatedAt:    "2026-11-13T12:00:00Z",
		}
		statePath := filepath.Join(dir, sqliteRestoreStateFilename)
		if err := writeRestoreStateDurable(dir, statePath, state); err != nil {
			t.Fatal(err)
		}

		if err := RecoverPendingSQLiteRestore(livePath); err != nil {
			t.Fatalf("RecoverPendingSQLiteRestore: %v", err)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "COMMITTED-NEW" {
			t.Fatalf("got %q, want COMMITTED-NEW", got)
		}
		if _, err := os.Lstat(statePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("state marker still exists")
		}
	})
}

func TestSQLiteRestoreCrashFailpoints_Subprocess(t *testing.T) {
	failpoints := []struct {
		point    string
		wantData string
	}{
		{point: "prepared", wantData: "OLD-LIVE-DATA"},
		{point: "applying", wantData: "OLD-LIVE-DATA"},
		{point: "after_move_live", wantData: "OLD-LIVE-DATA"},
		{point: "after_move_candidate", wantData: "NEW-CAND-DATA"}, // wait: after_move_candidate is still in applying phase!
		{point: "verifying", wantData: "OLD-LIVE-DATA"},
		{point: "after_init_new_db", wantData: "OLD-LIVE-DATA"},
		{point: "committed_before_cleanup", wantData: "NEW-CAND-DATA"},
		{point: "rolling_back", wantData: "OLD-LIVE-DATA"},
		{point: "rolled_back_before_cleanup", wantData: "OLD-LIVE-DATA"},
	}
	// Correct expectation: in applying phase, after_move_candidate has NOT committed yet,
	// so startup recovery MUST roll back to OLD-LIVE-DATA!
	failpoints[3].wantData = "OLD-LIVE-DATA"

	for _, fp := range failpoints {
		t.Run(fp.point, func(t *testing.T) {
			dir := t.TempDir()
			livePath := filepath.Join(dir, "x-ui.db")
			candSrcPath := filepath.Join(dir, "cand.db")
			seedPanelSQLiteDB(t, livePath, "OLD-LIVE-DATA")
			seedPanelSQLiteDB(t, candSrcPath, "NEW-CAND-DATA")

			cmd := exec.Command(os.Args[0], "-test.run=^TestSQLiteRestoreCrashHelper$")
			cmd.Env = append(os.Environ(),
				"MIKROZIA_TEST_CRASH_HELPER=1",
				"MIKROZIA_TEST_CRASH_DIR="+dir,
				"MIKROZIA_TEST_CRASH_POINT="+fp.point,
			)
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 91 {
				t.Fatalf("subprocess for %s expected exit 91, got err=%v out=%s", fp.point, err, string(out))
			}

			// Verify state marker exists before startup recovery.
			statePath := filepath.Join(dir, sqliteRestoreStateFilename)
			raw, readErr := os.ReadFile(statePath)
			if readErr != nil {
				t.Fatalf("expected durable marker at %s after crash at %s: %v", statePath, fp.point, readErr)
			}
			var st sqliteRestoreState
			if uErr := json.Unmarshal(raw, &st); uErr != nil {
				t.Fatalf("unmarshal marker after crash: %v", uErr)
			}

			// Second process runs startup recovery before InitDB.
			if err := RecoverPendingSQLiteRestore(livePath); err != nil {
				t.Fatalf("RecoverPendingSQLiteRestore(%s): %v", fp.point, err)
			}
			if err := ValidateSQLiteDB(livePath); err != nil {
				t.Fatalf("ValidateSQLiteDB after recovery(%s): %v", fp.point, err)
			}
			got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate")
			if got != fp.wantData {
				t.Fatalf("after crash at %s, recovered data = %q, want %q", fp.point, got, fp.wantData)
			}
			if _, err := os.Lstat(statePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("state marker remained after recovery(%s)", fp.point)
			}
			opDir := filepath.Join(dir, st.OpDir)
			if _, err := os.Lstat(opDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("opDir remained after recovery(%s)", fp.point)
			}
		})
	}
}

func TestSQLiteRestoreCrashHelper(t *testing.T) {
	if os.Getenv("MIKROZIA_TEST_CRASH_HELPER") != "1" {
		return
	}
	dir := os.Getenv("MIKROZIA_TEST_CRASH_DIR")
	targetPoint := os.Getenv("MIKROZIA_TEST_CRASH_POINT")
	_ = os.Setenv("XUI_DB_FOLDER", dir)

	livePath := filepath.Join(dir, "x-ui.db")
	candSrcPath := filepath.Join(dir, "cand.db")

	sqliteRestoreFailpointHook = func(point string) {
		if point == targetPoint {
			os.Exit(91)
		}
	}

	if err := InitDB(livePath); err != nil {
		fmt.Fprintf(os.Stderr, "InitDB(live): %v\n", err)
		os.Exit(2)
	}
	tx, err := BeginSQLiteRestore(livePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "BeginSQLiteRestore: %v\n", err)
		os.Exit(3)
	}
	if err := tx.PrepareCandidate(candSrcPath, false); err != nil {
		fmt.Fprintf(os.Stderr, "PrepareCandidate: %v\n", err)
		os.Exit(4)
	}
	if err := tx.CreateFallbackSnapshot(); err != nil {
		fmt.Fprintf(os.Stderr, "CreateFallbackSnapshot: %v\n", err)
		os.Exit(5)
	}
	if err := tx.WritePreparedState(); err != nil {
		fmt.Fprintf(os.Stderr, "WritePreparedState: %v\n", err)
		os.Exit(6)
	}
	if err := CloseDB(); err != nil {
		fmt.Fprintf(os.Stderr, "CloseDB: %v\n", err)
		os.Exit(7)
	}
	if err := tx.Apply(); err != nil {
		fmt.Fprintf(os.Stderr, "Apply: %v\n", err)
		os.Exit(8)
	}
	if err := InitDB(livePath); err != nil {
		fmt.Fprintf(os.Stderr, "InitDB(new): %v\n", err)
		os.Exit(9)
	}
	tx.MarkPostInitFailpoint()
	if targetPoint == "rolling_back" || targetPoint == "rolled_back_before_cleanup" {
		_ = tx.Rollback(errors.New("simulated failure before rollback crash"), nil)
		os.Exit(10)
	}
	if _, err := tx.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "Commit: %v\n", err)
		os.Exit(11)
	}
	os.Exit(0)
}

func withRestoreOps(t *testing.T, modify func(*sqliteRestoreOps)) {
	t.Helper()
	orig := restoreOps
	t.Cleanup(func() { restoreOps = orig })
	next := orig
	modify(&next)
	restoreOps = next
}

func countOpDirs(t *testing.T, dbDir string) int {
	t.Helper()
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), sqliteRestoreOpDirPrefix) {
			n++
		}
	}
	return n
}

func TestSQLiteRestoreTransaction_FaultInjectionAndConcurrency(t *testing.T) {
	t.Run("5.1 invalid fallback blocks Apply and preserves live DB", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		candSrc := filepath.Join(dir, "cand.db")
		seedPanelSQLiteDB(t, livePath, "LIVE-KEEP")
		seedPanelSQLiteDB(t, candSrc, "CAND-NEW")

		if err := InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = CloseDB() })

		tx, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.PrepareCandidate(candSrc, false); err != nil {
			t.Fatal(err)
		}
		if err := tx.CreateFallbackSnapshot(); err != nil {
			t.Fatal(err)
		}
		// Corrupt fallback before Apply.
		if err := os.WriteFile(tx.FallbackPath(), []byte("corrupt-fallback"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := tx.Apply(); err == nil {
			t.Fatal("expected Apply to refuse invalid fallback")
		}
		if err := tx.AbortBeforeApply(); err != nil {
			t.Fatalf("AbortBeforeApply: %v", err)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "LIVE-KEEP" {
			t.Fatalf("live DB altered: %q", got)
		}
	})

	t.Run("5.2 CloseDB failure aborts before any rename", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		candSrc := filepath.Join(dir, "cand.db")
		seedPanelSQLiteDB(t, livePath, "LIVE-KEEP")
		seedPanelSQLiteDB(t, candSrc, "CAND-NEW")

		if err := InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = CloseDB() })

		tx, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.PrepareCandidate(candSrc, false); err != nil {
			t.Fatal(err)
		}
		if err := tx.CreateFallbackSnapshot(); err != nil {
			t.Fatal(err)
		}
		if err := tx.WritePreparedState(); err != nil {
			t.Fatal(err)
		}

		renameCalled := false
		withRestoreOps(t, func(ops *sqliteRestoreOps) {
			ops.closeDB = func() error { return errors.New("injected closeDB failure") }
			ops.rename = func(oldpath, newpath string) error {
				renameCalled = true
				return os.Rename(oldpath, newpath)
			}
		})

		if err := tx.CloseLiveDB(); err == nil || !strings.Contains(err.Error(), "injected closeDB failure") {
			t.Fatalf("expected injected closeDB failure, got %v", err)
		}
		if renameCalled {
			t.Fatal("rename must not be called when CloseDB fails")
		}
		if err := tx.AbortBeforeApply(); err != nil {
			t.Fatalf("AbortBeforeApply: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, sqliteRestoreStateFilename)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("state marker should be removed")
		}
		if countOpDirs(t, dir) != 0 {
			t.Fatal("opDir should be removed")
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "LIVE-KEEP" {
			t.Fatalf("live DB altered: %q", got)
		}
	})

	t.Run("5.3 first rename failure triggers Rollback and preserves original error", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		candSrc := filepath.Join(dir, "cand.db")
		seedPanelSQLiteDB(t, livePath, "LIVE-OLD")
		seedPanelSQLiteDB(t, candSrc, "CAND-NEW")

		if err := InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = CloseDB() })

		tx, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.PrepareCandidate(candSrc, false); err != nil {
			t.Fatal(err)
		}
		if err := tx.CreateFallbackSnapshot(); err != nil {
			t.Fatal(err)
		}
		if err := tx.WritePreparedState(); err != nil {
			t.Fatal(err)
		}
		if err := tx.CloseLiveDB(); err != nil {
			t.Fatal(err)
		}

		injected := errors.New("injected first rename failure")
		withRestoreOps(t, func(ops *sqliteRestoreOps) {
			ops.rename = func(oldpath, newpath string) error {
				if oldpath == livePath && strings.HasSuffix(newpath, sqliteRestorePrevLiveFile) {
					return injected
				}
				return os.Rename(oldpath, newpath)
			}
		})

		applyErr := tx.Apply()
		if !errors.Is(applyErr, injected) {
			t.Fatalf("expected injected error, got %v", applyErr)
		}
		rbErr := tx.Rollback(applyErr, nil)
		if !errors.Is(rbErr, injected) {
			t.Fatalf("expected original error preserved after Rollback, got %v", rbErr)
		}
		if err := ValidateSQLiteDB(livePath); err != nil {
			t.Fatalf("live DB invalid after rollback: %v", err)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "LIVE-OLD" {
			t.Fatalf("got %q, want LIVE-OLD", got)
		}
	})

	t.Run("5.4 second rename failure (candidate to live) rolls back to previous DB", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		candSrc := filepath.Join(dir, "cand.db")
		seedPanelSQLiteDB(t, livePath, "LIVE-OLD")
		seedPanelSQLiteDB(t, candSrc, "CAND-NEW")

		if err := InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = CloseDB() })

		tx, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.PrepareCandidate(candSrc, false); err != nil {
			t.Fatal(err)
		}
		if err := tx.CreateFallbackSnapshot(); err != nil {
			t.Fatal(err)
		}
		if err := tx.WritePreparedState(); err != nil {
			t.Fatal(err)
		}
		if err := tx.CloseLiveDB(); err != nil {
			t.Fatal(err)
		}

		injected := errors.New("injected candidate rename failure")
		withRestoreOps(t, func(ops *sqliteRestoreOps) {
			ops.rename = func(oldpath, newpath string) error {
				if strings.HasSuffix(oldpath, sqliteRestoreCandidateFile) && newpath == livePath {
					return injected
				}
				return os.Rename(oldpath, newpath)
			}
		})

		applyErr := tx.Apply()
		if !errors.Is(applyErr, injected) {
			t.Fatalf("expected injected error, got %v", applyErr)
		}
		rbErr := tx.Rollback(applyErr, nil)
		if !errors.Is(rbErr, injected) {
			t.Fatalf("expected injected error preserved, got %v", rbErr)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "LIVE-OLD" {
			t.Fatalf("got %q, want LIVE-OLD", got)
		}
		if countOpDirs(t, dir) != 0 {
			t.Fatal("opDir should be cleaned up after successful rollback")
		}
	})

	t.Run("5.6 rollback failure preserves marker opDir fallback and recovers on next startup", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		candSrc := filepath.Join(dir, "cand.db")
		seedPanelSQLiteDB(t, livePath, "LIVE-OLD")
		seedPanelSQLiteDB(t, candSrc, "CAND-NEW")

		if err := InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = CloseDB() })

		tx, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.PrepareCandidate(candSrc, false); err != nil {
			t.Fatal(err)
		}
		if err := tx.CreateFallbackSnapshot(); err != nil {
			t.Fatal(err)
		}
		if err := tx.WritePreparedState(); err != nil {
			t.Fatal(err)
		}
		if err := tx.CloseLiveDB(); err != nil {
			t.Fatal(err)
		}
		if err := tx.Apply(); err != nil {
			t.Fatal(err)
		}

		origCause := errors.New("post-apply init failure")
		rbCopyErr := errors.New("injected rollback copy failure")
		withRestoreOps(t, func(ops *sqliteRestoreOps) {
			ops.copyFile = func(srcPath, dstPath string) error {
				return rbCopyErr
			}
		})

		err = tx.Rollback(origCause, nil)
		if !errors.Is(err, origCause) || !errors.Is(err, rbCopyErr) {
			t.Fatalf("expected both origCause and rbCopyErr, got %v", err)
		}
		if _, statErr := os.Lstat(filepath.Join(dir, sqliteRestoreStateFilename)); statErr != nil {
			t.Fatalf("state marker must be preserved on rollback failure: %v", statErr)
		}
		if countOpDirs(t, dir) != 1 {
			t.Fatalf("opDir must be preserved on rollback failure")
		}
		if _, statErr := os.Lstat(tx.FallbackPath()); statErr != nil {
			t.Fatalf("fallback must be preserved on rollback failure: %v", statErr)
		}

		// Clear fault and run startup recovery.
		restoreOps = defaultSQLiteRestoreOps()
		if err := RecoverPendingSQLiteRestore(livePath); err != nil {
			t.Fatalf("RecoverPendingSQLiteRestore after rollback failure: %v", err)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "LIVE-OLD" {
			t.Fatalf("got %q, want LIVE-OLD", got)
		}
		if countOpDirs(t, dir) != 0 {
			t.Fatal("opDir should be removed after startup recovery")
		}
	})

	t.Run("5.7 cleanup failure after committed keeps new DB and completes on next startup", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		candSrc := filepath.Join(dir, "cand.db")
		seedPanelSQLiteDB(t, livePath, "LIVE-OLD")
		seedPanelSQLiteDB(t, candSrc, "CAND-NEW")

		if err := InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = CloseDB() })

		tx, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.PrepareCandidate(candSrc, false); err != nil {
			t.Fatal(err)
		}
		if err := tx.CreateFallbackSnapshot(); err != nil {
			t.Fatal(err)
		}
		if err := tx.WritePreparedState(); err != nil {
			t.Fatal(err)
		}
		if err := tx.CloseLiveDB(); err != nil {
			t.Fatal(err)
		}
		if err := tx.Apply(); err != nil {
			t.Fatal(err)
		}

		cleanupErr := errors.New("injected post-commit removeAll failure")
		withRestoreOps(t, func(ops *sqliteRestoreOps) {
			ops.removeAll = func(string) error { return cleanupErr }
		})

		if durable, err := tx.Commit(); !durable || !errors.Is(err, cleanupErr) {
			t.Fatalf("expected durable=true and cleanupErr, got durable=%v err=%v", durable, err)
		}
		statePath := filepath.Join(dir, sqliteRestoreStateFilename)
		raw, readErr := os.ReadFile(statePath)
		if readErr != nil || !strings.Contains(string(raw), `"committed"`) {
			t.Fatalf("expected committed marker preserved, got %q err=%v", string(raw), readErr)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "CAND-NEW" {
			t.Fatalf("new DB must remain active, got %q", got)
		}

		// Clear fault and run startup recovery: must keep CAND-NEW and finish cleanup.
		restoreOps = defaultSQLiteRestoreOps()
		if err := RecoverPendingSQLiteRestore(livePath); err != nil {
			t.Fatalf("RecoverPendingSQLiteRestore after committed cleanup failure: %v", err)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "CAND-NEW" {
			t.Fatalf("startup recovery rolled back committed DB! got %q", got)
		}
		if _, err := os.Lstat(statePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("state marker should be removed after startup recovery")
		}
		if countOpDirs(t, dir) != 0 {
			t.Fatal("opDir should be removed after startup recovery")
		}
	})

	t.Run("5.8 concurrent second restore rejected without creating opDir", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		seedPanelSQLiteDB(t, livePath, "LIVE-OLD")

		tx1, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx1.AbortBeforeApply() }()

		if countOpDirs(t, dir) != 1 {
			t.Fatalf("expected 1 opDir, got %d", countOpDirs(t, dir))
		}
		if tx2, err := BeginSQLiteRestore(livePath); err == nil {
			_ = tx2.AbortBeforeApply()
			t.Fatal("expected second BeginSQLiteRestore to be rejected")
		}
		if countOpDirs(t, dir) != 1 {
			t.Fatalf("second rejected restore must not create another opDir, got %d", countOpDirs(t, dir))
		}
	})
}

func TestSQLiteRestoreInitLiveDBFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	livePath := filepath.Join(dir, "x-ui.db")
	candSrc := filepath.Join(dir, "upload.db")
	seedPanelSQLiteDB(t, livePath, "OLD-LIVE")
	seedPanelSQLiteDB(t, candSrc, "CAND-NEW")
	if err := InitDB(livePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	tx, err := BeginSQLiteRestore(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.PrepareCandidate(candSrc, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateFallbackSnapshot(); err != nil {
		t.Fatal(err)
	}
	if err := tx.WritePreparedState(); err != nil {
		t.Fatal(err)
	}
	if err := tx.CloseLiveDB(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply(); err != nil {
		t.Fatal(err)
	}

	initErr := errors.New("injected InitLiveDB failure")
	initCalls := 0
	withRestoreOps(t, func(ops *sqliteRestoreOps) {
		origInit := ops.initDB
		ops.initDB = func(dbPath string) error {
			initCalls++
			if initCalls == 1 {
				return initErr
			}
			return origInit(dbPath)
		}
	})

	err = tx.InitLiveDB()
	if !errors.Is(err, initErr) {
		t.Fatalf("expected InitLiveDB error %v, got %v", initErr, err)
	}
	rbErr := tx.Rollback(err, nil)
	if !errors.Is(rbErr, initErr) {
		t.Fatalf("expected Rollback to preserve initErr, got %v", rbErr)
	}
	if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "OLD-LIVE" {
		t.Fatalf("expected OLD-LIVE restored, got %q", got)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, sqliteRestoreStateFilename)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("expected state marker removed after rollback")
	}
	if countOpDirs(t, dir) != 0 {
		t.Fatal("expected opDir removed after rollback")
	}
}

func TestSQLiteRestoreCommitPreCommitFailuresRollBack(t *testing.T) {
	subtests := []struct {
		name      string
		injectErr func(ops *sqliteRestoreOps, targetErr error)
	}{
		{
			name: "rename_failure_during_commit_marker",
			injectErr: func(ops *sqliteRestoreOps, targetErr error) {
				origRename := ops.rename
				ops.rename = func(oldpath, newpath string) error {
					if filepath.Base(newpath) == sqliteRestoreStateFilename {
						return targetErr
					}
					return origRename(oldpath, newpath)
				}
			},
		},
		{
			name: "sync_failure_during_commit_marker",
			injectErr: func(ops *sqliteRestoreOps, targetErr error) {
				ops.syncTempFile = func(*os.File) error {
					return targetErr
				}
			},
		},
	}

	for _, tc := range subtests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XUI_DB_FOLDER", dir)
			livePath := filepath.Join(dir, "x-ui.db")
			candSrc := filepath.Join(dir, "upload.db")
			seedPanelSQLiteDB(t, livePath, "OLD-LIVE")
			seedPanelSQLiteDB(t, candSrc, "CAND-NEW")
			if err := InitDB(livePath); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = CloseDB() })

			tx, err := BeginSQLiteRestore(livePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.PrepareCandidate(candSrc, false); err != nil {
				t.Fatal(err)
			}
			if err := tx.CreateFallbackSnapshot(); err != nil {
				t.Fatal(err)
			}
			if err := tx.WritePreparedState(); err != nil {
				t.Fatal(err)
			}
			if err := tx.CloseLiveDB(); err != nil {
				t.Fatal(err)
			}
			if err := tx.Apply(); err != nil {
				t.Fatal(err)
			}
			if err := tx.InitLiveDB(); err != nil {
				t.Fatal(err)
			}

			injected := errors.New("injected pre-commit failure: " + tc.name)
			withRestoreOps(t, func(ops *sqliteRestoreOps) {
				tc.injectErr(ops, injected)
			})

			durable, commitErr := tx.Commit()
			if durable {
				t.Fatalf("expected durable=false on pre-commit marker failure, got durable=true (err=%v)", commitErr)
			}
			if !errors.Is(commitErr, injected) {
				t.Fatalf("expected commitErr %v, got %v", injected, commitErr)
			}

			// Clear fault and verify Rollback succeeds and restores OLD-LIVE.
			restoreOps = defaultSQLiteRestoreOps()
			if rbErr := tx.Rollback(commitErr, nil); !errors.Is(rbErr, injected) {
				t.Fatalf("expected Rollback to preserve commitErr %v, got %v", injected, rbErr)
			}
			if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "OLD-LIVE" {
				t.Fatalf("expected OLD-LIVE after pre-commit failure rollback, got %q", got)
			}
			if _, statErr := os.Lstat(filepath.Join(dir, sqliteRestoreStateFilename)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("expected state marker removed after rollback")
			}
			if countOpDirs(t, dir) != 0 {
				t.Fatal("expected opDir removed after rollback")
			}
		})
	}
}

func TestSQLiteRestoreRollbackCleanupFailureAfterOpDirRemoval(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	livePath := filepath.Join(dir, "x-ui.db")
	candSrc := filepath.Join(dir, "upload.db")
	seedPanelSQLiteDB(t, livePath, "OLD-LIVE")
	seedPanelSQLiteDB(t, candSrc, "CAND-NEW")
	if err := InitDB(livePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	tx, err := BeginSQLiteRestore(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.PrepareCandidate(candSrc, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.CreateFallbackSnapshot(); err != nil {
		t.Fatal(err)
	}
	if err := tx.WritePreparedState(); err != nil {
		t.Fatal(err)
	}
	if err := tx.CloseLiveDB(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply(); err != nil {
		t.Fatal(err)
	}

	causeErr := errors.New("simulated post-apply failure")
	cleanupSyncErr := errors.New("injected syncDir failure after opDir removal")
	withRestoreOps(t, func(ops *sqliteRestoreOps) {
		origSyncDir := ops.syncDir
		ops.syncDir = func(dirPath string) error {
			// Trigger failure only after opDir has been removed during cleanup.
			if dirPath == dir && countOpDirs(t, dir) == 0 {
				if _, statErr := os.Lstat(filepath.Join(dir, sqliteRestoreStateFilename)); statErr == nil {
					return cleanupSyncErr
				}
			}
			return origSyncDir(dirPath)
		}
	})

	rbErr := tx.Rollback(causeErr, nil)
	if !errors.Is(rbErr, causeErr) || !errors.Is(rbErr, cleanupSyncErr) {
		t.Fatalf("expected both causeErr and cleanupSyncErr, got %v", rbErr)
	}
	if countOpDirs(t, dir) != 0 {
		t.Fatal("expected opDir already removed before cleanupSyncErr")
	}
	statePath := filepath.Join(dir, sqliteRestoreStateFilename)
	raw, readErr := os.ReadFile(statePath)
	if readErr != nil || !strings.Contains(string(raw), `"rolled_back"`) {
		t.Fatalf("expected rolled_back marker preserved, got %q err=%v", string(raw), readErr)
	}
	if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "OLD-LIVE" {
		t.Fatalf("expected OLD-LIVE active, got %q", got)
	}

	// Clear fault and verify startup recovery removes marker without looking for deleted fallback.
	restoreOps = defaultSQLiteRestoreOps()
	if err := RecoverPendingSQLiteRestore(livePath); err != nil {
		t.Fatalf("RecoverPendingSQLiteRestore after rolled_back opDir removal failed: %v", err)
	}
	if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "OLD-LIVE" {
		t.Fatalf("expected OLD-LIVE preserved after startup recovery, got %q", got)
	}
	if _, statErr := os.Lstat(statePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("expected rolled_back marker removed after startup recovery")
	}
}

func TestRecoverPendingSQLiteRestoreOpDirMissingByPhase(t *testing.T) {
	writeMarker := func(t *testing.T, dir, opDirName string, phase sqliteRestorePhase) string {
		t.Helper()
		statePath := filepath.Join(dir, sqliteRestoreStateFilename)
		state := sqliteRestoreState{
			Version:      sqliteRestoreStateVersion,
			Phase:        phase,
			TargetDB:     "x-ui.db",
			OpDir:        opDirName,
			Candidate:    sqliteRestoreCandidateFile,
			Fallback:     sqliteRestoreFallbackFile,
			PrevLive:     sqliteRestorePrevLiveFile,
			RecoveryCopy: sqliteRestoreRecoveryFile,
			FailedLive:   sqliteRestoreFailedLiveFile,
			OperationID:  "op-missing-test",
			CreatedAt:    "2026-11-13T00:00:00Z",
		}
		if err := writeRestoreStateDurable(dir, statePath, state); err != nil {
			t.Fatal(err)
		}
		return statePath
	}

	for _, phase := range []sqliteRestorePhase{restorePhaseApplying, restorePhaseVerifying} {
		t.Run(string(phase)+"_missing_opDir_halts", func(t *testing.T) {
			dir := t.TempDir()
			livePath := filepath.Join(dir, "x-ui.db")
			seedPanelSQLiteDB(t, livePath, "LIVE-DB")
			statePath := writeMarker(t, dir, sqliteRestoreOpDirPrefix+"gone", phase)

			err := RecoverPendingSQLiteRestore(livePath)
			if err == nil || !strings.Contains(err.Error(), "missing operation directory") {
				t.Fatalf("expected missing operation directory halt for phase %s, got %v", phase, err)
			}
			if _, statErr := os.Lstat(statePath); statErr != nil {
				t.Fatalf("expected marker preserved on halt, got stat err=%v", statErr)
			}
		})
	}

	for _, phase := range []sqliteRestorePhase{restorePhaseCommitted, restorePhaseRolledBack} {
		t.Run(string(phase)+"_missing_opDir_healthy_live_cleans_marker", func(t *testing.T) {
			dir := t.TempDir()
			livePath := filepath.Join(dir, "x-ui.db")
			seedPanelSQLiteDB(t, livePath, "LIVE-DB")
			statePath := writeMarker(t, dir, sqliteRestoreOpDirPrefix+"gone", phase)

			if err := RecoverPendingSQLiteRestore(livePath); err != nil {
				t.Fatalf("expected cleanup to succeed for phase %s with healthy live DB, got %v", phase, err)
			}
			if _, statErr := os.Lstat(statePath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("expected marker removed for phase %s, got stat err=%v", phase, statErr)
			}
			if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "LIVE-DB" {
				t.Fatalf("expected LIVE-DB intact, got %q", got)
			}
		})
	}
}

func TestSQLiteRestoreCommitPreDurableAndPostDurablePanics(t *testing.T) {
	t.Run("pre_durable_panic_returns_durable_false_and_allows_rollback", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		candSrc := filepath.Join(dir, "upload.db")
		seedPanelSQLiteDB(t, livePath, "OLD-PRE-PANIC")
		seedPanelSQLiteDB(t, candSrc, "NEW-PRE-PANIC")
		if err := InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = CloseDB() })

		tx, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.PrepareCandidate(candSrc, false); err != nil {
			t.Fatal(err)
		}
		if err := tx.CreateFallbackSnapshot(); err != nil {
			t.Fatal(err)
		}
		if err := tx.WritePreparedState(); err != nil {
			t.Fatal(err)
		}
		if err := tx.CloseLiveDB(); err != nil {
			t.Fatal(err)
		}
		if err := tx.Apply(); err != nil {
			t.Fatal(err)
		}
		if err := tx.InitLiveDB(); err != nil {
			t.Fatal(err)
		}

		withRestoreOps(t, func(ops *sqliteRestoreOps) {
			ops.syncTempFile = func(*os.File) error {
				panic("SECRET-PRE-DURABLE-PANIC")
			}
		})

		durable, commitErr := tx.Commit()
		if durable {
			t.Fatalf("expected durable=false on pre-durable panic, got true (err=%v)", commitErr)
		}
		if commitErr == nil || strings.Contains(commitErr.Error(), "SECRET-PRE-DURABLE-PANIC") {
			t.Fatalf("expected sanitized pre-durable panic error, got %v", commitErr)
		}

		restoreOps = defaultSQLiteRestoreOps()
		if rbErr := tx.Rollback(commitErr, nil); !errors.Is(rbErr, commitErr) {
			t.Fatalf("expected Rollback to preserve commitErr, got %v", rbErr)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "OLD-PRE-PANIC" {
			t.Fatalf("expected OLD-PRE-PANIC restored, got %q", got)
		}
		tx2, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatalf("expected mutex released after rollback, got %v", err)
		}
		_ = tx2.AbortBeforeApply()
	})

	t.Run("post_durable_panic_returns_durable_true_keeps_new_db_and_releases_mutex", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XUI_DB_FOLDER", dir)
		livePath := filepath.Join(dir, "x-ui.db")
		candSrc := filepath.Join(dir, "upload.db")
		seedPanelSQLiteDB(t, livePath, "OLD-POST-PANIC")
		seedPanelSQLiteDB(t, candSrc, "NEW-POST-PANIC")
		if err := InitDB(livePath); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = CloseDB() })

		tx, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.PrepareCandidate(candSrc, false); err != nil {
			t.Fatal(err)
		}
		if err := tx.CreateFallbackSnapshot(); err != nil {
			t.Fatal(err)
		}
		if err := tx.WritePreparedState(); err != nil {
			t.Fatal(err)
		}
		if err := tx.CloseLiveDB(); err != nil {
			t.Fatal(err)
		}
		if err := tx.Apply(); err != nil {
			t.Fatal(err)
		}
		if err := tx.InitLiveDB(); err != nil {
			t.Fatal(err)
		}

		withRestoreOps(t, func(ops *sqliteRestoreOps) {
			ops.removeAll = func(string) error {
				panic("SECRET-POST-DURABLE-PANIC")
			}
		})

		durable, commitErr := tx.Commit()
		if !durable {
			t.Fatalf("expected durable=true on post-durable panic, got false (err=%v)", commitErr)
		}
		if commitErr == nil || strings.Contains(commitErr.Error(), "SECRET-POST-DURABLE-PANIC") {
			t.Fatalf("expected sanitized post-durable panic error, got %v", commitErr)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "NEW-POST-PANIC" {
			t.Fatalf("expected NEW-POST-PANIC active, got %q", got)
		}
		statePath := filepath.Join(dir, sqliteRestoreStateFilename)
		raw, readErr := os.ReadFile(statePath)
		if readErr != nil || !strings.Contains(string(raw), `"committed"`) {
			t.Fatalf("expected committed marker preserved, got %q err=%v", string(raw), readErr)
		}

		// Second BeginSQLiteRestore must fail due to pending marker, NOT leaked mutex.
		if tx2, err := BeginSQLiteRestore(livePath); err == nil {
			_ = tx2.AbortBeforeApply()
			t.Fatal("expected pending marker to block second restore before startup recovery")
		} else if strings.Contains(err.Error(), "already in progress") {
			t.Fatalf("mutex leaked on post-durable panic: %v", err)
		} else if !strings.Contains(err.Error(), "pending SQLite restore state already exists") {
			t.Fatalf("unexpected error blocking second restore: %v", err)
		}

		// Clear fault and run startup recovery: must keep NEW-POST-PANIC and finish cleanup.
		restoreOps = defaultSQLiteRestoreOps()
		if err := RecoverPendingSQLiteRestore(livePath); err != nil {
			t.Fatalf("RecoverPendingSQLiteRestore after post-durable panic: %v", err)
		}
		if got := readSettingFromSQLiteFile(t, livePath, "remarkTemplate"); got != "NEW-POST-PANIC" {
			t.Fatalf("expected NEW-POST-PANIC kept after startup recovery, got %q", got)
		}
		if _, statErr := os.Lstat(statePath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("expected marker removed after startup recovery")
		}
		tx3, err := BeginSQLiteRestore(livePath)
		if err != nil {
			t.Fatalf("expected second restore possible after recovery, got %v", err)
		}
		_ = tx3.AbortBeforeApply()
	})
}
