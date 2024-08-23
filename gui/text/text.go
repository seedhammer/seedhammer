package text

import (
	"image"
	"iter"
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
	Face            *bitmap.Face
	Alignment       Alignment
	LineHeightScale float32
	LetterSpacing   int
}

type Alignment int

const (
	AlignStart Alignment = iota
	AlignEnd
	AlignCenter
)

func (l Style) LineHeight() int {
	lheight := l.Face.Metrics().Height.Ceil()
	if l.LineHeightScale != 0 {
		lheight = int(float32(lheight) * l.LineHeightScale)
	}
	return lheight
}

func (l Style) Measure(maxWidth int, txt string) image.Point {
	var dims image.Point
	for line := range l.Layout(maxWidth, txt) {
		dims.X = max(dims.X, line.Width)
		dims.Y = line.Dot.Y
	}
	m := l.Face.Metrics()
	dims.Y += m.Descent.Ceil()
	return dims
}

func (l Style) Layout(maxWidth int, txt string) iter.Seq[Line] {
	return func(yield func(Line) bool) {
		prevC := rune(-1)
		adv := fixed.I(0)
		wordAdv := fixed.I(0)
		wordIdx := 0
		prev := 0
		idx := 0
		m := l.Face.Metrics()
		asc := m.Ascent
		lheight := l.LineHeight()
		doty := asc.Ceil()
		endLine := func() bool {
			prevC = -1
			dotx := 0
			width := adv.Ceil()
			switch l.Alignment {
			case AlignCenter:
				dotx = (maxWidth - width) / 2
			case AlignEnd:
				dotx = maxWidth - width
			}
			l := Line{
				Text:  txt[prev:idx],
				Width: width,
				Dot:   image.Pt(dotx, doty),
			}
			wordIdx = 0
			wordAdv = 0
			doty += lheight
			return yield(l)
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
				if !endLine() {
					return
				}
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
			if !endLine() {
				return
			}
		}
	}
}
