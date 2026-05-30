package feedback

import (
	"errors"
	"testing"
	"time"
)

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		eventType EventType
		expected  string
	}{
		{EventStatus, "status"},
		{EventProgress, "progress"},
		{EventSuccess, "success"},
		{EventError, "error"},
		{EventWarning, "warning"},
		{EventType(999), "unknown"},
	}

	for _, tt := range tests {
		result := tt.eventType.String()
		if result != tt.expected {
			t.Errorf("EventType(%d).String() = %s, want %s", tt.eventType, result, tt.expected)
		}
	}
}

func TestEventStruct(t *testing.T) {
	now := time.Now()
	testErr := errors.New("test error")

	event := Event{
		Type:      EventStatus,
		Message:   "Test message",
		Device:    "sda1",
		Progress:  50.0,
		Error:     testErr,
		Timestamp: now,
	}

	if event.Type != EventStatus {
		t.Errorf("Expected Type to be %v, got %v", EventStatus, event.Type)
	}
	if event.Message != "Test message" {
		t.Errorf("Expected Message to be 'Test message', got %s", event.Message)
	}
	if event.Device != "sda1" {
		t.Errorf("Expected Device to be 'sda1', got %s", event.Device)
	}
	if event.Progress != 50.0 {
		t.Errorf("Expected Progress to be 50.0, got %f", event.Progress)
	}
	if event.Error != testErr {
		t.Errorf("Expected Error to be testErr, got %v", event.Error)
	}
	if !event.Timestamp.Equal(now) {
		t.Errorf("Expected Timestamp to be %v, got %v", now, event.Timestamp)
	}
}

func TestConsoleFeedback(t *testing.T) {
	feedback := &ConsoleFeedback{}

	// Test Notify doesn't panic
	event := Event{
		Type:      EventStatus,
		Message:   "Test",
		Timestamp: time.Now(),
	}

	// This will print to stdout, but shouldn't panic
	feedback.Notify(event)

	// Test Halt doesn't panic
	err := feedback.Halt()
	if err != nil {
		t.Errorf("Halt returned error: %v", err)
	}
}

func TestLogFeedback(t *testing.T) {
	feedback := NewLogFeedback()

	if feedback == nil {
		t.Fatal("NewLogFeedback returned nil")
	}

	// Test Notify doesn't panic
	event := Event{
		Type:      EventStatus,
		Message:   "Test",
		Timestamp: time.Now(),
	}

	feedback.Notify(event)

	// Test Halt doesn't panic
	err := feedback.Halt()
	if err != nil {
		t.Errorf("Halt returned error: %v", err)
	}
}

// Test that EventType constants have the expected values
func TestEventTypeConstants(t *testing.T) {
	// These are iota-based, so we can verify the order
	if EventStatus != 0 {
		t.Errorf("Expected EventStatus to be 0, got %d", EventStatus)
	}
	if EventProgress != 1 {
		t.Errorf("Expected EventProgress to be 1, got %d", EventProgress)
	}
	if EventSuccess != 2 {
		t.Errorf("Expected EventSuccess to be 2, got %d", EventSuccess)
	}
	if EventError != 3 {
		t.Errorf("Expected EventError to be 3, got %d", EventError)
	}
	if EventWarning != 4 {
		t.Errorf("Expected EventWarning to be 4, got %d", EventWarning)
	}
}
