//go:build windows

package logger

import (
	"os"

	"github.com/op/go-logging"
)

// newConsoleBackend writes logs to stderr.
func newConsoleBackend() (backend logging.Backend, includeTime bool) {
	return logging.NewLogBackend(os.Stderr, "", 0), true
}
