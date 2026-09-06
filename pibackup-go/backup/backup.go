// Package backup provides backup functionality using rsync.
//
// This package is the single home for the actual backup workflow:
// deciding whether a mounted source should be backed up, computing its
// backup name, running rsync, and performing post-backup disk flushing.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/benjamin/pibackup/pibackup-go/feedback"
	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// Service handles backup operations.
type Service struct {
	logger     *slog.Logger
	fs         fs.FileSystem
	feedback   feedback.Feedback
	config     *Config
	names      *nameRegistry
}

// Config holds configuration for the backup service.
type Config struct {
	BackupPath string
}

// NewService creates a new backup service.
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
		names:    newNameRegistry(fs, config.BackupPath),
	}
}

// Skip indicates the source was intentionally skipped (e.g. .backupignore or
// no valid filesystem), as opposed to an error during backup.
type Skip struct {
	Reason string
}

func (s Skip) Error() string { return "backup skipped: " + s.Reason }

// Run performs a full backup of the contents mounted at mountPoint into the
// backup directory, using deviceName for feedback and logging.
//
// It returns:
//   - nil on success
//   - a Skip error when the source should be ignored (.backupignore, or an
//     invalid/inaccessible filesystem)
//   - any other error from the backup process
func (s *Service) Run(ctx context.Context, mountPoint, device, deviceName string) error {
	// Validate filesystem before doing anything destructive.
	if !s.hasValidFilesystem(mountPoint) {
		s.notifyError(deviceName, "No valid filesystem")
		return Skip{Reason: "no valid filesystem"}
	}

	// Honour the opt-out marker on the source.
	if s.shouldSkipBackup(mountPoint) {
		s.notify(deviceName, feedback.EventWarning,
			fmt.Sprintf("Backup skipped: %s (.backupignore)", deviceName), 0)
		return Skip{Reason: ".backupignore present"}
	}

	startTime := time.Now()
	backupName := s.getBackupName(mountPoint)
	backupDir := filepath.Join(s.config.BackupPath, backupName)

	s.logger.Info("preparing backup", "device", device, "source", mountPoint, "destination", backupDir)
	s.notify(deviceName, feedback.EventProgress, fmt.Sprintf("Backing up: %s", deviceName), 30)

	sourcePath := s.getRsyncSourcePath(mountPoint, deviceName)

	if err := s.runRsync(ctx, sourcePath, backupDir, device, deviceName); err != nil {
		return err
	}

	s.performPostBackupTasks(backupDir)

	duration := time.Since(startTime)
	s.logger.Info("backup completed successfully",
		"device", device, "destination", backupDir, "duration_seconds", duration.Seconds())
	s.notify(deviceName, feedback.EventSuccess,
		fmt.Sprintf("Backup completed in %.1fs", duration.Seconds()), 100)

	return nil
}

// runRsync executes the rsync command to copy the source into the destination.
func (s *Service) runRsync(ctx context.Context, source, destination, device, deviceName string) error {
	// Archive mode preserves permissions/times/ownership/symlinks; the chmod
	// flag normalises the on-disk permissions of the backup copies.
	rsyncArgs := []string{
		"-a",
		"--chmod=Du=rwx,Dgo=rwx,Fu=rw,Fog=rw",
	}

	// rsync copies the *contents* of a source dir when it ends with a slash,
	// and the dir itself when it does not. We always want the contents.
	sourcePath := strings.TrimSuffix(source, "/") + "/"
	destPath := strings.TrimSuffix(destination, "/") + "/"
	rsyncArgs = append(rsyncArgs, sourcePath, destPath)

	cmd := exec.CommandContext(ctx, "rsync", rsyncArgs...)
	// Capture rsync output so failures land in the structured log instead of
	// being lost on stdout/stderr.
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.logger.Error("rsync failed",
			"device", device, "error", err, "output", string(out))
		s.notifyError(deviceName, "Backup failed")
		return fmt.Errorf("rsync failed: %w (output: %s)", err, string(out))
	}
	return nil
}

// performPostBackupTasks flushes the destination disk buffers and refreshes
// the destination directory timestamp so the most recent backup is obvious.
func (s *Service) performPostBackupTasks(backupDir string) {
	if err := exec.CommandContext(context.Background(), "sync").Run(); err != nil {
		s.logger.Warn("failed to flush disk buffers", "error", err)
	}
	if err := exec.CommandContext(context.Background(), "touch", backupDir).Run(); err != nil {
		s.logger.Warn("failed to update destination timestamp", "error", err)
	}
}

// hasValidFilesystem reports whether the mount point exists and is readable.
// An empty but readable directory is considered valid.
func (s *Service) hasValidFilesystem(mountPoint string) bool {
	s.logger.Debug("checking filesystem validity", "mount_point", mountPoint)

	if _, err := s.fs.Stat(mountPoint); err != nil {
		s.logger.Debug("mount point not accessible", "mount_point", mountPoint, "error", err)
		return false
	}

	files, err := s.fs.ReadDir(mountPoint)
	if err != nil {
		s.logger.Debug("failed to read mount point directory", "mount_point", mountPoint, "error", err)
		return false
	}

	s.logger.Debug("filesystem check", "mount_point", mountPoint, "file_count", len(files))
	return true
}

