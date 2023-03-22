// package font converts an OpenType font into a form usable for engraving.
package font

import (
	"math"
	"unicode"

	"golang.org/x/image/math/f32"
)

type Face struct {
	Metrics Metrics
	// Index maps a character to its segment range.
	Index [unicode.MaxASCII]Glyph
	// Segments encoded as opcode, args, opcode, args...
	Segments []uint32
}

type Glyph struct {
	Advance    float32
	Start, End uint16
}

// Segment is like sfnt.Segment but with float32 coordinates.
type Segment struct {
	Op   SegmentOp
	Args [3]f32.Vec2
}

type Metrics struct {
	Ascent, Height float32
}

type SegmentOp uint32

const (
	SegmentOpMoveTo SegmentOp = iota
	SegmentOpLineTo
	SegmentOpQuadTo
	SegmentOpCubeTo
)

func (f *Face) Decode(ch rune) (float32, []Segment, bool) {
	if int(ch) >= len(f.Index) {
		return 0, nil, false
	}
	glyph := f.Index[ch]
	enc := f.Segments[glyph.Start:glyph.End]
	var segs []Segment
	decPoint := func() f32.Vec2 {
		x := math.Float32frombits(enc[0])
		y := math.Float32frombits(enc[1])
		enc = enc[2:]
		return f32.Vec2{x, y}
	}
	for len(enc) > 0 {
		seg := Segment{
			Op: SegmentOp(enc[0]),
		}
		enc = enc[1:]
		switch seg.Op {
		case SegmentOpMoveTo, SegmentOpLineTo:
			seg.Args[0] = decPoint()
		case SegmentOpQuadTo:
			seg.Args[0] = decPoint()
			seg.Args[1] = decPoint()
		case SegmentOpCubeTo:
			seg.Args[0] = decPoint()
			seg.Args[1] = decPoint()
			seg.Args[2] = decPoint()
		}
		segs = append(segs, seg)
	}
	return glyph.Advance, segs, true
}
