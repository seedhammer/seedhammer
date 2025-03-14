package mjolnir2

import (
	"image"
	"iter"
	"math"

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
	// The aceleration delay cycle counts for moving and engraving.
	// Zero values is equal to [pioCyclesPerStep].
	MoveDelays    []uint16
	EngraveDelays []uint16
	// Needle period in cycles.
	NeedlePeriod int
	// Needle activation duration in cycles.
	NeedleActivation int
}

func (e *engraver) Engrave(plan engrave.Plan) iter.Seq[step] {
	return func(yield func(step) bool) {
		pen := image.Point{}
		needleCycles := 0
		engraving := false
		var needle uint8
		for cmd := range plan {
			var l bresenham
			dist := cmd.Coord.Sub(pen)
			pen = cmd.Coord
			dirx, diry, steps := l.Reset(dist)
			accelDelays := e.MoveDelays
			if cmd.Line {
				accelDelays = e.EngraveDelays
			}
			maxAccel := min(len(accelDelays), steps/2)
			for i := range steps {
				delay := 0
				switch {
				case steps-1-i < maxAccel: // Deceleration
					delay = int(accelDelays[steps-1-i])
				case i < maxAccel: // Acceleration
					delay = int(accelDelays[i])
				default: // Top speed.
					delay = int(accelDelays[len(accelDelays)-1])
				}
				// Zero means a delay of [pioCyclesPerStep] cycles.
				delay += pioCyclesPerStep
				// Delay motion at the beginning and end of an
				// engraving segment to ensure the needle completes
				// a cycle.
				if i == 0 && engraving != cmd.Line {
					engraving = cmd.Line
					delay = max(delay, e.NeedlePeriod-needleCycles)
					if engraving {
						// Start of engraving segments resets cycle.
						needleCycles = 0
					}
				}
				// Issue needle changes happening before the next motor step.
				for {
					// rem is the remaining number of cycles until the next
					// needle change.
					rem := delay
					if cmd.Line || needle == 1 {
						needle = 1
						rem = e.NeedleActivation - needleCycles
						if rem <= 0 {
							needle = 0
							rem = e.NeedlePeriod - needleCycles
						}
					}
					// Constrain the remaining cycle count by the fixed step delay.
					rem = max(pioCyclesPerStep, rem)
					// Stop if the delay is within the fixed step delay
					// of the remaining count.
					if delay-pioCyclesPerStep <= rem {
						needleCycles += delay
						needleCycles = needleCycles % e.NeedlePeriod
						break
					}
					needleCycles += rem
					needleCycles = needleCycles % e.NeedlePeriod
					// Insert needle change (with no motor steps).
					s := step{
						DirX:   dirx,
						DirY:   diry,
						Needle: needle,
						Delay:  rem - pioCyclesPerStep,
					}
					delay -= rem
					if !yield(s) {
						return
					}
				}
				stepx, stepy := l.Step()
				for delay > 0 {
					const maxDelay = 0b1<<mjolnir2delayBits - 1
					d := min(delay-pioCyclesPerStep, maxDelay)
					s := step{
						DirX:   dirx,
						DirY:   diry,
						Needle: needle,
						StepX:  stepx,
						StepY:  stepy,
						Delay:  d,
					}
					delay -= d + pioCyclesPerStep
					if !yield(s) {
						return
					}
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

func newAccelCurve(speed, accel int) []uint16 {
	accelDist := stepsForSpeed(speed, accel)
	pioFreq := speed * pioCyclesPerStep
	curve := make([]uint16, accelDist)
	delaySum := 0
	for i := range curve {
		s := i + 1
		t := math.Sqrt(2 * float64(s) / float64(accel))
		delay := int(float64(pioFreq)*t) - delaySum
		delaySum += delay
		delay -= pioCyclesPerStep
		curve[i] = uint16(delay)
	}
	return curve
}

// stepsForSpeed computes the distance to reach speed from
// an acceleration.
func stepsForSpeed(speed, accel int) int {
	accelTime := float64(speed) / float64(accel)
	return int(float64(accel) * accelTime * accelTime / 2)
}
