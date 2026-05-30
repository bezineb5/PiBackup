package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadConfig(t *testing.T) {
	// Reset Viper before test
	viper.Reset()

	// Create a temporary config file
	tmpDir, err := os.MkdirTemp("", "config_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `
backup:
  path: /tmp/backups
  usb_mount_path: /media/usb
webdav:
  port: 8080
  enabled: true
logging:
  level: INFO
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Load the config
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify the config values
	if cfg.BackupPath != "/tmp/backups" {
		t.Errorf("Expected backup_path /tmp/backups, got %s", cfg.BackupPath)
	}
	if cfg.USBMountPath != "/media/usb" {
		t.Errorf("Expected usb_mount_path /media/usb, got %s", cfg.USBMountPath)
	}
	if cfg.WebDAVPort != "8080" {
		t.Errorf("Expected webdav_port 8080, got %s", cfg.WebDAVPort)
	}
	if !cfg.EnableWebDAV {
		t.Error("Expected enable_webdav true, got false")
	}
}

func TestLoadConfigWithDefaults(t *testing.T) {
	// Reset Viper before test
	viper.Reset()

	// Load config with empty string (will search default paths and use defaults)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load should not fail when searching default paths, got: %v", err)
	}

	// Verify default values
	if cfg.BackupPath != "/share" {
		t.Errorf("Expected default backup_path /share, got %s", cfg.BackupPath)
	}
	if cfg.USBMountPath != "/media" {
		t.Errorf("Expected default usb_mount_path /media, got %s", cfg.USBMountPath)
	}
	if cfg.WebDAVPort != "80" {
		t.Errorf("Expected default webdav_port 80, got %s", cfg.WebDAVPort)
	}
	// Note: EnableWebDAV default is true in config.go
	if !cfg.EnableWebDAV {
		t.Error("Expected default enable_webdav true, got false")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	// Reset Viper before test
	viper.Reset()

	// Create a temporary config file with invalid YAML
	tmpDir, err := os.MkdirTemp("", "config_invalid_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "invalid.yaml")
	invalidContent := `
backup:
  path: /tmp/backups
  invalid indentation
`
	if err := os.WriteFile(configPath, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("Failed to write invalid config file: %v", err)
	}

	// Load the config (should fail for invalid YAML)
	_, err = Load(configPath)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}
