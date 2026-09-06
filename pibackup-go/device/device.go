package device

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/benjamin/pibackup/pibackup-go/backup"
	"github.com/benjamin/pibackup/pibackup-go/feedback"
	"github.com/benjamin/pibackup/pibackup-go/fs"
	"github.com/benjamin/pibackup/pibackup-go/mount"
)

// Service handles device detection and processing. It coordinates the mount
// service (for safe mount/unmount with noatime/ro/sync) and the backup service
// (for the actual rsync copy). It deliberately does not call mount/umount/rsync
// itself; those live in their respective packages so there is a single source
// of truth for the safety-critical operations.
type Service struct {
	logger       *slog.Logger
	fs           fs.FileSystem
	feedback     feedback.Feedback
	checkers     []Checker
	mountService *mount.Service
	backupService *backup.Service
	mountPath    string
	backupPath   string
}

// Config holds configuration for the device service.
type Config struct {
	MountPath  string
	BackupPath string
}

// NewService creates a new device service.
func NewService(
	logger *slog.Logger,
	fs fs.FileSystem,
	feedback feedback.Feedback,
	config *Config,
	mountService *mount.Service,
	backupService *backup.Service,
) *Service {
	return &Service{
		logger:        logger,
		fs:            fs,
		feedback:      feedback,
		checkers:      Checkers(logger, fs),
		mountService:  mountService,
		backupService: backupService,
		mountPath:     config.MountPath,
		backupPath:    config.BackupPath,
	}
}

// readyRetryAttempts is how many times WaitForReady polls the device's sysfs
// attributes before giving up. It is a var (not a const) so tests can
// shorten the retry budget.
var readyRetryAttempts = 10

// readyRetryInterval is the initial delay between readiness polls, doubled
// each attempt up to a cap. Var for test override.
var readyRetryInterval = 200 * time.Millisecond

// readyRetryMaxInterval caps the backoff between readiness polls. Var for
// test override.
var readyRetryMaxInterval = 2 * time.Second

// WaitForReady polls the device's sysfs attributes until they are populated
// enough to identify it as USB/removable storage with a medium present, or
// until the attempts are exhausted. It replaces the fixed sleep previously
// used to let a freshly-appeared device settle.
//
// Returns true if the device is ready and recognised as USB storage; false
// otherwise (timeout, or definitively not USB storage).
func (s *Service) WaitForReady(ctx context.Context, deviceName string) bool {
	devicePath := filepath.Join("/dev", deviceName)
	interval := readyRetryInterval

	for attempt := 1; attempt <= readyRetryAttempts; attempt++ {
		// Respect shutdown while polling.
		select {
		case <-ctx.Done():
			return false
		default:
		}

		if s.IsUSBStorage(deviceName) && s.hasMedium(devicePath) {
			s.logger.Debug("device ready",
				"device", deviceName, "attempts", attempt)
			return true
		}

		s.logger.Debug("device not ready, retrying",
			"device", deviceName, "attempt", attempt,
			"next_interval", interval)

		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
		}

		interval *= 2
		if interval > readyRetryMaxInterval {
			interval = readyRetryMaxInterval
		}
	}

	s.logger.Info("device did not become ready in time", "device", deviceName,
		"attempts", readyRetryAttempts)
	return false
}
// IsUSBStorage checks if a device is USB (or removable SD) storage using the
// registered checker strategies.
func (s *Service) IsUSBStorage(device string) bool {
	// Device must exist in /sys/block.
	sysPath := filepath.Join("/sys/block", device)
	if _, err := s.fs.Stat(sysPath); err != nil {
		s.logger.Debug("device not found in /sys/block", "device", device, "error", err)
		return false
	}

	for _, checker := range s.checkers {
		if checker.Check(device) {
			return true
		}
	}
	return false
}

// FindUSBDevices returns a list of USB storage devices and their partitions.
func (s *Service) FindUSBDevices() []string {
	var devices []string

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
			devices = append(devices, deviceName)
			for _, partition := range s.partitions(deviceName) {
				s.logger.Info("found partition", "partition", partition, "parent", deviceName)
				devices = append(devices, partition)
			}
		}
	}

	s.logger.Info("USB device scan complete", "found", len(devices))
	return devices
}

