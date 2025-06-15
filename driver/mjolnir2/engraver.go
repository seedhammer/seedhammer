package mjolnir2

import (
	"errors"
	"image"
	"iter"
	"runtime"
	"time"

	"seedhammer.com/bresenham"
	"seedhammer.com/engrave"
)

// engravingDriver is an engraving driver suitable for
// driving through interrupts and DMA.
type engravingDriver struct {
	transfer  func(buf []uint32)
	engraving engraving
	commands  chan engrave.Command
	stall     chan struct{}
	buf, buf2 []uint32
	idx       int
	eof       bool
}

// step is a 5-pin pio command with a bit per output pin.
type step uint8

type axis uint8

const (
	xaxis axis = 0b1 << iota
	yaxis
)

type engravingConfig struct {
	// Move speed in steps/second.
	Speed int
	// EngraveSpeed in steps/second.
	EngravingSpeed int
	// Acceleration (and deceleration) steps/second².
	Acceleration int
	// Engraver ticks per second. A tick represents the duration
	// of a completed pio step.
	TicksPerSecond int
	// Needle period and activation duration.
	NeedlePeriod     time.Duration
	NeedleActivation time.Duration
}

const (
	// No-op is the pio step that clears every pin
	// and stops the needle.
	noop = 0b00000
	// pioStepsPerWord is the number of pio steps that
	// fit into a 32-bit pio FIFO entry.
	pioStepsPerWord = 32 / mjolnir2pinBits
)

const (
	// Pin offsets from base pin.
	pinDirY = iota
	pinDirX
	pinNeedle
	pinStepY
	pinStepX
)

// engraving represents the state of an engraving, along with pre-computed
// values for efficiency.
type engraving struct {
	// State.
	phase   phase
	pen     image.Point
	step    step
	line    bresenham.Line
	steps   int
	engrave bool
	// The current speed, in ticks.
	ta int
	// s tracks the number of integer steps.
	s int
	// Δs accumulates fractional steps, in integer units of 1/2a⁻¹.
	Δs int
	// t tracks the tick in each, starting a 0 for each phase.
	t int
	// sd is the step to start decelerating.
	sd int
	// tneedle tracks the cyclical needle tick.
	tneedle int

	// Pre-computed constants.

	// Needle period activation in ticks.
	needlePeriod int
	needleAct    int
	// Speeds, represented as the number of ticks to
	// reach velocity v. It equals
	//
	//   t_a = v / a
	//
	// In units of ticks:
	//
	//   t_a = (v/tps) / (a/(tps * tps)) = v * tps / a
	//
	speed, engravingSpeed int
	// Acceleration in seconds, a_s, converted to steps/ticks²:
	//
	//   a = a_s / (tps * tps)
	//
	// aInv2 equals 2a⁻¹.
	aInv2 int
}

type phase int

const (
	delayPhase phase = iota
	accelPhase
	movePhase
	decelPhase
	endPhase
)

func (c engravingConfig) New() engraving {
	tps := uint64(c.TicksPerSecond)
	as := uint64(c.Acceleration)
	return engraving{
		needlePeriod:   int(c.NeedlePeriod * time.Duration(c.TicksPerSecond) / time.Second),
		needleAct:      int(c.NeedleActivation * time.Duration(c.TicksPerSecond) / time.Second),
		aInv2:          int(2 * tps * tps / as),
		speed:          int(uint64(c.Speed) * tps / as),
		engravingSpeed: int(uint64(c.EngravingSpeed) * tps / as),
	}
}

// Command resets the engraver to step through a command.
// Call [Step] to step through it.
func (e *engraving) Command(cmd engrave.Command) {
	dist := cmd.Coord.Sub(e.pen)
	e.pen = cmd.Coord
	dirx, diry, steps := e.line.Reset(dist)
	e.steps = steps
	e.step = step(0).WithDirs(dirx, diry)

	e.s = 0
	e.Δs = 0
	e.t = 0
	e.sd = 0
	// Quarter cycle delay.
	e.ta = e.needlePeriod / 4
	// However, no delay seems better.
	e.ta = 0
	e.phase = delayPhase
	if cmd.Line == e.engrave {
		// Middle of engraving, or moving.
		// Don't delay.
		e.t = e.ta
	}
	e.engrave = cmd.Line
}

