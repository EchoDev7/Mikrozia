//go:build !windows

package database

import (
	"fmt"
	"os"
)

func syncDir(dirPath string) error {
	d, err := os.Open(dirPath)
	if err != nil {
		return err
	}
	if syncErr := d.Sync(); syncErr != nil {
		_ = d.Close()
		return syncErr
	}
	return d.Close()
}

func validateRestoreFilePerm(info os.FileInfo, path string) error {
	perm := info.Mode().Perm()
	if perm&0o077 != 0 || perm != 0o600 {
		return fmt.Errorf("insecure permissions %#o on %s (expected 0600)", perm, path)
	}
	return nil
}

func validateRestoreDirPerm(info os.FileInfo, path string) error {
	perm := info.Mode().Perm()
	if perm&0o077 != 0 || perm != 0o700 {
		return fmt.Errorf("insecure permissions %#o on %s (expected 0700)", perm, path)
	}
	return nil
}
