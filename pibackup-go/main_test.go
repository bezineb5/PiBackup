package main

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benjamin/pibackup/pibackup-go/backup"
	"github.com/benjamin/pibackup/pibackup-go/config"
	"github.com/benjamin/pibackup/pibackup-go/device"
	fspkg "github.com/benjamin/pibackup/pibackup-go/fs"
	"github.com/benjamin/pibackup/pibackup-go/mount"
)

// Test that the filesystem mock works correctly
func TestMockFileSystem(t *testing.T) {
	mockFS := fspkg.NewMockFileSystem()

	// Test MkdirAll
	err := mockFS.MkdirAll("/test/path", 0755)
	if err != nil {
		t.Errorf("MkdirAll failed: %v", err)
	}

	// Test Join
	joined := filepath.Join("a", "b", "c")
	if joined != "a/b/c" {
		t.Errorf("Join failed: got %s, want a/b/c", joined)
	}

	// Test Base
	base := filepath.Base("/path/to/file.txt")
	if base != "file.txt" {
		t.Errorf("Base failed: got %s, want file.txt", base)
	}
}

// Test device service creation
func TestDeviceService_Creation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	mockFS := fspkg.NewMockFileSystem()

	// Set up mock filesystem for device detection
	mockFS.ReadDirReturns["/sys/block"] = []fs.DirEntry{
		fspkg.NewMockDirEntry("sda", true),
		fspkg.NewMockDirEntry("sdb", true),
	}

	service := device.NewService(
		logger,
		mockFS,
		nil, // feedback
		&device.Config{
			MountPath:  "/media",
			BackupPath: "/share",
		},
		mount.NewService(logger, mockFS, false),
		backup.NewService(logger, mockFS, nil, &backup.Config{BackupPath: "/share"}),
	)

	if service == nil {
		t.Error("Device service creation failed")
	}
}

// Test config loading
func TestConfig_Load(t *testing.T) {
	// Test with empty config file (uses defaults)
	cfg, err := config.Load("")
	if err != nil {
		t.Errorf("Config load failed: %v", err)
	}

	if cfg == nil {
		t.Error("Config is nil")
	}

	// Check default values
	if cfg.BackupPath != "/share" {
		t.Errorf("BackupPath: got %s, want /share", cfg.BackupPath)
	}

	if cfg.USBMountPath != "/media" {
		t.Errorf("USBMountPath: got %s, want /media", cfg.USBMountPath)
	}

	if cfg.EnableWebDAV != true {
		t.Errorf("EnableWebDAV: got %v, want true", cfg.EnableWebDAV)
	}

	if cfg.WebDAVPort != "80" {
		t.Errorf("WebDAVPort: got %s, want 80", cfg.WebDAVPort)
	}
}

// Test path joining with filesystem abstraction
func TestFileSystem_PathOperations(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal path",
			input:    "/media/usb",
			expected: "/media/usb",
		},
		{
			name:     "path with spaces",
			input:    "/media/My Drive",
			expected: "/media/My Drive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test Join
			result := filepath.Join("/media", tt.input)
			if !strings.HasSuffix(result, tt.input) {
				t.Errorf("Join failed for %s: got %s", tt.name, result)
			}

			// Test Base
			base := filepath.Base(tt.input)
			if base == "" {
				t.Errorf("Base returned empty for %s", tt.name)
			}
		})
	}
}

// Test that the application structure is correct
func TestApp_Structure(t *testing.T) {
	cfg := &config.Config{
		BackupPath:   "/tmp/test-backup",
		USBMountPath: "/tmp/test-mount",
		LogPath:      "/tmp/test-logs",
		LogLevel:     slog.LevelDebug,
		EnableWebDAV: false,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	mockFS := fspkg.NewMockFileSystem()

	// Create a test app
	app := &App{
		config: cfg,
		logger: logger,
		fs:     mockFS,
	}

	if app.config == nil {
		t.Error("App config is nil")
	}

	if app.logger == nil {
		t.Error("App logger is nil")
	}

	if app.fs == nil {
		t.Error("App filesystem is nil")
	}
}

// Test filesystem operations
func TestRealFileSystem(t *testing.T) {
	realFS := fspkg.NewRealFileSystem()

	// Test creating a temporary directory
	tempDir, err := os.MkdirTemp("", "filesystem-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test MkdirAll
	testPath := filepath.Join(tempDir, "test", "nested", "path")
	err = realFS.MkdirAll(testPath, 0755)
	if err != nil {
		t.Errorf("MkdirAll failed: %v", err)
	}

	// Verify directory was created
	info, err := realFS.Stat(testPath)
	if err != nil {
		t.Errorf("Stat failed: %v", err)
	}

	if !info.IsDir() {
		t.Error("Created path is not a directory")
	}

	// Test WriteFile and ReadFile
	testFile := filepath.Join(tempDir, "test.txt")
	testData := []byte("Hello, World!")

	err = realFS.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Errorf("WriteFile failed: %v", err)
	}

	readData, err := realFS.ReadFile(testFile)
	if err != nil {
		t.Errorf("ReadFile failed: %v", err)
	}

	if string(readData) != string(testData) {
		t.Errorf("ReadFile data mismatch: got %s, want %s", string(readData), string(testData))
	}

	// Test Join
	joined := filepath.Join("a", "b", "c")
	if joined != "a/b/c" {
		t.Errorf("Join failed: got %s, want a/b/c", joined)
	}

	// Test Base
	base := filepath.Base("/path/to/file.txt")
	if base != "file.txt" {
		t.Errorf("Base failed: got %s, want file.txt", base)
	}

	// Test ReadDir
	entries, err := realFS.ReadDir(tempDir)
	if err != nil {
		t.Errorf("ReadDir failed: %v", err)
	}

	if len(entries) < 1 {
		t.Error("ReadDir returned no entries")
	}
}
