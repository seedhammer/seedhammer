//go:build tinygo

// package mjolnir2 implements a driver for the particular
// engraving hardware in the Seedhammer II.
package mjolnir2

import (
	"device/rp"
	"errors"
	"image"
	"machine"
	"time"

	"seedhammer.com/driver/pio"
	"seedhammer.com/engrave"
)

type Device struct {
	Pio     *rp.PIO0_Type
	BasePin machine.Pin
	XDiag   machine.Pin
	YDiag   machine.Pin

	// Home is the homing vector, whose direction
	// specifies the direction of the origin,
	// and length specifies the distance before
	// giving up.
	Home image.Point
	// TopSpeed in steps/s.
	TopSpeed int
	// EngravingSpeed in steps/s.
	EngravingSpeed int
	// HomingSpeed in steps/s.
	HomingSpeed int
	// Acceleration in steps/s².
	Acceleration     int
	NeedlePeriod     time.Duration
	NeedleActivation time.Duration

	xnotify, ynotify chan struct{}
}

const (
	// Pin offsets from base pin.
	pinDirY = iota
	pinDirX
	pinNeedle
	pinStepY
	pinStepX
)

const (
	pioSM      = 0
	progOffset = 0

	// pioStepsPerWord is the number of pio steps that
	// fit into a 32-bit pio FIFO entry.
	pioStepsPerWord = 32 / mjolnir2pinBits

	// No-op is the pio step that clears every pin
	// and stops the needle.
	noop = 0b00000
)

func (d *Device) Configure() error {
	if d.xnotify == nil {
		d.xnotify = make(chan struct{}, 1)
	}
	if d.ynotify == nil {
		d.ynotify = make(chan struct{}, 1)
	}
	conf := mjolnir2ProgramDefaultConfig(progOffset)
	conf.SidesetBase = uint8(pinStepY + d.BasePin)
	conf.OutBase = uint8(pinDirY + d.BasePin)
	conf.OutCount = mjolnir2pinBits
	conf.FIFOMode = pio.FIFOJoinTX
	conf.PullThreshold = mjolnir2pinBits * pioStepsPerWord
	conf.Autopull = true
	conf.Freq = uint32(d.TopSpeed) * pioCyclesPerStep
	pio.Configure(d.Pio, pioSM, conf.Build())
	pio.Program(d.Pio, progOffset, mjolnir2Instructions)
	// Register x must be cleared for stall to disable all pins.
	pio.Instr(d.Pio, pioSM).Set(clearXInst)

	d.XDiag.Configure(machine.PinConfig{Mode: machine.PinInput})
	d.XDiag.SetInterrupt(machine.PinRising, d.diagInterrupt)
	d.YDiag.Configure(machine.PinConfig{Mode: machine.PinInput})
	d.YDiag.SetInterrupt(machine.PinRising, d.diagInterrupt)
	return nil
}

func (d *Device) diagInterrupt(pin machine.Pin) {
	var stepPin machine.Pin
	var notify chan struct{}
	switch pin {
	case d.XDiag:
		stepPin = pinStepX + d.BasePin
		notify = d.xnotify
	case d.YDiag:
		stepPin = pinStepY + d.BasePin
		notify = d.ynotify
	default:
		return
	}
	// Disconnect the step pin from the PIO program and set it low.
	stepPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	stepPin.Low()
	select {
	case notify <- struct{}{}:
	default:
	}
}

func (d *Device) Engrave(plan engrave.Plan, quit <-chan struct{}) error {
	if err := d.engrave(d.HomingSpeed, quit, true, func(yield func(engrave.Command) bool) {
		yield(engrave.Move(d.Home))
	}); err != nil {
		return err
	}

	return d.engrave(d.TopSpeed, quit, false, plan)
}

func (d *Device) engrave(moveSpeed int, quit <-chan struct{}, homing bool, plan engrave.Plan) error {
	// Clear notifications.
	select {
	case <-d.xnotify:
	default:
	}
	select {
	case <-d.ynotify:
	default:
	}
	pio.ConfigurePins(d.Pio, pioSM, d.BasePin, mjolnir2pinBits)
	pio.Pindirs(d.Pio, pioSM, d.BasePin, mjolnir2pinBits, machine.PinOutput)
	// Reset and start state machine.
	pio.Restart(d.Pio, 0b1<<pioSM)
	pio.Jump(d.Pio, pioSM, progOffset)
	pio.Enable(d.Pio, 0b1<<pioSM)
	defer pio.Disable(d.Pio, 0b1<<pioSM)
	txReg := pio.Tx(d.Pio, pioSM)
	defer func() {
		// Wait for all steps to complete. We can't wait for
		// TX FIFO stalling, because the pio program doesn't stall.
		// Instead, submit a no-op step and wait for empty FIFO.

		pio.WaitTxNotFull(d.Pio, 0b1<<pioSM)
		txReg.Set(noop)
		pio.WaitTxEmpty(d.Pio, 0b1<<pioSM)
	}()

	xdiag, ydiag := false, false
	eng := &engravingConfig{
		Speed:            moveSpeed,
		EngravingSpeed:   d.EngravingSpeed,
		Acceleration:     d.Acceleration,
		TicksPerSecond:   d.TopSpeed,
		NeedlePeriod:     d.NeedlePeriod,
		NeedleActivation: d.NeedleActivation,
	}
	// // buf of pio words waiting to be submitted.
	// buf := make([]uint32, 4*1024)
	// // cmdIdx indexes pio steps in buf. Note that there
	// // are [pioCmdsPerWord]*len(buf) such indices.
	// cmdIdx := 0
	// for cmd := range plan {
	// 	select {
	// 	case <-quit:
	// 		return nil
	// 	case <-d.xnotify:
	// 		if !homing {
	// 			return errors.New("mjolnir2: x-axis stepper driver failed")
	// 		} else if ydiag {
	// 			return nil
	// 		}
	// 		xdiag = true
	// 	case <-d.ynotify:
	// 		if !homing {
	// 			return errors.New("mjolnir2: y-axis stepper driver failed")
	// 		} else if xdiag {
	// 			return nil
	// 		}
	// 		ydiag = true
	// 	default:
	// 	}
	// }
	for step := range eng.Engrave(plan) {
		select {
		case <-quit:
			return nil
		case <-d.xnotify:
			if !homing {
				return errors.New("mjolnir2: x-axis stepper driver failed")
			} else if ydiag {
				return nil
			}
			xdiag = true
		case <-d.ynotify:
			if !homing {
				return errors.New("mjolnir2: y-axis stepper driver failed")
			} else if xdiag {
				return nil
			}
			ydiag = true
		default:
		}
		pins := uint32(
			step.DirX<<pinDirX |
				step.DirY<<pinDirY |
				step.Needle<<pinNeedle |
				step.StepX<<pinStepX |
				step.StepY<<pinStepY,
		)

		// Wait for FIFO.
		pio.WaitTxNotFull(d.Pio, 0b1<<pioSM)
		txReg.Set(pins)
	}
	if homing {
		return errors.New("mjolnir2: homing timed out")
	}
	return nil
}
