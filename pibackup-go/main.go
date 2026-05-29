package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"strconv"

	"github.com/benjamin/pibackup/pibackup-go/feedback"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Config holds application configuration
type Config struct {
	BackupPath   string
	USBMountPath string
	LogPath      string
	LogLevel     slog.Level
	Feedback     feedback.Feedback // User feedback implementation
	WebDAVPort   string            // WebDAV server port
	EnableWebDAV bool              // Whether to enable WebDAV server
}

// App represents the main application
type App struct {
	config    *Config
	logger    *slog.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex // Prevents parallel operations
	webdavSrv *WebDAVServer
}

// NewApp creates a new application instance
func NewApp(config *Config) *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		config: config,
		ctx:    ctx,
		cancel: cancel,
	}
}

// setupLogging configures structured logging with rotation
func (app *App) setupLogging() error {
	// Create log directory if it doesn't exist
	if err := os.MkdirAll(app.config.LogPath, 0755); err != nil {
		return fmt.Errorf("failed to create log directory %s: %w", app.config.LogPath, err)
	}

	// Set up log rotation with Viper configuration
	writer := &lumberjack.Logger{
		Filename:   filepath.Join(app.config.LogPath, "pibackup.log"),
		MaxSize:    viper.GetInt("logging.max_size"),    // MB per file
		MaxBackups: viper.GetInt("logging.max_backups"), // Keep backup files
		MaxAge:     viper.GetInt("logging.max_age"),     // Keep files for days
		Compress:   viper.GetBool("logging.compress"),   // Compress old files
	}

	// Create structured logger
	app.logger = slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level:     app.config.LogLevel,
		AddSource: true,
	}))

	// Set as default logger
	slog.SetDefault(app.logger)

	return nil
}

// notify sends an event to the user feedback system
func (app *App) notify(eventType feedback.EventType, message string, device string, progress float64, err error) {
	if app.config.Feedback != nil {
		app.config.Feedback.Notify(feedback.Event{
			Type:      eventType,
			Message:   message,
			Device:    device,
			Progress:  progress,
			Error:     err,
			Timestamp: time.Now(),
		})
	}
}

// Run starts the application
func (app *App) Run() error {
	// Set up logging first
	if err := app.setupLogging(); err != nil {
		return fmt.Errorf("failed to setup logging: %w", err)
	}

	app.logger.Info("application started", "version", "1.0.0")
	app.notify(feedback.EventStatus, "PiBackup started - waiting for devices", "", 0, nil)

	// Create backup directory if it doesn't exist
	if err := os.MkdirAll(app.config.BackupPath, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Start WebDAV server if enabled
	if app.config.EnableWebDAV {
		app.webdavSrv = NewWebDAVServer(app.config.BackupPath, app.config.WebDAVPort, app.config.Feedback)
		if err := app.webdavSrv.Start(); err != nil {
			app.logger.Error("failed to start WebDAV server", "error", err)
			app.notify(feedback.EventError, "WebDAV server failed to start", "", 0, err)
		} else {
			app.logger.Info("WebDAV server started", "port", app.config.WebDAVPort)
			app.notify(feedback.EventSuccess, fmt.Sprintf("WebDAV server ready on port %s", app.config.WebDAVPort), "", 0, nil)
		}
	}

	// Create watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	// Watch /sys/block for device changes
	if err := watcher.Add("/sys/block"); err != nil {
		return fmt.Errorf("failed to watch /sys/block: %w", err)
	}

	app.logger.Info("watcher started", "path", "/sys/block")

	// Process existing devices first
	app.processExistingDevices()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start touch event handler if CAP1166 feedback is available
	var touchChan <-chan feedback.TouchEventType
	if capFeedback, ok := app.config.Feedback.(*feedback.CAP1166Feedback); ok {
		touchChan = capFeedback.TouchEvents()
	}

	// Watch for new devices
	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Create == fsnotify.Create {
				app.handleDeviceEvent(event)
			}
		case err := <-watcher.Errors:
			app.logger.Error("watcher error", "error", err)
		case event := <-touchChan:
			app.handleTouchEvent(event)
		case sig := <-sigChan:
			app.logger.Info("shutting down", "signal", sig.String())
			app.notify(feedback.EventStatus, "Shutting down...", "", 0, nil)
			app.cancel()

			// Stop WebDAV server
			if app.webdavSrv != nil {
				if err := app.webdavSrv.Stop(); err != nil {
					app.logger.Error("failed to stop WebDAV server", "error", err)
				}
			}

			// Halt feedback implementation
			if app.config.Feedback != nil {
				if err := app.config.Feedback.Halt(); err != nil {
					app.logger.Error("failed to halt feedback", "error", err)
				}
			}

			return nil
		case <-app.ctx.Done():
			return nil
		}
	}
}

