package device

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/benjamin/pibackup/pibackup-go/feedback"
	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// Service handles device detection and processing
type Service struct {
	logger     *slog.Logger
	fs         fs.FileSystem
	feedback   feedback.Feedback
	checkers   []Checker
	mountPath  string
	backupPath string
}

// Config holds configuration for the device service
type Config struct {
	MountPath  string
	BackupPath string
}

// NewService creates a new device service
func NewService(
	logger *slog.Logger,
	fs fs.FileSystem,
	feedback feedback.Feedback,
	config *Config,
) *Service {
	return &Service{
		logger:     logger,
		fs:         fs,
		feedback:   feedback,
		checkers:   Checkers(logger, fs),
		mountPath:  config.MountPath,
		backupPath: config.BackupPath,
	}
}

// IsUSBStorage checks if a device is USB storage using strategy pattern
func (s *Service) IsUSBStorage(device string) bool {
	// Check if device exists in /sys/block
	sysPath := filepath.Join("/sys/block", device)
	if _, err := s.fs.Stat(sysPath); err != nil {
		s.logger.Debug("device not found in /sys/block", "device", device, "error", err)
		return false
	}

	// Use strategy pattern for different device types
	for _, checker := range s.checkers {
		if checker.Check(device) {
			return true
		}
	}

	return false
}

// FindUSBDevices returns a list of USB storage devices and their partitions
func (s *Service) FindUSBDevices() []string {
	var devices []string

	// Read /sys/block directory
	entries, err := s.fs.ReadDir("/sys/block")
	if err != nil {
		s.logger.Error("failed to read /sys/block", "error", err)
		return devices
	}

	s.logger.Info("scanning /sys/block", "entries", len(entries))

	for _, entry := range entries {
		deviceName := entry.Name()
		s.logger.Debug("checking device", "device", deviceName)

		if s.IsUSBStorage(deviceName) {
			s.logger.Info("found USB device", "device", deviceName)

			// Add the whole device
			devices = append(devices, deviceName)

			// Also add its partitions (e.g., sda1, sda2 for device sda)
			partitions := s.findPartitions(deviceName)
			for _, partition := range partitions {
				s.logger.Info("found partition", "partition", partition, "parent", deviceName)
				devices = append(devices, partition)
			}
		}
	}

	s.logger.Info("USB device scan complete", "found", len(devices))
	return devices
}

// findPartitions returns a list of partition names for a device
func (s *Service) findPartitions(deviceName string) []string {
	var partitions []string

	// Check /sys/block/<deviceName> for partition subdirectories
	blockPath := filepath.Join("/sys/block", deviceName)
	entries, err := s.fs.ReadDir(blockPath)
	if err != nil {
		s.logger.Debug("could not read block device directory", "path", blockPath, "error", err)
		return partitions
	}

	// Look for partition entries (they start with the device name followed by a digit)
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			// Check if this is a partition (e.g., "sda1", "sda2")
			if len(name) > len(deviceName) {
				extra := name[len(deviceName):]
				if isAllDigits(extra) {
					partitions = append(partitions, name)
				}
			}
		}
	}

	return partitions
}

// ProcessDevice handles a single device in a goroutine
func (s *Service) ProcessDevice(ctx context.Context, device string) error {
	deviceName := filepath.Base(device)
	devicePath := filepath.Join("/dev", deviceName)

	// Check if device has a medium (size > 0)
	if !s.hasMedium(devicePath) {
		s.logger.Info("device has no medium, skipping", "device", devicePath)
		if s.feedback != nil {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventWarning,
				Message:   fmt.Sprintf("Device skipped: %s (no medium)", deviceName),
				Device:    deviceName,
				Progress:  0,
				Timestamp: time.Now(),
			})
		}
		return nil
	}

	// Check if this is a whole-disk device with partitions
	// If so, skip it and let the partitions be processed separately
	if s.hasPartitions(deviceName) {
		s.logger.Info("skipping whole-disk device with partitions, will process partitions directly", "device", devicePath)
		if s.feedback != nil {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventWarning,
				Message:   fmt.Sprintf("Device skipped: %s (has partitions)", deviceName),
				Device:    deviceName,
				Progress:  0,
				Timestamp: time.Now(),
			})
		}
		return nil
	}

	s.logger.Info("backup started", "device", device)
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventProgress,
			Message:   fmt.Sprintf("Starting backup: %s", deviceName),
			Device:    deviceName,
			Progress:  0,
			Timestamp: time.Now(),
		})
	}

	// Get mount information
	mountInfo := s.getMountInfo(device, deviceName)
	if mountInfo.Error != nil {
		return mountInfo.Error
	}

	// Setup cleanup
	defer s.cleanupMount(mountInfo, deviceName)

	// Validate filesystem
	if !s.hasValidFilesystem(mountInfo.MountPoint) {
		if s.feedback != nil {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventError,
				Message:   "No valid filesystem",
				Device:    deviceName,
				Progress:  0,
				Timestamp: time.Now(),
			})
		}
		return nil
	}

	// Check if backup should be skipped
	if s.shouldSkipBackup(mountInfo.MountPoint) {
		if s.feedback != nil {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventWarning,
				Message:   fmt.Sprintf("Backup skipped: %s (.backupignore)", deviceName),
				Device:    deviceName,
				Progress:  0,
				Timestamp: time.Now(),
			})
		}
		return nil
	}

	// Perform backup
	return s.performBackup(ctx, mountInfo.MountPoint, device, deviceName)
}

