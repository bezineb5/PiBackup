package backup

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

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
	service.PerformPostBackupTasks(backupDir)
}
