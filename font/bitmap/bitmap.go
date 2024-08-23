package bitmap

import (
	"encoding/binary"
	"sort"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"seedhammer.com/image/alpha4"
)

type Face struct {
	data []byte
}

func NewFace(data []byte) *Face {
	return &Face{data}
}

type Kern struct {
	R1, R2 uint8
	Kern   fixed.Int26_6
}

type Glyph struct {
	Advance  fixed.Int26_6
	ImageOff uint16
	Rect     alpha4.Rectangle
}

const (
	indexLen      = unicode.MaxASCII
	indexElemSize = 4 + 2 + 4
	KernElemSize  = 2*1 + 4
)

const (
	offAscent   = 0
	offDescent  = offAscent + 4
	offHeight   = offDescent + 4
	offIndex    = offHeight + 4
	offNumKerns = offIndex + indexLen*indexElemSize
	OffKerns    = offNumKerns + 2

	offIndexAdvance  = 0
	offIndexImageOff = offIndexAdvance + 4
	offIndexBounds   = offIndexImageOff + 2
)

var bo = binary.LittleEndian

func (f *Face) Metrics() font.Metrics {
	return font.Metrics{
		Ascent:  fixed.Int26_6(bo.Uint32(f.data[offAscent:])),
		Descent: fixed.Int26_6(bo.Uint32(f.data[offDescent:])),
		Height:  fixed.Int26_6(bo.Uint32(f.data[offHeight:])),
	}
}

func (f *Face) glyphFor(r rune) (Glyph, bool) {
	if r < 0 || int(r) >= indexLen {
		return Glyph{}, false
	}
	index := f.data[offIndex:offNumKerns]
	g := index[r*indexElemSize : (r+1)*indexElemSize]
	return Glyph{
		Advance:  fixed.Int26_6(bo.Uint32(g[offIndexAdvance:])),
		ImageOff: bo.Uint16(g[offIndexImageOff:]),
		Rect: alpha4.Rectangle{
			MinX: int8(g[offIndexBounds+0]),
			MinY: int8(g[offIndexBounds+1]),
			MaxX: int8(g[offIndexBounds+2]),
			MaxY: int8(g[offIndexBounds+3]),
		},
	}, true
}

func (f *Face) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	g, ok := f.glyphFor(r)
	if !ok {
		return 0, false
	}
	return g.Advance, true
}

func (f *Face) Kern(r1, r2 rune) fixed.Int26_6 {
	nkerns := int(bo.Uint16(f.data[offNumKerns:]))
	kerns := f.data[OffKerns : OffKerns+nkerns*KernElemSize]
	i, found := sort.Find(nkerns, func(i int) int {
		kr1, kr2 := rune(kerns[i*KernElemSize+0]), rune(kerns[i*KernElemSize+1])
		if d := int(r1 - kr1); d != 0 {
			return d
		}
		return int(r2 - kr2)
	})
	if !found {
		return 0
	}
	return fixed.Int26_6(bo.Uint32(kerns[i*KernElemSize+2:]))
}

func (f *Face) Glyph(r rune) (alpha4.Image, fixed.Int26_6, bool) {
	g, ok := f.glyphFor(r)
	if !ok {
		return alpha4.Image{}, 0, false
	}
	start := int(g.ImageOff)
	bounds := g.Rect.Rect()
	npixels := bounds.Dx() * bounds.Dy()
	return alpha4.Image{
		Pix:  f.data[start : start+(npixels+1)/2],
		Rect: g.Rect,
	}, g.Advance, true
}
