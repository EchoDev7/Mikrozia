//go:build windows

package service

import "os/exec"

func configurePgToolProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}

func syncPostgresSnapshotDir(string) error {
	return nil
}
