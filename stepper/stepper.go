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
	progress chan Progress
	knots    knotBuffer
	// safeKnots track the number of knots that
	// are safe to traverse, because they end in
	// standstill.
	safeKnots int
	buf       []uint32
	idx       int
	needle    bool
	pos       bezier.Point
	// progresses holds the history of accumulated progress for the last 3
	// DMA buffers: the most recently completed buffer, the buffer
	// that's in progress and the buffer being filled.
	// Assuming buffers are larger than the hardware FIFO size,
	// the oldest progress is a conservative minimum of the engraving
	// progress.
	progresses [3]Progress
}

type Progress struct {
	Ticks uint
	Knots uint
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

// knotBuffer is a circular buffer of knots.
type knotBuffer struct {
	knots      [MaxSplineLength]bspline.Knot
	start, len int
}

func (b *knotBuffer) Capacity() int {
	return len(b.knots) - b.Length()
}

func (b *knotBuffer) Length() int {
	return b.len
}

func (b *knotBuffer) At(i int) bspline.Knot {
	if i < 0 || b.len <= i {
		panic("index out of range")
	}
	idx := (b.start + i) % len(b.knots)
	return b.knots[idx]
}

func (b *knotBuffer) Push(k bspline.Knot) {
	if b.Capacity() == 0 {
		panic("buffer overflow")
	}
	idx := (b.start + b.len) % len(b.knots)
	b.knots[idx] = k
	b.len++
}

func (b *knotBuffer) Pop() bspline.Knot {
	if b.len == 0 {
		panic("knot buffer underflow")
	}
	k := b.knots[b.start]
	b.start = (b.start + 1) % len(b.knots)
	b.len--
	return k
}

func (e *Driver) HandleTransferCompleted() {
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
	p := e.progresses[:]
	// Replace the accumulated progress.
	var p0 Progress
	select {
	case p0 = <-e.progress:
	default:
	}
	p0.Knots += p[0].Knots
	p0.Ticks += p[0].Ticks
	select {
	case e.progress <- p0:
	default:
	}
	copy(p, p[1:])
	p[len(p)-1] = Progress{}
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
	// Fill knot buffer.
fill:
	for e.knots.Capacity() > 0 {
		select {
		case k := <-e.knotCh:
			e.knots.Push(k)
			// Wait for the spline to be capped, so any stalls are
			// guaranteed to happen at standstill.
			n := e.knots.Length()
			if n < 3 {
				break
			}
			p1, p2, p3 := e.knots.At(n-3).Ctrl, e.knots.At(n-2).Ctrl, e.knots.At(n-1).Ctrl
			if p1 != p2 || p2 != p3 {
				break
			}
			e.safeKnots = n
		default:
			break fill
		}
	}
	for !e.full() {
		prog := e.progresses[:]
		for !e.stepper.Step() {
			if e.safeKnots == 0 {
				return
			}
			prog[len(prog)-1].Knots += 1
			k := e.knots.Pop()
			e.safeKnots--
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
		prog[len(prog)-1].Ticks++
	}
}

func (e *Driver) full() bool {
	return e.idx == len(e.buf)*stepsPerWord
}

func (e *Driver) empty() bool {
	return e.idx == 0
}

func Engrave(d Device, progress chan Progress) *Driver {
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
	stalled := true
	var blocked Axis
loop:
	for {
		stallKnots := d.knotCh
		if !moreCommands {
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
		case <-d.stall:
			stalled = true
		case stallKnots <- knot:
			knot, moreCommands = knots()
		}
		// During stalls, we're responsible for filling the buffer
		// and restarting the interrupt handler.
		if stalled {
			d.fillBuffer()
			if !moreCommands && d.empty() {
				// We're done.
				break
			}
			if d.full() || !moreCommands {
				stalled = false
				steps := d.swapBuffers()
				// The interrupt handler assumes a filled buffer.
				d.fillBuffer()
				d.dev.Transfer(steps)
			}
		}
	}
	if mode == ModeHoming {
		if blocked != (XAxis | YAxis) {
			return errors.New("mjolnir2: homing timed out")
		}
		return nil
	}
	switch {
	case blocked&XAxis != 0:
		return errors.New("mjolnir2: x-axis blocked")
	case blocked&YAxis != 0:
		return errors.New("mjolnir2: y-axis blocked")
	default:
		return nil
	}
}
