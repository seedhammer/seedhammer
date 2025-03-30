package mjolnir2

import (
	"image"
	"slices"
	"testing"
	"time"

	"seedhammer.com/engrave"
)

func TestEngraver(t *testing.T) {
	const mm = 6400
	cmds := []engrave.Command{
		engrave.Move(image.Pt(0, 0)),
		engrave.Move(image.Pt(1, 0)),
		engrave.Line(image.Pt(100*mm, 10*mm)),
		engrave.Move(image.Pt(10*mm, 30*mm)),
		engrave.Line(image.Pt(60*mm, 30*mm)),
		engrave.Line(image.Pt(50*mm, 10*mm)),
		engrave.Move(image.Pt(0, 0)),
	}
	plan := engrave.Plan(slices.Values(cmds))
	pen := image.Point{}
	for pen == cmds[0].Coord {
		cmds = cmds[1:]
	}
	const (
		speed          = 40. * mm
		engravingSpeed = 15. * mm
		accel          = 100. * mm

		ticksPerSecond = speed
	)
	eng := &engraver{
		Speed:            speed,
		EngravingSpeed:   engravingSpeed,
		Acceleration:     accel,
		TicksPerSecond:   ticksPerSecond,
		NeedlePeriod:     20 * time.Millisecond,
		NeedleActivation: 6 * time.Millisecond,
	}
	for step := range eng.Engrave(plan) {
		pen.X += int(step.StepX) * (1 - int(step.DirX)*2)
		pen.Y += int(step.StepY) * (1 - int(step.DirY)*2)
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
