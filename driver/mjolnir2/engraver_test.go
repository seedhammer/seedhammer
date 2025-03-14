package mjolnir2

import (
	"image"
	"math"
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
		engrave.Move(image.Pt(0, 0)),
	}
	plan := engrave.Plan(slices.Values(cmds))
	pen := image.Point{}
	for pen == cmds[0].Coord {
		cmds = cmds[1:]
	}
	accelDelays := []uint16{16, 8, 4, 2, 1}
	eng := &engraver{
		MoveDelays:       accelDelays,
		EngraveDelays:    accelDelays,
		NeedlePeriod:     20,
		NeedleActivation: 6,
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

func TestAccelerationCurve(t *testing.T) {
	const (
		speed = 100
		accel = 50
	)
	curve := newAccelCurve(speed, accel)
	sum := 0
	for _, v := range curve {
		sum += int(v) + pioCyclesPerStep
	}
	dist := stepsForSpeed(speed, accel)
	if dist != len(curve) {
		t.Errorf("speed = %d, acceleration = %d got distance %d, expected %d", speed, accel, len(curve), dist)
	}
	gotTime := int(math.Round(float64(sum) / (speed * pioCyclesPerStep)))
	expTime := int(math.Round(math.Sqrt(2 * float64(dist) / float64(accel))))
	if expTime != gotTime {
		t.Errorf("speed = %d, acceleration = %d got time %d, expected %d", speed, accel, gotTime, expTime)
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
