// Package feedback provides user feedback interfaces and implementations.
//
// This file implements support for the Pimoroni TouchPhat HAT.
// TouchPhat is a capacitive touch add-on board for Raspberry Pi.
// More info: https://shop.pimoroni.com/products/touch-phat
//
// Physical layout (left to right, top to bottom):
// ┌─────────┬─────────┬─────────┐
// │  Back   │    A    │    B    │
// │  LED 5  │  LED 4  │  LED 3  │
// ├─────────┼─────────┼─────────┤
// │  C      │    D    │  Enter  │
// │  LED 2  │  LED 1  │  LED 0  │
// └─────────┴─────────┴─────────┘
//
// TouchPhat uses CAP1166 chip with I2C address 0x2C.
// Each button (channel) has a corresponding LED with reversed mapping:
// Back(0)→LED5, A(1)→LED4, B(2)→LED3, C(3)→LED2, D(4)→LED1, Enter(5)→LED0

package feedback

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/benjamin/pibackup/pibackup-go/drivers/cap1xxx"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/host/v3"
)

// TouchFeature represents a physical feature on the Pimoroni Touch pHAT
type TouchFeature int

const (
	// Touch pHAT has 6 buttons and 6 LEDs
	// Physical layout (left to right, top to bottom):
	// Row 1: Back, A, B
	// Row 2: C,   D, Enter
	Back TouchFeature = iota
	A
	B
	C
	D
	Enter
)

// String returns the name of the touch feature
func (f TouchFeature) String() string {
	switch f {
	case Back:
		return "Back"
	case A:
		return "A"
	case B:
		return "B"
	case C:
		return "C"
	case D:
		return "D"
	case Enter:
		return "Enter"
	default:
		return "Unknown"
	}
}

// Pimoroni Touch pHAT mappings:
// - Pads (input channels): Back=0, A=1, B=2, C=3, D=4, Enter=5
// - LEDs: Back→5, A→4, B→3, C→2, D→1, Enter→0
// This is defined in the Touch pHAT Python driver as:
// NAMES = ['Back', 'A', 'B', 'C', 'D', 'Enter']
// LEDMAP = [5, 4, 3, 2, 1, 0]

// channel returns the CAP1166 channel for a touch feature
func (f TouchFeature) channel() int {
	return int(f)
}

// led returns the CAP1166 LED for a touch feature
func (f TouchFeature) led() int {
	// LEDMAP = [5, 4, 3, 2, 1, 0]
	// Back(0)→5, A(1)→4, B(2)→3, C(3)→2, D(4)→1, Enter(5)→0
	return 5 - int(f)
}

// Configuration: Which features to use for which functions
const (
	PowerButton  = Back // Use Back button for power off (long press)
	BackupButton = A    // Use A button for manual backup
	PowerLED     = Back // Use LED next to Back button for power status
	BackupLED    = A    // Use LED next to A button for backup status
	ErrorLED     = B    // Use LED next to B button for errors
)

// Long press duration for power button
const LongPressDuration = 1 * time.Second

// TouchPhatFeedback implements HardwareController for Pimoroni TouchPhat.
// TouchPhat uses CAP1166 chip with I2C address 0x2C.
type TouchPhatFeedback struct {
	dev       *cap1xxx.Dev
	alertPin  gpio.PinIn
	resetPin  gpio.PinOut
	ledStates map[int]bool
	ctx       context.Context
	cancel    context.CancelFunc
	touchChan chan TouchEventType
	// For long press detection
	touchStartTime map[TouchFeature]time.Time
}

