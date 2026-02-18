package stepper

import (
	"errors"

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

// Driver is an engraving driver suitable for
// stepping through a [bspline.Curve] using DMA.
type Driver struct {
	seg      bspline.Segment
	stepper  bezier.Interpolator
	knotCh   chan bspline.Knot
	stall    chan struct{}
	quit     <-chan struct{}
	diag     <-chan Axis
	progress chan uint
	needle   bool
	pos      bezier.Point
	start    func(Device)
	blocked  Axis
	mode     Mode
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
	idx := 0
	n := len(buf) * stepsPerWord
loop:
	for idx < n {
		for !e.stepper.Step() {
			select {
			case k := <-e.knotCh:
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
		select {
		case e.stall <- struct{}{}:
		default:
		}
	}
	return idx
}

var (
	errDone = errors.New("homing complete")
)

func Step(mode Mode, startDev func(Device), quit <-chan struct{}, diag <-chan Axis, spline bspline.Curve) error {
	d := &Driver{
		knotCh:   make(chan bspline.Knot, 64),
		stall:    make(chan struct{}, 1),
		progress: make(chan uint, 1),
		start:    startDev,
		quit:     quit,
		diag:     diag,
		mode:     mode,
	}
	const bufSize = 5
	buf := make([]bspline.Knot, bufSize)
	n := 0
	dur := uint(0)
	for k := range spline {
		buf[n] = k
		n++
		dur += k.T
		for n == len(buf) {
			buf := buf[:n]
			progress, wrote, err := d.Write(buf)
			n = copy(buf, buf[wrote:])
			dur -= progress
			if err != nil {
				if err == errDone {
					return nil
				}
				return err
			}
		}
	}
	// Write remaining and wait for completion.
	for dur > 0 {
		buf := buf[:n]
		progress, wrote, err := d.Write(buf)
		n = copy(buf, buf[wrote:])
		dur -= progress
		if err != nil {
			if err == errDone {
				return nil
			}
			return err
		}
	}
	if mode == ModeHoming {
		return errors.New("stepper: homing timed out")
	}
	return nil
}

func (d *Driver) Write(knots []bspline.Knot) (uint, int, error) {
	var k bspline.Knot
	knotsCh := d.knotCh
	stall := d.stall
	if len(knots) > 0 {
		k = knots[0]
		knots = knots[1:]
	} else {
		stall = nil
		knotsCh = nil
	}
	if d.start != nil && (len(knotsCh) == cap(knotsCh) || len(knots) == 0) {
		d.start(d.fillBufferCallback)
		d.start = nil
	}
	wrote := 0
	for {
		select {
		case axis := <-d.diag:
			d.blocked |= axis
			if d.blocked&SAxis != 0 {
				return 0, wrote, errors.New("stepper: power loss or short circuit")
			}
			switch d.mode {
			case ModeHoming:
				if d.blocked == (XAxis | YAxis) {
					return 0, wrote, errDone
				}
			default:
				switch {
				case d.blocked&XAxis != 0:
					return 0, wrote, errors.New("stepper: x-axis blocked")
				case d.blocked&YAxis != 0:
					return 0, wrote, errors.New("stepper: y-axis blocked")
				}
			}
		case <-d.quit:
			return 0, wrote, errDone
		case <-stall:
			return 0, wrote, errors.New("stepper: command buffer underrun caused stall")
		case progress := <-d.progress:
			return progress, wrote, nil
		case knotsCh <- k:
			wrote++
			// Return when the channel buffer is full.
			if len(knotsCh) == cap(knotsCh) || len(knots) == 0 {
				return 0, wrote, nil
			}
			k = knots[0]
			knots = knots[1:]
		}
	}
}
