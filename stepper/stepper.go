package stepper

import (
	"seedhammer.com/bezier"
	"seedhammer.com/bspline"
)

// Driver is an engraving driver suitable for
// stepping through a [bspline.Curve] using DMA.
type Driver struct {
	seg     bspline.Segment
	stepper bezier.Interpolator
	needle  bool
	pos     bezier.Point

	w     Writer
	buf   []uint32
	steps int
}

type Writer interface {
	Write(steps []uint32) (completed int, err error)
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

func (d *Driver) fill() {
	n := len(d.buf) * stepsPerWord
	for d.steps < n && d.stepper.Step() {
		// The 5-bit pins. Note that the all-zero
		// value means halt, so the code below is
		// careful to set at least one direction pin.
		var pins uint8
		pos := d.stepper.Position()
		// Clamp to 1 step per tick.
		step := pos.Sub(d.pos)
		step.X = max(min(step.X, 1), -1)
		step.Y = max(min(step.Y, 1), -1)
		d.pos = d.pos.Add(step)
		if step.X != 0 {
			pins |= 0b1 << pinStepX
		}
		if step.X == -1 || step.X == 0 {
			pins |= 0b1 << pinDirX
		}
		if step.Y != 0 {
			pins |= 0b1 << pinStepY
		}
		if step.Y == -1 || step.Y == 0 {
			pins |= 0b1 << pinDirY
		}
		if d.needle {
			pins |= 0b1 << pinNeedle
		}
		word := d.steps / stepsPerWord
		stepIdx := d.steps % stepsPerWord
		w := uint32(0)
		if stepIdx != 0 {
			w = d.buf[word]
		}
		w |= uint32(pins) << (stepIdx * pinBits)
		d.buf[word] = w
		d.steps++
	}
}

func NewDriver(w Writer) *Driver {
	return &Driver{
		w:   w,
		buf: make([]uint32, 128),
	}
}

func (d *Driver) Knot(k bspline.Knot) (completed uint, err error) {
	c, ticks, needle := d.seg.Knot(k)
	d.needle = needle
	d.stepper.Segment(c, ticks)
	for {
		before := d.steps
		d.fill()
		if d.steps == before {
			return completed, nil
		}
		n, err := d.flush()
		completed += n
		if err != nil {
			return completed, err
		}
	}
}

func (d *Driver) Flush() error {
	// Ensure partially filled words are written,
	// by rounding up the step count.
	if rem := d.steps % stepsPerWord; rem > 0 {
		d.steps += stepsPerWord - rem
	}
	_, err := d.flush()
	return err
}

func (d *Driver) flush() (completed uint, err error) {
	// Write whole words.
	n := d.steps / stepsPerWord
	d.steps -= n * stepsPerWord
	buf := d.buf[:n]
	var nwords int
	nwords, err = d.w.Write(buf)
	copy(d.buf, d.buf[n:])
	return uint(nwords) * stepsPerWord, err
}
