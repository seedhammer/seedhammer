// package input implements an input driver for the joystick and buttons on
// the Waveshare 1.3" 240x240 HAT.
package wshat

import (
	"fmt"
	"time"

	"periph.io/x/conn/v3/gpio"
	"periph.io/x/host/v3"
	"periph.io/x/host/v3/bcm283x"
	"seedhammer.com/gui"
)

func Open(ch chan<- gui.ButtonEvent) error {
	if _, err := host.Init(); err != nil {
		return err
	}
	buttons := []struct {
		Button gui.Button
		Pin    gpio.PinIn
	}{
		{gui.Up, bcm283x.GPIO6},
		{gui.Down, bcm283x.GPIO19},
		{gui.Left, bcm283x.GPIO5},
		{gui.Right, bcm283x.GPIO26},
		{gui.Center, bcm283x.GPIO13},
		{gui.Button1, bcm283x.GPIO21},
		{gui.Button2, bcm283x.GPIO20},
		{gui.Button3, bcm283x.GPIO16},
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
						ch <- gui.ButtonEvent{Button: btn.Button, Pressed: pressed}
					}
				}
			}
		}()
	}
	return nil
}
