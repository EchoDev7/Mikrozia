package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
)

func waitForTestFiles(t *testing.T, paths ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		allPresent := true
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				allPresent = false
				break
			}
		}
		if allPresent {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for handshake files %v", paths)
}

func readTestPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			last = strings.TrimSpace(string(raw))
			if pid, convErr := strconv.Atoi(last); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("invalid pid in %s: %q", path, last)
	return 0
}

// testProcessAlive ignores zombies: in a container the killed grandchild can be
// reparented to PID 1 and stay unreaped, but it can no longer execute code.
func testProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	if runtime.GOOS == "linux" {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return true
		}
		// "pid (comm) state ..." — comm may contain spaces, so split after ')'.
		if i := strings.LastIndexByte(string(raw), ')'); i >= 0 && i+2 < len(raw) {
			return raw[i+2] != 'Z'
		}
		return true
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return true
	}
	stat := strings.TrimSpace(string(out))
	return stat == "" || !strings.HasPrefix(stat, "Z")
}

func TestRunBoundedCommand_PassfileCleanupAndTimeoutKillsChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake executable test")
	}

	dsn := `postgres://xui:FakeExecSecret!1@127.0.0.1:5432/xui?sslmode=require`
	toolEnv, err := database.PreparePostgresToolEnv(dsn)
	if err != nil {
		t.Fatalf("PreparePostgresToolEnv failed: %v", err)
	}

	t.Run("success removes PGPASSFILE and hides password from args/env", func(t *testing.T) {
		tmpDir := t.TempDir()
		argsFile := filepath.Join(tmpDir, "args.txt")
		script := filepath.Join(tmpDir, "fake_ok.sh")
		body := "#!/bin/sh\n" +
			"printf '%s\\n' \"$@\" > \"" + argsFile + "\"\n" +
			"if [ -n \"$PGPASSWORD\" ]; then echo PGPASSWORD_PRESENT >&2; exit 2; fi\n" +
			"if [ ! -f \"$PGPASSFILE\" ]; then echo PGPASSFILE_MISSING >&2; exit 3; fi\n" +
			"printf 'ok-out'\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}

		envCopy, err := database.PreparePostgresToolEnv(dsn)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer envCopy.Cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			stdout, stderr, timedOut, err := runBoundedCommand(ctx, script, []string{"--dbname", envCopy.Database}, envCopy.Env)
			if err != nil || timedOut {
				t.Fatalf("unexpected err=%v timedOut=%v stderr=%s", err, timedOut, stderr)
			}
			if string(stdout) != "ok-out" {
				t.Fatalf("stdout = %q, want ok-out", string(stdout))
			}
		}()
		if _, err := os.Stat(envCopy.PassfilePath); !os.IsNotExist(err) {
			t.Fatalf("PGPASSFILE still exists after success: %v", err)
		}
		argsBytes, _ := os.ReadFile(argsFile)
		if strings.Contains(string(argsBytes), "FakeExecSecret!1") {
			t.Fatalf("password leaked into args: %s", string(argsBytes))
		}
	})

	t.Run("error removes PGPASSFILE", func(t *testing.T) {
		tmpDir := t.TempDir()
		script := filepath.Join(tmpDir, "fake_fail.sh")
		body := "#!/bin/sh\necho 'boom: " + dsn + "' >&2\nexit 1\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}

		envCopy, err := database.PreparePostgresToolEnv(dsn)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer envCopy.Cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, stderr, timedOut, err := runBoundedCommand(ctx, script, nil, envCopy.Env)
			if err == nil || timedOut {
				t.Fatalf("expected normal failure, got err=%v timedOut=%v", err, timedOut)
			}
			redacted := database.RedactPostgresString(stderr, dsn)
			if strings.Contains(redacted, "FakeExecSecret!1") {
				t.Fatalf("secret leaked in redacted stderr: %s", redacted)
			}
		}()
		if _, err := os.Stat(envCopy.PassfilePath); !os.IsNotExist(err) {
			t.Fatalf("PGPASSFILE still exists after error: %v", err)
		}
	})

	t.Run("timeout kills child process group and removes PGPASSFILE", func(t *testing.T) {
		tmpDir := t.TempDir()
		ready := filepath.Join(tmpDir, "ready")
		trigger := filepath.Join(tmpDir, "trigger")
		marker := filepath.Join(tmpDir, "survived")
		parentPIDFile := filepath.Join(tmpDir, "parent.pid")
		childPIDFile := filepath.Join(tmpDir, "child.pid")
		childPGIDFile := filepath.Join(tmpDir, "child.pgid")
		script := filepath.Join(tmpDir, "fake_hang.sh")
		// The child records its pid/pgid, then blocks until a trigger appears and
		// writes a marker. The parent shell stays alive forever, so only the
		// process-group kill can end the run; the trigger is created afterwards.
		body := "#!/bin/sh\n" +
			"sh -c '\n" +
			"  printf \"%s\" \"$$\" > \"$1\"\n" +
			"  ps -A -o pid=,pgid= | awk -v p=\"$$\" \"\\$1==p{print \\$2; exit}\" > \"$2\"\n" +
			"  while [ ! -f \"$3\" ]; do sleep 0.01; done\n" +
			"  : > \"$4\"\n" +
			"' sh \"$1\" \"$2\" \"$3\" \"$4\" &\n" +
			"printf \"%s\" \"$$\" > \"$5\"\n" +
			": > \"$6\"\n" +
			"while :; do sleep 0.05; done\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}

		envCopy, err := database.PreparePostgresToolEnv(dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = envCopy.Cleanup() })

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		type runResult struct {
			timedOut bool
			err      error
		}
		done := make(chan runResult, 1)
		go func() {
			_, _, timedOut, err := runBoundedCommand(ctx, script, []string{
				childPIDFile, childPGIDFile, trigger, marker, parentPIDFile, ready,
			}, envCopy.Env)
			done <- runResult{timedOut: timedOut, err: err}
		}()

		waitForTestFiles(t, ready)
		parentPID := readTestPID(t, parentPIDFile)
		childPID := readTestPID(t, childPIDFile)
		childPGID := readTestPID(t, childPGIDFile)
		if childPGID != parentPID {
			t.Fatalf("child process group %d != parent shell pid %d; killing the group would miss the child", childPGID, parentPID)
		}
		if !testProcessAlive(parentPID) {
			t.Fatal("parent shell exited before the timeout; timeout path was not exercised")
		}
		if !testProcessAlive(childPID) {
			t.Fatal("child exited before the timeout; process-group kill was not exercised")
		}

		var res runResult
		select {
		case res = <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("runBoundedCommand did not return after the context deadline")
		}
		if !res.timedOut || !errors.Is(res.err, context.DeadlineExceeded) {
			t.Fatalf("expected timeout error, got timedOut=%v err=%v", res.timedOut, res.err)
		}
		if err := envCopy.Cleanup(); err != nil {
			t.Fatalf("cleanup after timeout failed: %v", err)
		}
		if _, err := os.Stat(envCopy.PassfilePath); !os.IsNotExist(err) {
			t.Fatalf("PGPASSFILE still exists after timeout: %v", err)
		}

		// Only now release the trigger: a child that survived the group kill
		// would act on it and create the marker.
		if err := os.WriteFile(trigger, []byte("go"), 0o600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				t.Fatalf("child %d survived the process-group kill and wrote the marker", childPID)
			}
			if !testProcessAlive(childPID) && !testProcessAlive(parentPID) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if testProcessAlive(childPID) {
			t.Fatalf("child %d is still running after the process-group kill", childPID)
		}
	})

	_ = toolEnv.Cleanup()
}

