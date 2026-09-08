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
	mu        sync.Mutex // Prevents parallel device processing
	scanMu    sync.Mutex // Prevents overlapping manual scans
	webdavSrv *WebDAVServer

	// Injected dependencies
	watcher       *fsnotify.Watcher
	ueventMonitor *uevent.Monitor
	deviceService *device.Service
	fs            fs.FileSystem
}

// NewApp creates a new application instance with explicit dependencies.
// The application context is created locally in Run and threaded through as
// an argument; it is not stored on the struct (contexts belong to a call,
// not a long-lived object).
func NewApp(
	config *config.Config,
	logger *slog.Logger,
	watcher *fsnotify.Watcher,
	ueventMonitor *uevent.Monitor,
	deviceService *device.Service,
	fs fs.FileSystem,
) *App {
	return &App{
		config:        config,
		logger:        logger,
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

// cleanup performs consistent shutdown cleanup. cancel is the application's
// shutdown signal; calling it interrupts any in-flight backup (rsync/mount/
// sync via context) and lets context-owned components unwind themselves. The
// WebDAV server drains its requests on this cancellation.
func (app *App) cleanup(cancel context.CancelFunc) {
	cancel()

	if app.config.Feedback != nil {
		if err := app.config.Feedback.Halt(); err != nil {
			app.logger.Error("failed to halt feedback", "error", err)
		}
	}
}

// Run starts the application
func (app *App) Run() error {
	// The application context owns the whole run. It is a local, not a struct
	// field: contexts belong to a call, not a long-lived object. cancel is the
	// shutdown signal fired by cleanup on SIGINT/SIGTERM or the shutdown touch.
	ctx, cancel := context.WithCancel(context.Background())
	defer app.cleanup(cancel)

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
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Start WebDAV server if enabled
	if app.config.EnableWebDAV {
		app.webdavSrv = NewWebDAVServer(app.config.BackupPath, app.config.WebDAVPort, app.config.Feedback)
		if err := app.webdavSrv.Start(ctx); err != nil {
			app.logger.Error("failed to start WebDAV server", "error", err)
			if app.config.Feedback != nil {
				app.config.Feedback.Notify(feedback.Event{
					Type:      feedback.EventError,
					Message:   "WebDAV server failed to start",
					Timestamp: time.Now(),
					Error:     err,
				})
			}
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
			return fmt.Errorf("failed to watch /dev: %w", err)
		}
		app.logger.Info("watcher started (fallback mode)", "path", "/dev")
	}

	// Process existing devices first, routed through the same per-device mutex
	// as event-triggered backups so a card present at boot is not backed up
	// twice (once by this scan, once by a queued 'add' uevent).
	app.scanDevices(ctx)

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
				go app.processDevice(ctx, devicePath)
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
				app.handleDeviceEvent(ctx, event)
			}
		case err := <-app.watcher.Errors:
			app.logger.Error("watcher error", "error", err)
		case event := <-touchChan:
			app.handleTouchEvent(ctx, cancel, event)
		case sig := <-sigChan:
			app.logger.Info("shutting down", "signal", sig.String())
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

// handleDeviceEvent processes device creation events
func (app *App) handleDeviceEvent(ctx context.Context, event fsnotify.Event) {
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
	if !app.deviceService.WaitForReady(ctx, deviceName) {
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
	go app.processDevice(ctx, devicePath)
}

// scanDevices enumerates currently-connected devices and backs each up
// through app.processDevice, which holds app.mu. This is the single entry
// point for both the startup scan and the manual (touch) scan, so they share
// the same per-device mutex as uevent-triggered backups. A device already
// being processed (e.g. its 'add' uevent fired during the scan) is simply
// skipped by processDevice's TryLock.
func (app *App) scanDevices(ctx context.Context) {
	app.logger.Info("scanning for devices")
	devices := app.deviceService.FindUSBDevices()
	app.logger.Info("device scan completed", "count", len(devices))
	for _, device := range devices {
		app.logger.Info("processing device", "device", device, "source", "scan")
		app.processDevice(ctx, device)
	}
}

// handleTouchEvent processes touch events from CAP1166. ctx is the app
// context (passed to in-flight backups so they abort on shutdown); cancel is
// the shutdown signal.
func (app *App) handleTouchEvent(ctx context.Context, cancel context.CancelFunc, event feedback.TouchEventType) {
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
		app.scanDevices(ctx)
	case feedback.TouchShutdown:
		app.logger.Info("shutdown triggered via touch")
		// Cancel the app context first. This interrupts any in-flight backup:
		// the rsync (run via exec.CommandContext), the mount/unmount, and the
		// post-backup sync all observe this context and abort. This is what
		// makes the power button actually responsive during a backup.
		app.cleanup(cancel)
		// Actually shut down the machine.
		if err := exec.Command("shutdown", "now").Start(); err != nil {
			app.logger.Error("failed to execute shutdown command", "error", err)
		}
	}
}

// processDevice handles a single device with mutex protection
func (app *App) processDevice(ctx context.Context, device string) {
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

	if err := app.deviceService.ProcessDevice(ctx, device); err != nil {
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