// ProcessExistingDevices processes all currently connected devices
func (s *Service) ProcessExistingDevices(ctx context.Context) {
	s.logger.Info("processing existing devices")
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventProgress,
			Message:   "Scanning for existing devices...",
			Device:    "",
			Progress:  0,
			Timestamp: time.Now(),
		})
	}

	devices := s.FindUSBDevices()
	s.logger.Info("device scan completed", "count", len(devices))
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventProgress,
			Message:   fmt.Sprintf("Found %d existing devices", len(devices)),
			Device:    "",
			Progress:  5,
			Timestamp: time.Now(),
		})
	}

	for _, device := range devices {
		s.logger.Info("processing existing device", "device", device, "type", "existing")
		if s.feedback != nil {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventProgress,
				Message:   fmt.Sprintf("Processing existing device: %s", filepath.Base(device)),
				Device:    filepath.Base(device),
				Progress:  5,
				Timestamp: time.Now(),
			})
		}
		if err := s.ProcessDevice(ctx, device); err != nil {
			s.logger.Error("device processing error", "device", device, "error", err)
			if s.feedback != nil {
				s.feedback.Notify(feedback.Event{
					Type:      feedback.EventError,
					Message:   err.Error(),
					Device:    filepath.Base(device),
					Progress:  0,
					Error:     err,
					Timestamp: time.Now(),
				})
			}
		}
	}

	// Send final status after processing all devices
	if s.feedback != nil {
		if len(devices) == 0 {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventWarning,
				Message:   "No devices found to backup",
				Device:    "",
				Progress:  100,
				Timestamp: time.Now(),
			})
		} else {
			s.feedback.Notify(feedback.Event{
				Type:      feedback.EventSuccess,
				Message:   fmt.Sprintf("Processed %d device(s)", len(devices)),
				Device:    "",
				Progress:  100,
				Timestamp: time.Now(),
			})
		}
	}
}

// MountInfo holds mount-related information
type MountInfo struct {
	MountPoint    string
	ShouldUnmount bool
	Error         error
}

// getMountInfo handles all mount-related logic
func (s *Service) getMountInfo(device, deviceName string) MountInfo {
	// Check if already mounted
	if s.isMounted(device) {
		return s.handleExistingMount(device, deviceName)
	}

	// Handle new mount
	return s.handleNewMount(device, deviceName)
}

// handleExistingMount processes already mounted devices
func (s *Service) handleExistingMount(device, deviceName string) MountInfo {
	mountPoint := s.getMountPoint(device)
	if mountPoint == "" {
		return MountInfo{
			Error: fmt.Errorf("mount point not found for mounted device"),
		}
	}

	s.logger.Info("using existing mount point", "device", device, "mount_point", mountPoint)
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventProgress,
			Message:   fmt.Sprintf("Using existing mount: %s", mountPoint),
			Device:    deviceName,
			Progress:  15,
			Timestamp: time.Now(),
		})
	}

	return MountInfo{
		MountPoint:    mountPoint,
		ShouldUnmount: false,
	}
}

