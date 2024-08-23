package widget

import (
	"image"
	"image/color"
	"math"

	"seedhammer.com/gui/op"
	"seedhammer.com/gui/text"
)

func Label(ops op.Ctx, l text.Style, col color.NRGBA, txt string) image.Point {
	return LabelW(ops, l, math.MaxInt, col, txt)
}

func LabelW(ops op.Ctx, l text.Style, width int, col color.NRGBA, txt string) image.Point {
	sz := l.Measure(width, txt)
	lines := l.Layout(sz.X, txt)
	for _, line := range lines {
		(&op.TextOp{
			Face:          l.Face,
			Dot:           image.Pt(line.Dot.X, line.Dot.Y),
			Txt:           line.Text,
			LetterSpacing: l.LetterSpacing,
		}).Add(ops)
		op.ColorOp(ops, col)
	}
	return sz
}
