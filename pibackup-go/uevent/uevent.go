//go:build linux
// +build linux

// Package uevent provides monitoring of Linux kernel uevents via go-udev.
// This is a pure Go implementation that doesn't require CGO.
// Only available on Linux systems.
package uevent

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/pilebones/go-udev/netlink"
)

// Event represents a block device uevent
type Event struct {
	Action  string // "add" or "remove" or "change"
	Device  string // device name (e.g., "sda1")
	DevPath string // full device path from kernel
}

// Monitor monitors kernel uevents
type Monitor struct {
	logger   *slog.Logger
	conn     *netlink.UEventConn
	Events   chan Event
	stopChan chan struct{}
}

// NewMonitor creates a new uevent monitor
func NewMonitor(logger *slog.Logger) (*Monitor, error) {
	// Create netlink connection
	conn := &netlink.UEventConn{}
	
	// Connect to udev-processed events (Mode 2 = UdevEvent) for richer information
	// Use KernelEvent (Mode 1) for raw kernel events
	if err := conn.Connect(netlink.UdevEvent); err != nil {
		return nil, fmt.Errorf("failed to connect to netlink: %w", err)
	}

	m := &Monitor{
		logger:   logger,
		conn:     conn,
		Events:   make(chan Event, 100),
		stopChan: make(chan struct{}),
	}
	
	// Start monitoring in a goroutine
	go m.run()
	
	return m, nil
}

// run reads uevents and forwards block device events
func (m *Monitor) run() {
	// Create channels for uevents and errors
	ueventChan := make(chan netlink.UEvent, 100)
	errChan := make(chan error, 10)
	
	// Start monitoring
	quit := m.conn.Monitor(ueventChan, errChan, nil)
	defer close(quit)
	
	for {
		select {
		case <-m.stopChan:
			m.conn.Close()
			close(m.Events)
			return
		case uevent := <-ueventChan:
			// Only handle block device events
			if uevent.Env["SUBSYSTEM"] != "block" {
				continue
			}
			
			// Extract device name from DEVPATH
			// DEVPATH is like: /devices/pci0000:00/.../block/sda/sda1
			deviceName := extractDeviceName(uevent.Env["DEVPATH"])
			
			m.logger.Debug("uevent received",
				"action", uevent.Action,
				"subsystem", uevent.Env["SUBSYSTEM"],
				"device", deviceName,
				"devpath", uevent.Env["DEVPATH"],
			)
			
			// Send the event
			m.Events <- Event{
				Action:  string(uevent.Action),
				Device:  deviceName,
				DevPath: uevent.Env["DEVPATH"],
			}
		case err := <-errChan:
			m.logger.Error("uevent error", "error", err)
		}
	}
}

// extractDeviceName extracts the device name from a DEVPATH
// e.g., "/devices/pci0000:00/.../block/sda/sda1" -> "sda1"
func extractDeviceName(devpath string) string {
	// The device name is the last component of the path
	return filepath.Base(devpath)
}

// Stop stops the monitor
func (m *Monitor) Stop() {
	close(m.stopChan)
}
