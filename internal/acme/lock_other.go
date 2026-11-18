//go:build !linux && !darwin

package acme

import "errors"

func (m *Manager) lockInstall() (func(), error) {
	return nil, errors.New("certificate installation requires the supported Linux container runtime")
}
