// Package mount provides mounting and unmounting functionality for storage devices.
package mount

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// Service handles mount operations
type Service struct {
	logger   *slog.Logger
	fs       fs.FileSystem
	readOnly bool
}

// NewService creates a new mount service
func NewService(logger *slog.Logger, fs fs.FileSystem, readOnly bool) *Service {
	return &Service{
		logger:   logger,
		fs:       fs,
		readOnly: readOnly,
	}
}

// MountEntry represents a mount entry from /proc/mounts
type MountEntry struct {
	Device     string
	MountPoint string
	Filesystem string
	Options    string
}

// parseMounts parses /proc/mounts into structured data
func (s *Service) parseMounts() ([]MountEntry, error) {
	data, err := s.fs.ReadFile("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc/mounts: %w", err)
	}

	var entries []MountEntry
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		entries = append(entries, MountEntry{
			Device:     fields[0],
			MountPoint: s.decodeMountPoint(fields[1]),
			Filesystem: fields[2],
			Options:    fields[3],
		})
	}

	return entries, nil
}

// isDeviceMatch checks if mount entry matches device
func (s *Service) isDeviceMatch(mountDevice, devicePath string) bool {
	// Exact match
	if mountDevice == devicePath {
		return true
	}

	// Partition match (e.g., sda1, sda2 for sda)
	return strings.HasPrefix(mountDevice, devicePath)
}

// IsMounted checks if a device is mounted
func (s *Service) IsMounted(device string) bool {
	entries, err := s.parseMounts()
	if err != nil {
		s.logger.Error("failed to parse mounts", "error", err)
		return false
	}

	devicePath := filepath.Join("/dev", device)
	s.logger.Debug("checking if device is mounted", "device", device, "path", devicePath)

	for _, entry := range entries {
		if s.isDeviceMatch(entry.Device, devicePath) {
			s.logger.Info("device is mounted", "device", device, "mount_point", entry.MountPoint)
			return true
		}
	}

	s.logger.Debug("device is not mounted", "device", device)
	return false
}

// GetMountPoint finds the mount point for a device
func (s *Service) GetMountPoint(device string) string {
	entries, err := s.parseMounts()
	if err != nil {
		s.logger.Error("failed to parse mounts", "error", err)
		return ""
	}

	devicePath := filepath.Join("/dev", device)
	s.logger.Debug("searching for mount point", "device", device, "path", devicePath)

	for _, entry := range entries {
		if s.isDeviceMatch(entry.Device, devicePath) {
			s.logger.Info("found mount point", "device", device, "mount_point", entry.MountPoint)
			return entry.MountPoint
		}
	}

	s.logger.Debug("no mount point found", "device", device)
	return ""
}

// IsMountPointInUse checks if a mount point is already in use
func (s *Service) IsMountPointInUse(mountPoint string) bool {
	entries, err := s.parseMounts()
	if err != nil {
		s.logger.Error("failed to parse mounts", "error", err)
		return false
	}

	for _, entry := range entries {
		if entry.MountPoint == mountPoint {
			s.logger.Debug("mount point already in use", "mount_point", mountPoint, "device", entry.Device)
			return true
		}
	}

	return false
}

// Mount mounts a device to a mount point with noatime to prevent metadata writes on read
func (s *Service) Mount(ctx context.Context, device, mountPoint string) error {
	devicePath := filepath.Join("/dev", device)
	s.logger.Info("attempting to mount device", "device", device, "mount_point", mountPoint)

	// Build mount options: always use noatime, add ro if readOnly is enabled
	options := []string{"noatime"}
	if s.readOnly {
		options = append(options, "ro")
	}

	cmd := exec.CommandContext(ctx, "mount", "-o", strings.Join(options, ","), devicePath, mountPoint)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to mount device: %w (output: %s)", err, string(output))
	}

	s.logger.Info("device mounted successfully", "device", device, "mount_point", mountPoint, "read_only", s.readOnly)
	return nil
}

// Unmount unmounts a device from its mount point
func (s *Service) Unmount(ctx context.Context, mountPoint string) error {
	s.logger.Info("attempting to unmount device", "mount_point", mountPoint)

	// Sync filesystem first to ensure all writes are flushed before unmounting
	s.logger.Debug("syncing filesystem before unmount", "mount_point", mountPoint)
	syncCmd := exec.CommandContext(ctx, "sync")
	if err := syncCmd.Run(); err != nil {
		s.logger.Error("sync failed before unmount", "mount_point", mountPoint, "error", err)
		// Continue with unmount even if sync fails
	}

	cmd := exec.CommandContext(ctx, "umount", mountPoint)
	if err := cmd.Run(); err != nil {
		s.logger.Error("unmount failed", "mount_point", mountPoint, "error", err)
		return err
	}

	s.logger.Info("device unmounted successfully", "mount_point", mountPoint)
	return nil
}

// decodeMountPoint decodes escaped characters in mount point paths
func (s *Service) decodeMountPoint(mountPoint string) string {
	// Use strconv.Unquote to properly decode all escape sequences
	// We need to wrap the string in quotes first since Unquote expects quoted strings
	quoted := `"` + mountPoint + `"`
	decoded, err := strconv.Unquote(quoted)
	if err != nil {
		// If unquoting fails, return the original string
		s.logger.Warn("failed to decode mount point, using original", "original", mountPoint, "error", err)
		return mountPoint
	}

	s.logger.Debug("decoded mount point", "original", mountPoint, "decoded", decoded)
	return decoded
}
