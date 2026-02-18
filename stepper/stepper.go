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
// driving through interrupts and DMA.
type Driver struct {
	startDev func(Device)
	seg      bspline.Segment
	stepper  bezier.Interpolator
	knotCh   chan bspline.Knot
	stall    chan struct{}
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

func Engrave(startDev func(Device)) *Driver {
	const bufSize = 64

	return &Driver{
		startDev: startDev,
		knotCh:   make(chan bspline.Knot, bufSize),
		stall:    make(chan struct{}, 1),
		progress: make(chan uint, 1),
	}
}

var errHomed = errors.New("homing complete")

func (d *Driver) Run(mode Mode, quit <-chan struct{}, diag <-chan Axis, spline bspline.Curve) error {
	progress := uint(0)
	started := false
	var blocked Axis
	Write := func(knots []bspline.Knot) (uint, error) {
		var k bspline.Knot
		knotsCh := d.knotCh
		if len(knots) > 0 {
			k = knots[0]
			knots = knots[1:]
		} else {
			knotsCh = nil
		}
		for {
			select {
			case axis := <-diag:
				blocked |= axis
				if blocked&SAxis != 0 {
					return progress, errors.New("stepper: power loss or short circuit")
				}
				switch mode {
				case ModeHoming:
					if blocked == (XAxis | YAxis) {
						return progress, errHomed
					}
				default:
					switch {
					case blocked&XAxis != 0:
						return progress, errors.New("stepper: x-axis blocked")
					case blocked&YAxis != 0:
						return progress, errors.New("stepper: y-axis blocked")
					}
				}
			case <-quit:
			case <-d.stall:
				if knotsCh == nil {
					// Done engraving.
					return progress, nil
				}
				return progress, errors.New("stepper: command buffer underrun caused stall")
			case progress = <-d.progress:
				return progress, nil
			case knotsCh <- k:
				// Return when the channel buffer is full or there
				// are no more knots available.
				if len(knotsCh) == cap(knotsCh) || len(knots) == 0 {
					if !started {
						started = true
						d.startDev(d.fillBufferCallback)
					}
					return progress, nil
				}
				k = knots[0]
				knots = knots[1:]
			}
		}
	}
	{
		const bufSize = 64
		buf := make([]bspline.Knot, bufSize)
		n := 0
		dur := uint(0)
		for k := range spline {
			buf[n] = k
			n++
			dur += k.T
			if n == len(buf) {
				progress, err := Write(buf[:n])
				dur -= progress
				n = 0
				if err != nil {
					return err
				}
			}
		}
		// Write remaining and wait for completion.
		for dur > 0 {
			progress, err := Write(buf[:n])
			n = 0
			dur -= progress
			if err != nil {
				if err == errHomed {
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
}
