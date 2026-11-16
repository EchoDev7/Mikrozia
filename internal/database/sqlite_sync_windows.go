//go:build windows

package database

import "os"

func syncDir(string) error {
	return nil
}

func validateRestoreFilePerm(os.FileInfo, string) error {
	return nil
}

func validateRestoreDirPerm(os.FileInfo, string) error {
	return nil
}
