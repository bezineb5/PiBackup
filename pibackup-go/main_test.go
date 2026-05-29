package main

import (
	"os"
	"strings"
	"testing"

	"github.com/benjamin/pibackup/pibackup-go/feedback"
)

func TestApp_decodeMountPoint(t *testing.T) {
	app := testApp(t)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal path",
			input:    "/media/usb",
			expected: "/media/usb",
		},
		{
			name:     "space escape",
			input:    "/media/My\\040Drive",
			expected: "/media/My Drive",
		},
		{
			name:     "multiple spaces",
			input:    "/media/My\\040USB\\040Drive",
			expected: "/media/My USB Drive",
		},
		{
			name:     "tab and space",
			input:    "/media/Drive\\011with\\040tab",
			expected: "/media/Drive\twith tab",
		},
		{
			name:     "newline and space",
			input:    "/media/Drive\\012with\\040newline",
			expected: "/media/Drive\nwith newline",
		},
		{
			name:     "backslash and space",
			input:    "/media/Drive\\134with\\040backslash",
			expected: "/media/Drive\\with backslash",
		},
		{
			name:     "multiple spaces complex",
			input:    "/media/Drive\\040with\\040spaces\\040and\\040more",
			expected: "/media/Drive with spaces and more",
		},
		{
			name:     "mixed escapes",
			input:    "/media/Drive\\040with\\011tab\\040and\\012newline",
			expected: "/media/Drive with\ttab and\nnewline",
		},
		{
			name:     "backslash and spaces",
			input:    "/media/Drive\\040with\\134backslash\\040and\\040spaces",
			expected: "/media/Drive with\\backslash and spaces",
		},
		// Additional test cases that demonstrate the improvement over manual method
		{
			name:     "carriage return",
			input:    "/media/Drive\\015with\\040carriage\\040return",
			expected: "/media/Drive\rwith carriage return",
		},
		{
			name:     "escape character",
			input:    "/media/Drive\\033with\\040escape",
			expected: "/media/Drive\x1bwith escape",
		},
		{
			name:     "delete character",
			input:    "/media/Drive\\177with\\040delete",
			expected: "/media/Drive\x7fwith delete",
		},
		{
			name:     "hex escape",
			input:    "/media/Drive\\x20with\\040hex\\040escape",
			expected: "/media/Drive with hex escape",
		},
		{
			name:     "unicode escape",
			input:    "/media/Drive\\u0020with\\040unicode\\040escape",
			expected: "/media/Drive with unicode escape",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "no escapes",
			input:    "/path/without/escapes",
			expected: "/path/without/escapes",
		},
		{
			name:     "invalid escape sequence",
			input:    "/media/Drive\\999invalid",
			expected: "/media/Drive\\999invalid", // Should return original on error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := app.decodeMountPoint(tt.input)
			if got != tt.expected {
				t.Errorf("decodeMountPoint() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// Benchmark the decodeMountPoint method
func BenchmarkApp_decodeMountPoint(b *testing.B) {
	app := testApp(&testing.T{})
	testInput := "/media/My\\040USB\\040Drive\\040with\\040spaces\\040and\\040more"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app.decodeMountPoint(testInput)
	}
}

// Test that the method handles edge cases gracefully
func TestApp_decodeMountPoint_edgeCases(t *testing.T) {
	app := testApp(t)

	// Test with very long strings
	longInput := "/media/" + strings.Repeat("\\040", 1000) + "long"
	result := app.decodeMountPoint(longInput)
	if result == "" {
		t.Error("decodeMountPoint() returned empty string for long input")
	}

	// Test with special characters that might cause issues
	specialInput := "/media/Drive\\040with\\040special\\040chars\\040!@#$%^&*()"
	result = app.decodeMountPoint(specialInput)
	if result == "" {
		t.Error("decodeMountPoint() returned empty string for special characters")
	}
}

// TestApp_generateUniqueID tests the generateUniqueID method
func TestApp_generateUniqueID(t *testing.T) {
	app := testApp(t)

	// Generate multiple IDs and collect them
	ids := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		ids[i] = app.generateUniqueID()
	}

	// Test all IDs at once using helper functions
	for _, id := range ids {
		assertLength(t, id, 6, "generateUniqueID() length")
		assertValidChars(t, id, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "generateUniqueID() characters")
	}

	assertUnique(t, ids, "generateUniqueID() uniqueness")
}

// TestApp_getBackupName tests the getBackupName method
func TestApp_getBackupName(t *testing.T) {
	app := testApp(t)

	t.Run("with existing unique.id file", func(t *testing.T) {
		testDir := createTestDir(t, "backup-name-test")
		uniqueIDPath := testDir + "/unique.id"
		err := os.WriteFile(uniqueIDPath, []byte("TEST123"), 0644)
		assertNoError(t, err, "Failed to create unique.id file")

		got := app.getBackupName(testDir)
		assertStringEqual(t, got, "TEST123", "getBackupName() with existing unique.id")
	})

	t.Run("without unique.id file", func(t *testing.T) {
		emptyDir := createTestDir(t, "backup-name-test-empty")
		got := app.getBackupName(emptyDir)

		assertLength(t, got, 6, "getBackupName() length")
		assertValidChars(t, got, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "getBackupName() characters")
	})

	t.Run("empty mount point", func(t *testing.T) {
		got := app.getBackupName("")
		assertValidBackupName(t, got, "getBackupName() for empty path")
	})
}

// TestApp_shouldSkipBackup tests the shouldSkipBackup method
func TestApp_shouldSkipBackup(t *testing.T) {
	app := testApp(t)

	t.Run("no .backupignore file", func(t *testing.T) {
		testDir := createTestDir(t, "backup-test")
		shouldSkip := app.shouldSkipBackup(testDir)
		assertBool(t, shouldSkip, false, "shouldSkipBackup() when no .backupignore exists")
	})

	t.Run("with .backupignore file", func(t *testing.T) {
		testDir := createTestDir(t, "backup-test")
		ignoreFile := testDir + "/.backupignore"
		err := os.WriteFile(ignoreFile, []byte(""), 0644)
		assertNoError(t, err, "Failed to create .backupignore file")

		shouldSkip := app.shouldSkipBackup(testDir)
		assertBool(t, shouldSkip, true, "shouldSkipBackup() when .backupignore exists")
	})
}

// TestEventType_String tests the EventType String method
func TestEventType_String(t *testing.T) {
	tests := []struct {
		eventType feedback.EventType
		expected  string
	}{
		{feedback.EventStatus, "status"},
		{feedback.EventProgress, "progress"},
		{feedback.EventSuccess, "success"},
		{feedback.EventError, "error"},
		{feedback.EventWarning, "warning"},
		{feedback.EventType(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.eventType.String()
			assertStringEqual(t, got, tt.expected, "EventType.String()")
		})
	}
}
