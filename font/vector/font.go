// package font converts an OpenType font into a form usable for engraving.
package vector

import (
	"encoding/binary"
	"unicode"

	"seedhammer.com/bezier"
)

type Face struct {
	data []byte
}

type Knot struct {
	Ctrl bezier.Point
	Line bool
}

func NewFace(data []byte) *Face {
	return &Face{data}
}

type Glyph struct {
	Advance    int
	Start, End int
}

// UniformBSpline is an iterator over a glyph's uniform spline.
type UniformBSpline struct {
	spline []byte
}

func (s *UniformBSpline) Next() (Knot, bool) {
	if len(s.spline) == 0 {
		return Knot{}, false
	}
	k := Knot{
		Line: s.spline[0] != 0,
		Ctrl: bezier.Point{
			X: int(int16(bo.Uint16(s.spline[1:]))),
			Y: int(int16(bo.Uint16(s.spline[3:]))),
		},
	}
	s.spline = s.spline[5:]
	return k, true
}

type Metrics struct {
	Ascent, Height int
}

const (
	indexLen      = unicode.MaxASCII
	IndexElemSize = 2 + 2 + 2

	offAscent  = 0
	offHeight  = offAscent + 2
	offIndex   = offHeight + 2
	OffSplines = offIndex + indexLen*IndexElemSize
)

var bo = binary.LittleEndian

func (f *Face) Metrics() Metrics {
	return Metrics{
		Ascent: int(bo.Uint16(f.data[offAscent:])),
		Height: int(bo.Uint16(f.data[offHeight:])),
	}
}

func (f *Face) Decode(ch rune) (int, UniformBSpline, bool) {
	if int(ch) >= indexLen {
		return 0, UniformBSpline{}, false
	}
	index := f.data[offIndex:OffSplines]
	gdata := index[ch*IndexElemSize : (ch+1)*IndexElemSize]
	g := Glyph{
		Advance: int(bo.Uint16(gdata[0:])),
		Start:   int(bo.Uint16(gdata[2:])),
		End:     int(bo.Uint16(gdata[2+2:])),
	}
	spline := f.data[g.Start:g.End]
	return g.Advance, UniformBSpline{spline: spline}, g.Advance > 0
}
