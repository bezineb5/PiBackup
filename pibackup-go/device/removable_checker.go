package device

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// RemovableChecker checks if a device is removable USB storage
type RemovableChecker struct {
	logger *slog.Logger
	fs     fs.FileSystem
}

// NewRemovableChecker creates a new removable device checker
func NewRemovableChecker(logger *slog.Logger, fs fs.FileSystem) *RemovableChecker {
	return &RemovableChecker{
		logger: logger,
		fs:     fs,
	}
}

// Check verifies if the device is removable
func (r *RemovableChecker) Check(device string) bool {
	if !strings.HasPrefix(device, "sd") {
		return false
	}

	sysPath := filepath.Join("/sys/block", device)
	removablePath := filepath.Join(sysPath, "removable")

	data, err := r.fs.ReadFile(removablePath)
	if err != nil {
		r.logger.Debug("could not read removable status", "device", device, "error", err)
		return false
	}

	if strings.TrimSpace(string(data)) == "1" {
		r.logger.Debug("device is removable USB storage", "device", device)
		return true
	}

	r.logger.Debug("device is not removable", "device", device)
	return false
}
