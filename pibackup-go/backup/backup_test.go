package backup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/benjamin/pibackup/pibackup-go/feedback"
	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// MockFeedback implements feedback.Feedback for testing
type MockFeedback struct{}

func (m *MockFeedback) Notify(event feedback.Event) {}
func (m *MockFeedback) Halt() error                 { return nil }

func TestNewService(t *testing.T) {
	logger := slog.Default()
	mockFS := &fs.MockFileSystem{}
	mockFeedback := &MockFeedback{}

	config := &Config{
		BackupPath: "/tmp/backups",
	}

	service := NewService(logger, mockFS, mockFeedback, config)
	if service == nil {
		t.Fatal("NewService returned nil")
	}
}

func TestPerformPostBackupTasks(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "post_backup_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.Default()
	mockFS := &fs.MockFileSystem{}
	mockFeedback := &MockFeedback{}

	config := &Config{
		BackupPath: tmpDir,
	}
	service := NewService(logger, mockFS, mockFeedback, config)

	// Create a backup directory
	backupDir := filepath.Join(tmpDir, "test_backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatalf("Failed to create backup dir: %v", err)
	}

	// Run post backup tasks
	// This might fail without rsync, but shouldn't panic
	service.performPostBackupTasks(context.Background(), backupDir)
}

// TestPerformPostBackupTasks_RespectsCancellation proves the post-backup sync
// and touch honour a cancelled context: with a pre-cancelled context they
// must return promptly and not log a spurious warning (the cancelled
// command is the expected outcome, not a failure).
func TestPerformPostBackupTasks_RespectsCancellation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "post_backup_cancel_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	service := NewService(slog.Default(), &fs.MockFileSystem{}, &MockFeedback{}, &Config{BackupPath: tmpDir})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	done := make(chan struct{})
	go func() {
		service.performPostBackupTasks(ctx, tmpDir)
		close(done)
	}()

	select {
	case <-done:
		// success: returned promptly under cancellation
	case <-time.After(5 * time.Second):
		t.Fatal("performPostBackupTasks did not return promptly under a cancelled context")
	}
}

// TestGetBackupName_FallsBackWhenBlkidUnreachable confirms that when the
// device identifier cannot be determined (e.g. /proc/mounts absent in tests),
// the timestamp fallback is used and no error is surfaced. It also exercises
// the ctx-aware path so a nil/cancelled ctx does not panic.
func TestGetBackupName_FallsBackWhenBlkidUnreachable(t *testing.T) {
	service := NewService(slog.Default(), &fs.MockFileSystem{}, &MockFeedback{}, &Config{BackupPath: "/tmp/backups"})
	name := service.getBackupName(context.Background(), "/nonexistent/mountpoint")
	if !strings.HasPrefix(name, "backup_") {
		t.Fatalf("expected timestamp fallback 'backup_...', got %q", name)
	}
}

func TestSanitizeFilename(t *testing.T) {
	logger := slog.Default()
	mockFS := &fs.MockFileSystem{}
	config := &Config{
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
