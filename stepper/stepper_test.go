package stepper

import (
	"iter"
	"slices"
	"testing"
	"time"

	qr "github.com/seedhammer/kortschak-qr"
	"seedhammer.com/bezier"
	"seedhammer.com/bip39"
	"seedhammer.com/bspline"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
	"seedhammer.com/slip39"
)

func TestPath(t *testing.T) {
	cmds := []struct {
		Engrave bool
		P       bezier.Point
	}{
		{false, bezier.Pt(0, 0)},
		{false, bezier.Pt(1, 0)},
		{true, bezier.Pt(100*mm, 10*mm)},
		{false, bezier.Pt(10*mm, 30*mm)},
		{true, bezier.Pt(60*mm, 30*mm)},
		{true, bezier.Pt(50*mm, 10*mm)},
		{false, bezier.Pt(0, 0)},
	}
	plan := func(yield func(engrave.Command) bool) {
		for _, cmd := range cmds {
			c := engrave.Move(cmd.P)
			if cmd.Engrave {
				c = engrave.Line(cmd.P)
			}
			if !yield(c) {
				return
			}
			// Increase chances of stalls.
			time.Sleep(20 * time.Millisecond)
		}
	}
	pen := bezier.Point{}
	for {
		if pen != cmds[0].P {
			break
		}
		cmds = cmds[1:]
	}
	spline := engrave.PlanEngraving(params.StepperConfig, plan)
	for s := range runEngraving(nil, spline) {
		dx, dy := (s>>pinDirX)&0b1, (s>>pinDirY)&0b1
		sx, sy := (s>>pinStepX)&0b1, (s>>pinStepY)&0b1
		needle := s>>pinNeedle&0b1 == 0b1
		pen.X += int(sx) * (1 - int(dx)*2)
		pen.Y += int(sy) * (1 - int(dy)*2)
		for len(cmds) > 0 {
			if pen != cmds[0].P || needle != cmds[0].Engrave {
				break
			}
			cmds = cmds[1:]
		}
	}
	if len(cmds) > 0 {
		t.Errorf("engraving didn't visit the points %v", cmds)
	}
}

func TestQuit(t *testing.T) {
	planDone := make(chan struct{})
	plan := func(yield func(engrave.Command) bool) {
		x := 0
		for yield(engrave.Move(bezier.Pt(x, 0))) {
			x = 80*mm - x
		}
		close(planDone)
	}
	count := 0
	quit := make(chan struct{})
	spline := engrave.PlanEngraving(params.StepperConfig, plan)
	for range runEngraving(quit, spline) {
		count++
		if count > 1e6 && quit != nil {
			close(quit)
			quit = nil
		}
	}
	<-planDone
}

type buffer struct {
	buf   []uint32
	steps int
}

type dev struct {
	transfers  chan buffer
	buf1, buf2 []uint32
}

func (d *dev) NextBuffer() []uint32 {
	return d.buf2
}

func (d *dev) Transfer(steps int) {
	d.transfers <- buffer{d.buf1, steps}
	d.buf1, d.buf2 = d.buf2, d.buf1
}

func runEngraving(quit <-chan struct{}, spline bspline.Curve) iter.Seq[uint8] {
	const bufSize = 128
	d := &dev{
		buf1:      make([]uint32, bufSize),
		buf2:      make([]uint32, bufSize),
		transfers: make(chan buffer, 1),
	}
	result := make(chan struct{}, 1)
	driver := Engrave(d, quit, spline)
	go func() {
		driver.Run()
		close(result)
	}()
	yieldOk := true
	return func(yield func(step uint8) bool) {
		for {
			select {
			case <-result:
				return
			case t := <-d.transfers:
				for i := range t.steps {
					w := t.buf[i/stepsPerWord]
					w >>= (i % stepsPerWord) * pinBits
					s := uint8(w & (0b1<<pinBits - 1))
					yieldOk = yieldOk && yield(s)
				}
				driver.HandleTransferCompleted()
			}
		}
	}
}

const (
	mm             = 6400
	speed          = 40 * mm
	engravingSpeed = 8 * mm
	accel          = 100 * mm
	jerk           = 2400 * mm
)

var (
	params = engrave.Params{
		Millimeter:  mm,
		StrokeWidth: mm / 3,
		StepperConfig: engrave.StepperConfig{
			Speed:          speed,
			EngravingSpeed: engravingSpeed,
			Acceleration:   accel,
			Jerk:           jerk,
			TicksPerSecond: speed,
		},
	}
)

func TestTiming(t *testing.T) {
	f := constant.Font
	const em = 300
	stringer := engrave.NewConstantStringer(f, params, em)
	const seedqr = "011513251154012711900771041507421289190620080870026613431420201617920614089619290300152408010643"
	qrc, err := qr.Encode(seedqr, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	qr, err := engrave.ConstantQR(qrc)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		plan engrave.Engraving
	}{
		{
			"String",
			func(yield func(engrave.Command) bool) {
				engrave.String(f, em, "HELLO WORLD").Engrave(yield)
			},
		},
		{
			"BIP39",
			func(yield func(engrave.Command) bool) {
				stringer.PaddedString(yield, "ZOO", bip39.ShortestWord, bip39.LongestWord)
			},
		},
		{
			"SLIP39",
			func(yield func(engrave.Command) bool) {
				stringer.PaddedString(yield, "ACADEMIC", slip39.ShortestWord, slip39.LongestWord)
			},
		},
		{
			"SeedQR",
			qr.Engrave(params.StepperConfig, params.StrokeWidth, 3),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spline := engrave.PlanEngraving(params.StepperConfig, test.plan)
			want := engrave.ProfileSpline(spline).Pattern
			got := profileSpline(spline)
			if !slices.Equal(got, want) {
				t.Errorf("got profile\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

// profileSpline is like [engrave.ProfileSpline] using a stepper to
// produce the pattern.
func profileSpline(spline bspline.Curve) []uint {
	var time uint
	wasNeedle := false
	var pattern []uint
	var lastt uint
	for s := range runEngraving(nil, spline) {
		needle := (s>>pinNeedle)&0b1 != 0
		if wasNeedle != needle {
			wasNeedle = needle
			pattern = append(pattern, time)
			lastt = time
		}
		time++
	}
	if time != lastt {
		pattern = append(pattern, time)
	}
	return pattern
}
