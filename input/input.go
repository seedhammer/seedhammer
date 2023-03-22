// package input implements an input driver for the joystick and buttons on
// the Waveshare 1.3" 240x240 HAT.
package input

import (
	"fmt"
	"time"

	"periph.io/x/conn/v3/gpio"
	"periph.io/x/host/v3"
	"periph.io/x/host/v3/bcm283x"
)

type Event struct {
	Button  Button
	Pressed bool
	// Rune is only valid if Button is Rune.
	Rune rune
}

type Button int

const (
	Up Button = iota
	Down
	Left
	Right
	Center
	Button1
	Button2
	Button3
	// Synthetic keys only generated in debug mode.
	Rune       // Enter rune.
	Screenshot // Dump a screenshot to the SD card.
)

func Open(ch chan<- Event) error {
	if _, err := host.Init(); err != nil {
		return err
	}
	buttons := []struct {
		Button Button
		Pin    gpio.PinIn
	}{
		{Up, bcm283x.GPIO6},
		{Down, bcm283x.GPIO19},
		{Left, bcm283x.GPIO5},
		{Right, bcm283x.GPIO26},
		{Center, bcm283x.GPIO13},
		{Button1, bcm283x.GPIO21},
		{Button2, bcm283x.GPIO20},
		{Button3, bcm283x.GPIO16},
	}
	for _, btn := range buttons {
		if err := btn.Pin.In(gpio.PullUp, gpio.BothEdges); err != nil {
			return fmt.Errorf("setupButtons: %w", err)
		}
		btn := btn
		go func() {
			pressed := false
			newPressed := false
			const debounceTimeout = 10 * time.Millisecond
			for {
				// Wait forever for event, except if we're waiting for
				// the debounce timeout.
				timeout := debounceTimeout
				if newPressed == pressed {
					timeout = -1
				}
				if btn.Pin.WaitForEdge(timeout) {
					newPressed = btn.Pin.Read() == gpio.Low
				} else {
					// Debounce timeout; ok to send event.
					if newPressed != pressed {
						pressed = newPressed
						ch <- Event{Button: btn.Button, Pressed: pressed}
					}
				}
			}
		}()
	}
	return nil
}
