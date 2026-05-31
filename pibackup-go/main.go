package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/benjamin/pibackup/pibackup-go/backup"
	"github.com/benjamin/pibackup/pibackup-go/config"
	"github.com/benjamin/pibackup/pibackup-go/device"
	"github.com/benjamin/pibackup/pibackup-go/fs"
	"github.com/benjamin/pibackup/pibackup-go/mount"
	"github.com/benjamin/pibackup/pibackup-go/uevent"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/natefinch/lumberjack.v2"
)

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
	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate configuration
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	// Create real filesystem
	realFS := fs.NewRealFileSystem()

	// Create logger with rotation
	logger, err := setupLogging(cfg)
	if err != nil {
		return fmt.Errorf("failed to setup logging: %w", err)
	}

	// Create watcher (fallback if uevent fails)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	// Create uevent monitor (primary device detection)
	var ueventMonitor *uevent.Monitor
	ueventMonitor, err = uevent.NewMonitor(logger)
	if err != nil {
		logger.Warn("failed to create uevent monitor, will use fsnotify fallback", "error", err)
		ueventMonitor = nil
	} else {
		logger.Info("uevent monitor started")
	}

	// Create services with dependency injection
	deviceService := device.NewService(
		logger,
		realFS,
		cfg.Feedback,
		&device.Config{
			MountPath:  cfg.USBMountPath,
			BackupPath: cfg.BackupPath,
		},
	)

	mountService := mount.NewService(logger, realFS)

	backupService := backup.NewService(
		logger,
		realFS,
		cfg.Feedback,
		&backup.Config{
			BackupPath: cfg.BackupPath,
		},
	)

	// Create and run application
	app := NewApp(
		cfg,
		logger,
		watcher,
		ueventMonitor,
		deviceService,
		mountService,
		backupService,
		realFS,
	)

	return app.Run()
}

// formatConsoleAttr formats attributes for human-readable console output
func formatConsoleAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		if t, ok := a.Value.Any().(time.Time); ok {
			return slog.Attr{
				Key:   "time",
				Value: slog.StringValue(t.Format("15:04:05.000")),
			}
		}
	}
	if a.Key == slog.LevelKey {
		if l, ok := a.Value.Any().(slog.Level); ok {
			return slog.Attr{
				Key:   "level",
				Value: slog.StringValue(fmt.Sprintf("[%s]", strings.ToUpper(l.String()))),
			}
		}
	}
	return a
}

// setupLogging configures structured logging with rotation and console output
func setupLogging(cfg *config.Config) (*slog.Logger, error) {
	// Create log directory if it doesn't exist
	if err := os.MkdirAll(cfg.LogPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory %s: %w", cfg.LogPath, err)
	}

	// Set up log rotation with Viper configuration
	fileWriter := &lumberjack.Logger{
		Filename:   filepath.Join(cfg.LogPath, "pibackup.log"),
		MaxSize:    viper.GetInt("logging.max_size"),    // MB per file
		MaxBackups: viper.GetInt("logging.max_backups"), // Keep backup files
		MaxAge:     viper.GetInt("logging.max_age"),     // Keep files for days
		Compress:   viper.GetBool("logging.compress"),   // Compress old files
	}

	// Create a multi-handler that writes JSON to file and human-readable text to console
	logger := slog.New(
		NewMultiHandler(
			slog.NewJSONHandler(fileWriter, &slog.HandlerOptions{
				Level:     cfg.LogLevel,
				AddSource: true,
			}),
			slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level:       cfg.LogLevel,
				ReplaceAttr: formatConsoleAttr,
			}),
		),
	)

	return logger, nil
}

// MultiHandler is a slog.Handler that writes to multiple handlers
type MultiHandler struct {
	handlers []slog.Handler
}

// Enabled implements slog.Handler
func (h *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle implements slog.Handler
func (h *MultiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if err := handler.Handle(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

// WithAttrs implements slog.Handler
func (h *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: handlers}
}

// WithGroup implements slog.Handler
func (h *MultiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithGroup(name)
	}
	return &MultiHandler{handlers: handlers}
}

// NewMultiHandler creates a new MultiHandler with the given handlers
func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return &MultiHandler{handlers: handlers}
}
