package gui

import (
	"image"
	"math"

	"seedhammer.com/backup"
	"seedhammer.com/engrave"
	"seedhammer.com/gui/layout"
	"seedhammer.com/gui/op"
	"seedhammer.com/gui/widget"
)

func debugEngraveFlow(ctx *Context, ops op.Ctx) {
	p := ctx.Platform
	quit := make(chan struct{})
	defer close(quit)
	errs := make(chan error, 1)
	e, err := p.Engraver()
	var statuses <-chan EngraverStatus
	if err != nil {
		errs <- err
	} else {
		statuses = e.Status()
		go func() {
			defer e.Close()
			const sz = backup.SquarePlate
			plan := debugPlan(p.EngraverParams().Millimeter, sz.Dims())
			errs <- e.Engrave(sz, false, plan, quit)
		}()
	}
	var lastSt EngraverStatus
	var eerr string
	var xLoadVals, yLoadVals maxValue
	var maxXLoad, maxYLoad int
	for {
		select {
		case err := <-errs:
			eerr = err.Error()
		case lastSt = <-statuses:
			if lastSt.XSpeed >= lastSt.StallSpeed {
				maxXLoad = xLoadVals.Put(lastSt.XLoad)
			}
			xload := lastSt.XLoad
			yload := lastSt.YLoad
			if lastSt.XSpeed < lastSt.StallSpeed {
				xload = 0
			}
			if lastSt.YSpeed < lastSt.StallSpeed {
				yload = 0
			}
			maxXLoad = xLoadVals.Put(xload)
			maxYLoad = yLoadVals.Put(yload)
		}
		p.Wakeup()
		drawDebug(ctx, ops, lastSt, maxXLoad, maxYLoad, eerr)
		ctx.Frame()
	}
}

func drawDebug(ctx *Context, ops op.Ctx, st EngraverStatus, maxXLoad, maxYLoad int, eerr string) {
	dims := ctx.Platform.DisplaySize()
	th := &descriptorTheme
	op.ColorOp(ops, th.Background)
	txtsz := layoutTitle(ctx, ops, dims.X, th.Text, "FOREVER, LAURA!")
	r := layout.Rectangle{Max: dims}
	r = r.Shrink(txtsz.Max.Y, 8, 0, 8)
	widget.Labelwf(ops.Begin(), ctx.Styles.body, r.Dx(), th.Text,
		"X Speed: %dmm/s\nY Speed: %dmm/s\nX Load: %d (now: %d)\nY Load: %d (now: %d)\nX Stalls: %d\nY Stalls: %d\nError: %s",
		st.XSpeed, st.YSpeed, maxXLoad, st.XLoad, maxYLoad, st.YLoad, st.XStalls, st.YStalls, eerr)
	op.Position(ops, ops.End(), r.Min)
}

func debugPlan(mm int, dims image.Point) engrave.Plan {
	return func(yield func(engrave.Command) bool) {
		margin := 1 * mm
		mp := image.Pt(margin, margin)
		dims := dims.Mul(mm)
		yield(engrave.Move(mp))
		const (
			repeats  = 10
			segments = 16
		)
		center := dims.Div(2)
		radius := dims.X/2 - margin
		rect := []engrave.Command{
			engrave.Move(mp),
			engrave.Move(image.Pt(dims.X-margin, margin)),
			engrave.Move(dims.Sub(mp)),
			engrave.Move(image.Pt(margin, dims.Y-margin)),
		}
		for {
			for range repeats {
				for _, c := range rect {
					if !yield(c) {
						return
					}
				}
			}
			for range repeats {
				for i := range segments {
					angle := 2 * math.Pi * float64(i) / segments
					p := image.Point{
						X: center.X + int(float64(radius)*math.Cos(angle)),
						Y: center.Y + int(float64(radius)*math.Sin(angle)),
					}
					if !yield(engrave.Move(p)) {
						return
					}
				}
			}
		}
	}
}

type maxValue struct {
	values [50]int
	index  int
}

func (m *maxValue) Put(v int) int {
	m.values[m.index] = v
	m.index = (m.index + 1) % len(m.values)
	mval := 0
	for _, v := range m.values {
		mval = max(v, mval)
	}
	return mval
}
