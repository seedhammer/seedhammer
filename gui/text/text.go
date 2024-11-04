package text

import (
	"fmt"
	"image"
	"iter"
	"strconv"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/math/fixed"
	"seedhammer.com/font/bitmap"
)

type Glyph struct {
	Rune    rune
	Dot     fixed.Int26_6
	Advance fixed.Int26_6
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

func (l Style) Measure(maxWidth int, format string, args ...any) image.Point {
	l.Alignment = AlignStart
	m := l.Face.Metrics()
	asc := m.Ascent
	lheight := l.LineHeight()
	dims := image.Point{Y: asc.Ceil()}
	for line := range l.Layout(maxWidth, format, args...) {
		dims.X = max(dims.X, (line.Dot + line.Advance).Ceil())
		if line.Rune == '\n' {
			dims.Y += lheight
		}
	}
	dims.Y += m.Descent.Ceil()
	return dims
}

// formatter is a simpler fmt.Sprintf that doesn't allocate.
type formatter struct {
	idx    int
	auxIdx int
	argIdx int
	buf    [20]byte
	bufLen int
	state  formatterState
}

type formatterState int

const (
	formatFormat formatterState = iota
	formatArg
	formatBuf
)

func (f *formatter) next(format string) rune {
	c, n := utf8.DecodeRuneInString(format[f.idx:])
	f.idx += n
	return c
}

func (f *formatter) doFloat(r byte, prec int, args []any) {
	switch arg := args[f.argIdx].(type) {
	case float32:
		f.bufLen = len(strconv.AppendFloat(buf[:0], float64(arg), r, prec, 32))
	case float64:
		f.bufLen = len(strconv.AppendFloat(buf[:0], arg, r, prec, 64))
	default:
		panic("unsupported argument type")
	}
	if f.bufLen > len(f.buf) {
		panic("float format string overflows buffer")
	}
	copy(f.buf[:], buf[:f.bufLen])
	f.argIdx++
	f.state = formatBuf
	f.auxIdx = 0
}

// TODO: get rid of this hack when TinyGo can eliminate the
// allocation for strconv.Append* functions.
var buf [20]byte

func (f *formatter) doInt(r byte, prec, pad int, args []any) {
	base := 2
	switch r {
	case 'x':
		base = 16
	case 'd':
		base = 10
	}
	switch arg := args[f.argIdx].(type) {
	case int:
		f.bufLen = len(strconv.AppendInt(buf[:0], int64(arg), base))
	case uint32:
		f.bufLen = len(strconv.AppendUint(buf[:0], uint64(arg), base))
	default:
		panic("unsupported argument type")
	}
	if f.bufLen > len(f.buf) {
		panic("float format string overflows buffer")
	}
	copy(f.buf[:], buf[:f.bufLen])
	f.argIdx++
	// Extend with zeroes.
	if prec != -1 && f.bufLen < prec {
		n := prec - f.bufLen
		buf := f.buf[:prec]
		copy(buf[n:], buf[:f.bufLen])
		for i := range n {
			buf[i] = '0'
		}
		f.bufLen = prec
	}
	// Pad with spaces.
	if pad != -1 && f.bufLen < pad {
		n := pad - f.bufLen
		buf := f.buf[:pad]
		copy(buf[n:], buf[:f.bufLen])
		for i := range n {
			buf[i] = ' '
		}
		f.bufLen = pad
	}
	f.state = formatBuf
	f.auxIdx = 0
}

func (f *formatter) Next(format string, args ...any) (rune, bool) {
	for {
		switch f.state {
		case formatFormat:
			if len(format[f.idx:]) == 0 {
				return 0, false
			}
			if r := f.next(format); r != '%' {
				return r, true
			}
			if len(format[f.idx:]) == 0 {
				panic("missing format verb")
			}
			start := f.idx
			r := f.next(format)
			prec := -1
			pad := -1
			dot := r == '.'
			if dot {
				if len(format[f.idx:]) == 0 {
					panic("missing precision")
				}
				start = f.idx
				r = f.next(format)
			}
			for '0' <= r && r <= '9' {
				if len(format[f.idx:]) == 0 {
					panic("missing format verb")
				}
				r = f.next(format)
			}
			if start < f.idx-1 {
				v, err := strconv.ParseUint(format[start:f.idx-1], 10, 32)
				if err != nil {
					panic(err)
				}
				if dot {
					prec = int(v)
				} else {
					pad = int(v)
				}
			}
			switch r := byte(r); r {
			case '%':
				return '%', true
			case 'f', 'F':
				if prec == -1 {
					prec = 6
				}
				fallthrough
			case 'g', 'G':
				f.doFloat(r, prec, args)
			case 'b', 'x', 'd':
				f.doInt(r, prec, pad, args)
			case 's':
				f.state = formatArg
				f.auxIdx = 0
			default:
				panic("unsupported format verb")
			}
		case formatArg:
			a := args[f.argIdx].(string)
			a = a[f.auxIdx:]
			if len(a) == 0 {
				f.argIdx++
				f.state = formatFormat
				continue
			}
			r, n := utf8.DecodeRuneInString(a)
			f.auxIdx += n
			return r, true
		case formatBuf:
			buf := f.buf[f.auxIdx:f.bufLen]
			if len(buf) == 0 {
				f.state = formatFormat
				continue
			}
			r, n := utf8.DecodeRune(buf)
			f.auxIdx += n
			return r, true
		}
	}
}

func (l Style) Layout(maxWidth int, format string, args ...any) iter.Seq[Glyph] {
	// Enable printf vet warnings.
	if false {
		_ = fmt.Sprintf(format, args...)
	}
	return func(yield func(Glyph) bool) {
		var cursor struct {
			prevR     rune
			formatter formatter
		}
		next := func() (rune, fixed.Int26_6, bool) {
			r, ok := cursor.formatter.Next(format, args...)
			if !ok {
				return 0, 0, false
			}
			a, ok := l.Face.GlyphAdvance(r)
			if !ok {
				cursor.prevR = -1
			}
			if cursor.prevR >= 0 {
				a += l.Face.Kern(cursor.prevR, r)
			}
			cursor.prevR = r
			a += fixed.I(l.LetterSpacing)
			return r, a, true
		}
		prevCur := cursor
		checkpoint := cursor
		width := fixed.I(0)
		runes := 0
		for {
			// Compute line extent in runes and width.
			lineRunes := 0
			lineWidth := fixed.I(0)
			cursor = checkpoint
			spaceBreak := false
			breakWidth := fixed.I(0)
			eof := false
			for {
				if runes > 0 && width.Ceil() > maxWidth {
					break
				}
				r, a, ok := next()
				if !ok {
					eof = true
					lineRunes = runes
					lineWidth = width
					break
				}
				space := unicode.IsSpace(r)
				if space || (lineRunes == 0 && (width+a).Ceil() > maxWidth) {
					spaceBreak = space
					breakWidth = a
					lineRunes = runes
					lineWidth = width
				}
				runes++
				width += a
				if r == '\n' {
					break
				}
			}
			runes -= lineRunes
			width -= lineWidth
			// Rewind and yield glyphs.
			checkpoint = cursor
			cursor = prevCur
			dot := fixed.I(0)
			switch l.Alignment {
			case AlignCenter:
				dot = (fixed.I(maxWidth) - lineWidth) / 2
			case AlignEnd:
				dot = fixed.I(maxWidth) - lineWidth
			}
			for lineRunes > 0 {
				lineRunes--
				r, a, ok := next()
				if !ok {
					panic("underflow")
				}
				g := Glyph{
					Rune:    r,
					Dot:     dot,
					Advance: a,
				}
				dot += a
				if !yield(g) {
					return
				}
			}
			if lineRunes == 0 && !eof {
				g := Glyph{
					Rune: '\n',
					Dot:  dot,
				}
				if !yield(g) {
					return
				}
			}
			// Skip line-breaking space.
			if spaceBreak {
				runes--
				width -= breakWidth
				next()
			}
			prevCur = cursor
			if eof {
				return
			}
		}
	}
}
