package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/benjamin/pibackup/pibackup-go/config"
	"github.com/benjamin/pibackup/pibackup-go/device"
	"github.com/benjamin/pibackup/pibackup-go/feedback"
	"github.com/benjamin/pibackup/pibackup-go/fs"
	"github.com/benjamin/pibackup/pibackup-go/uevent"
	"github.com/fsnotify/fsnotify"
)

// App represents the main application
type App struct {
	config    *config.Config
	logger    *slog.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex // Prevents parallel device processing
	scanMu    sync.Mutex // Prevents overlapping manual scans
	webdavSrv *WebDAVServer

	// Injected dependencies
	watcher       *fsnotify.Watcher
	ueventMonitor *uevent.Monitor
	deviceService *device.Service
	fs            fs.FileSystem
}

// NewApp creates a new application instance with explicit dependencies
func NewApp(
	config *config.Config,
	logger *slog.Logger,
	watcher *fsnotify.Watcher,
	ueventMonitor *uevent.Monitor,
	deviceService *device.Service,
	fs fs.FileSystem,
) *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		config:        config,
		logger:        logger,
		ctx:           ctx,
		cancel:        cancel,
		watcher:        watcher,
		ueventMonitor: ueventMonitor,
		deviceService: deviceService,
		fs:            fs,
	}
}

// ueventEvents returns the uevent monitor's event channel, or nil if no
// monitor is available (e.g. on non-Linux platforms or when initialisation
// failed). A nil channel blocks forever in a select, so this guard keeps
// Run() safe without a special-case branch.
func (app *App) ueventEvents() <-chan uevent.Event {
	if app.ueventMonitor == nil {
		return nil
	}
	return app.ueventMonitor.Events
}

// cleanup performs consistent shutdown cleanup
func (app *App) cleanup() {
	app.cancel()

	if app.webdavSrv != nil {
		if err := app.webdavSrv.Stop(); err != nil {
			app.logger.Error("failed to stop WebDAV server", "error", err)
		}
	}

	if app.config.Feedback != nil {
		if err := app.config.Feedback.Halt(); err != nil {
			app.logger.Error("failed to halt feedback", "error", err)
		}
	}
}