func TestMissingPostgresToolsReturnImmutableImageError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	svc := &ServerService{}

	cases := []struct {
		name string
		call func() error
	}{
		{
			name: "exportPostgresDBFile missing pg_dump",
			call: func() error {
				_, err := svc.exportPostgresDBFile(context.Background(), filepath.Join(t.TempDir(), "backup.dump"))
				return err
			},
		},
		{
			name: "restorePostgresDump missing pg_restore",
			call: func() error {
				return svc.restorePostgresDump(filepath.Join(t.TempDir(), "missing.dump"), false)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("expected error when binary is missing, got nil")
			}
			msg := err.Error()
			for _, want := range []string{"official Mikrozia", "/etc/x-ui", "/root/cert", "replace or upgrade"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("error %q missing expected guidance %q", msg, want)
				}
			}
			for _, forbidden := range []string{"install", "apk", "apt", "postgresql-client"} {
				if strings.Contains(strings.ToLower(msg), forbidden) {
					t.Fatalf("error %q must not recommend runtime install (%q)", msg, forbidden)
				}
			}
		})
	}
}

func TestExportAndRestorePostgres_ExactSchemaArgsAndGuards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake executable test")
	}

	origHook := pgResolveCanonicalSchemaHook
	t.Cleanup(func() {
		pgResolveCanonicalSchemaHook = origHook
	})

	dsn := "postgres://xui:FakeExecSecret!1@127.0.0.1:5432/xui?sslmode=disable"
	t.Setenv("XUI_DB_DSN", dsn)

	t.Run("exportPostgresDBFile passes literal quoted schema pattern and strict-names", func(t *testing.T) {
		schemas := []struct {
			schema      string
			wantPattern string
		}{
			{"public", `"public"`},
			{"TenantA", `"TenantA"`},
			{"tenant space", `"tenant space"`},
			{"tenant.name", `"tenant.name"`},
			{`tenant"name`, `"tenant""name"`},
			{"tenant*", `"tenant*"`},
		}

		for _, tc := range schemas {
			t.Run(tc.schema, func(t *testing.T) {
				tmpDir := t.TempDir()
				binDir := filepath.Join(tmpDir, "bin")
				if err := os.MkdirAll(binDir, 0o755); err != nil {
					t.Fatal(err)
				}
				argsFile := filepath.Join(tmpDir, "dump_args.txt")
				pgOptionsFile := filepath.Join(tmpDir, "pgoptions.txt")
				fakeDump := filepath.Join(binDir, "pg_dump")
				script := "#!/bin/sh\n" +
					"printf '%s\\n' \"$@\" > \"" + argsFile + "\"\n" +
					"printf '%s' \"$PGOPTIONS\" > \"" + pgOptionsFile + "\"\n" +
					"printf 'PGDMP_FAKE_CONTENT'\n"
				if err := os.WriteFile(fakeDump, []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}

				t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
				pgResolveCanonicalSchemaHook = func(context.Context) (string, error) {
					return tc.schema, nil
				}

				destPath := filepath.Join(tmpDir, "out.dump")
				svc := &ServerService{}
				n, err := svc.exportPostgresDBFile(context.Background(), destPath)
				if err != nil {
					t.Fatalf("exportPostgresDBFile failed: %v", err)
				}
				if n == 0 {
					t.Fatal("expected non-zero backup size")
				}

				gotBytes, err := os.ReadFile(argsFile)
				if err != nil {
					t.Fatal(err)
				}
				gotArgs := strings.Split(strings.TrimSuffix(string(gotBytes), "\n"), "\n")
				wantArgs := []string{
					"--format=custom",
					"--no-owner",
					"--no-privileges",
					"--strict-names",
					"--schema=" + tc.wantPattern,
					"--dbname",
					"xui",
				}
				if !reflect.DeepEqual(gotArgs, wantArgs) {
					t.Fatalf("pg_dump args = %#v, want %#v", gotArgs, wantArgs)
				}

				pgOptBytes, _ := os.ReadFile(pgOptionsFile)
				if len(pgOptBytes) != 0 {
					t.Fatalf("PGOPTIONS must be empty, got %q", string(pgOptBytes))
				}
			})
		}
	})

	t.Run("exportPostgresDBFile fails before creating destPath on schema resolution error", func(t *testing.T) {
		tmpDir := t.TempDir()
		binDir := filepath.Join(tmpDir, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		ranMarker := filepath.Join(tmpDir, "pg_dump_ran")
		fakeDump := filepath.Join(binDir, "pg_dump")
		if err := os.WriteFile(fakeDump, []byte("#!/bin/sh\ntouch \""+ranMarker+"\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		pgResolveCanonicalSchemaHook = func(context.Context) (string, error) {
			return "", errors.New("core tables are split across schemas")
		}

		destPath := filepath.Join(tmpDir, "should_not_exist.dump")
		svc := &ServerService{}
		if _, err := svc.exportPostgresDBFile(context.Background(), destPath); err == nil {
			t.Fatal("expected error when schema resolution fails")
		}
		if _, err := os.Stat(destPath); !os.IsNotExist(err) {
			t.Fatalf("destPath must not be created on schema resolution error: %v", err)
		}
		if _, err := os.Stat(ranMarker); !os.IsNotExist(err) {
			t.Fatalf("pg_dump must not execute on schema resolution error: %v", err)
		}
	})

	t.Run("restorePostgresDump fails before running pg_restore on schema resolution error", func(t *testing.T) {
		tmpDir := t.TempDir()
		binDir := filepath.Join(tmpDir, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		restoreRan := filepath.Join(tmpDir, "restore_ran")
		fakeRestore := filepath.Join(binDir, "pg_restore")
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = \"--list\" ]; then exit 0; fi\n" +
			"touch \"" + restoreRan + "\"\n"
		if err := os.WriteFile(fakeRestore, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "pg_dump"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		dumpPath := filepath.Join(tmpDir, "backup.dump")
		if err := os.WriteFile(dumpPath, []byte("PGDMP"), 0o600); err != nil {
			t.Fatal(err)
		}

		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		pgResolveCanonicalSchemaHook = func(context.Context) (string, error) {
			return "", errors.New("missing core table")
		}

		svc := &ServerService{}
		if err := svc.restorePostgresDump(dumpPath, false); err == nil {
			t.Fatal("expected error when schema resolution fails")
		}
		if _, err := os.Stat(restoreRan); !os.IsNotExist(err) {
			t.Fatalf("pg_restore must not execute restore step when schema resolution fails: %v", err)
		}
	})

	t.Run("restorePostgresDump passes exact canonical schema and strict-names", func(t *testing.T) {
		tmpDir := t.TempDir()
		binDir := filepath.Join(tmpDir, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		argsFile := filepath.Join(tmpDir, "restore_args.txt")
		fakeRestore := filepath.Join(binDir, "pg_restore")
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = \"--list\" ]; then exit 0; fi\n" +
			"printf '%s\\n' \"$@\" > \"" + argsFile + "\"\n"
		if err := os.WriteFile(fakeRestore, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "pg_dump"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		dumpPath := filepath.Join(tmpDir, "backup.dump")
		if err := os.WriteFile(dumpPath, []byte("PGDMP"), 0o600); err != nil {
			t.Fatal(err)
		}

		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("XUI_DB_FOLDER", tmpDir)
		t.Setenv("XUI_DB_TYPE", "sqlite")
		t.Cleanup(func() { _ = database.CloseDB() })

		schema := `tenant"space.1*`
		pgResolveCanonicalSchemaHook = func(context.Context) (string, error) {
			return schema, nil
		}

		svc := &ServerService{}
		var counts postgresImportCallCounts
		_ = svc.restorePostgresDumpWithOps(dumpPath, false, newTestPostgresImportOps(&counts))

		gotBytes, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("expected fake pg_restore to record args: %v", err)
		}
		gotArgs := strings.Split(strings.TrimSuffix(string(gotBytes), "\n"), "\n")
		pattern, err := database.QuotePgDumpSchemaPattern(schema)
		if err != nil {
			t.Fatalf("quote schema: %v", err)
		}
		wantArgs := []string{
			"--clean",
			"--if-exists",
			"--no-owner",
			"--no-privileges",
			"--strict-names",
			"--schema=" + pattern,
			"--single-transaction",
			"--dbname",
			"xui",
			dumpPath,
		}
		if !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("pg_restore args = %#v, want %#v", gotArgs, wantArgs)
		}
	})
}
