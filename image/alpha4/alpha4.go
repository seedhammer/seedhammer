// Package alpha4 implements an [image.Alpha] replacement
// with compact 4-bit alpha values.
package alpha4

import (
	"image"
	"image/color"
)

type Image struct {
	Pix  []byte
	Rect Rectangle
}

type Rectangle struct {
	MinX, MinY, MaxX, MaxY int8
}

func New(r Rectangle) *Image {
	npixels := int(r.MaxX-r.MinX) * int(r.MaxY-r.MinY)
	return &Image{
		Pix:  make([]byte, (npixels+1)/2),
		Rect: r,
	}
}

func Rect(r image.Rectangle) Rectangle {
	return Rectangle{
		MinX: int8(r.Min.X),
		MaxX: int8(r.Max.X),
		MinY: int8(r.Min.Y),
		MaxY: int8(r.Max.Y),
	}
}

func (p *Image) ColorModel() color.Model {
	return color.AlphaModel
}

func (p *Image) Bounds() image.Rectangle { return p.Rect.Rect() }

func (p *Image) At(x, y int) color.Color {
	return p.AlphaAt(x, y)
}

func (p *Image) RGBA64At(x, y int) color.RGBA64 {
	a := uint16(p.AlphaAt(x, y).A)
	a |= a << 8
	return color.RGBA64{a, a, a, a}
}

func (p *Image) AlphaAt(x, y int) color.Alpha {
	if !(image.Point{x, y}.In(p.Rect.Rect())) {
		return color.Alpha{}
	}
	i := p.PixOffset(x, y)
	a2 := p.Pix[i/2]
	v := Val(i, a2)
	return color.Alpha{v<<4 | v}
}

func Val(i int, a2 byte) byte {
	return (a2 >> ((i & 0b1) * 4)) & 0b1111
}

func (p *Image) PixOffset(x, y int) int {
	return (y-int(p.Rect.MinY))*int(p.Rect.MaxX-p.Rect.MinX) + (x - int(p.Rect.MinX))
}

func (r Rectangle) Rect() image.Rectangle {
	return image.Rect(int(r.MinX), int(r.MinY), int(r.MaxX), int(r.MaxY))
}

func (p *Image) Set(x, y int, c color.Color) {
	panic("not implemented")
}

func (p *Image) SetAlpha4(x, y int, a byte) {
	if !(image.Point{x, y}).In(p.Rect.Rect()) {
		return
	}
	i := p.PixOffset(x, y)
	a2 := p.Pix[i/2]
	mask := byte(0b1111) << ((^i & 0b1) * 4)
	a2 &= mask
	a <<= ((i & 0b1) * 4)
	p.Pix[i/2] = a2 | a
}

func (p *Image) SetRGBA64(x, y int, c color.RGBA64) {
	a := byte(c.A >> 12)
	p.SetAlpha4(x, y, a)
}