// partitions returns the partition names of a whole-disk device by walking
// /sys/block/<deviceName>. A partition is any subdirectory whose name is the
// device name followed by digits (e.g. "sda" -> ["sda1","sda2"]). The result
// is sorted for stable ordering. This is the single helper for partition
// discovery; hasPartitions/getFirstPartition callers derive from it.
func (s *Service) partitions(deviceName string) []string {
	blockPath := filepath.Join("/sys/block", deviceName)
	entries, err := s.fs.ReadDir(blockPath)
	if err != nil {
		s.logger.Debug("could not read block device directory",
			"path", blockPath, "error", err)
		return nil
	}

	var parts []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) > len(deviceName) && isAllDigits(name[len(deviceName):]) {
			parts = append(parts, name)
		}
	}
	sort.Strings(parts)
	return parts
}

// ProcessDevice handles a single device end-to-end: it ensures the device has
// a medium and is not a whole disk with partitions, mounts it (via the mount
// service) if needed, runs the backup (via the backup service), and unmounts
// it again.
func (s *Service) ProcessDevice(ctx context.Context, device string) error {
	deviceName := filepath.Base(device)
	devicePath := filepath.Join("/dev", deviceName)

	// A whole-disk device with partitions is skipped; the partitions are
	// processed individually.
	if s.hasPartitions(deviceName) {
		s.logger.Info("skipping whole-disk device with partitions, will process partitions directly", "device", devicePath)
		s.notify(deviceName, feedback.EventWarning,
			fmt.Sprintf("Device skipped: %s (has partitions)", deviceName), 0)
		return nil
	}

	// Reject card readers / empty slots.
	if !s.hasMedium(devicePath) {
		s.logger.Info("device has no medium, skipping", "device", devicePath)
		s.notify(deviceName, feedback.EventWarning,
			fmt.Sprintf("Device skipped: %s (no medium)", deviceName), 0)
		return nil
	}

	s.logger.Info("backup started", "device", device)
	s.notify(deviceName, feedback.EventProgress, fmt.Sprintf("Starting backup: %s", deviceName), 0)

	// Resolve the mount point, mounting if necessary. cleanup unmounts on the
	// way out only when we performed the mount ourselves.
	mountPoint, shouldUnmount, err := s.ensureMounted(ctx, deviceName)
	if err != nil {
		return err
	}
	defer s.cleanupMount(ctx, mountPoint, shouldUnmount, deviceName)

	// Delegate the actual copy to the backup service.
	if err := s.backupService.Run(ctx, mountPoint, devicePath, deviceName); err != nil {
		if _, ok := err.(backup.Skip); ok {
			// Intentional skip; already reported via feedback.
			return nil
		}
		s.logger.Error("backup failed", "device", device, "error", err)
		s.notify(deviceName, feedback.EventError, err.Error(), 0)
		return err
	}
	return nil
}

// ProcessExistingDevices processes all currently connected devices.
func (s *Service) ProcessExistingDevices(ctx context.Context) {
	s.logger.Info("processing existing devices")
	s.notify("", feedback.EventProgress, "Scanning for existing devices...", 0)

	devices := s.FindUSBDevices()
	s.logger.Info("device scan completed", "count", len(devices))
	s.notify("", feedback.EventProgress, fmt.Sprintf("Found %d existing devices", len(devices)), 5)

	for _, device := range devices {
		s.logger.Info("processing existing device", "device", device, "type", "existing")
		s.notify(filepath.Base(device), feedback.EventProgress,
			fmt.Sprintf("Processing existing device: %s", filepath.Base(device)), 5)
		if err := s.ProcessDevice(ctx, device); err != nil {
			s.logger.Error("device processing error", "device", device, "error", err)
		}
	}

	if len(devices) == 0 {
		s.notify("", feedback.EventWarning, "No devices found to backup", 100)
	} else {
		s.notify("", feedback.EventSuccess, fmt.Sprintf("Processed %d device(s)", len(devices)), 100)
	}
}

