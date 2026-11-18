//go:build linux || darwin

package acme

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func (m *Manager) lockInstall() (func(), error) {
	path := filepath.Join(m.accountHome, ".mikrozia-install.lock")
	if err := checkPath(path); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.accountHome, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.New("another certificate installation is in progress; retry after it finishes")
	}
	return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN); _ = file.Close() }, nil
}
