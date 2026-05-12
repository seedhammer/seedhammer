package widget

import (
	"image"
	"image/color"
	"math"

	"seedhammer.com/gui/op"
	"seedhammer.com/gui/text"
)

func Label(buf *op.Buffer, st text.Style, col color.RGBA, txt string) (op.Op, image.Point) {
	return Labelwf(buf, st, math.MaxInt, col, "%s", txt)
}

func Labelw(buf *op.Buffer, st text.Style, width int, col color.RGBA, txt string) (op.Op, image.Point) {
	return Labelwf(buf, st, width, col, "%s", txt)
}

func Labelf(buf *op.Buffer, l text.Style, col color.RGBA, txt string, args ...any) (op.Op, image.Point) {
	return Labelwf(buf, l, math.MaxInt, col, txt, args...)
}

func Labelwf(buf *op.Buffer, st text.Style, width int, col color.RGBA, format string, args ...any) (op.Op, image.Point) {
	m := st.Face.Metrics()
	lheight := st.LineHeight()
	l := &text.Layout{
		MaxWidth: width,
		Style:    st,
	}
	minx, maxx := math.MaxInt, math.MinInt
	y := m.Ascent.Ceil()
	var glyphs op.Op
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
		glyphs = op.Layer(
			glyphs,
			op.Compose(
				op.Color(buf, col),
				op.Glyph(buf, st.Face, g.Rune),
			).Offset(off),
		)
	}
	// Adjust the text body to the origin.
	glyphs = glyphs.Offset(image.Pt(-minx, 0))
	return glyphs, image.Pt(maxx-minx, y+m.Descent.Ceil())
}
