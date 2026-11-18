package global

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/lifecycle"
)

func TestCertificateIPCReportsAcknowledgedSuccessAndFailure(t *testing.T) {
	dir, err := os.MkdirTemp("", "ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(dir) })
	t.Setenv("TMPDIR", dir)
	SetCertificateReloadHook(func() error { return nil })
	t.Cleanup(func() { SetCertificateReloadHook(nil) })
	closeServer, err := ServeCertificateReload()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeServer)
	info, err := os.Stat(certificateSocket())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("reload socket is not private")
	}
	if err := RequestCertificateReload(); err != nil {
		t.Fatal(err)
	}
	SetCertificateReloadHook(func() error { return errors.New("injected bind failure") })
	if err := RequestCertificateReload(); err == nil || err.Error() != "certificate listener reload returned HTTP 500" {
		t.Fatalf("failure acknowledgement=%v", err)
	}
	SetCertificateReloadHook(func() error { return &lifecycle.ConflictError{Requested: "certificate reload", Active: "restart"} })
	if err := RequestCertificateReload(); err == nil || err.Error() != "server lifecycle operation is in progress; retry certificate reload after it completes" {
		t.Fatalf("conflict acknowledgement=%v", err)
	}
	SetCertificateReloadHook(func() error { return lifecycle.ErrClosing })
	if err := RequestCertificateReload(); !errors.Is(err, lifecycle.ErrClosing) {
		t.Fatalf("shutdown acknowledgement=%v", err)
	}
}

func TestCertificateIPCWaitsForReloadCompletion(t *testing.T) {
	dir, err := os.MkdirTemp("", "ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(dir) })
	t.Setenv("TMPDIR", dir)
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	SetCertificateReloadHook(func() error {
		close(entered)
		<-release
		return errors.New("injected delayed reload failure")
	})
	t.Cleanup(func() { SetCertificateReloadHook(nil) })
	closeServer, err := ServeCertificateReload()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeServer)
	result := make(chan error, 1)
	go func() { result <- RequestCertificateReload() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reload hook was not reached")
	}
	select {
	case err := <-result:
		t.Fatalf("request acknowledged before reload completed: %v", err)
	default:
	}
	release <- struct{}{}
	select {
	case err := <-result:
		if err == nil || err.Error() != "certificate listener reload returned HTTP 500" {
			t.Fatalf("delayed failure acknowledgement=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("completed reload was not acknowledged")
	}
}
