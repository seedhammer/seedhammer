package widget

import (
	"image"
	"image/color"
	"math"

	"seedhammer.com/gui/op"
	"seedhammer.com/gui/text"
)

func Label(ops op.Ctx, st text.Style, col color.RGBA, txt string) image.Point {
	return Labelwf(ops, st, math.MaxInt, col, "%s", txt)
}

func Labelw(ops op.Ctx, st text.Style, width int, col color.RGBA, txt string) image.Point {
	return Labelwf(ops, st, width, col, "%s", txt)
}

func Labelf(ops op.Ctx, l text.Style, col color.RGBA, txt string, args ...any) image.Point {
	return Labelwf(ops, l, math.MaxInt, col, txt, args...)
}

func Labelwf(ops op.Ctx, st text.Style, width int, col color.RGBA, format string, args ...any) image.Point {
	m := st.Face.Metrics()
	lheight := st.LineHeight()
	l := &text.Layout{
		MaxWidth: width,
		Style:    st,
	}
	minx, maxx := math.MaxInt, math.MinInt
	y := m.Ascent.Ceil()
	ops.Begin()
	for {
		g, ok := l.Next(format, args...)
		if !ok {
			break
		}
		minx = min(minx, g.Dot.Floor())
		maxx = max(maxx, (g.Dot + g.Advance).Ceil())
		if g.Rune == '\n' {
			y += lheight
			continue
		}
		off := image.Pt(g.Dot.Round(), y)
		op.Offset(ops, off)
		op.GlyphOp(ops, st.Face, g.Rune)
		op.ColorOp(ops, col)
	}
	// Adjust the text body to the origin.
	op.Position(ops, ops.End(), image.Pt(-minx, 0))
	return image.Pt(maxx-minx, y+m.Descent.Ceil())
}