// handleDeviceEvent processes device creation events
func (app *App) handleDeviceEvent(event fsnotify.Event) {
	deviceName := filepath.Base(event.Name)
	if !strings.HasPrefix(deviceName, "sd") {
		// Skip non-SD devices
		app.logger.Info("device skipped", "device", deviceName, "reason", "not_sd")
		app.notify(feedback.EventStatus, fmt.Sprintf("Device skipped: %s", deviceName), deviceName, 0, nil)
		return
	}

	devicePath := filepath.Join("/dev", deviceName)
	app.logger.Info("device detected", "device", devicePath, "event", "create")
	app.notify(feedback.EventStatus, fmt.Sprintf("Device detected: %s", deviceName), deviceName, 0, nil)

	// Give the device a moment to initialize
	time.Sleep(2 * time.Second)

	if app.isUSBStorage(devicePath) {
		app.logger.Info("USB storage device detected", "device", devicePath)
		app.notify(feedback.EventStatus, fmt.Sprintf("USB storage detected: %s", deviceName), deviceName, 0, nil)
		go app.processDevice(devicePath)
	} else {
		app.logger.Info("device skipped", "device", devicePath, "reason", "not_usb_storage")
		app.notify(feedback.EventStatus, fmt.Sprintf("Device skipped: %s", deviceName), deviceName, 0, nil)
	}
}

// handleTouchEvent processes touch events from CAP1166
func (app *App) handleTouchEvent(event feedback.TouchEventType) {
	switch event {
	case feedback.TouchManualBackup:
		app.logger.Info("manual backup triggered via touch")
		// Trigger backup of all connected devices
		app.processExistingDevices()
	case feedback.TouchShutdown:
		app.logger.Info("shutdown triggered via touch")
		app.cancel()
	}
}

// processDevice handles a single device in a goroutine
func (app *App) processDevice(device string) {
	// Prevent parallel operations
	if !app.mu.TryLock() {
		app.logger.Info("backup already in progress, skipping device", "device", device)
		app.notify(feedback.EventError, "Backup already in progress", filepath.Base(device), 0, nil)
		return
	}
	defer app.mu.Unlock()

	if err := app.mountAndBackup(device); err != nil {
		app.logger.Error("device processing error", "device", device, "error", err)
		app.notify(feedback.EventError, err.Error(), filepath.Base(device), 0, err)
	}
}

func (app *App) processExistingDevices() {
	app.logger.Info("processing existing devices")
	app.notify(feedback.EventStatus, "Scanning for existing devices...", "", 0, nil)
	devices := app.findUSBDevices()
	app.logger.Info("device scan completed", "count", len(devices))
	app.notify(feedback.EventStatus, fmt.Sprintf("Found %d existing devices", len(devices)), "", 0, nil)

	for _, device := range devices {
		app.logger.Info("processing existing device", "device", device, "type", "existing")
		app.notify(feedback.EventStatus, fmt.Sprintf("Processing existing device: %s", filepath.Base(device)), filepath.Base(device), 0, nil)
		if err := app.mountAndBackup(device); err != nil {
			app.logger.Error("device processing error", "device", device, "error", err)
			app.notify(feedback.EventError, err.Error(), filepath.Base(device), 0, err)
		}
	}
}