// Step computes the pins for the next pio step
// in the current command. Step returns false if
// there are no more steps.
func (st *engraving) Step() (step, bool) {
	// Delay engraving to ensure there's a dot at the
	// beginning and end of a line.
	if st.phase == delayPhase && st.t == st.ta {
		st.phase++
		st.t = 0
		st.ta = st.speed
		if st.engrave {
			st.ta = st.engravingSpeed
		}
	}
	// Accelerate until half the distance is travelled
	// or the maximum velocity reached.
	if st.phase == accelPhase && (st.s == st.steps/2 || st.t == st.ta) {
		st.phase++
		// Spend as many steps decelerating as spent accelerating.
		st.sd = st.steps - st.s
		// Record actual time spent accelerating, except in the
		// degenerate case (no steps spent accelerating).
		if st.s > 0 {
			st.ta = st.t
		}
		st.t = 0
	}
	// Move at constant speed until deceleration.
	if st.phase == movePhase && st.s == st.sd {
		st.phase++
		st.t = 0
	}
	// Decelerate until endpoint.
	if st.phase == decelPhase && st.s == st.steps {
		st.phase++
		st.t = 0
	}
	// Complete needle cycle, if needed.
	if st.phase == endPhase && (!st.engrave || st.tneedle == 0) {
		return 0, false
	}
	step := st.step
	// Advance needle cycle.
	st.tneedle = (st.tneedle + 1) % st.needlePeriod
	if st.engrave && st.tneedle < st.needleAct {
		step = step.WithNeedle()
	}
	// Advance time and Δs.
	st.t++
	switch st.phase {
	case accelPhase:
		// The distance travelled under acceleration a for 1 tick
		// equals
		//
		//   Δs(t) = 1/2 * at² - 1/2 * a(t-1)²
		//         = a(t-1/2)
		//         = (2t-1)/(2a⁻¹)     (eliminating fractions)
		st.Δs += 2*st.t - 1
	case movePhase:
		// Under constant velocity v, the distance travelled per tick
		// equals
		//
		//  Δs(t) = vt - v(t-1) = v
		//        = a*t_a             (since v = a*t_a)
		//        = 2t_a / 2a⁻¹
		st.Δs += 2 * st.ta
	case decelPhase:
		// Starting at velocity v, the distance travelled under
		// deceleration equals
		//
		//   Δs(t) = vt - 1/2*at² - (v(t-1) - 1/2*a(t-1)²)
		//         = a(t_a-t+1/2) = (2t_a-2t+1)/(2a⁻¹).
		st.Δs += 2*st.ta - 2*st.t + 1
	default:
		// The start and end phases don't step.
	}
	// Step when Δs reaches 1.
	if st.Δs >= st.aInv2 {
		st.s++
		// Reduce nominator.
		st.Δs -= st.aInv2
		step = step.WithSteps(st.line.Step())
	}
	return step, true
}

func (s step) WithDirs(dirx, diry uint8) step {
	s |= step(dirx<<pinDirX | diry<<pinDirY)
	return s
}

func (s step) WithNeedle() step {
	s |= 0b1 << pinNeedle
	return s
}

func (s step) WithSteps(stepx, stepy uint8) step {
	s |= step(stepx<<pinStepX | stepy<<pinStepY)
	return s
}

func (s step) StepX() uint8 {
	return uint8(s >> pinStepX & 0b1)
}

func (s step) StepY() uint8 {
	return uint8(s >> pinStepY & 0b1)
}

func (s step) DirX() uint8 {
	return uint8(s >> pinDirX & 0b1)
}

func (s step) DirY() uint8 {
	return uint8(s >> pinDirY & 0b1)
}

func (e *engravingDriver) reset(transfer func([]uint32), eng engraving) {
	const bufSize = 64
	e.transfer = transfer
	e.commands = make(chan engrave.Command, bufSize)
	e.stall = make(chan struct{}, 1)
	e.engraving = eng
	e.eof = true
	e.idx = 0
}

func (e *engravingDriver) handleTransferCompleted() {
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
	e.transfer(e.swapBuffers())
	// Fill buffer here in the interrupt handler to avoid stalling
	// the engraver because of unfortunate goroutine scheduling.
	e.fillBuffer()
}

func (e *engravingDriver) fillBuffer() {
outer:
	for !e.full() {
		if e.eof {
			// Fetch next command.
			select {
			case cmd := <-e.commands:
				e.engraving.Command(cmd)
				e.eof = false
			default:
				return
			}
		}
		step, ok := e.engraving.Step()
		if !ok {
			e.eof = true
			continue outer
		}
		idx := e.idx / pioStepsPerWord
		stepIdx := e.idx % pioStepsPerWord
		w := e.buf[idx]
		if stepIdx == 0 {
			w = 0
		}
		w |= uint32(step) << (stepIdx * mjolnir2pinBits)
		e.buf[idx] = w
		e.idx++
	}
}

func (e *engravingDriver) full() bool {
	return e.idx == len(e.buf)*pioStepsPerWord
}

func (e *engravingDriver) empty() bool {
	return e.idx == 0
}

func (e *engravingDriver) swapBuffers() []uint32 {
	// Round buffer size up to include any partly filled word.
	n := (e.idx + pioStepsPerWord - 1) / pioStepsPerWord
	buf := e.buf[:n]
	// Swap.
	e.buf, e.buf2 = e.buf2, e.buf
	e.idx = 0
	return buf
}

func (e *engravingDriver) engrave(transfer func([]uint32), diag <-chan axis, conf engraving, quit <-chan struct{}, homing bool, plan engrave.Plan) error {
	// Keep the DMA buffers alive.
	defer runtime.KeepAlive(e)
	e.reset(transfer, conf)
	cmds, c := iter.Pull(iter.Seq[engrave.Command](plan))
	defer c()
	cmd, moreCommands := cmds()
	if !moreCommands {
		return nil
	}
	stalled := true
	var blocked axis
	for {
		stallCmds := e.commands
		if !moreCommands {
			stallCmds = nil
		}
		select {
		case <-quit:
			return nil
		case axis := <-diag:
			if !homing {
				switch axis {
				case xaxis:
					return errors.New("mjolnir2: x-axis blocked")
				case yaxis:
					return errors.New("mjolnir2: y-axis blocked")
				default:
					panic("invalid axis")
				}
			}
			blocked |= axis
			if blocked == (xaxis | yaxis) {
				return nil
			}
		case <-e.stall:
			stalled = true
		case stallCmds <- cmd:
			cmd, moreCommands = cmds()
		}
		// During stalls, we're responsible for filling the buffer
		// and restarting the interrupt handler.
		if stalled {
			e.fillBuffer()
			if !moreCommands && e.empty() {
				// We're done.
				break
			}
			if e.full() || !moreCommands {
				stalled = false
				buf := e.swapBuffers()
				// The interrupt handler assumes a filled buffer.
				e.fillBuffer()
				transfer(buf)
			}
		}
	}
	if homing {
		return errors.New("mjolnir2: homing timed out")
	}
	return nil
}
