package feedback

import (
	"log/slog"
	"os"
)

// LogFeedback implements Feedback for structured JSON logging
type LogFeedback struct {
	logger *slog.Logger
}

// NewLogFeedback creates a new LogFeedback instance
func NewLogFeedback() *LogFeedback {
	return &LogFeedback{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
}

// Notify logs events as structured JSON
func (l *LogFeedback) Notify(event Event) {
	attrs := []any{
		"type", event.Type.String(),
		"message", event.Message,
		"timestamp", event.Timestamp,
	}

	if event.Device != "" {
		attrs = append(attrs, "device", event.Device)
	}
	if event.Progress > 0 {
		attrs = append(attrs, "progress", event.Progress)
	}
	if event.Error != nil {
		attrs = append(attrs, "error", event.Error.Error())
	}

	switch event.Type {
	case EventError:
		l.logger.Error("user feedback", attrs...)
	case EventWarning:
		l.logger.Warn("user feedback", attrs...)
	default:
		l.logger.Info("user feedback", attrs...)
	}
}

// Halt logs shutdown message and returns nil
func (l *LogFeedback) Halt() error {
	l.logger.Info("log feedback halted")
	return nil
}
