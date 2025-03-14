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
	accelDelays      []uint16
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
)

func (d *Device) Configure() error {
	d.accelDelays = newAccelCurve(d.TopSpeed, d.Acceleration)

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
	conf.PullThreshold = mjolnir2delayBits + mjolnir2pinBits
	conf.Autopull = true
	conf.Freq = uint32(d.TopSpeed) * pioCyclesPerStep
	pio.Configure(d.Pio, pioSM, conf.Build())
	pio.Program(d.Pio, progOffset, mjolnir2Instructions)

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
	xdiag, ydiag := false, false
	accelLen := max(1, stepsForSpeed(moveSpeed, d.Acceleration))
	engraveAccelLen := max(1, stepsForSpeed(d.EngravingSpeed, d.Acceleration))
	pioFreq := d.TopSpeed * pioCyclesPerStep
	needlePeriod := int(d.NeedlePeriod * time.Duration(pioFreq) / time.Second)
	needleActivation := int(d.NeedleActivation * time.Duration(pioFreq) / time.Second)
	eng := &engraver{
		MoveDelays:       d.accelDelays[:accelLen],
		EngraveDelays:    d.accelDelays[:engraveAccelLen],
		NeedlePeriod:     needlePeriod,
		NeedleActivation: needleActivation,
	}
	for step := range eng.Engrave(plan) {
		select {
		case <-quit:
			return nil
		case <-d.xnotify:
			if !homing {
				return errors.New("mjolnir2: x-axis stepper driver failed")
			}
			xdiag = true
		case <-d.ynotify:
			if !homing {
				return errors.New("mjolnir2: y-axis stepper driver failed")
			}
			ydiag = true
		default:
		}
		if homing && ydiag && xdiag {
			return nil
		}
		pins := uint32(
			step.DirX<<pinDirX |
				step.DirY<<pinDirY |
				step.Needle<<pinNeedle |
				step.StepX<<pinStepX |
				step.StepY<<pinStepY,
		)

		cmd := pins<<mjolnir2delayBits | uint32(step.Delay)
		// Wait for FIFO.
		for d.Pio.GetFSTAT_TXFULL()&(0b1<<pioSM) != 0 {
		}
		txReg.Set(cmd)
	}
	if homing {
		return errors.New("mjolnir2: homing timed out")
	}
	return nil
}
