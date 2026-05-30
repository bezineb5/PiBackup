package feedback

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/devices/v3/cap1xxx"
	"periph.io/x/host/v3"
)

// CAP1166 assignments - paired by feature (channel N controls LED N)
const (
	ManualBackup = 0 // Channel 0, LED 0
	GPSSync      = 1 // Channel 1, LED 1
	RSYNC        = 2 // Channel 2, LED 2
	Shutdown     = 3 // Channel 3, LED 3
	LEDError     = 4 // Error indicator
	LEDPower     = 5 // Power indicator (always on when ready)
)

// CAP1166Feedback implements HardwareController using CAP1166 capacitive touch sensor
type CAP1166Feedback struct {
	dev       *cap1xxx.Dev
	alertPin  gpio.PinIn
	resetPin  gpio.PinOut
	ledStates map[int]bool
	ctx       context.Context
	cancel    context.CancelFunc
	touchChan chan TouchEventType
}

// NewCAP1166Feedback creates a new CAP1166 feedback implementation
func NewCAP1166Feedback() (*CAP1166Feedback, error) {
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
	// This is needed because we use LEDs 4 (Error) and 5 (Power) as standalone indicators
	opts.LinkedLEDs = false

	// Create CAP1166 device
	dev, err := cap1xxx.NewI2C(i2cBus, &opts)
	if err != nil {
		return nil, fmt.Errorf("couldn't open cap1166: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	feedback := &CAP1166Feedback{
		dev:       dev,
		alertPin:  alertPin,
		resetPin:  resetPin,
		ledStates: make(map[int]bool),
		ctx:       ctx,
		cancel:    cancel,
		touchChan: make(chan TouchEventType, 10),
	}

	// Start monitoring touch events
	go feedback.monitorTouchEvents()

	slog.Info("CAP1166 feedback initialized successfully")
	// Turn on power LED to indicate ready state
	feedback.setLED(LEDPower, true)
	return feedback, nil
}

// Notify handles user feedback events and controls LEDs
func (c *CAP1166Feedback) Notify(event Event) {
	switch event.Type {
	case EventStatus:
		// Blink ManualBackup LED for status updates
		c.blinkLED(ManualBackup, 200*time.Millisecond, 2)

	case EventProgress:
		// Show progress on GPSSync LED
		brightness := min(int((event.Progress/100.0)*255), 255)
		c.setLEDBrightness(GPSSync, brightness)

	case EventSuccess:
		// Solid green on RSYNC LED for success
		c.setLED(RSYNC, true)
		time.AfterFunc(3*time.Second, func() {
			c.setLED(RSYNC, false)
		})

	case EventError:
		// Blink red on Error LED
		c.blinkLED(LEDError, 100*time.Millisecond, 5)

	case EventWarning:
		// Blink yellow on Shutdown LED for warnings
		c.blinkLED(Shutdown, 300*time.Millisecond, 3)
	}
}

// TouchEvents returns the channel for touch input events
func (c *CAP1166Feedback) TouchEvents() <-chan TouchEventType {
	return c.touchChan
}

// Halt cleans up the CAP1166 feedback
func (c *CAP1166Feedback) Halt() error {
	c.Shutdown()
	return nil
}

// Shutdown cleans up the CAP1166 feedback
func (c *CAP1166Feedback) Shutdown() {
	slog.Info("shutting down CAP1166 feedback")

	// Turn off all LEDs
	for i := range 6 {
		c.setLED(i, false)
	}

	// Blink ManualBackup LED for 2 seconds
	c.blinkLED(ManualBackup, 2*time.Second, 3)
	time.Sleep(2 * time.Second)

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
func (c *CAP1166Feedback) monitorTouchEvents() {
	slog.Info("monitoring for touch events")

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		// Wait for edge detection on alert pin
		if c.alertPin.WaitForEdge(-1) {
			var statuses [6]cap1xxx.TouchStatus // CAP1166 has 6 channels
			if err := c.dev.InputStatus(statuses[:]); err != nil {
				slog.Error("error reading inputs", "error", err)
				continue
			}

			// Check each sensor for touch events
			for i, status := range statuses {
				if status == cap1xxx.PressedStatus {
					c.handleTouchPress(i)
				}
			}

			// Clear the interrupt so it can be triggered again
			if err := c.dev.ClearInterrupt(); err != nil {
				slog.Error("error clearing interrupt", "error", err)
			}
		}
	}
}

// handleTouchPress handles touch press events
func (c *CAP1166Feedback) handleTouchPress(channel int) {
	slog.Info("touch detected", "channel", channel)

	switch channel {
	case ManualBackup: // Manual backup button
		c.handleManualBackup()
	case Shutdown: // Shutdown button
		c.handleShutdown()
	}
}

// handleManualBackup triggers a manual backup
func (c *CAP1166Feedback) handleManualBackup() {
	slog.Info("manual backup triggered")
	c.touchChan <- TouchManualBackup
}

// handleShutdown initiates shutdown sequence
func (c *CAP1166Feedback) handleShutdown() {
	slog.Info("shutdown triggered")
	c.touchChan <- TouchShutdown
}

// setLED controls an LED
func (c *CAP1166Feedback) setLED(led int, state bool) {
	if led < 0 || led >= 6 {
		return
	}

	c.ledStates[led] = state

	if err := c.dev.SetLED(led, state); err != nil {
		slog.Error("failed to set LED", "led", led, "state", state, "error", err)
	}
}

// setLEDBrightness sets LED brightness (0-255)
// Note: CAP1166 doesn't support brightness control, only on/off
func (c *CAP1166Feedback) setLEDBrightness(led int, brightness int) {
	if led < 0 || led >= 6 {
		return
	}

	// CAP1166 only supports on/off, so we use brightness > 0 as on
	state := brightness > 0
	c.setLED(led, state)
}

// blinkLED blinks an LED for a specified duration and number of times
func (c *CAP1166Feedback) blinkLED(led int, duration time.Duration, count int) {
	go func() {
		for range count {
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			c.setLED(led, true)
			time.Sleep(duration)

			select {
			case <-c.ctx.Done():
				return
			default:
			}

			c.setLED(led, false)
			time.Sleep(duration)
		}
	}()
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
