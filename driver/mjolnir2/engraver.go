package mjolnir2

import (
	"image"
	"time"

	"seedhammer.com/engrave"
)

// step is a 5-pin pio command with a bit per output pin.
type step uint8

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
	line    bresenham
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
	startPhase phase = iota
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
	e.phase = startPhase
	e.engrave = cmd.Line
	dist := cmd.Coord.Sub(e.pen)
	e.pen = cmd.Coord
	dirx, diry, steps := e.line.Reset(dist)
	e.steps = steps
	e.step = step(0).WithDirs(dirx, diry)
	e.s = 0
	e.Δs = 0
	e.t = 0
	e.sd = 0
	e.tneedle = 0
}

// Step computes the pins for the next pio step
// in the current command. Step returns false if
// there are no more steps.
func (st *engraving) Step() (step, bool) {
	// Complete a needle cycle of engraving to ensure
	// there's a dot at the beginning.
	if st.phase == startPhase && (!st.engrave || st.tneedle == st.needlePeriod-1) {
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

// bresenham implements a line stepper with the Bresenham
// algorithm.
type bresenham struct {
	// D is the minor axis error, doubled.
	D int
	// dmajor, dminor is the line vector.
	dmajor, dminor int
	// swap is 0 if the major axis is x, 1 otherwise.
	swap uint8
}

// Reset the stepper with a signed distance. It returns the
// directions and the number of steps.
func (l *bresenham) Reset(dist image.Point) (uint8, uint8, int) {
	var dirx, diry uint8
	if dist.X < 0 {
		dirx = 1
		dist.X = -dist.X
	}
	if dist.Y < 0 {
		diry = 1
		dist.Y = -dist.Y
	}
	l.swap = 0
	if dist.Y > dist.X {
		l.swap = 1
		dist.X, dist.Y = dist.Y, dist.X
	}
	l.dmajor, l.dminor = dist.X, dist.Y
	l.D = 2*l.dminor - l.dmajor
	return dirx, diry, l.dmajor
}

func (l *bresenham) Step() (uint8, uint8) {
	var maj, min uint8 = 1, 0
	if l.D > 0 {
		min = 1
	}
	l.D -= 2 * l.dmajor * int(min)
	l.D += 2 * l.dminor
	return (maj &^ l.swap) | (min & l.swap),
		(maj & l.swap) | (min &^ l.swap)
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
