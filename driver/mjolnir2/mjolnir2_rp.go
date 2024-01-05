//go:build tinygo && rp

// package mjolnir2 implements a driver for the particular hardware
// in the Seedhammer v2.
package mjolnir2

import (
	"errors"
	"image"
	"machine"
	"time"

	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
)

// Speeds, in mm/min.
const (
	homingSpeed  = 900
	engraveSpeed = 500
	travelSpeed  = 1200
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
	stallThreshold = 220
	// Maximum distance to travel before giving up homing.
	maxHomingMM = 250
)

// stepsPerMM in fullsteps.
const stepsPerMM = 200 / 8

var Params = engrave.Params{
	StrokeWidth: stepsPerMM * 3 / 10,
	Millimeter:  stepsPerMM,
}

type Device struct {
	enable       machine.Pin
	xaxis, yaxis *tmc2209.Device
	needle       struct {
		enable func(bool)
	}

	// Engraving state.
	delay        int
	quit         <-chan struct{}
	commands     <-chan command
	pen          image.Point
	line         bresenham
	phase        bool
	homing       bool
	xdiag, ydiag bool
}

type command struct {
	Delay      int
	Line       bresenham
	DirX, DirY bool
	Engrave    bool
}

func New(enable machine.Pin, X, Y *tmc2209.Device, needle func(enable bool)) (*Device, error) {
	enable.Configure(machine.PinConfig{Mode: machine.PinOutput})
	enable.Set(true)
	d := &Device{
		enable: enable,
		xaxis:  X, yaxis: Y,
	}
	d.needle.enable = needle
	return d, nil
}

func (d *Device) Close() {
}

func (d *Device) stallThreshold(threshold uint8) error {
	if err := d.xaxis.StallThreshold(threshold); err != nil {
		return err
	}
	return d.yaxis.StallThreshold(threshold)
}

func (d *Device) Engrave(plan engrave.Plan, quit <-chan struct{}) error {
	d.enable.Set(false)
	defer d.enable.Set(true)
	d.quit = quit
	d.xaxis.Reset()
	d.yaxis.Reset()
	if err := d.home(); err != nil {
		return err
	}
	plan = engrave.Offset(originX, originY, plan)
	if err := d.engrave(engraveSpeed, plan); err != nil {
		return err
	}
	// Return to "eject" position.
	return d.engrave(travelSpeed, func(yield func(engrave.Command) bool) {
		yield(engrave.Move(image.Pt(d.pen.X, ejectY)))
	})
}

func (d *Device) home() error {
	d.pen = image.Point{}
	d.homing = true
	if err := d.stallThreshold(stallThreshold); err != nil {
		return err
	}
	err := d.engrave(homingSpeed, func(yield func(engrave.Command) bool) {
		dist := maxHomingMM * stepsPerMM
		home := image.Pt(-dist, -dist)
		yield(engrave.Move(home))
	})
	d.homing = false
	if err != nil {
		return err
	}
	if err := d.stallThreshold(0); err != nil {
		return err
	}
	if !d.xdiag || !d.ydiag {
		select {
		case <-d.quit:
		default:
			return errors.New("mjolnir2: homing timed out")
		}
	}
	d.pen = image.Point{}
	return nil
}

func (d *Device) engrave(speed int, plan engrave.Plan) error {
	d.delay = 0
	d.line = bresenham{}
	d.phase = false
	d.xdiag = false
	d.ydiag = false
	const buffer = 50
	commands := make(chan command, buffer)
	d.commands = commands
	microstepsPerMinute := speed * stepsPerMM * tmc2209.Microsteps
	period := time.Minute / time.Duration(microstepsPerMinute)
	moveDelay := int(NeedlePeriod / period)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(commands)
		needleOn := false
	loop:
		for cmd := range plan {
			c := command{
				Engrave: cmd.Line,
			}
			dist := cmd.Coord.Sub(d.pen)
			d.pen = cmd.Coord
			c.DirX, c.DirY = c.Line.Reset(dist.Mul(tmc2209.Microsteps))
			c.DirX = c.DirX != invertX
			c.DirY = c.DirY != invertY
			if needleOn != c.Engrave {
				needleOn = c.Engrave
				// Delay movement until needle has completed its cycle.
				c.Delay += moveDelay
			}
			select {
			case <-done:
				break loop
			case commands <- c:
			}
		}
	}()
	for {
		if d.tick() {
			break
		}
		// Callback twice per microstep.
		time.Sleep(period / 2)
	}
	if d.xdiag {
		if err := d.xaxis.Error(); err != nil {
			return err
		}
		if !d.homing {
			return errors.New("mjolnir2: x-axis stepper driver failed")
		}
	}
	if d.ydiag {
		if err := d.yaxis.Error(); err != nil {
			return err
		}
		if !d.homing {
			return errors.New("mjolnir2: y-axis stepper driver failed")
		}
	}
	return nil
}

// tick drives the engraving hardware. It is called twice for each (micro-)step.
func (d *Device) tick() bool {
	if !d.phase {
		// The first phase sets up the next step and drives the (low frequency) needle. It
		// is not timing sensitive as long as it completes before the next callback.
		d.xaxis.Step(false)
		d.yaxis.Step(false)
		select {
		case <-d.quit:
			return true
		default:
		}
		d.xdiag = d.xdiag || d.xaxis.Diag()
		d.ydiag = d.ydiag || d.yaxis.Diag()
		// Stop when any axis report a fault, except when homing where
		// the fault indicators must both indicate stalls.
		if ((d.xdiag || d.ydiag) && !d.homing) || d.xdiag && d.ydiag {
			return true
		}
	loop:
		for d.line.Done() && d.delay == 0 {
			select {
			case cmd, ok := <-d.commands:
				d.line = cmd.Line
				d.xaxis.Dir(!cmd.DirX)
				d.yaxis.Dir(!cmd.DirY)
				d.needle.enable(cmd.Engrave)
				d.delay += cmd.Delay
				if !ok && d.delay == 0 {
					return true
				}
			default:
				// Buffer under-run; disable needle.
				d.needle.enable(false)
				break loop
			}
		}
		if d.delay > 0 {
			d.delay--
		}
	} else {
		// The second callback only sets the step pins. The short code
		// reduces jitter.
		x, y := false, false
		if d.delay == 0 && !d.line.Done() {
			x, y = d.line.Step()
		}
		d.xaxis.Step(x && !d.xdiag)
		d.yaxis.Step(y && !d.ydiag)
	}
	d.phase = !d.phase
	return false
}
