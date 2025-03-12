package mjolnir2

import (
	"image"
	"slices"
	"testing"

	"seedhammer.com/engrave"
)

func TestEngraver(t *testing.T) {
	cmds := []engrave.Command{
		engrave.Move(image.Pt(0, 0)),
		engrave.Move(image.Pt(50, 10)),
		engrave.Line(image.Pt(10, 30)),
		engrave.Line(image.Pt(60, 30)),
		engrave.Line(image.Pt(50, 10)),
	}
	plan := engrave.Plan(slices.Values(cmds))
	pen := image.Point{}
	for pen == cmds[0].Coord {
		cmds = cmds[1:]
	}
	for step := range engravePlan(plan) {
		pen.X += step.StepX * (step.DirX*2 - 1)
		pen.Y += step.StepY * (step.DirY*2 - 1)
		for len(cmds) > 0 && pen == cmds[0].Coord {
			cmds = cmds[1:]
		}
	}
	if len(cmds) > 0 {
		t.Errorf("engraving didn't visit the points %v", cmds)
	}
}

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
	l := new(bresenham)
	for _, dir := range dirs {
		for _, dist := range tests {
			dist = dist.Sub(dir)
			dirx, diry := l.Reset(dist)
			p := image.Pt(0, 0)
			steps := 0
			for !l.Done() {
				steps++
				dx, dy := l.Step()
				if dx {
					if dirx {
						p.X++
					} else {
						p.X--
					}
				}
				if dy {
					if diry {
						p.Y++
					} else {
						p.Y--
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
