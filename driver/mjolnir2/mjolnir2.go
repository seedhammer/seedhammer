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
	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
)

// Speeds, in mm/s.
const (
	engraveSpeed = 500 / 60
	homingSpeed  = 900 / 60
	travelSpeed  = 1200 / 60
)

const NeedlePeriod = 20 * time.Millisecond

const (
	// The machine origin of the top-left plate corner.
	originX = (2 - 2.2) * stepsPerMM
	originY = (24 - 19) * stepsPerMM
	invertX = true
	invertY = false
	// The Y offset after completing an engraving.
	ejectY = 2 * stepsPerMM
)

// Sensorless homing parameters.
const (
	// stallThreshold is the TMC2209 SGTHRS for triggering a
	// stall.
	stallThreshold = 110
	// Maximum distance to travel before giving up homing.
	maxHomingMM = 250
)

var Params = engrave.Params{
	StrokeWidth: stepsPerMM * 3 / 10,
	Millimeter:  stepsPerMM,
}

type Device struct {
	Pio          *rp.PIO0_Type
	XAxis, YAxis *tmc2209.Device
	EnablePin    machine.Pin
	BasePin      machine.Pin
	XDiag        machine.Pin
	YDiag        machine.Pin

	xnotify, ynotify chan struct{}
}

const (
	// Pin offsets from base pin.
	pinDirY = iota
	pinDirX
	pinNeedle
	pinStepY
	pinStepX
	pinCount
)

const pioSM = 0

func (d *Device) Configure() error {
	if d.xnotify == nil {
		d.xnotify = make(chan struct{}, 1)
	}
	if d.ynotify == nil {
		d.ynotify = make(chan struct{}, 1)
	}
	progOff := uint8(0)
	conf := mjolnir2ProgramDefaultConfig(progOff)
	conf.SidesetBase = pinStepY + d.BasePin
	conf.OutBase = pinDirY + d.BasePin
	conf.OutCount = pinCount
	conf.FIFOMode = pio.FIFOJoinTX
	conf.PullThreshold = pinCount + delayBits
	conf.Autopull = true
	const (
		microstepsPerSecond = topSpeed * stepsPerMM * tmc2209.Microsteps
		pioFreq             = uint32(microstepsPerSecond * pioCyclesPerStep)
	)
	conf.Freq = pioFreq
	pio.Configure(d.Pio, pioSM, conf.Build())
	pio.Program(d.Pio, progOff, mjolnir2Instructions)

	// Set up pins.
	d.EnablePin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	d.EnablePin.Set(true)
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

func (d *Device) Close() {
}

func (d *Device) stallThreshold(threshold uint8) error {
	if err := d.XAxis.StallThreshold(threshold); err != nil {
		return err
	}
	return d.YAxis.StallThreshold(threshold)
}

func (d *Device) Engrave(plan engrave.Plan, quit <-chan struct{}) error {
	d.EnablePin.Set(false)
	defer d.EnablePin.Set(true)
	if err := d.home(quit); err != nil {
		return err
	}
	plan = engrave.Offset(originX, originY, plan)
	if err := d.engrave(engraveSpeed, quit, false, plan); err != nil {
		return err
	}
	// Return to "eject" position.
	return d.engrave(travelSpeed, quit, false, func(yield func(engrave.Command) bool) {
		yield(engrave.Move(image.Pt(0, ejectY)))
	})
}

func (d *Device) home(quit <-chan struct{}) error {
	{
		d.engrave(homingSpeed, quit, false, func(yield func(engrave.Command) bool) {
			const dist = 10 * stepsPerMM
			home := image.Pt(dist, dist)
			yield(engrave.Move(home))
		})
	}
	if err := d.stallThreshold(stallThreshold); err != nil {
		return err
	}
	engErr := d.engrave(homingSpeed, quit, true, func(yield func(engrave.Command) bool) {
		const dist = maxHomingMM * stepsPerMM
		home := image.Pt(-dist, -dist)
		yield(engrave.Move(home))
	})
	if err := d.stallThreshold(0); err != nil {
		return err
	}
	return engErr
}

func (d *Device) engrave(speed int, quit <-chan struct{}, homing bool, plan engrave.Plan) error {
	pio.ConfigurePins(d.Pio, pioSM, d.BasePin, pinCount)
	pio.Pindirs(d.Pio, pioSM, d.BasePin, pinCount, machine.PinOutput)
	// Reset and start state machine.
	pio.Restart(d.Pio, 0b1<<pioSM)
	pio.Enable(d.Pio, 0b1<<pioSM)
	defer pio.Disable(d.Pio, 0b1<<pioSM)

	// Scale plan coordinates from steps to microsteps.
	plan = engrave.Scale(tmc2209.Microsteps, plan)
	txReg := pio.Tx(d.Pio, pioSM)
	xdiag, ydiag := false, false
	// Clear notifications.
	select {
	case <-d.xnotify:
	default:
	}
	select {
	case <-d.ynotify:
	default:
	}
	for step := range engravePlan(plan) {
		select {
		case <-quit:
			return nil
		case <-d.xnotify:
			if err := d.XAxis.Error(); err != nil {
				return err
			}
			if !homing {
				return errors.New("mjolnir2: x-axis stepper driver failed")
			}
			xdiag = true
		case <-d.ynotify:
			if err := d.YAxis.Error(); err != nil {
				return err
			}
			if !homing {
				return errors.New("mjolnir2: y-axis stepper driver failed")
			}
			ydiag = true
		default:
		}
		if homing && ydiag && xdiag {
			return nil
		}
		dirx := step.DirX
		if !invertX {
			dirx = 1 - dirx
		}
		diry := step.DirY
		if !invertY {
			diry = 1 - diry
		}
		pins := uint32(
			dirx<<pinDirX |
				diry<<pinDirY |
				0b0<<pinNeedle |
				step.StepX<<pinStepX |
				step.StepY<<pinStepY,
		)

		delay := pioCyclesPerStep*topSpeed/speed - pioCyclesPerStep
		// delay := max(0, pioCyclesPerStep*(topSpeed+step.Speed-1)/step.Speed-pioCyclesPerStep)
		cmd := pins<<delayBits | uint32(delay)
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
