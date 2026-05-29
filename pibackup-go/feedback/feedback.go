// Package feedback provides user feedback interfaces and implementations.
package feedback

import "time"

// EventType defines the types of events that can occur
type EventType int

const (
	EventStatus EventType = iota
	EventProgress
	EventSuccess
	EventError
	EventWarning
)

// String returns the string representation of the EventType
func (e EventType) String() string {
	switch e {
	case EventStatus:
		return "status"
	case EventProgress:
		return "progress"
	case EventSuccess:
		return "success"
	case EventError:
		return "error"
	case EventWarning:
		return "warning"
	default:
		return "unknown"
	}
}

// Event represents a user-facing event
type Event struct {
	Type      EventType
	Message   string
	Device    string
	Progress  float64
	Error     error
	Timestamp time.Time
}

// Feedback defines the interface for output-only feedback devices (console, log)
type Feedback interface {
	// Notify user of application state changes
	Notify(event Event)

	// Halt performs cleanup when shutting down
	Halt() error
}

// TouchEventType represents events from touch-sensitive hardware
type TouchEventType int

const (
	TouchManualBackup TouchEventType = iota
	TouchShutdown
)

// HardwareController extends Feedback with input capabilities for hardware devices
type HardwareController interface {
	Feedback
	// TouchEvents returns a channel for receiving touch input events
	TouchEvents() <-chan TouchEventType
}
