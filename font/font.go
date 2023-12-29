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
	Segments []uint32
}

type Glyph struct {
	Advance    int
	Start, End uint16
}

// Segment is like sfnt.Segment but with integer coordinates.
type Segment struct {
	Op   SegmentOp
	Args [3]image.Point
}

type Metrics struct {
	Ascent, Height int
}

type SegmentOp uint32

const (
	SegmentOpMoveTo SegmentOp = iota
	SegmentOpLineTo
)

func (f *Face) Decode(ch rune) (int, []Segment, bool) {
	if int(ch) >= len(f.Index) {
		return 0, nil, false
	}
	glyph := f.Index[ch]
	if glyph == (Glyph{}) {
		return 0, nil, false
	}
	enc := f.Segments[glyph.Start:glyph.End]
	var segs []Segment
	decPoint := func() image.Point {
		x := int(int32(enc[0]))
		y := int(int32(enc[1]))
		enc = enc[2:]
		return image.Pt(x, y)
	}
	for len(enc) > 0 {
		seg := Segment{
			Op: SegmentOp(enc[0]),
		}
		enc = enc[1:]
		switch seg.Op {
		case SegmentOpMoveTo, SegmentOpLineTo:
			seg.Args[0] = decPoint()
		}
		segs = append(segs, seg)
	}
	return glyph.Advance, segs, true
}
