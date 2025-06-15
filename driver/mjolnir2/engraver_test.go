package mjolnir2

import (
	"image"
	"iter"
	"testing"
	"time"

	"seedhammer.com/engrave"
)

const mm = 6400

func TestPath(t *testing.T) {
	cmds := []engrave.Command{
		engrave.Move(image.Pt(0, 0)),
		engrave.Move(image.Pt(1, 0)),
		engrave.Line(image.Pt(100*mm, 10*mm)),
		engrave.Move(image.Pt(10*mm, 30*mm)),
		engrave.Line(image.Pt(60*mm, 30*mm)),
		engrave.Line(image.Pt(50*mm, 10*mm)),
		engrave.Move(image.Pt(0, 0)),
	}
	planDone := make(chan struct{})
	plan := func(yield func(engrave.Command) bool) {
		defer close(planDone)
		for _, cmd := range cmds {
			if !yield(cmd) {
				return
			}
			// Increase chances of stalls.
			time.Sleep(20 * time.Millisecond)
		}
	}
	pen := image.Point{}
	for pen == cmds[0].Coord {
		cmds = cmds[1:]
	}
	for st := range runEngraving(nil, nil, false, plan) {
		if err := st.Error; err != nil {
			t.Fatal(err)
		}
		s := st.Step
		pen.X += int(s.StepX()) * (1 - int(s.DirX())*2)
		pen.Y += int(s.StepY()) * (1 - int(s.DirY())*2)
		for len(cmds) > 0 && pen == cmds[0].Coord {
			cmds = cmds[1:]
		}
	}
	if len(cmds) > 0 {
		t.Errorf("engraving didn't visit the points %v", cmds)
	}
	<-planDone
}

func TestQuit(t *testing.T) {
	planDone := make(chan struct{})
	plan := func(yield func(engrave.Command) bool) {
		x := 0
		for yield(engrave.Move(image.Point{X: x})) {
			x = 80*mm - x
		}
		close(planDone)
	}
	count := 0
	quit := make(chan struct{})
	for step := range runEngraving(nil, quit, false, plan) {
		if err := step.Error; err != nil {
			t.Fatal(err)
		}
		count++
		if count > 1e6 && quit != nil {
			close(quit)
			quit = nil
		}
	}
	<-planDone
}

func TestBlockedAxis(t *testing.T) {
loop:
	for _, a := range []axis{xaxis, yaxis} {
		planDone := make(chan struct{})
		plan := func(yield func(engrave.Command) bool) {
			x := 0
			for yield(engrave.Move(image.Point{X: x})) {
				x = 80*mm - x
			}
			close(planDone)
		}
		diag := make(chan axis)
		count := 0
		for step := range runEngraving(diag, nil, false, plan) {
			if err := step.Error; err != nil {
				// Ok.
				<-planDone
				continue loop
			}
			count++
			if count > 1e6 && diag != nil {
				diag <- a
				diag = nil
			}
		}
		t.Errorf("engraving completed; expected a blocked axis")
	}
}

func TestHomingTimeout(t *testing.T) {
	plan := func(yield func(engrave.Command) bool) {
		yield(engrave.Move(image.Point{X: 100 * mm}))
	}
	for step := range runEngraving(nil, nil, true, plan) {
		if err := step.Error; err != nil {
			// Timed out.
			return
		}
	}
	t.Errorf("engraving completed succesfully; expected homing to time out")
}

func TestHoming(t *testing.T) {
	plan := func(yield func(engrave.Command) bool) {
		yield(engrave.Move(image.Point{X: 100 * mm}))
	}
	diag := make(chan axis, 2)
	diag <- xaxis
	diag <- yaxis
	for step := range runEngraving(diag, nil, true, plan) {
		if err := step.Error; err != nil {
			t.Fatal(err)
		}
	}
}

func runEngraving(diag <-chan axis, quit <-chan struct{}, homing bool, plan engrave.Plan) iter.Seq[stepAndError] {
	const (
		speed          = 40. * mm
		engravingSpeed = 15. * mm
		accel          = 100. * mm

		ticksPerSecond = speed
	)
	conf := engravingConfig{
		Speed:            speed,
		EngravingSpeed:   engravingSpeed,
		Acceleration:     accel,
		TicksPerSecond:   ticksPerSecond,
		NeedlePeriod:     20 * time.Millisecond,
		NeedleActivation: 6 * time.Millisecond,
	}.New()
	const bufSize = 100
	driver := engravingDriver{
		buf:  make([]uint32, bufSize),
		buf2: make([]uint32, bufSize),
	}
	transfers := make(chan []uint32, 1)
	transfer := func(buf []uint32) {
		transfers <- buf
	}
	result := make(chan error, 1)
	go func() {
		result <- driver.engrave(transfer, diag, conf, quit, homing, plan)
	}()
	yieldOk := true
	return func(yield func(stepAndError) bool) {
		for {
			select {
			case err := <-result:
				yieldOk = yieldOk && yield(stepAndError{Error: err})
				return
			case buf := <-transfers:
				for _, w := range buf {
					for range pioStepsPerWord {
						s := step(w & (0b1<<mjolnir2pinBits - 1))
						w >>= mjolnir2pinBits
						yieldOk = yieldOk && yield(stepAndError{Step: s})
					}
				}
				driver.handleTransferCompleted()
			}
		}
	}
}

type stepAndError struct {
	Step  step
	Error error
}
