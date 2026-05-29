package feedback

import (
	"fmt"
	"time"
)

// ConsoleFeedback implements Feedback for console output
type ConsoleFeedback struct{}

// Notify prints events to the console
func (c *ConsoleFeedback) Notify(event Event) {
	var prefix string
	switch event.Type {
	case EventStatus:
		prefix = "[STATUS]"
	case EventProgress:
		prefix = fmt.Sprintf("[PROGRESS: %.0f%%]", event.Progress)
	case EventSuccess:
		prefix = "[SUCCESS]"
	case EventError:
		prefix = "[ERROR]"
	case EventWarning:
		prefix = "[WARNING]"
	}

	message := event.Message
	if event.Device != "" {
		message = fmt.Sprintf("%s (%s)", message, event.Device)
	}
	if event.Error != nil {
		message = fmt.Sprintf("%s: %v", message, event.Error)
	}

	fmt.Printf("%s %s\n", event.Timestamp.Format(time.RFC3339), prefix+message)
}

// Halt performs no action for console feedback
func (c *ConsoleFeedback) Halt() error {
	return nil
}
