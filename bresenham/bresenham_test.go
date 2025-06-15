package bresenham

import (
	"image"
	"testing"
)

func TestBresenham(t *testing.T) {
	tests := []image.Point{
		image.Pt(0, 0),
		image.Pt(0, 1),
		image.Pt(1, 0),
		image.Pt(1, 1),
		image.Pt(1, 100),
		image.Pt(100, 1),
		image.Pt(100, 0),
		image.Pt(1000, 50),
		image.Pt(20, 50),
	}
	dirs := []image.Point{
		image.Pt(1, 1),
		image.Pt(-1, 1),
		image.Pt(1, -1),
		image.Pt(-1, -1),
	}
	l := new(Line)
	for _, dir := range dirs {
		for _, dist := range tests {
			dist = dist.Sub(dir)
			dirx, diry, steps := l.Reset(dist)
			p := image.Pt(0, 0)
			for range steps {
				dx, dy := l.Step()
				if dx == 1 {
					if dirx == 1 {
						p.X--
					} else {
						p.X++
					}
				}
				if dy == 1 {
					if diry == 1 {
						p.Y--
					} else {
						p.Y++
					}
				}
			}
			dabs := dist
			if dabs.X < 0 {
				dabs.X = -dabs.X
			}
			if dabs.Y < 0 {
				dabs.Y = -dabs.Y
			}
			if want := max(dabs.X, dabs.Y); steps != want {
				t.Errorf("%v stepped %d times, expected %d", dist, steps, want)
			}
			if p != dist {
				t.Errorf("stepped to %v, expected %v", p, dist)
			}
		}
	}
}