// handleNewMount processes new device mounting
func (s *Service) handleNewMount(device, deviceName string) MountInfo {
	// Check if device has a partition, prefer to mount the partition
	partition := s.getFirstPartition(deviceName)
	actualDevice := device
	if partition != "" {
		actualDevice = filepath.Join("/dev", partition)
		s.logger.Info("mounting partition instead of whole disk", "partition", partition, "device", deviceName)
	}

	// Use the actual device name (partition or whole disk) for mount point
	actualDeviceName := filepath.Base(actualDevice)
	mountPoint := filepath.Join(s.mountPath, actualDeviceName)

	// Create mount point
	if err := s.fs.MkdirAll(mountPoint, 0755); err != nil {
		return MountInfo{
			Error: fmt.Errorf("failed to create mount point: %w", err),
		}
	}

	// Check if mount point is in use
	if s.isMountPointInUse(mountPoint) {
		s.logger.Warn("mount point already in use", "device", actualDevice, "mount_point", mountPoint)
		return MountInfo{
			MountPoint:    mountPoint,
			ShouldUnmount: false,
		}
	}

	// Mount the actual device (partition or whole disk)
	return s.mountDevice(actualDevice, mountPoint, actualDeviceName)
}

// mountDevice performs the actual mounting
func (s *Service) mountDevice(device, mountPoint, deviceName string) MountInfo {
	s.logger.Info("attempting to mount device", "device", device, "mount_point", mountPoint)
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventProgress,
			Message:   fmt.Sprintf("Mounting device: %s", deviceName),
			Device:    deviceName,
			Progress:  10,
			Timestamp: time.Now(),
		})
	}

	// Get the actual device path to mount
	// If device has partitions, mount the first partition (e.g., sda1 instead of sda)
	devicePath := s.getMountableDevicePath(device)
	s.logger.Debug("mount command", "device_path", devicePath, "mount_point", mountPoint)

	cmd := exec.CommandContext(context.Background(), "mount", devicePath, mountPoint)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return MountInfo{
			Error: fmt.Errorf("failed to mount device: %w (output: %s)", err, string(output)),
		}
	}

	s.logger.Info("device mounted successfully", "device", device, "mount_point", mountPoint)
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventProgress,
			Message:   fmt.Sprintf("Device mounted: %s", deviceName),
			Device:    deviceName,
			Progress:  20,
			Timestamp: time.Now(),
		})
	}

	return MountInfo{
		MountPoint:    mountPoint,
		ShouldUnmount: true,
	}
}

// cleanupMount handles mount cleanup
func (s *Service) cleanupMount(mountInfo MountInfo, deviceName string) {
	if !mountInfo.ShouldUnmount {
		return
	}

	s.logger.Info("attempting to unmount device", "mount_point", mountInfo.MountPoint)
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventProgress,
			Message:   fmt.Sprintf("Unmounting device: %s", deviceName),
			Device:    deviceName,
			Progress:  90,
			Timestamp: time.Now(),
		})
	}

	cmd := exec.CommandContext(context.Background(), "umount", mountInfo.MountPoint)
	if err := cmd.Run(); err != nil {
		s.logger.Error("unmount failed", "mount_point", mountInfo.MountPoint, "error", err)
	} else {
		s.logger.Info("device unmounted successfully", "mount_point", mountInfo.MountPoint)
	}

	s.fs.Remove(mountInfo.MountPoint)
}

// performBackup handles the actual backup process
func (s *Service) performBackup(ctx context.Context, mountPoint, device, deviceName string) error {
	startTime := time.Now()
	backupName := s.getBackupName(mountPoint)
	backupDir := filepath.Join(s.backupPath, backupName)

	s.logger.Info("preparing backup", "device", device, "source", mountPoint, "destination", backupDir)
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventProgress,
			Message:   fmt.Sprintf("Backing up: %s", deviceName),
			Device:    deviceName,
			Progress:  30,
			Timestamp: time.Now(),
		})
	}

	// Get the actual source path to rsync
	// If mount point contains a single directory with the same name as the device,
	// use that as the source to avoid an extra directory level in the backup
	sourcePath := s.getRsyncSourcePath(mountPoint, deviceName)

	// Run backup
	if err := s.runRsyncBackup(ctx, sourcePath, backupDir, device, deviceName); err != nil {
		return err
	}

	// Post-backup tasks
	s.performPostBackupTasks(backupDir)

	// Log completion
	duration := time.Since(startTime)
	s.logger.Info("backup completed successfully", "device", device, "duration_seconds", duration.Seconds())
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventSuccess,
			Message:   fmt.Sprintf("Backup completed in %.1fs", duration.Seconds()),
			Device:    deviceName,
			Progress:  100,
			Timestamp: time.Now(),
		})
	}

	return nil
}