// findUSBDevices returns a list of USB storage devices
func (app *App) findUSBDevices() []string {
	var devices []string

	// Read /sys/block directory
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		app.logger.Error("failed to read /sys/block", "error", err)
		return devices
	}

	app.logger.Info("scanning /sys/block", "entries", len(entries))

	for _, entry := range entries {
		deviceName := entry.Name()
		app.logger.Debug("checking device", "device", deviceName)

		if app.isUSBStorage(deviceName) {
			app.logger.Info("found USB device", "device", deviceName)
			devices = append(devices, deviceName)
		}
	}

	app.logger.Info("USB device scan complete", "found", len(devices))
	return devices
}

// DeviceChecker defines the interface for device type checking
type DeviceChecker interface {
	Check(device string) bool
}

// USBDeviceChecker checks if device is USB
type USBDeviceChecker struct {
	logger *slog.Logger
}

func (u *USBDeviceChecker) Check(device string) bool {
	sysPath := filepath.Join("/sys/block", device)
	subsystemPath := filepath.Join(sysPath, "device", "subsystem")

	linkTarget, err := os.Readlink(subsystemPath)
	if err != nil {
		u.logger.Debug("could not read subsystem link", "device", device, "error", err)
		return false
	}

	if strings.Contains(linkTarget, "usb") {
		u.logger.Debug("device is USB", "device", device, "subsystem", linkTarget)
		return true
	}

	u.logger.Debug("device is not USB", "device", device, "subsystem", linkTarget)
	return false
}

// SDCardChecker checks if device is SD card
type SDCardChecker struct {
	logger *slog.Logger
}

func (s *SDCardChecker) Check(device string) bool {
	if !strings.HasPrefix(device, "mmcblk") {
		return false
	}

	// Skip system SD card
	if device == "mmcblk0" {
		s.logger.Debug("skipping system SD card", "device", device)
		return false
	}

	s.logger.Debug("device is removable SD card", "device", device)
	return true
}

// RemovableChecker checks if device is removable
type RemovableChecker struct {
	logger *slog.Logger
}

func (r *RemovableChecker) Check(device string) bool {
	if !strings.HasPrefix(device, "sd") {
		return false
	}

	sysPath := filepath.Join("/sys/block", device)
	removablePath := filepath.Join(sysPath, "removable")

	data, err := os.ReadFile(removablePath)
	if err != nil {
		r.logger.Debug("could not read removable status", "device", device, "error", err)
		return false
	}

	if strings.TrimSpace(string(data)) == "1" {
		r.logger.Debug("device is removable USB storage", "device", device)
		return true
	}

	r.logger.Debug("device is not removable", "device", device)
	return false
}

// isUSBStorage checks if a device is USB storage using strategy pattern
func (app *App) isUSBStorage(device string) bool {
	// Check if device exists in /sys/block
	sysPath := filepath.Join("/sys/block", device)
	if _, err := os.Stat(sysPath); err != nil {
		app.logger.Debug("device not found in /sys/block", "device", device, "error", err)
		return false
	}

	// Use strategy pattern for different device types
	checkers := []DeviceChecker{
		&USBDeviceChecker{logger: app.logger},
		&SDCardChecker{logger: app.logger},
		&RemovableChecker{logger: app.logger},
	}

	for _, checker := range checkers {
		if checker.Check(device) {
			return true
		}
	}

	return false
}

// MountEntry represents a mount entry from /proc/mounts
type MountEntry struct {
	Device     string
	MountPoint string
	Filesystem string
	Options    string
}

// parseMounts parses /proc/mounts into structured data
func (app *App) parseMounts() ([]MountEntry, error) {
	data, err := os.ReadFile("/proc/mounts")
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
			MountPoint: app.decodeMountPoint(fields[1]),
			Filesystem: fields[2],
			Options:    fields[3],
		})
	}

	return entries, nil
}

