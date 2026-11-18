package acme

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

var ErrUnsafePath = errors.New("ACME storage contains a symlink or non-regular path")

// Check existing components before handing destinations to the bundled external client.
func checkPath(path string) error {
	if path == "" {
		return ErrUnsafePath
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if runtime.GOOS == "darwin" && current == "/var" {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || (current != path && !info.IsDir()) || (current == path && !info.IsDir() && !info.Mode().IsRegular())) {
			return ErrUnsafePath
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

func readStorageFile(path string) ([]byte, error) {
	if err := checkPath(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (m *Manager) checkStorage(domain string) error {
	for _, path := range []string{m.accountHome, filepath.Join(m.accountHome, metaDir), m.CertPath(domain), m.KeyPath(domain), m.metaPath(domain), m.tokenPath(domain), m.renewalPath(domain)} {
		if err := checkPath(path); err != nil {
			return err
		}
	}
	return nil
}
