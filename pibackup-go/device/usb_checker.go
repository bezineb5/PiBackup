package device

import (
	"log/slog"
	"path/filepath"
	"strings"
	"unicode"

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
	// Try direct path first (works for whole devices like sda)
	sysPath := filepath.Join("/sys/block", device)
	subsystemPath := filepath.Join(sysPath, "device", "subsystem")

	linkTarget, err := u.fs.Readlink(subsystemPath)
	if err == nil {
		if strings.Contains(linkTarget, "usb") {
			u.logger.Debug("device is USB", "device", device, "subsystem", linkTarget)
			return true
		}
		u.logger.Debug("device is not USB", "device", device, "subsystem", linkTarget)
		return false
	}

	// For partitions (sda1), the path is /sys/block/sda/sda1/device/subsystem
	// Try to find the parent device
	if len(device) > 0 && unicode.IsDigit(rune(device[len(device)-1])) {
		// This looks like a partition (ends with digit)
		// Try parent device path
		parentName := getParentDeviceName(device)
		if parentName != "" {
			sysPath := filepath.Join("/sys/block", parentName, device, "device", "subsystem")
			linkTarget, err := u.fs.Readlink(sysPath)
			if err == nil {
				if strings.Contains(linkTarget, "usb") {
					u.logger.Debug("partition is USB", "device", device, "parent", parentName, "subsystem", linkTarget)
					return true
				}
				u.logger.Debug("partition is not USB", "device", device, "parent", parentName, "subsystem", linkTarget)
				return false
			}
		}
	}

	u.logger.Debug("could not read subsystem link", "device", device, "error", err)
	return false
}

// getParentDeviceName extracts the parent device name from a partition
// e.g., "sda1" -> "sda", "mmcblk0p1" -> "mmcblk0"
func getParentDeviceName(deviceName string) string {
	// For sd* devices: sda1 -> sda
	if strings.HasPrefix(deviceName, "sd") && len(deviceName) > 2 {
		// Remove trailing digits
		for i := len(deviceName) - 1; i >= 2; i-- {
			if !unicode.IsDigit(rune(deviceName[i])) {
				return deviceName[:i+1]
			}
		}
		return deviceName[:2]
	}
	
	// For mmcblk* devices: mmcblk0p1 -> mmcblk0
	if strings.HasPrefix(deviceName, "mmcblk") {
		// Find the 'p' and remove everything after it
		if idx := strings.Index(deviceName, "p"); idx != -1 {
			return deviceName[:idx]
		}
		return deviceName
	}
	
	return ""
}
