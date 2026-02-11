package gui

import (
	"math"

	"seedhammer.com/bezier"
	"seedhammer.com/bspline"
	"seedhammer.com/engrave"
	"seedhammer.com/gui/layout"
	"seedhammer.com/gui/op"
	"seedhammer.com/gui/widget"
)

func qaEngraveFlow(ctx *Context, ops op.Ctx) {
	p := ctx.Platform
	errs := make(chan error, 1)
	go func() {
		const sz = SquarePlate
		params := p.EngraverParams()
		dims := sz.Dims(params.Millimeter)
		plan := engrave.PlanEngraving(params.StepperConfig,
			qaPlan(params.Millimeter, dims))
		errs <- p.Engrave(false, plan, nil)
	}()
	var eerr string
	var xLoadVals, yLoadVals maxValue
	var maxXLoad, maxYLoad int
	for !ctx.Done {
		lastSt := p.EngraverStatus()
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
		if err := lastSt.Error; eerr == "" && err != nil {
			eerr = err.Error()
		}
		drawQA(ctx, ops, lastSt, maxXLoad, maxYLoad, eerr)
		p.Wakeup()
		ctx.Frame()
		select {
		case err := <-errs:
			eerr = err.Error()
		default:
		}
	}
}

func drawQA(ctx *Context, ops op.Ctx, st EngraverStatus, maxXLoad, maxYLoad int, eerr string) {
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

func qaPlan(mm int, dims bezier.Point) engrave.Engraving {
	return func(yield func(engrave.Command) bool) {
		margin := 1 * mm
		mp := bezier.Pt(margin, margin)
		yield(engrave.Move(mp))
		const (
			repeats = 10
		)
		center := dims.Div(2)
		topRight := bezier.Pt(int(dims.X)-margin, margin)
		bottomLeft := bezier.Pt(margin, int(dims.Y)-margin)
		bottomRight := bezier.Pt(int(dims.X)-margin, int(dims.Y)-margin)
		rect := []bezier.Point{
			mp,
			topRight,
			bottomRight,
			bottomLeft,
		}
		diag := []bezier.Point{
			bottomLeft, topRight,
		}
		// This is the inlined result of
		//
		//	const segments = 50
		//  radius := int(center.X) - margin
		//  circle := circleBSpline(segments, bezier.Point{}, radius)
		circle := []bezier.Point{
			{X: 265600, Y: 0},
			{X: 265600, Y: 0},
			{X: 265600, Y: 0},
			{X: 264943, Y: 7201},
			{X: 264216, Y: 33327},
			{X: 257455, Y: 68497},
			{X: 243152, Y: 108519},
			{X: 221958, Y: 147112},
			{X: 195748, Y: 180655},
			{X: 170814, Y: 204137},
			{X: 144083, Y: 223836},
			{X: 114838, Y: 240157},
			{X: 83848, Y: 252648},
			{X: 51518, Y: 261167},
			{X: 18380, Y: 265564},
			{X: -15049, Y: 265773},
			{X: -48240, Y: 261792},
			{X: -80671, Y: 253681},
			{X: -111829, Y: 241571},
			{X: -141225, Y: 225649},
			{X: -168392, Y: 206170},
			{X: -192905, Y: 183439},
			{X: -214378, Y: 157812},
			{X: -232450, Y: 129714},
			{X: -246944, Y: 99501},
			{X: -257203, Y: 67976},
			{X: -262409, Y: 41418},
			{X: -265134, Y: 13548},
			{X: -265229, Y: -9429},
			{X: -263901, Y: -33793},
			{X: -258157, Y: -65821},
			{X: -247003, Y: -99236},
			{X: -233694, Y: -127757},
			{X: -214702, Y: -157454},
			{X: -194315, Y: -182048},
			{X: -169720, Y: -205246},
			{X: -142598, Y: -224858},
			{X: -113778, Y: -240839},
			{X: -80805, Y: -253718},
			{X: -45974, Y: -262322},
			{X: -2994, Y: -266268},
			{X: 41843, Y: -263055},
			{X: 82246, Y: -253449},
			{X: 111924, Y: -241239},
			{X: 131989, Y: -230092},
			{X: 148732, Y: -219797},
			{X: 168446, Y: -206105},
			{X: 193278, Y: -183670},
			{X: 220126, Y: -150105},
			{X: 242697, Y: -109688},
			{X: 257406, Y: -68697},
			{X: 264201, Y: -33411},
			{X: 264945, Y: -7216},
			{X: 265600, Y: 0},
			{X: 265600, Y: 0},
			{X: 265600, Y: 0},
		}
		cont := true
		for {
			for range repeats {
				for _, c := range rect {
					cont = cont && yield(engrave.Move(c))
				}
			}
			for range repeats {
				for _, c := range diag {
					cont = cont && yield(engrave.Move(c))
				}
			}
			for range repeats {
				cont = cont && yield(engrave.Move(circle[0].Add(center)))
				for _, c := range circle {
					c = c.Add(center)
					cont = cont && yield(engrave.ControlPoint(false, c))
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

func circleBSpline(segments int, center bezier.Point, radius int) []bezier.Point {
	var ctrls []bezier.Point
	for i := range segments + 1 {
		angle := 2 * math.Pi * float64(i) / float64(segments)
		p := center.Add(bezier.Pt(
			int(float64(radius)*math.Cos(angle)),
			int(float64(radius)*math.Sin(angle)),
		))
		ctrls = append(ctrls, p)
	}
	knots, err := bspline.InterpolatePoints(ctrls)
	if err != nil {
		panic(err)
	}
	return knots
}