// runRsyncBackup executes the rsync command to perform the actual backup
func (s *Service) runRsyncBackup(ctx context.Context, source, destination, device, deviceName string) error {
	rsyncArgs := []string{
		"-a",                                  // Archive mode
		"--chmod=Du=rwx,Dgo=rwx,Fu=rw,Fog=rw", // Preserve permissions
	}

	// Add debug-level flags only in debug mode
	// Note: Would need log level passed to service for this
	// For now, we'll skip debug flags

	// Ensure source has trailing slash for rsync to copy contents, not the directory itself
	sourcePathWithSlash := source
	if !strings.HasSuffix(source, "/") {
		sourcePathWithSlash = source + "/"
	}
	destPathWithSlash := destination
	if !strings.HasSuffix(destination, "/") {
		destPathWithSlash = destination + "/"
	}
	rsyncArgs = append(rsyncArgs,
		sourcePathWithSlash,
		destPathWithSlash,
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

// performPostBackupTasks handles post-backup operations
func (s *Service) performPostBackupTasks(backupDir string) {
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

// hasValidFilesystem checks if mount point has valid filesystem
func (s *Service) hasValidFilesystem(mountPoint string) bool {
	s.logger.Debug("checking filesystem validity", "mount_point", mountPoint)
	if s.feedback != nil {
		s.feedback.Notify(feedback.Event{
			Type:      feedback.EventProgress,
			Message:   fmt.Sprintf("Checking filesystem: %s", mountPoint),
			Device:    "",
			Progress:  25,
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
			Type:      feedback.EventProgress,
			Message:   fmt.Sprintf("Filesystem check: %d files found", len(files)),
			Device:    "",
			Progress:  25,
			Timestamp: time.Now(),
		})
	}

	// Consider it valid if we can read the directory, even if empty
	return true
}

// shouldSkipBackup checks if backup should be skipped
func (s *Service) shouldSkipBackup(mountPoint string) bool {
	// Check for .backupignore file at the root of the device
	ignoreFile := filepath.Join(mountPoint, ".backupignore")
	if _, err := s.fs.Stat(ignoreFile); err == nil {
		s.logger.Info("backup skipped due to .backupignore file", "device", mountPoint)
		return true
	}
	return false
}

// getBackupName generates a unique backup name
func (s *Service) getBackupName(mountPoint string) string {
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

// getRsyncSourcePath returns the optimal source path for rsync
// If the mount point contains a directory with the same name as the device,
// returns that directory path to avoid an extra directory level in the backup
func (s *Service) getRsyncSourcePath(mountPoint, deviceName string) string {
	// List contents of mount point
	entries, err := s.fs.ReadDir(mountPoint)
	if err != nil {
		s.logger.Debug("could not read mount point directory", "mount_point", mountPoint, "error", err)
		return mountPoint
	}

	// Check if there's a directory with the same name as the device
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == deviceName {
			s.logger.Info("using device directory as source to avoid extra level", "dir", entry.Name())
			return filepath.Join(mountPoint, deviceName)
		}
	}

	// Otherwise, use the mount point itself
	return mountPoint
}

// hasPartitions checks if a device has partition subdirectories
func (s *Service) hasPartitions(deviceName string) bool {
	// Check /sys/block/<deviceName> for partition entries
	// Partitions appear as subdirectories like sda1, sda2, etc.
	blockPath := filepath.Join("/sys/block", deviceName)
	entries, err := s.fs.ReadDir(blockPath)
	if err != nil {
		s.logger.Debug("could not read block device directory", "path", blockPath, "error", err)
		return false
	}

	// Look for partition entries (they start with the device name followed by a digit)
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			// Check if this is a partition (e.g., "sda1", "sda2")
			if len(name) > len(deviceName) {
				// Check if the extra characters are all digits
				extra := name[len(deviceName):]
				if isAllDigits(extra) {
					return true
				}
			}
		}
	}

	return false
}

// isAllDigits checks if a string contains only digit characters
func isAllDigits(s string) bool {
	for _, c := range s {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return len(s) > 0
}

// getFirstPartition returns the first partition of a device, or empty string if none
func (s *Service) getFirstPartition(deviceName string) string {
	blockPath := filepath.Join("/sys/block", deviceName)
	entries, err := s.fs.ReadDir(blockPath)
	if err != nil {
		return ""
	}

	// Look for the first partition (e.g., sda1, sdb1)
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			// Check if this is a partition (device name + digit)
			if len(name) > len(deviceName) {
				extra := name[len(deviceName):]
				if isDigitString(extra) {
					return name
				}
			}
		}
	}
	return ""
}

