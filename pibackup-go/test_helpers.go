package main

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/benjamin/pibackup/pibackup-go/config"
	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// testApp creates a minimal App instance for testing
func testApp(t *testing.T) *App {
	t.Helper()

	// Create a test logger that writes to os.Stderr for test output
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create a mock filesystem
	mockFS := &fs.MockFileSystem{}

	return &App{
		logger: logger,
		fs:     mockFS,
		config: testConfig(),
	}
}

// testConfig creates a minimal Config instance for testing
func testConfig() *config.Config {
	return &config.Config{
		BackupPath:   "/tmp/test-backup",
		USBMountPath: "/tmp/test-mount",
		LogPath:      "/tmp/test.log",
		LogLevel:     slog.LevelDebug,
		EnableWebDAV: false,
	}
}

// createTestDir creates a temporary directory for testing
func createTestDir(t *testing.T, name string) string {
	t.Helper()

	dir, err := os.MkdirTemp("", name)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Clean up the directory when the test is done
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})

	return dir
}

// assertStringEqual is a helper for string comparisons in tests
func assertStringEqual(t *testing.T, got, want, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", msg, got, want)
	}
}

// assertNoError is a helper for error checking in tests
func assertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: unexpected error: %v", msg, err)
	}
}

// assertError is a helper for checking that an error occurred
func assertError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected error but got none", msg)
	}
}

// assertLength is a helper for checking string length
func assertLength(t *testing.T, got string, want int, msg string) {
	t.Helper()
	if len(got) != want {
		t.Errorf("%s: got length %d, want %d", msg, len(got), want)
	}
}

// assertValidChars is a helper for checking if string contains only valid characters
func assertValidChars(t *testing.T, got string, validChars string, msg string) {
	t.Helper()
	for _, char := range got {
		if !strings.ContainsRune(validChars, char) {
			t.Errorf("%s: contains invalid character %c", msg, char)
		}
	}
}

// assertUnique is a helper for checking if all values in a slice are unique
func assertUnique(t *testing.T, values []string, msg string) {
	t.Helper()
	seen := make(map[string]bool)
	for _, value := range values {
		if seen[value] {
			t.Errorf("%s: duplicate value found: %s", msg, value)
		}
		seen[value] = true
	}
}

// assertHasPrefix is a helper for checking if string has a specific prefix
func assertHasPrefix(t *testing.T, got, prefix, msg string) {
	t.Helper()
	if !strings.HasPrefix(got, prefix) {
		t.Errorf("%s: got %q, want prefix %q", msg, got, prefix)
	}
}

// assertBool is a helper for boolean assertions
func assertBool(t *testing.T, got, want bool, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", msg, got, want)
	}
}

// assertValidBackupName is a helper for checking backup name validity
func assertValidBackupName(t *testing.T, got, msg string) {
	t.Helper()
	isValid := strings.HasPrefix(got, "backup_") || len(got) == 6
	if !isValid {
		t.Errorf("%s: got %q, want either 'backup_' prefix or 6-char ID", msg, got)
	}
}