// shouldSkipBackup reports whether a .backupignore marker is present at the
// root of the mounted source.
func (s *Service) shouldSkipBackup(mountPoint string) bool {
	ignoreFile := filepath.Join(mountPoint, ".backupignore")
	if _, err := s.fs.Stat(ignoreFile); err == nil {
		s.logger.Info("backup skipped due to .backupignore file", "device", mountPoint)
		return true
	}
	return false
}

// getBackupName returns a stable, human-friendly name for this backup, without
// ever writing to the source card.
//
// Resolution order:
//  1. An existing unique.id file on the source is honoured (read-only) for
//     backward compatibility with cards previously stamped by older builds.
//  2. The Pi-side name registry, keyed by the device's stable blkid UUID/serial.
//     On first sight a fresh 6-char ID is generated and persisted there.
//  3. A timestamp fallback if no stable identifier could be determined.
func (s *Service) getBackupName(mountPoint string) string {
	// 1. Existing unique.id on the source (read-only; never written).
	if data, err := s.fs.ReadFile(filepath.Join(mountPoint, "unique.id")); err == nil {
		if name := strings.TrimSpace(string(data)); name != "" {
			s.logger.Debug("using unique.id from device", "id", name)
			return name
		}
	}

	// 2. Stable device identifier + Pi-side name registry.
	deviceKey := s.getDeviceIdentifierFromMount(mountPoint)
	if deviceKey != "" {
		if name, err := s.names.Lookup(deviceKey); err == nil && name != "" {
			s.logger.Info("using registered device name", "id", deviceKey, "name", name)
			return name
		}
		// First time we see this device: mint a name and persist it on the Pi.
		name := s.generateUniqueID()
		if stored, err := s.names.Assign(deviceKey, name); err == nil {
			s.logger.Info("registered new device name", "id", deviceKey, "name", stored)
			return stored
		} else {
			s.logger.Warn("failed to persist device name, using ephemeral name",
				"id", deviceKey, "name", name, "error", err)
			return name
		}
	}

	// 3. Timestamp fallback (no stable identifier available).
	name := fmt.Sprintf("backup_%s", time.Now().Format("20060102_150405"))
	s.logger.Info("using timestamp backup name (no stable device identifier)", "name", name)
	return name
}

// generateUniqueID creates a random 6-character alphanumeric ID.
func (s *Service) generateUniqueID() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}

// getDeviceIdentifierFromMount looks up the block device backing mountPoint
// in /proc/mounts and returns a sanitised blkid UUID or serial for it. It only
// reads from the source (via blkid); it never writes to the card.
func (s *Service) getDeviceIdentifierFromMount(mountPoint string) string {
	data, err := s.fs.ReadFile("/proc/mounts")
	if err != nil {
		s.logger.Debug("failed to read /proc/mounts", "error", err)
		return ""
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] != mountPoint {
			continue
		}
		devicePath := fields[0]
		s.logger.Debug("found device for mount point",
			"device", devicePath, "mount_point", mountPoint)

		out, err := exec.Command("blkid", "-s", "UUID", "-s", "SERIAL", "-o", "value", devicePath).Output()
		if err != nil {
			s.logger.Debug("failed to get device info with blkid",
				"device", devicePath, "error", err)
			return ""
		}
		if id := strings.TrimSpace(string(out)); id != "" {
			if safe := s.sanitizeFilename(id); safe != "" {
				s.logger.Debug("using device identifier", "identifier", safe)
				return safe
			}
		}
		return s.sanitizeFilename(devicePath)
	}
	return ""
}

// getRsyncSourcePath returns the optimal rsync source path. If the mount point
// contains a single top-level directory matching the device name, that
// directory is used to avoid an extra nesting level in the backup.
func (s *Service) getRsyncSourcePath(mountPoint, deviceName string) string {
	entries, err := s.fs.ReadDir(mountPoint)
	if err != nil {
		s.logger.Debug("could not read mount point directory",
			"mount_point", mountPoint, "error", err)
		return mountPoint
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == deviceName {
			s.logger.Info("using device directory as source to avoid extra level",
				"dir", entry.Name())
			return filepath.Join(mountPoint, deviceName)
		}
	}
	return mountPoint
}

// sanitizeFilename makes a string safe for use as a filename.
func (s *Service) sanitizeFilename(name string) string {
	var result strings.Builder
	for _, r := range name {
		if !isSafeFilenameRune(r) {
			result.WriteRune('_')
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func isSafeFilenameRune(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	switch r {
	case '-', '_', '.', ' ':
		return true
	}
	return false
}

// notify is a small helper to emit a feedback event when a feedback sink exists.
func (s *Service) notify(device string, t feedback.EventType, msg string, progress float64) {
	if s.feedback == nil {
		return
	}
	s.feedback.Notify(feedback.Event{
		Type:      t,
		Message:   msg,
		Device:    device,
		Progress:  progress,
		Timestamp: time.Now(),
	})
}

// notifyError emits an error feedback event.
func (s *Service) notifyError(device, msg string) {
	s.notify(device, feedback.EventError, msg, 0)
}

// isAllDigits reports whether s is non-empty and all digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

