package device

import (
	"context"
	"log/slog"
	"testing"

	"github.com/benjamin/pibackup/pibackup-go/fs"
)

func TestNewService(t *testing.T) {
	logger := slog.Default()
	mockFS := &fs.MockFileSystem{}
	config := &Config{
		MountPath: "/media",
		BackupPath: "/backups",
	}

	service := NewService(logger, mockFS, nil, config)
	if service == nil {
		t.Fatal("NewService returned nil")
	}
}

func TestGetParentDeviceName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"sda1", "sda1", "sda"},
		{"sda10", "sda10", "sda"},
		{"sdb2", "sdb2", "sdb"},
		{"mmcblk0p1", "mmcblk0p1", "mmcblk0"},
		{"mmcblk1p2", "mmcblk1p2", "mmcblk1"},
		{"sda", "sda", "sda"},
		{"mmcblk0", "mmcblk0", "mmcblk0"},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getParentDeviceName(tt.input)
			if result != tt.expected {
				t.Errorf("getParentDeviceName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestProcessDevice(t *testing.T) {
	// Create a mock filesystem
	mockFS := &fs.MockFileSystem{}
	
	// Create service
	logger := slog.Default()
	config := &Config{
		MountPath: "/tmp/mount",
		BackupPath: "/tmp/backups",
	}
	service := NewService(logger, mockFS, nil, config)

	// Test processing a device with context
	ctx := context.Background()
	devicePath := "/dev/sda1"
	
	// This will test the flow without actually mounting
	// With MockFileSystem, it won't do much but shouldn't panic
	err := service.ProcessDevice(ctx, devicePath)
	// We expect an error with MockFileSystem, but it shouldn't panic
	_ = err
}

func TestProcessExistingDevices(t *testing.T) {
	// Create a mock filesystem
	mockFS := &fs.MockFileSystem{}
	
	// Create service
	logger := slog.Default()
	config := &Config{
		MountPath: "/tmp/mount",
		BackupPath: "/tmp/backups",
	}
	service := NewService(logger, mockFS, nil, config)

	// Test processing existing devices
	ctx := context.Background()
	service.ProcessExistingDevices(ctx)
	
	// Verify no panic occurred
}