// Run starts the application
func (app *App) Run() error {
	// Set as default logger
	slog.SetDefault(app.logger)

	app.logger.Info("application started", "version", "1.0.0")
	if app.config.Feedback != nil {
		app.config.Feedback.Notify(feedback.Event{
			Type:      feedback.EventStatus,
			Message:   "PiBackup started - waiting for devices",
			Timestamp: time.Now(),
		})
	}

	// Create backup directory if it doesn't exist
	if err := app.fs.MkdirAll(app.config.BackupPath, 0755); err != nil {
		app.cleanup()
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Start WebDAV server if enabled
	if app.config.EnableWebDAV {
		app.webdavSrv = NewWebDAVServer(app.config.BackupPath, app.config.WebDAVPort, app.config.Feedback)
		if err := app.webdavSrv.Start(); err != nil {
			app.logger.Error("failed to start WebDAV server", "error", err)
			if app.config.Feedback != nil {
				app.config.Feedback.Notify(feedback.Event{
					Type:      feedback.EventError,
					Message:   "WebDAV server failed to start",
					Timestamp: time.Now(),
					Error:     err,
				})
			}
			app.cleanup()
			return err
		} else {
			app.logger.Info("WebDAV server started", "port", app.config.WebDAVPort)
			if app.config.Feedback != nil {
				app.config.Feedback.Notify(feedback.Event{
					Type:      feedback.EventSuccess,
					Message:   fmt.Sprintf("WebDAV server ready on port %s", app.config.WebDAVPort),
					Timestamp: time.Now(),
				})
			}
		}
	}

	// Start uevent monitor for kernel-level device events (primary)
	// This uses netlink sockets - no CGO required
	if app.ueventMonitor != nil {
		app.logger.Info("uevent monitor already started")
	} else {
		// Fallback: watch /dev for new block devices
		if err := app.watcher.Add("/dev"); err != nil {
			app.cleanup()
			return fmt.Errorf("failed to watch /dev: %w", err)
		}
		app.logger.Info("watcher started (fallback mode)", "path", "/dev")
	}

	// Process existing devices first
	app.deviceService.ProcessExistingDevices(app.ctx)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start touch event handler if TouchPhat feedback is available
	var touchChan <-chan feedback.TouchEventType
	if capFeedback, ok := app.config.Feedback.(*feedback.TouchPhatFeedback); ok {
		touchChan = capFeedback.TouchEvents()
	}

	// Watch for new devices
	for {
		select {
		case uevent := <-app.ueventEvents():
			// Handle kernel uevent (primary)
			switch uevent.Action {
			case "add":
				devicePath := filepath.Join("/dev", uevent.Device)
				app.logger.Info("uevent: device added", "device", devicePath)
				go app.processDevice(devicePath)
			case "remove":
				app.logger.Info("uevent: device removed", "device", uevent.Device)
				// Handle device removal if needed
			case "change":
				app.logger.Debug("uevent: device changed", "device", uevent.Device)
				// Handle device change if needed
			default:
				app.logger.Debug("uevent: unknown action", "action", uevent.Action, "device", uevent.Device)
			}
		case event := <-app.watcher.Events:
			if event.Op&fsnotify.Create == fsnotify.Create {
				app.handleDeviceEvent(event)
			}
		case err := <-app.watcher.Errors:
			app.logger.Error("watcher error", "error", err)
		case event := <-touchChan:
			app.handleTouchEvent(event)
		case sig := <-sigChan:
			app.logger.Info("shutting down", "signal", sig.String())
			app.cleanup()
			return nil
		case <-app.ctx.Done():
			app.cleanup()
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
		if app.config.Feedback != nil {
			app.config.Feedback.Notify(feedback.Event{
				Type:      feedback.EventStatus,
				Message:   fmt.Sprintf("Device skipped: %s", deviceName),
				Device:    deviceName,
				Timestamp: time.Now(),
			})
		}
		return
	}

	devicePath := filepath.Join("/dev", deviceName)
	app.logger.Info("device detected", "device", devicePath, "event", "create")
	if app.config.Feedback != nil {
		app.config.Feedback.Notify(feedback.Event{
			Type:      feedback.EventStatus,
			Message:   fmt.Sprintf("Device detected: %s", deviceName),
			Device:    deviceName,
			Timestamp: time.Now(),
		})
	}

	// Wait for the device to settle: poll its sysfs attributes (USB subsystem,
	// removable flag, medium size) until they are populated, instead of a
	// fixed blind sleep. This mirrors what the uevent path effectively gets
	// for free from the kernel.
	if !app.deviceService.WaitForReady(app.ctx, deviceName) {
		app.logger.Info("device skipped", "device", devicePath, "reason", "not_ready_or_not_usb_storage")
		if app.config.Feedback != nil {
			app.config.Feedback.Notify(feedback.Event{
				Type:      feedback.EventStatus,
				Message:   fmt.Sprintf("Device skipped: %s", deviceName),
				Device:    deviceName,
				Timestamp: time.Now(),
			})
		}
		return
	}

	app.logger.Info("USB storage device detected", "device", devicePath)
	if app.config.Feedback != nil {
		app.config.Feedback.Notify(feedback.Event{
			Type:      feedback.EventStatus,
				Message:   fmt.Sprintf("USB storage detected: %s", deviceName),
				Device:    deviceName,
				Timestamp: time.Now(),
			})
	}
	go app.processDevice(devicePath)
}

// handleTouchEvent processes touch events from CAP1166
func (app *App) handleTouchEvent(event feedback.TouchEventType) {
	switch event {
	case feedback.TouchManualBackup:
		app.logger.Info("manual backup triggered via touch")
		// Debounce: a manual scan reuses the per-device TryLock inside
		// ProcessDevice, but the scan itself can overlap if the button is
		// held. A non-blocking lock collapses repeated triggers into one scan.
		if !app.scanMu.TryLock() {
			app.logger.Info("scan already in progress, ignoring manual trigger")
			return
		}
		defer app.scanMu.Unlock()
		app.deviceService.ProcessExistingDevices(app.ctx)
	case feedback.TouchShutdown:
		app.logger.Info("shutdown triggered via touch")
		// Refuse to power off while a backup is in flight; mid-rsync shutdown
		// could leave a partial backup on disk and risk the source card.
		if !app.mu.TryLock() {
			app.logger.Warn("shutdown deferred: backup in progress")
			if app.config.Feedback != nil {
				app.config.Feedback.Notify(feedback.Event{
					Type:      feedback.EventWarning,
					Message:   "Shutdown deferred: backup in progress",
					Timestamp: time.Now(),
				})
			}
			return
		}
		app.mu.Unlock()
		app.cleanup()
		// Actually shut down the machine
		if err := exec.Command("shutdown", "now").Start(); err != nil {
			app.logger.Error("failed to execute shutdown command", "error", err)
		}
	}
}

// processDevice handles a single device with mutex protection
func (app *App) processDevice(device string) {
	// Prevent parallel operations
	if !app.mu.TryLock() {
		app.logger.Info("backup already in progress, skipping device", "device", device)
		if app.config.Feedback != nil {
			app.config.Feedback.Notify(feedback.Event{
				Type:      feedback.EventError,
				Message:   "Backup already in progress",
				Device:    filepath.Base(device),
				Timestamp: time.Now(),
			})
		}
		return
	}
	defer app.mu.Unlock()

	if err := app.deviceService.ProcessDevice(app.ctx, device); err != nil {
		app.logger.Error("device processing error", "device", device, "error", err)
		if app.config.Feedback != nil {
			app.config.Feedback.Notify(feedback.Event{
				Type:      feedback.EventError,
				Message:   err.Error(),
				Device:    filepath.Base(device),
				Timestamp: time.Now(),
				Error:     err,
			})
		}
	}
}