// ensureMounted returns the mount point for the device. If the device is
// already mounted, the existing mount point is reused (and not unmounted by
// us). Otherwise the first partition (or the whole device if it has none) is
// mounted under s.mountPath and marked for unmount on cleanup. All mount
// operations honour the provided context so shutdown can interrupt them.
func (s *Service) ensureMounted(ctx context.Context, deviceName string) (mountPoint string, shouldUnmount bool, err error) {
	if s.mountService.IsMounted(deviceName) {
		existing := s.mountService.GetMountPoint(deviceName)
		if existing == "" {
			return "", false, fmt.Errorf("mount point not found for mounted device")
		}
		s.logger.Info("using existing mount point", "device", deviceName, "mount_point", existing)
		s.notify(deviceName, feedback.EventProgress,
			fmt.Sprintf("Using existing mount: %s", existing), 15)
		return existing, false, nil
	}

	// Prefer the first partition over the whole disk for mounting.
	mountable := deviceName
	if partition := s.firstPartition(deviceName); partition != "" {
		mountable = partition
		s.logger.Info("mounting partition instead of whole disk", "partition", partition, "device", deviceName)
	}

	mountPoint = filepath.Join(s.mountPath, mountable)
	if err := s.fs.MkdirAll(mountPoint, 0755); err != nil {
		return "", false, fmt.Errorf("failed to create mount point: %w", err)
	}

	if s.mountService.IsMountPointInUse(mountPoint) {
		s.logger.Warn("mount point already in use", "device", mountable, "mount_point", mountPoint)
		return mountPoint, false, nil
	}

	s.notify(mountable, feedback.EventProgress, fmt.Sprintf("Mounting device: %s", mountable), 10)
	if err := s.mountService.Mount(ctx, mountable, mountPoint); err != nil {
		return "", false, err
	}
	s.notify(mountable, feedback.EventProgress, fmt.Sprintf("Device mounted: %s", mountable), 20)
	return mountPoint, true, nil
}

// cleanupMount unmounts a mount point we own and removes the directory. It
// honours the provided context so shutdown can interrupt the unmount.
func (s *Service) cleanupMount(ctx context.Context, mountPoint string, shouldUnmount bool, deviceName string) {
	if !shouldUnmount {
		return
	}
	s.notify(deviceName, feedback.EventProgress, fmt.Sprintf("Unmounting device: %s", deviceName), 90)
	if err := s.mountService.Unmount(ctx, mountPoint); err != nil {
		s.logger.Error("unmount failed", "mount_point", mountPoint, "error", err)
	}
	s.fs.Remove(mountPoint)
}

// hasPartitions reports whether the whole-disk device has any partition
// entries. It is a thin predicate over partitions.
func (s *Service) hasPartitions(deviceName string) bool {
	return len(s.partitions(deviceName)) > 0
}

// firstPartition returns the first (lowest-numbered) partition name of a
// device, or "" if it has none. It is a thin accessor over partitions.
func (s *Service) firstPartition(deviceName string) string {
	parts := s.partitions(deviceName)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// hasMedium reports whether the device has a storage medium with non-zero size.
func (s *Service) hasMedium(devicePath string) bool {
	deviceName := filepath.Base(devicePath)

	// Whole devices: /sys/block/<dev>/size
	if data, err := s.fs.ReadFile(filepath.Join("/sys/block", deviceName, "size")); err == nil {
		return parseSizePositive(data, s.logger)
	}

	// Partitions (sda1): /sys/block/<parent>/<part>/size
	if len(deviceName) > 0 && unicode.IsDigit(rune(deviceName[len(deviceName)-1])) {
		parentName := deviceName[:len(deviceName)-1]
		if data, err := s.fs.ReadFile(filepath.Join("/sys/block", parentName, deviceName, "size")); err == nil {
			return parseSizePositive(data, s.logger)
		}
	}

	s.logger.Debug("could not read device size", "device", devicePath)
	return false
}

func parseSizePositive(data []byte, logger *slog.Logger) bool {
	sizeSectors := strings.TrimSpace(string(data))
	size, err := strconv.ParseInt(sizeSectors, 10, 64)
	if err != nil {
		logger.Debug("could not parse device size", "size", sizeSectors, "error", err)
		return false
	}
	return size > 0
}

// notify emits a feedback event when a feedback sink is configured.
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

// isAllDigits reports whether s is non-empty and all digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