// isDigitString checks if a string contains only digit characters
func isDigitString(s string) bool {
	for _, c := range s {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return len(s) > 0
}

// hasMedium checks if a device has a storage medium inserted
// hasMedium checks if a device has a storage medium inserted
// For whole devices (sda), checks /sys/block/sda/size
// For partitions (sda1), checks /sys/block/sda/sda1/size
func (s *Service) hasMedium(devicePath string) bool {
	deviceName := filepath.Base(devicePath)

	// Try direct path first (works for whole devices like sda)
	sizePath := filepath.Join("/sys/block", deviceName, "size")
	data, err := s.fs.ReadFile(sizePath)
	if err == nil {
		sizeSectors := strings.TrimSpace(string(data))
		size, err := strconv.ParseInt(sizeSectors, 10, 64)
		if err != nil {
			s.logger.Debug("could not parse device size", "size", sizeSectors, "error", err)
			return false
		}
		return size > 0
	}

	// For partitions (sda1), the size is in /sys/block/sda/sda1/size
	// Try to find the parent device by removing the trailing digit
	if len(deviceName) > 0 && unicode.IsDigit(rune(deviceName[len(deviceName)-1])) {
		parentName := deviceName[:len(deviceName)-1]
		sizePath := filepath.Join("/sys/block", parentName, deviceName, "size")
		data, err := s.fs.ReadFile(sizePath)
		if err == nil {
			sizeSectors := strings.TrimSpace(string(data))
			size, err := strconv.ParseInt(sizeSectors, 10, 64)
			if err != nil {
				s.logger.Debug("could not parse partition size", "size", sizeSectors, "error", err)
				return false
			}
			return size > 0
		}
	}

	// Could not determine size
	s.logger.Debug("could not read device size", "device", devicePath)
	return false
}

// getMountableDevicePath returns the path to mount for a device
// If the device has partitions, returns the first partition (e.g., sda1)
// Otherwise returns the device itself (for devices without partition tables)
func (s *Service) getMountableDevicePath(device string) string {
	deviceName := filepath.Base(device)

	// Check if device has partitions by looking for partition subdirectories
	// Partitions appear as subdirectories in /sys/block/<device> (e.g., /sys/block/sda/sda1)
	blockPath := filepath.Join("/sys/block", deviceName)
	entries, err := s.fs.ReadDir(blockPath)
	if err != nil {
		s.logger.Debug("could not read block device directory", "path", blockPath, "error", err)
		// Fall back to device itself
		return filepath.Join("/dev", deviceName)
	}

	// Look for partition entries (they start with the device name followed by a number)
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			// Check if this is a partition (e.g., "sda1", "sdb2")
			if len(name) > len(deviceName) && name[:len(deviceName)] == deviceName {
				// This is a partition of our device
				return filepath.Join("/dev", name)
			}
		}
	}

	// No partitions found, mount the device itself
	return filepath.Join("/dev", deviceName)
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
	for _, line := range strings.Split(string(data), "\n") {
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

// isMounted checks if a device is mounted
func (s *Service) isMounted(device string) bool {
	entries, err := s.parseMounts()
	if err != nil {
		s.logger.Error("failed to parse mounts", "error", err)
		return false
	}

	devicePath := filepath.Join("/dev", device)
	s.logger.Debug("checking if device is mounted", "device", device, "path", devicePath)

	// Use functional approach to find matches
	for _, entry := range entries {
		if s.isDeviceMatch(entry.Device, devicePath) {
			s.logger.Info("device is mounted", "device", device, "mount_point", entry.MountPoint)
			return true
		}
	}

	s.logger.Debug("device is not mounted", "device", device)
	return false
}

// getMountPoint finds the mount point for a device
func (s *Service) getMountPoint(device string) string {
	entries, err := s.parseMounts()
	if err != nil {
		s.logger.Error("failed to parse mounts", "error", err)
		return ""
	}

	devicePath := filepath.Join("/dev", device)
	s.logger.Debug("searching for mount point", "device", device, "path", devicePath)

	// Find matching mount entry
	for _, entry := range entries {
		if s.isDeviceMatch(entry.Device, devicePath) {
			s.logger.Info("found mount point", "device", device, "mount_point", entry.MountPoint)
			return entry.MountPoint
		}
	}

	s.logger.Debug("no mount point found", "device", device)
	return ""
}

// isMountPointInUse checks if a mount point is already in use
func (s *Service) isMountPointInUse(mountPoint string) bool {
	entries, err := s.parseMounts()
	if err != nil {
		s.logger.Error("failed to parse mounts", "error", err)
		return false
	}

	// Check if mount point is already in use
	for _, entry := range entries {
		if entry.MountPoint == mountPoint {
			s.logger.Debug("mount point already in use", "mount_point", mountPoint, "device", entry.Device)
			return true
		}
	}

	return false
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
