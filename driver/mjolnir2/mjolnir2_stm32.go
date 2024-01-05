//go:build tinygo && stm32

// package mjolnir2 implements a driver for the particular hardware
// in the Seedhammer v2.
package mjolnir2

import (
	"errors"
	"fmt"
	"image"
	"time"

	"machine"

	"seedhammer.com/driver/bts7960"
	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
)

// Speeds, in mm/min.
const (
	homingSpeed  = 900
	engraveSpeed = 500
	travelSpeed  = 1200
)

const (
	// The machine origin of the top-left plate corner.
	originX = 2 * stepsPerMM
	originY = 24 * stepsPerMM
	// The Y offset after completing an engraving.
	ejectY = 2 * stepsPerMM
)

const (
	// needleActivation is the duration of the stronger, initial pulse
	// for a cycle.
	needleActivation = 5 * time.Millisecond
	// needleCoast is the duration of the weaker pulse following the
	// activation.
	needleCoast = 1 * time.Millisecond
	// needleBounce is the delay after the coast pulse for allowing
	// the needle to retract.
	needleBounce = needleActivation + needleCoast
)

// Sensorless homing parameters.
const (
	// stallThreshold is the TMC2209 SGTHRS for triggering a
	// stall.
	stallThreshold = 120
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
	xaxis, yaxis *tmc2209.Device
	needle       struct {
		dev     *bts7960.Device
		on      bool
		counter int
		// tick counts for the needle timings.
		activation int
		coast      int
		bounce     int
	}
	timer *machine.TIM

	// Engraving state.
	delay        int
	quit         <-chan struct{}
	commands     <-chan command
	done         chan<- struct{}
	pen          image.Point
	line         bresenham
	phase        bool
	homing       bool
	xdiag, ydiag bool
}

type command struct {
	Line       bresenham
	DirX, DirY bool
	Engrave    bool
}

func New(X, Y *tmc2209.Device, needle *bts7960.Device, timer *machine.TIM) (*Device, error) {
	d := &Device{
		xaxis: X, yaxis: Y,
		timer: timer,
	}
	d.needle.dev = needle
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
	d.quit = quit
	d.xaxis.Enable(true)
	defer d.xaxis.Enable(false)
	d.yaxis.Enable(true)
	defer d.yaxis.Enable(false)
	defer d.needle.dev.Enable(false)
	if err := d.home(); err != nil {
		return err
	}
	plan = engrave.Offset(originX, originY, plan)
	if err := d.engrave(engraveSpeed, plan); err != nil {
		return err
	}
	// Return to "eject" position.
	return d.engrave(travelSpeed, func(yield func(engrave.Command)) {
		yield(engrave.Move(image.Pt(d.pen.X, ejectY)))
	})
}

func (d *Device) home() error {
	d.pen = image.Point{}
	d.homing = true
	if err := d.stallThreshold(stallThreshold); err != nil {
		return err
	}
	err := d.engrave(homingSpeed, func(yield func(engrave.Command)) {
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
	d.line = bresenham{}
	d.delay = 0
	d.phase = false
	d.xdiag = false
	d.ydiag = false
	const buffer = 50
	commands := make(chan command, buffer)
	done := make(chan struct{})
	d.commands = commands
	d.done = done
	microstepsPerMinute := speed * stepsPerMM * tmc2209.Microsteps
	period := time.Minute / time.Duration(microstepsPerMinute)
	d.needle.activation = int(needleActivation / period)
	d.needle.coast = int(needleCoast / period)
	d.needle.bounce = int(needleBounce / period)
	// Callback twice per microstep.
	if err := d.timer.Configure(machine.PWMConfig{Period: uint64(period / 2)}); err != nil {
		return fmt.Errorf("mjolnir2: %w", err)
	}
	d.timer.SetWraparoundInterrupt(d.tick)
	plan(func(cmd engrave.Command) {
		c := command{
			Engrave: cmd.Line,
		}
		dist := cmd.Coord.Sub(d.pen)
		d.pen = cmd.Coord
		c.DirX, c.DirY = c.Line.Reset(dist.Mul(tmc2209.Microsteps))
		select {
		case <-done:
			return
		case commands <- c:
		}
	})
	close(commands)
	<-done
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

func (d *Device) complete() {
	d.timer.SetWraparoundInterrupt(nil)
	d.needle.dev.Enable(false)
	close(d.done)
}

// tick drives the engraving hardware. It is called twice for each (micro-)step.
func (d *Device) tick() {
	if !d.phase {
		// The first phase sets up the next step and drives the (low frequency) needle. It
		// is not timing sensitive as long as it completes before the next callback.
		d.xaxis.Step(false)
		d.yaxis.Step(false)
		select {
		case <-d.quit:
			d.complete()
			return
		default:
		}
		d.xdiag = d.xdiag || d.xaxis.Diag()
		d.ydiag = d.ydiag || d.yaxis.Diag()
		// Stop when any axis report a fault, except when homing where
		// the fault indicators must both indicate stalls.
		if ((d.xdiag || d.ydiag) && !d.homing) || d.xdiag && d.ydiag {
			d.complete()
			return
		}
		needle := &d.needle
	loop:
		for d.line.Done() && d.delay == 0 {
			select {
			case cmd, ok := <-d.commands:
				d.line = cmd.Line
				d.xaxis.Dir(!cmd.DirX)
				d.yaxis.Dir(!cmd.DirY)
				if needle.on != cmd.Engrave {
					needle.on = cmd.Engrave
					// Complete the needle cycle (if any).
					d.delay = needle.counter
					if needle.on {
						// In addition, delay steppers until the first dot.
						d.delay += needle.activation + needle.coast
					}
				}
				if !ok && d.delay == 0 {
					d.complete()
					return
				}
			default:
				// Buffer under-run; disable needle.
				needle.on = false
				break loop
			}
		}
		if needle.on && needle.counter == 0 {
			needle.counter = needle.activation + needle.coast + needle.bounce
		}
		nc := needle.counter
		switch {
		case nc > needle.coast+needle.bounce:
			needle.dev.Enable(true)
			needle.dev.Speed(3, 4)
		case nc > needle.bounce:
			needle.dev.Enable(true)
			needle.dev.Speed(1, 2)
		default:
			needle.dev.Enable(false)
		}
		if needle.counter > 0 {
			needle.counter--
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
}
