// package font converts an OpenType font into a form usable for engraving.
package font

import (
	"image"
	"unicode"
)

type Face struct {
	Metrics Metrics
	// Index maps a character to its segment range.
	Index [unicode.MaxASCII]Glyph
	// Segments encoded as opcode, args, opcode, args...
	Segments []byte
}

type Glyph struct {
	Advance    int
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
	Ascent, Height int
}

type SegmentOp uint32

const (
	SegmentOpMoveTo SegmentOp = iota
	SegmentOpLineTo
)

func (f *Face) Decode(ch rune) (int, Segments, bool) {
	if int(ch) >= len(f.Index) {
		return 0, Segments{}, false
	}
	glyph := f.Index[ch]
	if glyph == (Glyph{}) {
		return 0, Segments{}, false
	}
	enc := f.Segments[glyph.Start:glyph.End]
	return glyph.Advance, Segments{segs: enc}, true
}