// isDeviceMatch checks if mount entry matches device
func (app *App) isDeviceMatch(mountDevice, devicePath string) bool {
	// Exact match
	if mountDevice == devicePath {
		return true
	}

	// Partition match (e.g., sda1, sda2 for sda)
	return strings.HasPrefix(mountDevice, devicePath)
}

// isMounted checks if a device is mounted using functional approach
func (app *App) isMounted(device string) bool {
	entries, err := app.parseMounts()
	if err != nil {
		app.logger.Error("failed to parse mounts", "error", err)
		return false
	}

	devicePath := filepath.Join("/dev", device)
	app.logger.Debug("checking if device is mounted", "device", device, "path", devicePath)

	// Use functional approach to find matches
	for _, entry := range entries {
		if app.isDeviceMatch(entry.Device, devicePath) {
			app.logger.Info("device is mounted", "device", device, "mount_point", entry.MountPoint)
			return true
		}
	}

	app.logger.Debug("device is not mounted", "device", device)
	return false
}

// generateUniqueID creates a random 6-character ID
func (app *App) generateUniqueID() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}

// flushDiskBuffers ensures data is written to disk
func (app *App) flushDiskBuffers() error {
	cmd := exec.CommandContext(app.ctx, "sync")
	return cmd.Run()
}

// MountInfo holds mount-related information
type MountInfo struct {
	MountPoint    string
	ShouldUnmount bool
	Error         error
}

func (app *App) mountAndBackup(device string) error {
	startTime := time.Now()
	deviceName := filepath.Base(device)

	app.logger.Info("backup started", "device", device)
	app.notify(feedback.EventStatus, fmt.Sprintf("Starting backup: %s", deviceName), deviceName, 0, nil)

	// Get mount information
	mountInfo := app.getMountInfo(device, deviceName)
	if mountInfo.Error != nil {
		return mountInfo.Error
	}

	// Setup cleanup
	defer app.cleanupMount(mountInfo, deviceName)

	// Validate filesystem
	if !app.hasValidFilesystem(mountInfo.MountPoint) {
		app.notify(feedback.EventError, "No valid filesystem", deviceName, 0, nil)
		return nil
	}

	// Check if backup should be skipped
	if app.shouldSkipBackup(mountInfo.MountPoint) {
		app.notify(feedback.EventStatus, fmt.Sprintf("Backup skipped: %s (.backupignore)", deviceName), deviceName, 0, nil)
		return nil
	}

	// Perform backup
	return app.performBackup(mountInfo.MountPoint, device, deviceName, startTime)
}

// getMountInfo handles all mount-related logic
func (app *App) getMountInfo(device, deviceName string) MountInfo {
	// Check if already mounted
	if app.isMounted(device) {
		return app.handleExistingMount(device, deviceName)
	}

	// Handle new mount
	return app.handleNewMount(device, deviceName)
}

// handleExistingMount processes already mounted devices
func (app *App) handleExistingMount(device, deviceName string) MountInfo {
	mountPoint := app.getMountPoint(device)
	if mountPoint == "" {
		return MountInfo{
			Error: fmt.Errorf("mount point not found for mounted device"),
		}
	}

	app.logger.Info("using existing mount point", "device", device, "mount_point", mountPoint)
	app.notify(feedback.EventStatus, fmt.Sprintf("Using existing mount: %s", mountPoint), deviceName, 0, nil)

	return MountInfo{
		MountPoint:    mountPoint,
		ShouldUnmount: false,
	}
}

