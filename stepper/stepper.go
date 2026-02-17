package stepper

import (
	"errors"
	"iter"

	"seedhammer.com/bezier"
	"seedhammer.com/bspline"
)

type Mode uint32

const (
	ModeEngrave Mode = iota
	ModeHoming
	ModeNostall
)

type Axis uint8

const (
	XAxis Axis = 0b1 << iota
	YAxis
	SAxis
)

const MaxSplineLength = 256

// Driver is an engraving driver suitable for
// driving through interrupts and DMA.
type Driver struct {
	startDev func(Device)
	seg      bspline.Segment
	stepper  bezier.Interpolator
	knotCh   chan bspline.Knot
	stall    chan struct{}
	done     bool
	progress chan uint
	needle   bool
	pos      bezier.Point
}

type Device func(int, []uint32) int

const (
	pinBits = 5
	// stepsPerWord is the number of pio steps that
	// fit into a 32-bit pio FIFO entry.
	stepsPerWord = 32 / pinBits
)

const (
	// Pin offsets from base pin.
	pinDirY = iota
	pinDirX
	pinNeedle
	pinStepY
	pinStepX
)

func (e *Driver) fillBufferCallback(steps int, buf []uint32) int {
	// Replace the accumulated progress.
	var p0 uint
	select {
	case p0 = <-e.progress:
	default:
	}
	p0 += uint(steps)
	select {
	case e.progress <- p0:
	default:
	}
	return e.fillBuffer(buf)
}

func (e *Driver) fillBuffer(buf []uint32) int {
	if e.done {
		return 0
	}
	idx := 0
	n := len(buf) * stepsPerWord
loop:
	for idx < n && !e.done {
		for !e.stepper.Step() {
			select {
			case k, ok := <-e.knotCh:
				if !ok {
					e.done = true
					close(e.stall)
					return idx
				}
				c, ticks, needle := e.seg.Knot(k)
				e.needle = needle
				e.stepper.Segment(c, ticks)
			default:
				break loop
			}
		}
		var pins uint8
		pos := e.stepper.Position()
		// Clamp to 1 step per tick.
		step := pos.Sub(e.pos)
		step.X = max(min(step.X, 1), -1)
		step.Y = max(min(step.Y, 1), -1)
		e.pos = e.pos.Add(step)
		switch step.X {
		case -1:
			pins |= 0b1<<pinStepX | 0b1<<pinDirX
		case 1:
			pins |= 0b1<<pinStepX | 0b0<<pinDirX
		}
		switch step.Y {
		case -1:
			pins |= 0b1<<pinStepY | 0b1<<pinDirY
		case 1:
			pins |= 0b1<<pinStepY | 0b0<<pinDirY
		}
		if e.needle {
			pins |= 0b1 << pinNeedle
		}
		word := idx / stepsPerWord
		stepIdx := idx % stepsPerWord
		w := uint32(0)
		if stepIdx != 0 {
			w = buf[word]
		}
		w |= uint32(pins) << (stepIdx * pinBits)
		buf[word] = w
		idx++
	}
	if idx == 0 {
		e.done = true
		select {
		case e.stall <- struct{}{}:
		default:
		}
	}
	return idx
}

func Engrave(startDev func(Device), progress chan uint) *Driver {
	const bufSize = 64

	return &Driver{
		startDev: startDev,
		knotCh:   make(chan bspline.Knot, bufSize),
		stall:    make(chan struct{}, 1),
		progress: progress,
	}
}

func (d *Driver) Run(mode Mode, quit <-chan struct{}, diag <-chan Axis, spline bspline.Curve) error {
	knots, c := iter.Pull(iter.Seq[bspline.Knot](spline))
	defer c()
	knot, moreCommands := knots()
	if !moreCommands {
		return nil
	}
	var blocked Axis
	started := false
	stallKnots := d.knotCh
loop:
	for {
		// Start engraving when the channel is full.
		if !started && (!moreCommands || cap(d.knotCh) == len(d.knotCh)) {
			d.startDev(d.fillBufferCallback)
			started = true
		}
		if !moreCommands && stallKnots != nil {
			close(stallKnots)
			stallKnots = nil
		}
		select {
		case axis := <-diag:
			blocked |= axis
			if mode != ModeHoming || blocked == (XAxis|YAxis) {
				break loop
			}
		case <-quit:
			break loop
		case _, ok := <-d.stall:
			if !ok {
				// Done.
				break loop
			}
			return errors.New("stepper: command buffer underrun caused stall")
		case stallKnots <- knot:
			knot, moreCommands = knots()
		}
	}
	if mode == ModeHoming {
		if blocked != (XAxis | YAxis) {
			return errors.New("stepper: homing timed out")
		}
		return nil
	}
	switch {
	case blocked&XAxis != 0:
		return errors.New("stepper: x-axis blocked")
	case blocked&YAxis != 0:
		return errors.New("stepper: y-axis blocked")
	case blocked&SAxis != 0:
		return errors.New("stepper: power loss or short circuit")
	default:
		return nil
	}
}
