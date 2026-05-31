// Package feedback provides user feedback interfaces and implementations.
//
// This file implements support for the Pimoroni TouchPhat HAT.
// TouchPhat is a capacitive touch add-on board for Raspberry Pi.
// More info: https://shop.pimoroni.com/products/touch-phat
package feedback

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/benjamin/pibackup/pibackup-go/drivers/cap1xxx"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/host/v3"
)

// TouchFeature represents a physical button/LED on the Pimoroni TouchPhat
// Buttons are arranged in 2 rows of 3:
// Top row: Back (0), A (1), B (2)
// Bottom row: C (3), D (4), Enter (5)
type TouchFeature int

const (
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

type FeedbackFeature int

const (
	Power FeedbackFeature = iota
	Sync
	Warning
)

func (f FeedbackFeature) String() string {
	switch f {
	case Power:
		return "Power"
	case Sync:
		return "Sync"
	case Warning:
		return "Warning"
	default:
		return "Unknown"
	}
}

var feedBackFeatureToTouch = map[FeedbackFeature]TouchFeature{
	Power:   Back,
	Sync:    A,
	Warning: Enter,
}

// buttonMapping: map[button_id]TouchFeature
// Maps physical channel number to TouchFeature
var buttonMapping = map[int]TouchFeature{
	5: Back,
	4: A,
	3: B,
	2: C,
	1: D,
	0: Enter,
}

// ledMapping: map[TouchFeature]int
// Maps TouchFeature to LED channel number
// TouchPhat has reversed LED mapping: Back→5, A→4, B→3, C→2, D→1, Enter→0
var ledMapping = map[TouchFeature]int{
	Back:  5,
	A:     4,
	B:     3,
	C:     2,
	D:     1,
	Enter: 0,
}

// Long press duration
const LongPressDuration = 1 * time.Second

// TouchPhatFeedback implements HardwareController for Pimoroni TouchPhat.
type TouchPhatFeedback struct {
	dev            *cap1xxx.Dev
	ledStates      map[int]bool
	ctx            context.Context
	cancel         context.CancelFunc
	touchChan      chan TouchEventType
	touchStartTime map[int]time.Time
}

// NewTouchPhatFeedback creates a new TouchPhat feedback implementation.
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

	// Configure CAP1166
	opts := cap1xxx.DefaultOpts
	opts.I2CAddr = 0x2C
	opts.LinkedLEDs = false

	dev, err := cap1xxx.NewI2C(i2cBus, &opts)
	if err != nil {
		return nil, fmt.Errorf("couldn't open cap1166: %w", err)
	}

	if err := dev.LinkLEDs(false); err != nil {
		dev.Halt()
		return nil, fmt.Errorf("failed to unlink LEDs: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	fb := &TouchPhatFeedback{
		dev:            dev,
		ledStates:      make(map[int]bool),
		ctx:            ctx,
		cancel:         cancel,
		touchChan:      make(chan TouchEventType, 10),
		touchStartTime: make(map[int]time.Time),
	}

	go fb.monitorTouchEvents()

	slog.Info("TouchPhat initialized")

	// Turn on power LED (Back button's LED)
	fb.SetLED(Power, true)
	return fb, nil
}

// Notify handles user feedback events
func (fb *TouchPhatFeedback) Notify(event Event) {
	switch event.Type {
	case EventStatus:
		// For generic status updates, briefly pulse the Sync LED
		fb.BlinkLED(Sync, 50*time.Millisecond, 1)
	case EventProgress:
		fb.SetLED(Sync, true)
	case EventSuccess:
		fb.SetLED(Sync, false)
		// Flash Sync LED to indicate success
		fb.BlinkLED(Sync, 100*time.Millisecond, 3)
	case EventError:
		fb.SetLED(Sync, false)
		fb.BlinkLED(Warning, 100*time.Millisecond, 5)
	case EventWarning:
		fb.SetLED(Sync, false)
		fb.BlinkLED(Warning, 300*time.Millisecond, 3)
	}
}

// TouchEvents returns the channel for touch input events
func (fb *TouchPhatFeedback) TouchEvents() <-chan TouchEventType {
	return fb.touchChan
}

// Halt cleans up
func (fb *TouchPhatFeedback) Halt() error {
	fb.Shutdown()
	return nil
}

// Shutdown cleans up
func (fb *TouchPhatFeedback) Shutdown() {
	slog.Info("Shutting down TouchPhat")
	fb.BlinkLED(Power, 100*time.Millisecond, 5)
	time.Sleep(1 * time.Second)
	close(fb.touchChan)
	fb.cancel()
	for _, led := range ledMapping {
		fb.dev.SetLED(led, false)
	}
	fb.dev.Halt()
	slog.Info("TouchPhat shutdown complete")
}

// monitorTouchEvents monitors for touch events
func (fb *TouchPhatFeedback) monitorTouchEvents() {
	slog.Info("Monitoring for touch events")

	for {
		select {
		case <-fb.ctx.Done():
			return
		default:
		}

		var statuses [6]cap1xxx.TouchStatus
		if err := fb.dev.InputStatus(statuses[:]); err != nil {
			slog.Error("Error reading inputs", "error", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		for ch, status := range statuses {
			if status == cap1xxx.PressedStatus {
				if _, exists := fb.touchStartTime[ch]; !exists {
					fb.touchStartTime[ch] = time.Now()
					slog.Debug("Button pressed", "channel", ch, "button", buttonMapping[ch])
				}
			} else {
				if startTime, exists := fb.touchStartTime[ch]; exists {
					delete(fb.touchStartTime, ch)
					pressDuration := time.Since(startTime)

					// Map back to a feature
					feedbackFeature, err := touchChanneltoFeature(ch)
					if err != nil {
						slog.Debug("No feedback feature", "error", err)
						continue
					}

					// Channel 0 (Back) = power, requires long press
					switch feedbackFeature {
					case Power:
						if pressDuration >= LongPressDuration {
							slog.Info("Long press on power button")
							fb.touchChan <- TouchShutdown
						}
					case Sync:
						slog.Info("Backup button pressed")
						fb.touchChan <- TouchManualBackup
					}
				}
			}
		}

		fb.dev.ClearInterrupt()
		time.Sleep(100 * time.Millisecond)
	}
}

func (fb *TouchPhatFeedback) SetLED(feedbackFeature FeedbackFeature, state bool) {
	touchFeature := feedBackFeatureToTouch[feedbackFeature]
	fb.setFeatureLED(touchFeature, state)
}

// SetLED sets an LED by TouchFeature
func (fb *TouchPhatFeedback) setFeatureLED(feature TouchFeature, state bool) {
	led := ledMapping[feature]
	fb.ledStates[led] = state
	fb.dev.SetLED(led, state)
}

// BlinkLED blinks an LED
func (fb *TouchPhatFeedback) BlinkLED(feedbackFeature FeedbackFeature, duration time.Duration, count int) {
	go func() {
		for range count {
			select {
			case <-fb.ctx.Done():
				return
			default:
			}
			fb.SetLED(feedbackFeature, true)
			time.Sleep(duration)
			fb.SetLED(feedbackFeature, false)
			time.Sleep(duration)
		}
	}()
}

func touchChanneltoFeature(touchChannel int) (FeedbackFeature, error) {
	selectedTouchFeature := buttonMapping[touchChannel]
	for feedbackFeature, touchFeature := range feedBackFeatureToTouch {
		if touchFeature == selectedTouchFeature {
			return feedbackFeature, nil
		}
	}

	return Power, fmt.Errorf("No feedback feature assigned to %d", touchChannel)
}
