// Package device provides device detection and type checking functionality.
package device

import (
	"log/slog"

	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// Checker defines the interface for device type checking
type Checker interface {
	Check(device string) bool
}

// Checkers returns the default set of device checkers
func Checkers(logger *slog.Logger, fs fs.FileSystem) []Checker {
	return []Checker{
		NewUSBDeviceChecker(logger, fs),
		NewSDCardChecker(logger),
		NewRemovableChecker(logger, fs),
	}
}
