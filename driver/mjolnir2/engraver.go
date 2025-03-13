package mjolnir2

import (
	"image"
	"iter"

	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
)

const (
	fullstepsPerMM = 200 / 8
	stepsPerMM     = fullstepsPerMM * tmc2209.Microsteps

	// topSpeed in mm/s.
	topSpeed = 20
	// acceleration in mm/s².
	acceleration = 5
)

type step struct {
	Speed        int
	DirX, DirY   int
	StepX, StepY int
	Line         bool
}

func engravePlan(plan engrave.Plan) iter.Seq[step] {
	return func(yield func(step) bool) {
		pen := image.Point{}
		for cmd := range plan {
			var l bresenham
			dist := cmd.Coord.Sub(pen)
			pen = cmd.Coord
			dirx, diry := l.Reset(dist)
			s := step{
				Line: cmd.Line,
			}
			if dirx {
				s.DirX = 1
			}
			if diry {
				s.DirY = 1
			}
			t := 0
			for !l.Done() {
				s := s
				t++
				speed := t * acceleration
				s.Speed = min(topSpeed, speed)
				stepx, stepy := l.Step()
				if stepx {
					s.StepX = 1
				}
				if stepy {
					s.StepY = 1
				}
				if !yield(s) {
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
	// steps is the remaining steps of the major axis.
	steps int
	// dmajor, dminor is the line vector.
	dmajor, dminor int
	// swap is 0 if the major axis is x, 1 otherwise.
	swap int
}

// Reset the stepper with a signed distance. It returns the
// directions.
func (l *bresenham) Reset(dist image.Point) (bool, bool) {
	dirx, diry := dist.X >= 0, dist.Y >= 0
	if !dirx {
		dist.X = -dist.X
	}
	if !diry {
		dist.Y = -dist.Y
	}
	l.swap = 0
	if dist.Y > dist.X {
		l.swap = 1
		dist.X, dist.Y = dist.Y, dist.X
	}
	l.dmajor, l.dminor = dist.X, dist.Y
	l.D = 2*l.dminor - l.dmajor
	l.steps = l.dmajor
	return dirx, diry
}

func (l *bresenham) Done() bool {
	return l.steps == 0
}

func (l *bresenham) Step() (bool, bool) {
	maj, min := 1, 0
	if l.D > 0 {
		min = 1
	}
	l.D -= 2 * l.dmajor * min
	l.D += 2 * l.dminor
	l.steps--
	return (maj&^l.swap)|(min&l.swap) != 0,
		(maj&l.swap)|(min&^l.swap) != 0
}
