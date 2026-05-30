// Package backup provides backup functionality using rsync.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/benjamin/pibackup/pibackup-go/feedback"
	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// Service handles backup operations
type Service struct {
	logger   *slog.Logger
	fs       fs.FileSystem
	feedback feedback.Feedback
	config   *Config
}

// Config holds configuration for the backup service
type Config struct {
	BackupPath string
}

// NewService creates a new backup service
func NewService(
	logger *slog.Logger,
	fs fs.FileSystem,
	feedback feedback.Feedback,
	config *Config,
) *Service {
	return &Service{
		logger:   logger,
		fs:       fs,
		feedback: feedback,
		config:   config,
	}
}

// RunRsyncBackup executes the rsync command to perform the actual backup
func (s *Service) RunRsyncBackup(ctx context.Context, source, destination, device, deviceName string) error {
	rsyncArgs := []string{
		"-a",                                  // Archive mode
		"--chmod=Du=rwx,Dgo=rwx,Fu=rw,Fog=rw", // Preserve permissions
	}

	// Add debug-level flags only in debug mode
	// Note: Would need log level passed to service for this
	// For now, we'll skip debug flags to avoid circular dependencies

	rsyncArgs = append(rsyncArgs,
		filepath.Join(source, ""),      // Source (with trailing slash for rsync)
		filepath.Join(destination, ""), // Destination (with trailing slash for rsync)
	)

	// Run rsync
	cmd := exec.CommandContext(ctx, "rsync", rsyncArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		s.logger.Error("rsync failed", "device", device, "error", err)
		if s.feedback != nil {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventError,
				Message:   "Backup failed",
				Device:    deviceName,
				Progress:  0,
				Error:     err,
				Timestamp: time.Now(),
			})
		}
		return fmt.Errorf("rsync failed: %w", err)
	}

	return nil
}

// PerformPostBackupTasks handles post-backup operations
func (s *Service) PerformPostBackupTasks(backupDir string) {
	// Flush disk buffers
	cmd := exec.CommandContext(context.Background(), "sync")
	if err := cmd.Run(); err != nil {
		s.logger.Warn("failed to flush disk buffers", "error", err)
	}

	// Update timestamps
	cmd = exec.CommandContext(context.Background(), "touch", backupDir)
	if err := cmd.Run(); err != nil {
		s.logger.Warn("failed to update destination timestamp", "error", err)
	}
}

// HasValidFilesystem checks if mount point has valid filesystem
func (s *Service) HasValidFilesystem(mountPoint string) bool {
	s.logger.Debug("checking filesystem validity", "mount_point", mountPoint)
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventStatus,
			Message:   fmt.Sprintf("Checking filesystem: %s", mountPoint),
			Device:    "",
			Progress:  0,
			Timestamp: time.Now(),
		})
	}

	// Check if mount point exists and is accessible
	if _, err := s.fs.Stat(mountPoint); err != nil {
		s.logger.Debug("mount point not accessible", "mount_point", mountPoint, "error", err)
		if s.feedback != nil {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventWarning,
				Message:   fmt.Sprintf("Mount point not accessible: %s (%v)", mountPoint, err),
				Device:    "",
				Progress:  0,
				Error:     err,
				Timestamp: time.Now(),
			})
		}

		// Try to list the parent directory to see what's there
		parentDir := filepath.Join(filepath.Dir(mountPoint))
		if entries, err := s.fs.ReadDir(parentDir); err == nil {
			s.logger.Debug("parent directory contents", "parent", parentDir, "entries", len(entries))
			for _, entry := range entries {
				s.logger.Debug("directory entry", "name", entry.Name(), "is_dir", entry.IsDir())
			}
		}

		return false
	}

	// Check if mount point has any files
	files, err := s.fs.ReadDir(mountPoint)
	if err != nil {
		s.logger.Debug("failed to read mount point directory", "mount_point", mountPoint, "error", err)
		if s.feedback != nil {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventWarning,
				Message:   fmt.Sprintf("Cannot read mount point: %s (%v)", mountPoint, err),
				Device:    "",
				Progress:  0,
				Error:     err,
				Timestamp: time.Now(),
			})
		}
		return false
	}

	s.logger.Debug("filesystem check", "mount_point", mountPoint, "file_count", len(files))
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventStatus,
			Message:   fmt.Sprintf("Filesystem check: %d files found", len(files)),
			Device:    "",
			Progress:  0,
			Timestamp: time.Now(),
		})
	}

	// Consider it valid if we can read the directory, even if empty
	return true
}

// ShouldSkipBackup checks if backup should be skipped
func (s *Service) ShouldSkipBackup(mountPoint string) bool {
	// Check for .backupignore file at the root of the device
	ignoreFile := filepath.Join(mountPoint, ".backupignore")
	if _, err := s.fs.Stat(ignoreFile); err == nil {
		s.logger.Info("backup skipped due to .backupignore file", "device", mountPoint)
		return true
	}
	return false
}

// GetBackupName generates a unique backup name
func (s *Service) GetBackupName(mountPoint string) string {
	// Check for unique.id file
	uniqueIDPath := filepath.Join(mountPoint, "unique.id")
	if data, err := s.fs.ReadFile(uniqueIDPath); err == nil {
		name := strings.TrimSpace(string(data))
		if name != "" {
			return name
		}
	}

	// Generate a unique ID and try to store it on the device
	uniqueID := s.generateUniqueID()

	// Try to write the unique ID to the device
	if err := s.fs.WriteFile(uniqueIDPath, []byte(uniqueID), 0644); err == nil {
		s.logger.Info("generated and stored unique ID", "device", mountPoint, "id", uniqueID)
		return uniqueID
	} else {
		s.logger.Warn("failed to store unique ID on device", "device", mountPoint, "error", err)
	}

	// Use timestamp as fallback
	return fmt.Sprintf("backup_%s", time.Now().Format("20060102_150405"))
}

// generateUniqueID creates a random 6-character ID
func (s *Service) generateUniqueID() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}
