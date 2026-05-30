package device

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// USBDeviceChecker checks if a device is a USB storage device
type USBDeviceChecker struct {
	logger *slog.Logger
	fs     fs.FileSystem
}

// NewUSBDeviceChecker creates a new USB device checker
func NewUSBDeviceChecker(logger *slog.Logger, fs fs.FileSystem) *USBDeviceChecker {
	return &USBDeviceChecker{
		logger: logger,
		fs:     fs,
	}
}

// Check verifies if the device is connected via USB
func (u *USBDeviceChecker) Check(device string) bool {
	sysPath := filepath.Join("/sys/block", device)
	subsystemPath := filepath.Join(sysPath, "device", "subsystem")

	linkTarget, err := u.fs.Readlink(subsystemPath)
	if err != nil {
		u.logger.Debug("could not read subsystem link", "device", device, "error", err)
		return false
	}

	if strings.Contains(linkTarget, "usb") {
		u.logger.Debug("device is USB", "device", device, "subsystem", linkTarget)
		return true
	}

	u.logger.Debug("device is not USB", "device", device, "subsystem", linkTarget)
	return false
}
