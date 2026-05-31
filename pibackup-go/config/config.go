// Package config provides configuration management for the application.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/benjamin/pibackup/pibackup-go/feedback"
	"github.com/spf13/viper"
)

// Config holds application configuration
type Config struct {
	BackupPath   string
	USBMountPath string
	LogPath      string
	LogLevel     slog.Level
	Feedback     feedback.Feedback
	WebDAVPort   string
	EnableWebDAV bool
}

// setupViper configures Viper for configuration management
func setupViper(configFile string) error {
	// Set config file name and type
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	// Add config file search paths
	viper.AddConfigPath("/etc/pibackup/")
	viper.AddConfigPath("$HOME/.pibackup/")
	viper.AddConfigPath(".")

	// If a specific config file is provided, use it
	if configFile != "" {
		viper.SetConfigFile(configFile)
	}

	// Set default values
	setDefaults()

	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("error reading config file: %w", err)
		}
		// Config file not found is not an error, we'll use defaults
		fmt.Println("No config file found, using defaults")
	}

	// Bind environment variables
	viper.AutomaticEnv()
	viper.SetEnvPrefix("PIBACKUP")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	return nil
}

// setDefaults sets default configuration values
func setDefaults() {
	// Backup settings
	viper.SetDefault("backup.path", "/share")
	viper.SetDefault("backup.usb_mount_path", "/media")
	viper.SetDefault("backup.skip_patterns", []string{".backupignore"})

	// Logging settings
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.path", "/share/logs")
	viper.SetDefault("logging.max_size", 10)
	viper.SetDefault("logging.max_backups", 20)
	viper.SetDefault("logging.max_age", 120)
	viper.SetDefault("logging.compress", true)

	// WebDAV settings
	viper.SetDefault("webdav.enabled", true)
	viper.SetDefault("webdav.port", "80")
	viper.SetDefault("webdav.username", "")
	viper.SetDefault("webdav.password", "")

	// Feedback settings
	viper.SetDefault("feedback.type", "touchphat")
	viper.SetDefault("feedback.led_brightness", 50)
}

// Load loads configuration from Viper and returns a Config struct
func Load(configFile string) (*Config, error) {
	// Set up Viper
	if err := setupViper(configFile); err != nil {
		return nil, err
	}

	// Parse log level
	logLevelStr := viper.GetString("logging.level")
	var logLevel slog.Level
	switch strings.ToLower(logLevelStr) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	// Create config struct
	cfg := &Config{
		BackupPath:   viper.GetString("backup.path"),
		USBMountPath: viper.GetString("backup.usb_mount_path"),
		LogPath:      viper.GetString("logging.path"),
		LogLevel:     logLevel,
		WebDAVPort:   viper.GetString("webdav.port"),
		EnableWebDAV: viper.GetBool("webdav.enabled"),
	}

	// Set up feedback implementation
	feedbackType := viper.GetString("feedback.type")
	var fb feedback.Feedback
	var err error

	switch feedbackType {
	case "touchphat":
		fb, err = feedback.NewTouchPhatFeedback()
		if err != nil {
			fmt.Printf("Failed to initialize TouchPhat feedback, falling back to none: %v\n", err)
			fb = nil
		}
	case "console":
		fb = &feedback.ConsoleFeedback{}
	case "log":
		fb = feedback.NewLogFeedback()
	case "none":
		fb = nil
	default:
		fmt.Printf("Unknown feedback type '%s', falling back to none\n", feedbackType)
		fb = nil
	}

	cfg.Feedback = fb

	return cfg, nil
}

// Validate validates the loaded configuration
func Validate(cfg *Config) error {
	// Check if backup path is accessible
	if err := os.MkdirAll(cfg.BackupPath, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory %s: %w", cfg.BackupPath, err)
	}

	// Check if USB mount path is accessible
	if err := os.MkdirAll(cfg.USBMountPath, 0755); err != nil {
		return fmt.Errorf("failed to create USB mount directory %s: %w", cfg.USBMountPath, err)
	}

	// Check if log path is accessible
	if err := os.MkdirAll(cfg.LogPath, 0755); err != nil {
		return fmt.Errorf("failed to create log directory %s: %w", cfg.LogPath, err)
	}

	return nil
}