// NewTouchPhatFeedback creates a new TouchPhat feedback implementation.
// TouchPhat is a capacitive touch add-on board for Raspberry Pi.
// More info: https://shop.pimoroni.com/products/touch-phat
func NewTouchPhatFeedback() (*TouchPhatFeedback, error) {
	// Initialize periph.io
	if _, err := host.Init(); err != nil {
		return nil, fmt.Errorf("failed to initialize periph.io: %w", err)
	}

	// Open I2C bus
	i2cBus, err := i2creg.Open("")
	if err != nil {
		return nil, fmt.Errorf("failed to open I2C bus: %w", err)
	}

	// Set up alert pin (GPIO25 for interrupt detection)
	alertPin := gpioreg.ByName("GPIO25")
	if alertPin == nil {
		return nil, fmt.Errorf("invalid alert GPIO pin number")
	}
	if err := alertPin.In(gpio.PullUp, gpio.BothEdges); err != nil {
		return nil, fmt.Errorf("can't monitor the alert pin: %w", err)
	}

	// Set up reset pin (GPIO21 for device reset)
	resetPin := gpioreg.ByName("GPIO21")
	if resetPin == nil {
		return nil, fmt.Errorf("invalid reset GPIO pin number")
	}

	// Configure CAP1166 options
	opts := cap1xxx.DefaultOpts
	opts.I2CAddr = 0x2C
	opts.AlertPin = alertPin
	opts.ResetPin = resetPin
	// Disable LinkedLEDs so we can manually control all LEDs
	opts.LinkedLEDs = false

	// Create CAP1166 device
	dev, err := cap1xxx.NewI2C(i2cBus, &opts)
	if err != nil {
		return nil, fmt.Errorf("couldn't open cap1166: %w", err)
	}

	// Unlink all LEDs so we can control them independently
	if err := dev.LinkLEDs(false); err != nil {
		dev.Halt()
		return nil, fmt.Errorf("failed to unlink LEDs: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	feedback := &TouchPhatFeedback{
		dev:            dev,
		alertPin:       alertPin,
		resetPin:       resetPin,
		ledStates:      make(map[int]bool),
		ctx:            ctx,
		cancel:         cancel,
		touchChan:      make(chan TouchEventType, 10),
		touchStartTime: make(map[TouchFeature]time.Time),
	}

	// Start monitoring touch events
	go feedback.monitorTouchEvents()

	slog.Info("CAP1166 feedback initialized successfully",
		"i2c_addr", fmt.Sprintf("0x%02X", opts.I2CAddr),
		"alert_pin", "GPIO25",
		"reset_pin", "GPIO21",
		"linked_leds", false,
		"power_button", PowerButton,
		"backup_button", BackupButton)

	// Turn on power LED to indicate ready state
	feedback.SetLED(PowerLED, true)
	return feedback, nil
}

// Notify handles user feedback events and controls LEDs
func (c *TouchPhatFeedback) Notify(event Event) {
	switch event.Type {
	case EventStatus:
		// Status updates - do nothing, we use LEDs for specific states

	case EventProgress:
		// Backup progress - turn on backup LED
		c.SetLED(BackupLED, true)

	case EventSuccess:
		// Backup complete - turn off backup LED
		c.SetLED(BackupLED, false)

	case EventError:
		// Error - blink Error LED
		c.BlinkLED(ErrorLED, 100*time.Millisecond, 5)

	case EventWarning:
		// Warning - blink Error LED slower
		c.BlinkLED(ErrorLED, 300*time.Millisecond, 3)
	}
}

// TouchEvents returns the channel for touch input events
func (c *TouchPhatFeedback) TouchEvents() <-chan TouchEventType {
	return c.touchChan
}

// Halt cleans up the CAP1166 feedback
func (c *TouchPhatFeedback) Halt() error {
	c.Shutdown()
	return nil
}

// Shutdown cleans up the CAP1166 feedback
func (c *TouchPhatFeedback) Shutdown() {
	slog.Info("shutting down CAP1166 feedback")

	// Blink power LED to indicate shutdown
	c.BlinkPowerLED()

	// Turn off all other LEDs
	for i := range 6 {
		if i != PowerLED.led() {
			c.SetLED(TouchFeature(i), false)
		}
	}

	// Wait for blink to complete
	time.Sleep(2 * time.Second)

	// Turn off power LED at the end
	c.SetLED(PowerLED, false)

	// Close touch channel
	close(c.touchChan)

	// Cancel context to stop monitoring
	c.cancel()

	// Close device
	if c.dev != nil {
		if err := c.dev.Halt(); err != nil {
			slog.Error("failed to halt CAP1166 device", "error", err)
		}
	}

	slog.Info("CAP1166 feedback shutdown complete")
}

// monitorTouchEvents monitors for touch events on the CAP1166
// Uses polling approach (like Python driver) for reliability
func (c *TouchPhatFeedback) monitorTouchEvents() {
	slog.Info("monitoring for touch events")

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		// Poll input status
		var statuses [6]cap1xxx.TouchStatus
		if err := c.dev.InputStatus(statuses[:]); err != nil {
			slog.Error("error reading inputs", "error", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Check each sensor for touch events
		for i, status := range statuses {
			feature := TouchFeature(i)

			if status == cap1xxx.PressedStatus {
				// Button is currently pressed
				// If this is the first time we see it pressed, record the start time
				if _, exists := c.touchStartTime[feature]; !exists {
					c.touchStartTime[feature] = time.Now()
					slog.Debug("button pressed", "feature", feature, "channel", i)
				}
			} else {
				// Button is not pressed (released)
				// Check if we were tracking this button
				if startTime, exists := c.touchStartTime[feature]; exists {
					// Button was released
					delete(c.touchStartTime, feature)

					// Check if it was a long press
					pressDuration := time.Since(startTime)

					if feature == PowerButton && pressDuration >= LongPressDuration {
						// Power button - only trigger on long press
						slog.Info("long press detected on power button", "feature", feature, "duration", pressDuration)
						c.handleTouchPress(feature)
					} else if feature != PowerButton {
						// For non-power buttons, trigger on any press
						slog.Debug("button released", "feature", feature, "duration", pressDuration)
						c.handleTouchPress(feature)
					} else {
						// Power button short press - ignore
						slog.Debug("power button short press ignored", "feature", feature, "duration", pressDuration)
					}
				}
			}
		}

		// Clear any pending interrupt
		if err := c.dev.ClearInterrupt(); err != nil {
			// Ignore errors on clear - interrupt might already be cleared
			_ = err
		}

		// Sleep briefly to prevent tight loop (100ms polling interval)
		time.Sleep(100 * time.Millisecond)
	}
}

// handleTouchPress handles touch press events
// Note: Long press detection is handled in monitorTouchEvents
func (c *TouchPhatFeedback) handleTouchPress(feature TouchFeature) {
	switch feature {
	case PowerButton: // Back button - power off (long press only)
		c.handlePowerOff()
	case BackupButton: // A button - manual backup
		c.handleManualBackup()
	default:
		slog.Info("touch on unused feature", "feature", feature)
	}
}

// handlePowerOff initiates power off sequence
func (c *TouchPhatFeedback) handlePowerOff() {
	slog.Info("power off triggered")
	// Blink power LED to indicate shutdown
	c.BlinkPowerLED()
	c.touchChan <- TouchShutdown
}

// handleManualBackup triggers a manual backup
func (c *TouchPhatFeedback) handleManualBackup() {
	slog.Info("manual backup triggered")
	c.touchChan <- TouchManualBackup
	// Turn on backup LED
	c.SetLED(BackupLED, true)
}

// BlinkPowerLED blinks the power LED to indicate shutdown
func (c *TouchPhatFeedback) BlinkPowerLED() {
	go func() {
		for i := 0; i < 5; i++ {
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			c.SetLED(PowerLED, false)
			time.Sleep(200 * time.Millisecond)

			select {
			case <-c.ctx.Done():
				return
			default:
			}

			c.SetLED(PowerLED, true)
			time.Sleep(200 * time.Millisecond)
		}
	}()
}

// SetLED controls an LED on the CAP1166
// Uses the TouchFeature enum to ensure correct LED mapping
func (c *TouchPhatFeedback) SetLED(feature TouchFeature, state bool) {
	led := feature.led()

	if led < 0 || led >= 6 {
		return
	}

	c.ledStates[led] = state

	if err := c.dev.SetLED(led, state); err != nil {
		slog.Error("failed to set LED", "feature", feature, "led", led, "state", state, "error", err)
	}
}

// BlinkLED blinks an LED for a specified duration and number of times
func (c *TouchPhatFeedback) BlinkLED(feature TouchFeature, duration time.Duration, count int) {
	go func() {
		for range count {
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			c.SetLED(feature, true)
			time.Sleep(duration)

			select {
			case <-c.ctx.Done():
				return
			default:
			}

			c.SetLED(feature, false)
			time.Sleep(duration)
		}
	}()
}
