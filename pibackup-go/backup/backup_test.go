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

func TestGetBackupNameWithUniqueID(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "backup_name_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a mount point with a unique.id file
	mountPoint := tmpDir
	uniqueIDFile := filepath.Join(mountPoint, "unique.id")
	uniqueID := "TEST_DEVICE_123"
	if err := os.WriteFile(uniqueIDFile, []byte(uniqueID), 0644); err != nil {
		t.Fatalf("Failed to create unique.id file: %v", err)
	}

	logger := slog.Default()
	realFS := fs.RealFileSystem{}
	mockFeedback := &MockFeedback{}

	config := &Config{
		BackupPath: "/tmp/backups",
	}
	service := NewService(logger, realFS, mockFeedback, config)

	// Test GetBackupName
	name := service.GetBackupName(mountPoint)
	if name != uniqueID {
		t.Errorf("GetBackupName(%q) = %q, want %q", mountPoint, name, uniqueID)
	}
}

func TestGetBackupNameWithoutUniqueID(t *testing.T) {
	// Create a temporary directory without unique.id
	tmpDir, err := os.MkdirTemp("", "backup_name_no_id_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.Default()
	realFS := fs.RealFileSystem{}
	mockFeedback := &MockFeedback{}

	config := &Config{
		BackupPath: "/tmp/backups",
	}
	service := NewService(logger, realFS, mockFeedback, config)

	// Test GetBackupName - it will generate a unique ID
	name := service.GetBackupName(tmpDir)
	// We just verify it returns something non-empty
	if name == "" {
		t.Error("GetBackupName returned empty string")
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
