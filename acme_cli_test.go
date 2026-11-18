package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
)

type passwordTerminalFake struct {
	terminal     bool
	password     string
	readErr      error
	panicOnRead  bool
	getStateErr  error
	restoreErr   error
	readCalls    int
	restoreCalls int
}

func (t *passwordTerminalFake) IsTerminal(int) bool { return t.terminal }

func (t *passwordTerminalFake) GetState(int) (any, error) {
	if t.getStateErr != nil {
		return nil, t.getStateErr
	}
	return "terminal-state", nil
}

func (t *passwordTerminalFake) Restore(_ int, state any) error {
	t.restoreCalls++
	if state != "terminal-state" {
		return errors.New("wrong terminal state")
	}
	return t.restoreErr
}

func (t *passwordTerminalFake) ReadPassword(int) ([]byte, error) {
	t.readCalls++
	if t.panicOnRead {
		panic("injected terminal panic")
	}
	if t.readErr != nil {
		return nil, t.readErr
	}
	return []byte(t.password), nil
}

func tokenPipe(t *testing.T, input string) *os.File {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func noTokenEnvironment(string) string { return "" }

func TestInteractiveTokenUsesPasswordInputAndRestoresTerminal(t *testing.T) {
	const token = "interactive-token-that-must-not-leak"
	terminal := &passwordTerminalFake{terminal: true, password: token}
	tokenValue, err := readAcmeTokenWith(tokenPipe(t, ""), io.Discard, false, noTokenEnvironment, terminal)
	if err != nil {
		t.Fatalf("interactive read: %v", err)
	}
	if tokenValue != token || terminal.readCalls != 1 || terminal.restoreCalls != 1 {
		t.Fatalf("token length = %d, password reads = %d, restores = %d", len(tokenValue), terminal.readCalls, terminal.restoreCalls)
	}
}

func TestInteractiveTokenRestoresTerminalOnReadErrorAndCtrlC(t *testing.T) {
	const token = "token-in-an-injected-error-that-must-not-leak"
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "read error", err: errors.New("read failed near " + token)},
		{name: "Ctrl-C", err: syscall.EINTR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terminal := &passwordTerminalFake{terminal: true, readErr: tc.err}
			value, err := readAcmeTokenWith(tokenPipe(t, ""), io.Discard, false, noTokenEnvironment, terminal)
			if err == nil || value != "" {
				t.Fatalf("value = %q, error = %v; expected a failed read", value, err)
			}
			if terminal.restoreCalls != 1 {
				t.Fatalf("terminal restores = %d, want 1", terminal.restoreCalls)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatalf("error leaked token material: %v", err)
			}
		})
	}
}

func TestInteractiveTokenRestoresTerminalDuringPanic(t *testing.T) {
	terminal := &passwordTerminalFake{terminal: true, panicOnRead: true}
	defer func() {
		if recover() == nil {
			t.Fatal("the injected panic must remain visible")
		}
		if terminal.restoreCalls != 1 {
			t.Fatalf("terminal restores = %d, want 1", terminal.restoreCalls)
		}
	}()
	_, _ = readAcmeTokenWith(tokenPipe(t, ""), io.Discard, false, noTokenEnvironment, terminal)
}

func TestNonInteractiveTokenRequiresEnvironmentOrExplicitStdin(t *testing.T) {
	const token = "noninteractive-token-that-must-not-leak"
	terminal := &passwordTerminalFake{terminal: false}

	value, err := readAcmeTokenWith(tokenPipe(t, token+"\n"), io.Discard, false, noTokenEnvironment, terminal)
	if err == nil || value != "" || strings.Contains(err.Error(), token) {
		t.Fatalf("implicit pipe input was accepted or leaked: value %q, error %v", value, err)
	}
	if terminal.readCalls != 0 {
		t.Fatal("a non-TTY input must not use the interactive password reader")
	}

	value, err = readAcmeTokenWith(tokenPipe(t, token+"\nignored-second-line\n"), io.Discard, true, noTokenEnvironment, terminal)
	if err != nil || value != token {
		t.Fatalf("explicit --stdin read = %q, %v", value, err)
	}

	value, err = readAcmeTokenWith(tokenPipe(t, ""), io.Discard, false, func(key string) string {
		if key == "MIKROZIA_CF_API_TOKEN" {
			return token
		}
		return ""
	}, terminal)
	if err != nil || value != token {
		t.Fatalf("environment read = %q, %v", value, err)
	}
}

func TestHTTP01DiagnosticWarnsWithoutClaimingPublicReachability(t *testing.T) {
	if !strings.Contains(acmeHTTP01PublicWarning, "cannot verify public HTTP-01 reachability") ||
		!strings.Contains(acmeHTTP01PublicWarning, "must temporarily make TCP 80 reachable from the public Internet") {
		t.Fatalf("insufficient HTTP-01 warning: %q", acmeHTTP01PublicWarning)
	}
	content, err := os.ReadFile("acme_cli.go")
	if err != nil {
		t.Fatalf("read CLI source: %v", err)
	}
	for _, forbidden := range []string{"DialContext", "JoinHostPort(domain", "port 80 reachability from inside the container"} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("outgoing container probe remains as public-reachability evidence: %q", forbidden)
		}
	}
}
