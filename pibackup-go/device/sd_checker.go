package device

import (
	"log/slog"
	"strings"
)

// SDCardChecker checks if a device is a removable SD card
type SDCardChecker struct {
	logger *slog.Logger
}

// NewSDCardChecker creates a new SD card checker
func NewSDCardChecker(logger *slog.Logger) *SDCardChecker {
	return &SDCardChecker{
		logger: logger,
	}
}

// Check verifies if the device is an SD card (mmcblk)
func (s *SDCardChecker) Check(device string) bool {
	if !strings.HasPrefix(device, "mmcblk") {
		return false
	}

	// Skip system SD card (typically mmcblk0 on Raspberry Pi)
	if device == "mmcblk0" {
		s.logger.Debug("skipping system SD card", "device", device)
		return false
	}

	s.logger.Debug("device is removable SD card", "device", device)
	return true
}
