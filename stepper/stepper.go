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
	dev      Device
	seg      bspline.Segment
	stepper  bezier.Interpolator
	knotCh   chan bspline.Knot
	stall    chan struct{}
	done     bool
	progress chan uint
	buf      []uint32
	idx      int
	needle   bool
	pos      bezier.Point
	// progressHist holds the progress for the last 3
	// DMA buffers: the most recently completed buffer, the buffer
	// that's in progress and the buffer being filled.
	//
	// Assuming buffers are larger than the hardware FIFO size,
	// the oldest progress is guaranteed to have been engraved by
	// hardware.
	progressHist [3]uint
}

type Device interface {
	NextBuffer() []uint32
	Transfer(steps int)
}

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

func (e *Driver) HandleTransferCompleted() {
	if e.done {
		return
	}
	if e.empty() {
		// If there's nothing to flush, we're stalled. Stalling
		// is acceptable because the engraver is at standstill
		// between commands.
		select {
		case e.stall <- struct{}{}:
		default:
		}
		return
	}
	p := e.progressHist[:]
	// Replace the accumulated progress.
	var p0 uint
	select {
	case p0 = <-e.progress:
	default:
	}
	p0 += p[0]
	select {
	case e.progress <- p0:
	default:
	}
	copy(p, p[1:])
	p[len(p)-1] = 0
	steps := e.swapBuffers()
	e.dev.Transfer(steps)
	// Fill buffer here in the interrupt handler to avoid stalling
	// the engraver because of unfortunate goroutine scheduling.
	e.fillBuffer()
}

func (e *Driver) swapBuffers() int {
	steps := e.idx
	e.idx = 0
	e.buf = e.dev.NextBuffer()
	return steps
}

func (e *Driver) fillBuffer() {
	for !e.full() && !e.done {
		for !e.stepper.Step() {
			k, ok := <-e.knotCh
			if !ok {
				e.done = true
				close(e.stall)
				return
			}
			c, ticks, needle := e.seg.Knot(k)
			e.needle = needle
			e.stepper.Segment(c, ticks)
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
		idx := e.idx / stepsPerWord
		stepIdx := e.idx % stepsPerWord
		w := e.buf[idx]
		if stepIdx == 0 {
			w = 0
		}
		w |= uint32(pins) << (stepIdx * pinBits)
		e.buf[idx] = w
		e.idx++
		prog := e.progressHist[:]
		prog[len(prog)-1]++
	}
}

func (e *Driver) full() bool {
	return e.idx == len(e.buf)*stepsPerWord
}

func (e *Driver) empty() bool {
	return e.idx == 0
}

func Engrave(d Device, progress chan uint) *Driver {
	const bufSize = 64

	return &Driver{
		buf:      d.NextBuffer(),
		dev:      d,
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
			started = true
			d.fillBuffer()
			steps := d.swapBuffers()
			// The interrupt handler assumes a filled buffer.
			d.fillBuffer()
			d.dev.Transfer(steps)
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
