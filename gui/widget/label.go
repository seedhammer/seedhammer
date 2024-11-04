package widget

import (
	"image"
	"image/color"
	"math"

	"seedhammer.com/gui/op"
	"seedhammer.com/gui/text"
)

func Labelf(ops op.Ctx, l text.Style, col color.NRGBA, txt string, args ...any) image.Point {
	return Labelwf(ops, l, math.MaxInt, col, txt, args...)
}

func Labelwf(ops op.Ctx, st text.Style, width int, col color.NRGBA, format string, args ...any) image.Point {
	sz := st.Measure(width, format, args...)
	m := st.Face.Metrics()
	lheight := st.LineHeight()
	offy := m.Ascent.Ceil()
	l := &text.Layout{
		MaxWidth: sz.X,
		Style:    st,
	}
	for {
		g, ok := l.Next(format, args...)
		if !ok {
			break
		}
		if g.Rune == '\n' {
			offy += lheight
			continue
		}
		off := image.Pt(g.Dot.Round(), offy)
		op.Offset(ops, off)
		op.GlyphOp(ops, st.Face, g.Rune)
		op.ColorOp(ops, col)
	}
	return sz
}