// handleNewMount processes new device mounting
func (app *App) handleNewMount(device, deviceName string) MountInfo {
	mountPoint := filepath.Join(app.config.USBMountPath, deviceName)

	// Create mount point
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return MountInfo{
			Error: fmt.Errorf("failed to create mount point: %w", err),
		}
	}

	// Check if mount point is in use
	if app.isMountPointInUse(mountPoint) {
		app.logger.Warn("mount point already in use", "device", device, "mount_point", mountPoint)
		return MountInfo{
			MountPoint:    mountPoint,
			ShouldUnmount: false,
		}
	}

	// Mount the device
	return app.mountDevice(device, mountPoint, deviceName)
}

// mountDevice performs the actual mounting
func (app *App) mountDevice(device, mountPoint, deviceName string) MountInfo {
	app.logger.Info("attempting to mount device", "device", device, "mount_point", mountPoint)
	app.notify(feedback.EventStatus, fmt.Sprintf("Mounting device: %s", deviceName), deviceName, 10, nil)

	devicePath := filepath.Join("/dev", device)
	app.logger.Debug("mount command", "device_path", devicePath, "mount_point", mountPoint)

	cmd := exec.CommandContext(app.ctx, "mount", devicePath, mountPoint)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return MountInfo{
			Error: fmt.Errorf("failed to mount device: %w (output: %s)", err, string(output)),
		}
	}

	app.logger.Info("device mounted successfully", "device", device, "mount_point", mountPoint)
	app.notify(feedback.EventStatus, fmt.Sprintf("Device mounted: %s", deviceName), deviceName, 20, nil)

	return MountInfo{
		MountPoint:    mountPoint,
		ShouldUnmount: true,
	}
}

// cleanupMount handles mount cleanup
func (app *App) cleanupMount(mountInfo MountInfo, deviceName string) {
	if !mountInfo.ShouldUnmount {
		return
	}

	app.logger.Info("attempting to unmount device", "mount_point", mountInfo.MountPoint)
	app.notify(feedback.EventStatus, fmt.Sprintf("Unmounting device: %s", deviceName), deviceName, 90, nil)

	if err := exec.CommandContext(app.ctx, "umount", mountInfo.MountPoint).Run(); err != nil {
		app.logger.Error("unmount failed", "mount_point", mountInfo.MountPoint, "error", err)
	} else {
		app.logger.Info("device unmounted successfully", "mount_point", mountInfo.MountPoint)
	}

	os.Remove(mountInfo.MountPoint)
}

// performBackup handles the actual backup process
func (app *App) performBackup(mountPoint, device, deviceName string, startTime time.Time) error {
	backupName := app.getBackupName(mountPoint)
	backupDir := filepath.Join(app.config.BackupPath, backupName)

	app.logger.Info("preparing backup", "device", device, "source", mountPoint, "destination", backupDir)
	app.notify(feedback.EventStatus, fmt.Sprintf("Backing up: %s", deviceName), deviceName, 30, nil)

	// Run backup
	if err := app.runRsyncBackup(mountPoint, backupDir, device, deviceName); err != nil {
		return err
	}

	// Post-backup tasks
	app.performPostBackupTasks(backupDir)

	// Log completion
	duration := time.Since(startTime)
	app.logger.Info("backup completed successfully", "device", device, "duration_seconds", duration.Seconds())
	app.notify(feedback.EventSuccess, fmt.Sprintf("Backup completed in %.1fs", duration.Seconds()), deviceName, 100, nil)

	return nil
}

// performPostBackupTasks handles post-backup operations
func (app *App) performPostBackupTasks(backupDir string) {
	// Flush disk buffers
	if err := app.flushDiskBuffers(); err != nil {
		app.logger.Warn("failed to flush disk buffers", "error", err)
	}

	// Update timestamps
	if err := exec.CommandContext(app.ctx, "touch", backupDir).Run(); err != nil {
		app.logger.Warn("failed to update destination timestamp", "error", err)
	}
}

