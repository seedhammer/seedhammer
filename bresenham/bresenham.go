// Package bresenham implements a line stepper with the Bresenham
// algorithm.
package bresenham

import "image"

type Line struct {
	// d is the minor axis error, doubled.
	d int
	// dmajor, dminor is the line vector.
	dmajor, dminor int
	// swap is 0 if the major axis is x, 1 otherwise.
	swap uint8
}

// Reset the stepper with a signed distance. It returns the
// directions and the number of steps.
func (l *Line) Reset(dist image.Point) (uint8, uint8, int) {
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
	l.d = 2*l.dminor - l.dmajor
	return dirx, diry, l.dmajor
}

func (l *Line) Step() (uint8, uint8) {
	var maj, min uint8 = 1, 0
	if l.d > 0 {
		min = 1
	}
	l.d -= 2 * l.dmajor * int(min)
	l.d += 2 * l.dminor
	return (maj &^ l.swap) | (min & l.swap),
		(maj & l.swap) | (min &^ l.swap)
}
