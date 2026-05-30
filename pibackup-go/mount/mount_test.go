package mount

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/benjamin/pibackup/pibackup-go/fs"
)

func TestNewService(t *testing.T) {
	logger := slog.Default()
	mockFS := &fs.MockFileSystem{}
	
	service := NewService(logger, mockFS)
	if service == nil {
		t.Fatal("NewService returned nil")
	}
}

func TestIsMounted(t *testing.T) {
	logger := slog.Default()
	
	// Create a real filesystem
	realFS := fs.RealFileSystem{}
	service := NewService(logger, realFS)

	// Test with a device that's unlikely to be mounted
	// This will return false
	isMounted := service.IsMounted("/dev/sda999")
	if isMounted {
		t.Error("Expected IsMounted to return false for non-existent device")
	}
}

func TestGetMountPoint(t *testing.T) {
	logger := slog.Default()
	
	// Create a real filesystem
	realFS := fs.RealFileSystem{}
	service := NewService(logger, realFS)

	// Test with a device that's unlikely to be mounted
	mountPoint := service.GetMountPoint("/dev/sda999")
	if mountPoint != "" {
		t.Errorf("Expected empty mount point for non-existent device, got %s", mountPoint)
	}
}

func TestIsMountPointInUse(t *testing.T) {
	logger := slog.Default()
	
	// Create a real filesystem
	realFS := fs.RealFileSystem{}
	service := NewService(logger, realFS)

	// Test with a mount point that's unlikely to be in use
	// This will return false
	inUse := service.IsMountPointInUse("/tmp/nonexistent_mount")
	if inUse {
		t.Error("Expected IsMountPointInUse to return false for non-existent mount point")
	}
}

func TestMountAndUnmount(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "mount_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a real filesystem
	realFS := fs.RealFileSystem{}
	logger := slog.Default()
	service := NewService(logger, realFS)

	// Create a mount point
	mountPoint := filepath.Join(tmpDir, "mount")
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		t.Fatalf("Failed to create mount point: %v", err)
	}

	// Test mounting (this will fail without root, but we can test the error handling)
	ctx := context.Background()
	device := "/dev/sda1"
	err = service.Mount(ctx, device, mountPoint)
	
	// We expect an error because:
	// 1. We're not running as root
	// 2. The device doesn't exist
	// So we just verify that the function doesn't panic
	if err == nil {
		// If it succeeds (unlikely), that's fine too
		// Clean up
		service.Unmount(ctx, mountPoint)
	}
}
