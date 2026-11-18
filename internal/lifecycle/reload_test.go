package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"
)

type controlledPanel struct{ tls, xray func() error }

func (p controlledPanel) ReloadTLS() error   { return p.tls() }
func (p controlledPanel) RestartXray() error { return p.xray() }

type controlledSubscription func() error

func (s controlledSubscription) ReloadTLS() error { return s() }

func TestReloadWaitsForEveryStageAndRejectsConcurrentReload(t *testing.T) {
	c := NewCoordinator()
	entered := make(chan string)
	release := make(chan struct{})
	stage := func(name string) func() error { return func() error { entered <- name; <-release; return nil } }
	p := controlledPanel{tls: stage("panel"), xray: stage("xray")}
	s := controlledSubscription(stage("subscription"))
	done := make(chan error, 1)
	go func() { done <- c.ReloadCertificates(func() (Panel, Subscription) { return p, s }) }()
	for _, want := range []string{"panel", "subscription", "xray"} {
		select {
		case got := <-entered:
			if got != want {
				t.Fatalf("stage=%s want=%s", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("stage blocked")
		}
		select {
		case err := <-done:
			t.Fatalf("early acknowledgement: %v", err)
		default:
		}
		err := c.ReloadCertificates(func() (Panel, Subscription) { t.Error("conflicting reload read pointers"); return p, s })
		var conflict *ConflictError
		if !errors.As(err, &conflict) || conflict.Active != "certificate reload" {
			t.Fatalf("concurrent reload=%v", err)
		}
		release <- struct{}{}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReloadDuringLifecycleNeverReadsReplacedPointers(t *testing.T) {
	for _, name := range []string{"panel/subscription restart", "Xray restart", "shutdown"} {
		t.Run(name, func(t *testing.T) {
			c := NewCoordinator()
			entered, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			operation := func() error { close(entered); <-release; return nil }
			go func() {
				if name == "shutdown" {
					done <- c.Shutdown(context.Background(), operation)
				} else {
					done <- c.Wait(context.Background(), name, operation)
				}
			}()
			waitSignal(t, entered)
			err := c.ReloadCertificates(func() (Panel, Subscription) {
				t.Error("reload touched lifecycle pointers during replacement/stop")
				return nil, nil
			})
			if name == "shutdown" {
				if !errors.Is(err, ErrClosing) {
					t.Fatalf("shutdown conflict=%v", err)
				}
			} else {
				var conflict *ConflictError
				if !errors.As(err, &conflict) || conflict.Active != name {
					t.Fatalf("restart conflict=%v", err)
				}
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLifecycleWaitDeadlineDoesNotRunOperation(t *testing.T) {
	c := NewCoordinator()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- c.Run("blocked reload", func() error { close(entered); <-release; return nil }) }()
	waitSignal(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := c.Wait(ctx, "restart", func() error { t.Error("expired restart executed"); return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline=%v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSignalRestartWaitsForReloadBeforeReplacingPointers(t *testing.T) {
	c := NewCoordinator()
	entered, release := make(chan struct{}), make(chan struct{})
	reloadDone, restartDone := make(chan error, 1), make(chan error, 1)
	generation := 1
	go func() {
		reloadDone <- c.Run("certificate reload", func() error {
			close(entered)
			<-release
			if generation != 1 {
				return errors.New("server pointer replaced during reload")
			}
			return nil
		})
	}()
	waitSignal(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { restartDone <- c.Wait(ctx, "SIGHUP restart", func() error { generation = 2; return nil }) }()
	close(release)
	if err := <-reloadDone; err != nil {
		t.Fatal(err)
	}
	if err := <-restartDone; err != nil {
		t.Fatal(err)
	}
	if err := c.Run("subsequent reload", func() error {
		if generation != 2 {
			return errors.New("reload retained stale server pointer")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
