package text

import (
	"fmt"
	"image"
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

func (s Style) Measure(maxWidth int, format string, args ...any) image.Point {
	s.Alignment = AlignStart
	m := s.Face.Metrics()
	asc := m.Ascent
	lheight := s.LineHeight()
	dims := image.Point{Y: asc.Ceil()}
	l := &Layout{
		MaxWidth: maxWidth,
		Style:    s,
	}
	for {
		g, ok := l.Next(format, args...)
		if !ok {
			break
		}
		dims.X = max(dims.X, (g.Dot + g.Advance).Ceil())
		if g.Rune == '\n' {
			dims.Y += lheight
		}
	}
	dims.Y += m.Descent.Ceil()
	return dims
}

// Formatter is a poor man's fmt.Sprintf that doesn't allocate.
// TODO: It can be replaced when something like
// https://github.com/golang/go/issues/62653 is implemented in
// the Go implementation.
type Formatter struct {
	idx    int
	auxIdx int
	argIdx int
	buf    [33]byte
	bufLen uint8
	state  formatterState
}

type formatterState uint8

const (
	formatFormat formatterState = iota
	formatArg
	formatBuf
)

func (f *Formatter) next(format string) rune {
	c, n := utf8.DecodeRuneInString(format[f.idx:])
	f.idx += n
	return c
}

func (f *Formatter) doFloat(r byte, prec int, args []any) {
	var n int
	switch arg := args[f.argIdx].(type) {
	case float32:
		n = len(strconv.AppendFloat(buf[:0], float64(arg), r, prec, 32))
	case float64:
		n = len(strconv.AppendFloat(buf[:0], arg, r, prec, 64))
	default:
		panic("unsupported argument type")
	}
	if n > len(f.buf) {
		panic("float format string overflows buffer")
	}
	copy(f.buf[:], buf[:n])
	f.bufLen = uint8(n)
	f.argIdx++
	f.state = formatBuf
	f.auxIdx = 0
}

// TODO: get rid of this hack when TinyGo can eliminate the
// allocation for strconv.Append* functions.
var buf [65]byte

func (f *Formatter) doChar(arg any) {
	var r rune
	switch arg := arg.(type) {
	case rune:
		r = arg
	case uint:
		r = rune(arg)
	case uint32:
		r = rune(arg)
	case uint64:
		r = rune(arg)
	case int64:
		r = rune(arg)
	default:
		panic("unsupported argument type")
	}
	buf := utf8.AppendRune(buf[:0], r)
	if len(buf) > len(f.buf) {
		panic("float format string overflows buffer")
	}
	f.bufLen = uint8(len(buf))
	copy(f.buf[:], buf)
	f.state = formatBuf
	f.auxIdx = 0
	f.argIdx++
}

func (f *Formatter) doInt(r byte, prec, pad int, args []any) {
	base := 2
	switch r {
	case 'x':
		base = 16
	case 'd':
		base = 10
	case 'b':
		base = 2
	}
	var bufLen int
	switch arg := args[f.argIdx].(type) {
	case int:
		bufLen = len(strconv.AppendInt(buf[:0], int64(arg), base))
	case uint:
		bufLen = len(strconv.AppendUint(buf[:0], uint64(arg), base))
	case uint32:
		bufLen = len(strconv.AppendUint(buf[:0], uint64(arg), base))
	case int32:
		bufLen = len(strconv.AppendInt(buf[:0], int64(arg), base))
	case uint64:
		bufLen = len(strconv.AppendUint(buf[:0], arg, base))
	case int64:
		bufLen = len(strconv.AppendInt(buf[:0], arg, base))
	default:
		panic("unsupported argument type")
	}
	if bufLen > len(f.buf) {
		panic("float format string overflows buffer")
	}
	copy(f.buf[:], buf[:bufLen])
	f.argIdx++
	// Extend with zeroes.
	if prec != -1 && bufLen < prec {
		n := prec - bufLen
		buf := f.buf[:prec]
		copy(buf[n:], buf[:bufLen])
		for i := range n {
			buf[i] = '0'
		}
		bufLen = prec
	}
	// Pad with spaces.
	if pad != -1 && bufLen < pad {
		n := pad - bufLen
		buf := f.buf[:pad]
		copy(buf[n:], buf[:bufLen])
		for i := range n {
			buf[i] = ' '
		}
		bufLen = pad
	}
	f.bufLen = uint8(bufLen)
	f.state = formatBuf
	f.auxIdx = 0
}

func (f *Formatter) Next(format string, args ...any) (rune, bool) {
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
			case 'c':
				f.doChar(args[f.argIdx])
			default:
				panic("unsupported format verb")
			}
		case formatArg:
			var r rune
			var n int
			switch arg := args[f.argIdx].(type) {
			case string:
				r, n = utf8.DecodeRuneInString(arg[f.auxIdx:])
			case []byte:
				r, n = utf8.DecodeRune(arg[f.auxIdx:])
			default:
				panic("unsupported string type")
			}
			if n == 0 {
				f.argIdx++
				f.state = formatFormat
				continue
			}
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

type Layout struct {
	MaxWidth int
	Style    Style

	state      layoutState
	cursor     state
	checkpoint state
	width      fixed.Int26_6
	runes      int
	spaceBreak bool
	breakWidth fixed.Int26_6
	eof        bool
	dot        fixed.Int26_6
	lineRunes  int
	lineWidth  fixed.Int26_6
}

type layoutState int

const (
	layoutInit layoutState = iota
	layoutRunes
	layoutEOL
)

// TODO: Convert to iterator when TinyGo can move its allocations to the stack.
func (l *Layout) Next(format string, args ...any) (Glyph, bool) {
	// Enable printf vet warnings.
	if false {
		_ = fmt.Sprintf(format, args...)
	}
	for {
		switch l.state {
		case layoutInit:
			l.init(format, args)
		case layoutRunes:
			if l.lineRunes == 0 {
				l.state = layoutEOL
				break
			}
			l.lineRunes--
			r, a, ok := l.cursor.next(l.Style, format, args)
			if !ok {
				panic("underflow")
			}
			g := Glyph{
				Rune:    r,
				Dot:     l.dot,
				Advance: a,
			}
			l.dot += a
			return g, true
		case layoutEOL:
			if l.eof {
				return Glyph{}, false
			}
			g := Glyph{
				Rune: '\n',
				Dot:  l.dot,
			}
			// Skip line-breaking space.
			if l.spaceBreak {
				l.runes--
				l.width -= l.breakWidth
				l.cursor.next(l.Style, format, args)
			}
			l.state = layoutInit
			return g, true
		}
	}
}

func (l *Layout) init(format string, args []any) {
	// Compute line extent in runes and width.
	l.lineRunes = 0
	l.lineWidth = fixed.I(0)
	l.spaceBreak = false
	l.breakWidth = fixed.I(0)
	for {
		if l.runes > 0 && l.width.Ceil() > l.MaxWidth {
			break
		}
		r, a, ok := l.checkpoint.next(l.Style, format, args)
		if !ok {
			l.eof = true
			l.lineRunes = l.runes
			l.lineWidth = l.width
			break
		}
		space := unicode.IsSpace(r)
		if space || (l.lineRunes == 0 && (l.width+a).Ceil() > l.MaxWidth) {
			l.spaceBreak = space
			l.breakWidth = a
			l.lineRunes = l.runes
			l.lineWidth = l.width
		}
		l.runes++
		l.width += a
		if r == '\n' {
			break
		}
	}
	l.runes -= l.lineRunes
	l.width -= l.lineWidth
	l.dot = fixed.I(0)
	switch l.Style.Alignment {
	case AlignCenter:
		l.dot = (fixed.I(l.MaxWidth) - l.lineWidth) / 2
	case AlignEnd:
		l.dot = fixed.I(l.MaxWidth) - l.lineWidth
	}
	l.state = layoutRunes
}

type state struct {
	prevR     rune
	formatter Formatter
}

func (s *state) next(l Style, format string, args []any) (rune, fixed.Int26_6, bool) {
	r, ok := s.formatter.Next(format, args...)
	if !ok {
		return 0, 0, false
	}
	a, ok := l.Face.GlyphAdvance(r)
	if !ok {
		s.prevR = -1
	}
	if s.prevR >= 0 {
		a += l.Face.Kern(s.prevR, r)
	}
	s.prevR = r
	a += fixed.I(l.LetterSpacing)
	return r, a, true
}
