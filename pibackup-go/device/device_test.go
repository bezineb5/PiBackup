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
		MountPath:  "/media",
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
		MountPath:  "/tmp/mount",
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
		MountPath:  "/tmp/mount",
		BackupPath: "/tmp/backups",
	}
	service := NewService(logger, mockFS, nil, config)

	// Test processing existing devices
	ctx := context.Background()
	service.ProcessExistingDevices(ctx)

	// Verify no panic occurred
}

func TestSanitizeFilename(t *testing.T) {
	logger := slog.Default()
	mockFS := &fs.MockFileSystem{}
	config := &Config{
		MountPath:  "/tmp/mount",
		BackupPath: "/tmp/backups",
	}
	service := NewService(logger, mockFS, nil, config)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Valid characters (unchanged)
		{"alphanumeric", "abc123", "abc123"},
		{"hyphen", "device-name", "device-name"},
		{"underscore", "device_name", "device_name"},
		{"period", "backup.2024", "backup.2024"},
		{"space", "My Device", "My Device"},
		{"mixed valid", "USB-Drive_1.0", "USB-Drive_1.0"},

		// Invalid characters (replaced with _)
		{"slash", "/dev/sda1", "_dev_sda1"},
		{"backslash", "path\\to\\file", "path_to_file"},
		{"colon", "UUID:123-456", "UUID_123-456"},
		{"pipe", "file|name", "file_name"},
		{"question", "file?name", "file_name"},
		{"asterisk", "file*.txt", "file_.txt"},
		{"quote", `file"name`, "file_name"},
		{"less", "file<name", "file_name"},
		{"greater", "file>name", "file_name"},

		// Multiple replacements
		{"complex", "/dev/sda:1|part", "_dev_sda_1_part"},
		{"device path", "/dev/sda1", "_dev_sda1"},

		// Empty and edge cases
		{"empty", "", ""},
		{"only invalid", "///", "___"},
		{"mixed", "USB-Drive:1/Part", "USB-Drive_1_Part"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.sanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