// runRsyncBackup executes the rsync command to perform the actual backup
func (app *App) runRsyncBackup(source, destination, device, deviceName string) error {
	// Build rsync arguments based on log level
	rsyncArgs := []string{
		"-a",                                  // Archive mode
		"--chmod=Du=rwx,Dgo=rwx,Fu=rw,Fog=rw", // Preserve permissions
	}

	// Add debug-level flags only in debug mode
	if app.config.LogLevel <= slog.LevelDebug {
		rsyncArgs = append(rsyncArgs, "-v", "--progress")
	}

	rsyncArgs = append(rsyncArgs,
		filepath.Join(source, ""),      // Source (with trailing slash for rsync)
		filepath.Join(destination, ""), // Destination (with trailing slash for rsync)
	)

	// Run rsync with better options for permission preservation
	rsyncCmd := exec.CommandContext(app.ctx, "rsync", rsyncArgs...)
	rsyncCmd.Stdout = os.Stdout
	rsyncCmd.Stderr = os.Stderr

	if err := rsyncCmd.Run(); err != nil {
		app.logger.Error("rsync failed", "device", device, "error", err)
		app.notify(feedback.EventError, "Backup failed", deviceName, 0, err)
		return fmt.Errorf("rsync failed: %w", err)
	}

	return nil
}

func (app *App) hasValidFilesystem(mountPoint string) bool {
	app.logger.Debug("checking filesystem validity", "mount_point", mountPoint)
	app.notify(feedback.EventStatus, fmt.Sprintf("Checking filesystem: %s", mountPoint), "", 0, nil)

	// Check if mount point exists and is accessible
	if _, err := os.Stat(mountPoint); err != nil {
		app.logger.Debug("mount point not accessible", "mount_point", mountPoint, "error", err)
		app.notify(feedback.EventWarning, fmt.Sprintf("Mount point not accessible: %s (%v)", mountPoint, err), "", 0, err)

		// Try to list the parent directory to see what's there
		parentDir := filepath.Dir(mountPoint)
		if entries, err := os.ReadDir(parentDir); err == nil {
			app.logger.Debug("parent directory contents", "parent", parentDir, "entries", len(entries))
			for _, entry := range entries {
				app.logger.Debug("directory entry", "name", entry.Name(), "is_dir", entry.IsDir())
			}
		}

		return false
	}

	// Check if mount point has any files
	files, err := os.ReadDir(mountPoint)
	if err != nil {
		app.logger.Debug("failed to read mount point directory", "mount_point", mountPoint, "error", err)
		app.notify(feedback.EventWarning, fmt.Sprintf("Cannot read mount point: %s (%v)", mountPoint, err), "", 0, err)
		return false
	}

	app.logger.Debug("filesystem check", "mount_point", mountPoint, "file_count", len(files))
	app.notify(feedback.EventStatus, fmt.Sprintf("Filesystem check: %d files found", len(files)), "", 0, nil)

	// Consider it valid if we can read the directory, even if empty
	// Empty drives are still valid for backup (they might be new drives)
	isValid := true
	app.logger.Debug("filesystem validation result", "mount_point", mountPoint, "is_valid", isValid)

	return isValid
}

func (app *App) shouldSkipBackup(mountPoint string) bool {
	// Check for .backupignore file at the root of the device
	ignoreFile := filepath.Join(mountPoint, ".backupignore")
	if _, err := os.Stat(ignoreFile); err == nil {
		app.logger.Info("backup skipped due to .backupignore file", "device", mountPoint)
		return true
	}
	return false
}

func (app *App) getBackupName(mountPoint string) string {
	// Check for unique.id file
	uniqueIDPath := filepath.Join(mountPoint, "unique.id")
	if data, err := os.ReadFile(uniqueIDPath); err == nil {
		name := strings.TrimSpace(string(data))
		if name != "" {
			return name
		}
	}

	// Generate a unique ID and try to store it on the device
	uniqueID := app.generateUniqueID()

	// Try to write the unique ID to the device
	if err := os.WriteFile(uniqueIDPath, []byte(uniqueID), 0644); err == nil {
		app.logger.Info("generated and stored unique ID", "device", mountPoint, "id", uniqueID)
		return uniqueID
	} else {
		app.logger.Warn("failed to store unique ID on device", "device", mountPoint, "error", err)
	}

	// Use timestamp as fallback
	return fmt.Sprintf("backup_%s", time.Now().Format("20060102_150405"))
}

