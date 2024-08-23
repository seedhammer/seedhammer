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

func Labelwf(ops op.Ctx, l text.Style, width int, col color.NRGBA, format string, args ...any) image.Point {
	sz := l.Measure(width, format, args...)
	m := l.Face.Metrics()
	lheight := l.LineHeight()
	offy := m.Ascent.Ceil()
	for g := range l.Layout(sz.X, format, args...) {
		if g.Rune == '\n' {
			offy += lheight
			continue
		}
		off := image.Pt(g.Dot.Round(), offy)
		op.Offset(ops, off)
		op.GlyphOp(ops, l.Face, g.Rune)
		op.ColorOp(ops, col)
	}
	return sz
}
