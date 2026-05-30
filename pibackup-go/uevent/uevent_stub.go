//go:build !linux
// +build !linux

// Package uevent provides monitoring of Linux kernel uevents.
// This is a stub implementation for non-Linux platforms.
package uevent

import "log/slog"

// Event represents a block device uevent
type Event struct {
	Action  string
	Device  string
	DevPath string
}

// Monitor is a stub monitor for non-Linux platforms
type Monitor struct {
	Events chan Event
}

// NewMonitor creates a new (stub) monitor
func NewMonitor(logger *slog.Logger) (*Monitor, error) {
	return &Monitor{
		Events: make(chan Event, 100),
	}, nil
}

// Stop stops the stub monitor
func (m *Monitor) Stop() {
	close(m.Events)
}
