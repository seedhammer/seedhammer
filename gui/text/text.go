package text

import (
	"image"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/math/fixed"
	"seedhammer.com/font/bitmap"
)

type Line struct {
	Text  string
	Width int
	Dot   image.Point
}

type Style struct {
	Face          *bitmap.Face
	Alignment     Alignment
	LineHeight    float32
	LetterSpacing int
}

type Alignment int

const (
	AlignStart Alignment = iota
	AlignEnd
	AlignCenter
)

func (l Style) Layout(maxWidth int, txt string) ([]Line, image.Point) {
	var lines []Line
	prevC := rune(-1)
	adv := fixed.I(0)
	wordAdv := fixed.I(0)
	wordIdx := 0
	maxAdv := 0
	prev := 0
	idx := 0
	m := l.Face.Metrics()
	asc, desc := m.Ascent, m.Descent
	lheight := m.Height.Ceil()
	if l.LineHeight != 0 {
		lheight = int(float32(lheight) * l.LineHeight)
	}
	doty := asc.Ceil()
	endLine := func() {
		prevC = -1
		if a := adv.Ceil(); a > maxAdv {
			maxAdv = a
		}
		lines = append(lines, Line{
			Text:  txt[prev:idx],
			Width: adv.Ceil(),
			Dot:   image.Pt(0, doty),
		})
		wordIdx = 0
		wordAdv = 0
		doty += lheight
	}
	for idx < len(txt) {
		c, n := utf8.DecodeRuneInString(txt[idx:])
		a, ok := l.Face.GlyphAdvance(c)
		if !ok {
			prevC = -1
			idx += n
			continue
		}
		softnl := unicode.IsSpace(c)
		if softnl {
			wordIdx = idx
			wordAdv = adv
		}
		if prevC >= 0 {
			a += l.Face.Kern(prevC, c)
		}
		a += fixed.I(l.LetterSpacing)
		if c == '\n' || (idx > prev && (adv+a).Ceil() > maxWidth) {
			if wordIdx > 0 {
				idx = wordIdx
				adv = wordAdv
				_, n = utf8.DecodeRuneInString(txt[idx:])
				softnl = true
			}
			endLine()
			prev = idx
			idx += n
			adv = a
			if softnl {
				// Skip space or newline.
				prev += n
				adv = 0
			}
			continue
		}
		idx += n
		prevC = c
		adv += a
	}
	idx = len(txt)
	if prev < idx {
		endLine()
	}
	for i, line := range lines {
		switch l.Alignment {
		case AlignCenter:
			lines[i].Dot.X = (maxAdv - line.Width) / 2
		case AlignEnd:
			lines[i].Dot.X = maxAdv - line.Width
		}
	}
	return lines, image.Point{
		X: maxAdv,
		Y: doty - lheight + desc.Ceil(),
	}
}