var (
	configFile   string
	backupPath   string
	logLevel     string
	webdavPort   string
	feedbackType string
)

func main() {
	// Create root command
	var rootCmd = &cobra.Command{
		Use:   "pibackup",
		Short: "PiBackup - Simple photo backup tool for Raspberry Pi",
		Long: `PiBackup is a lightweight photo backup tool that automatically detects
USB storage devices and backs them up to a specified directory.

Features:
- Automatic device detection and mounting
- Efficient file copying with rsync
- WebDAV server for remote access
- Structured logging with rotation
- Multiple feedback implementations`,
		RunE: runApp,
	}

	// Add command-line flags
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "config file (default is config.yaml)")
	rootCmd.PersistentFlags().StringVarP(&backupPath, "backup-path", "b", "", "backup directory path")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "", "logging level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVarP(&webdavPort, "webdav-port", "p", "", "WebDAV server port")
	rootCmd.PersistentFlags().StringVarP(&feedbackType, "feedback", "f", "", "feedback type (cap1166, console, log, none)")

	// Bind flags to viper
	viper.BindPFlag("backup.path", rootCmd.PersistentFlags().Lookup("backup-path"))
	viper.BindPFlag("logging.level", rootCmd.PersistentFlags().Lookup("log-level"))
	viper.BindPFlag("webdav.port", rootCmd.PersistentFlags().Lookup("webdav-port"))
	viper.BindPFlag("feedback.type", rootCmd.PersistentFlags().Lookup("feedback"))

	// Execute the command
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runApp(cmd *cobra.Command, args []string) error {
	// Load configuration using Viper
	config, err := loadConfig(configFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate configuration
	if err := validateConfig(config); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	// Create and run application
	app := NewApp(config)
	return app.Run()
}

// isMountPointInUse checks if a mount point is already in use
func (app *App) isMountPointInUse(mountPoint string) bool {
	entries, err := app.parseMounts()
	if err != nil {
		app.logger.Error("failed to parse mounts", "error", err)
		return false
	}

	// Check if mount point is already in use
	for _, entry := range entries {
		if entry.MountPoint == mountPoint {
			app.logger.Debug("mount point already in use", "mount_point", mountPoint, "device", entry.Device)
			return true
		}
	}

	return false
}

// getMountPoint finds the mount point for a device using functional approach
func (app *App) getMountPoint(device string) string {
	entries, err := app.parseMounts()
	if err != nil {
		app.logger.Error("failed to parse mounts", "error", err)
		return ""
	}

	devicePath := filepath.Join("/dev", device)
	app.logger.Debug("searching for mount point", "device", device, "path", devicePath)

	// Find matching mount entry
	for _, entry := range entries {
		if app.isDeviceMatch(entry.Device, devicePath) {
			app.logger.Info("found mount point", "device", device, "mount_point", entry.MountPoint)
			return entry.MountPoint
		}
	}

	app.logger.Debug("no mount point found", "device", device)
	return ""
}

// decodeMountPoint decodes escaped characters in mount point paths
func (app *App) decodeMountPoint(mountPoint string) string {
	// Use strconv.Unquote to properly decode all escape sequences
	// We need to wrap the string in quotes first since Unquote expects quoted strings
	quoted := `"` + mountPoint + `"`
	decoded, err := strconv.Unquote(quoted)
	if err != nil {
		// If unquoting fails, return the original string
		app.logger.Warn("failed to decode mount point, using original", "original", mountPoint, "error", err)
		return mountPoint
	}

	app.logger.Debug("decoded mount point", "original", mountPoint, "decoded", decoded)
	return decoded
}
