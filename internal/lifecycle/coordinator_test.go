package lifecycle

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCertificateReloadInterleavings(t *testing.T) {
	cases := []struct {
		name     string
		conflict string
		shutdown bool
	}{
		{name: "two reloads", conflict: "certificate reload"},
		{name: "panel and subscription restart", conflict: "SIGHUP restart"},
		{name: "xray restart", conflict: "SIGUSR1 restart"},
		{name: "shutdown", conflict: "shutdown", shutdown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCoordinator()
			entered := make(chan struct{})
			release := make(chan struct{})
			completed := make(chan struct{})
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				err := c.Run("certificate reload", func() error {
					close(entered)
					<-release
					close(completed)
					return nil
				})
				if err != nil {
					t.Errorf("first reload: %v", err)
				}
			}()
			waitSignal(t, entered)
			conflictDone := make(chan error, 1)
			if tc.shutdown {
				go func() {
					conflictDone <- c.Shutdown(context.Background(), func() error {
						select {
						case <-completed:
							return nil
						default:
							return errors.New("shutdown ran before reload completed")
						}
					})
				}()
			} else {
				go func() {
					conflictDone <- c.Run(tc.conflict, func() error { return errors.New("conflicting operation unexpectedly ran") })
				}()
			}
			if tc.shutdown {
				waitClosing(t, c)
				if err := c.Run("late reload", func() error { return nil }); !errors.Is(err, ErrClosing) {
					t.Fatalf("reload after shutdown request = %v, want ErrClosing", err)
				}
			} else {
				if err := <-conflictDone; err == nil || !strings.Contains(err.Error(), "conflicts with") || !strings.Contains(err.Error(), tc.conflict) {
					t.Fatalf("conflict error = %v", err)
				}
			}
			select {
			case <-completed:
				t.Fatal("first reload completed before controlled release")
			default:
			}
			close(release)
			waitSignal(t, completed)
			waitSignal(t, finished)
			if tc.shutdown {
				if err := <-conflictDone; err != nil {
					t.Fatalf("shutdown after reload completion: %v", err)
				}
			}
		})
	}
}

func TestLifecycleConflictAllowsCertificateRecovery(t *testing.T) {
	c := NewCoordinator()
	entered := make(chan struct{})
	release := make(chan struct{})
	operationDone := make(chan struct{})
	go func() {
		_ = c.Run("SIGUSR1 restart", func() error {
			close(entered)
			<-release
			return nil
		})
		close(operationDone)
	}()
	waitSignal(t, entered)
	if err := c.Run("certificate reload", func() error { return nil }); err == nil || !strings.Contains(err.Error(), "SIGUSR1 restart") {
		t.Fatalf("certificate conflict = %v", err)
	}
	close(release)
	waitSignal(t, operationDone)
	reloaded := false
	if err := c.Run("certificate reload", func() error { reloaded = true; return nil }); err != nil {
		t.Fatalf("recovery reload: %v", err)
	}
	if !reloaded {
		t.Fatal("recovery reload did not complete")
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for controlled lifecycle operation")
	}
}

func waitClosing(t *testing.T, c *Coordinator) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		c.mu.Lock()
		closing := c.closing
		c.mu.Unlock()
		if closing {
			return
		}
		select {
		case <-deadline:
			t.Fatal("shutdown did not mark lifecycle as closing")
		default:
			runtime.Gosched()
		}
	}
}
