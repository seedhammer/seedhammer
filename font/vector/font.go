// package font converts an OpenType font into a form usable for engraving.
package vector

import (
	"encoding/binary"
	"image"
	"unicode"
)

type Face struct {
	data []byte
}

func NewFace(data []byte) *Face {
	return &Face{data}
}

type Glyph struct {
	Advance    int8
	Start, End uint16
}

// Segments is an iterator over a glyph's segments
type Segments struct {
	segs []byte
}

func (s *Segments) Next() (Segment, bool) {
	if len(s.segs) == 0 {
		return Segment{}, false
	}
	seg := Segment{
		Op: SegmentOp(s.segs[0]),
		Arg: image.Point{
			X: int(int8(s.segs[1])),
			Y: int(int8(s.segs[2])),
		},
	}
	s.segs = s.segs[3:]
	return seg, true
}

// Segment is like sfnt.Segment but with integer coordinates.
type Segment struct {
	Op  SegmentOp
	Arg image.Point
}

type Metrics struct {
	Ascent, Height int8
}

type SegmentOp uint32

const (
	SegmentOpMoveTo SegmentOp = iota
	SegmentOpLineTo
)

const (
	indexLen      = unicode.MaxASCII
	IndexElemSize = 1 + 2 + 2

	offAscent   = 0
	offHeight   = offAscent + 1
	offIndex    = offHeight + 1
	OffSegments = offIndex + indexLen*IndexElemSize
)

var bo = binary.LittleEndian

func (f *Face) Metrics() Metrics {
	return Metrics{
		Ascent: int8(f.data[offAscent]),
		Height: int8(f.data[offHeight]),
	}
}

func (f *Face) Decode(ch rune) (int, Segments, bool) {
	if int(ch) >= indexLen {
		return 0, Segments{}, false
	}
	index := f.data[offIndex:OffSegments]
	gdata := index[ch*IndexElemSize : (ch+1)*IndexElemSize]
	g := Glyph{
		Advance: int8(gdata[0]),
		Start:   bo.Uint16(gdata[1:]),
		End:     bo.Uint16(gdata[1+2:]),
	}
	segs := f.data[g.Start:g.End]
	return int(g.Advance), Segments{segs: segs}, g.Advance > 0
}
