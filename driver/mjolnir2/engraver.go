package mjolnir2

import (
	"image"
	"iter"
	"time"

	"seedhammer.com/engrave"
)

type step struct {
	// The delay in cycles before issuing the
	// step and needle. A delay of zero is the
	// fixed delay [pioCyclesPerStep].
	Delay        int
	DirX, DirY   uint8
	Needle       uint8
	StepX, StepY uint8
}

type engraver struct {
	// Move speed in steps/second.
	Speed int
	// EngraveSpeed in steps/second.
	EngravingSpeed int
	// Acceleration (and deceleration) steps/second².
	Acceleration int
	// Engraver ticks per second.
	TicksPerSecond int
	// Needle period and activation duration.
	NeedlePeriod     time.Duration
	NeedleActivation time.Duration
}

func (e *engraver) Engrave(plan engrave.Plan) iter.Seq[step] {
	return func(yield func(step) bool) {
		pen := image.Point{}
		tps := uint64(e.TicksPerSecond)
		as := uint64(e.Acceleration)
		// Acceleration a_s, converted to steps/ticks²:
		//
		//   a = a_s / (tps * tps)
		//
		// aInv2 equals 2a⁻¹.
		aInv2 := int(2 * tps * tps / as)
		needlePeriod := int(e.NeedlePeriod * time.Duration(e.TicksPerSecond) / time.Second)
		needleAct := int(e.NeedleActivation * time.Duration(e.TicksPerSecond) / time.Second)
		for cmd := range plan {
			var l bresenham
			dist := cmd.Coord.Sub(pen)
			pen = cmd.Coord
			dirx, diry, steps := l.Reset(dist)
			maxv := uint64(e.Speed)
			step := step{
				DirX: dirx,
				DirY: diry,
			}
			if cmd.Line {
				maxv = uint64(e.EngravingSpeed)
			}

			type phase int
			const (
				startPhase phase = iota
				accelPhase
				movePhase
				decelPhase
				endPhase
			)
			ph := startPhase
			// Compute the number of ticks, ta, that accelerates to velocity v.
			//
			// In units of seconds:
			//
			//   t_a = v / a
			//
			// In units of ticks:
			//
			//   t_a = (v/tps) / (a/(tps * tps)) = v * tps / a
			//
			ta := int(maxv * tps / as)
			// s tracks the number of integer steps.
			s := 0
			// Δs accumulates fractional steps, in integer units of 1/2a⁻¹.
			Δs := 0
			// t tracks the tick in each, starting a 0 for each phase.
			t := 0
			// sd is the step to start decelerating.
			var sd int
			// tneedle tracks the cyclical needle tick.
			tneedle := 0
			for {
				// Complete a needle cycle of engraving to ensure
				// there's a dot at the beginning.
				if ph == startPhase && (!cmd.Line || tneedle == needlePeriod-1) {
					ph++
					t = 0
				}
				// Accelerate until half the distance is travelled
				// or the maximum velocity reached.
				if ph == accelPhase && (s == steps/2 || t == ta) {
					ph++
					// Spend as many steps decelerating as spent accelerating.
					sd = steps - s
					// Record actual time spent accelerating, except in the
					// degenerate case (no steps spent accelerating).
					if s > 0 {
						ta = t
					}
					t = 0
				}
				// Move at constant speed until deceleration.
				if ph == movePhase && s == sd {
					ph++
					t = 0
				}
				// Decelerate until endpoint.
				if ph == decelPhase && s == steps {
					ph++
					t = 0
				}
				// Complete needle cycle, if needed.
				if ph == endPhase && (!cmd.Line || tneedle == 0) {
					break
				}
				step := step
				// Advance needle cycle.
				tneedle = (tneedle + 1) % needlePeriod
				if cmd.Line && tneedle < needleAct {
					step.Needle = 1
				}
				// Advance time and Δs.
				t++
				switch ph {
				case accelPhase:
					// The distance travelled under acceleration a for 1 tick
					// equals
					//
					//   Δs(t) = 1/2 * at² - 1/2 * a(t-1)²
					//         = a(t-1/2)
					//         = (2t-1)/(2a⁻¹)     (eliminating fractions)
					Δs += 2*t - 1
				case movePhase:
					// Under constant velocity v, the distance travelled per tick
					// equals
					//
					//  Δs(t) = vt - v(t-1) = v
					//        = a*t_a             (since v = a*t_a)
					//        = 2t_a / 2a⁻¹
					Δs += 2 * ta
				case decelPhase:
					// Starting at velocity v, the distance travelled under
					// deceleration equals
					//
					//   Δs(t) = vt - 1/2*at² - (v(t-1) - 1/2*a(t-1)²)
					//         = a(t_a-t+1/2) = (2t_a-2t+1)/(2a⁻¹).
					Δs += 2*ta - 2*t + 1
				default:
					// The start and end phases don't step.
				}
				// Step when Δs reaches 1.
				if Δs >= aInv2 {
					s++
					// Reduce nominator.
					Δs -= aInv2
					step.StepX, step.StepY = l.Step()
				}
				if !yield(step) {
					return
				}
			}
		}
	}
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
