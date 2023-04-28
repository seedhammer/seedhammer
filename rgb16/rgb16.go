// package rgb16 contains an image.Image implementation of a 16-bit
// RGB image.
package rgb16

import (
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
)

type Image struct {
	Pix    []byte
	Stride int
	Rect   image.Rectangle
}

func New(r image.Rectangle) *Image {
	return &Image{
		Pix:    make([]byte, r.Dx()*r.Dy()*2),
		Stride: r.Dx(),
		Rect:   r,
	}
}

func (p *Image) Bounds() image.Rectangle {
	return p.Rect
}

func (p *Image) ColorModel() color.Model {
	return color.RGBAModel
}

func (p *Image) Set(x, y int, c color.Color) {
	if !(image.Point{x, y}).In(p.Rect) {
		return
	}
	bo.PutUint16(p.Pix[p.PixOffset(x, y):], colorToRGB565(c))
}

func (p *Image) PixOffset(x, y int) int {
	off := image.Pt(x, y).Sub(p.Rect.Min)
	return off.Y*p.Stride + off.X*2
}

func (p *Image) At(x, y int) color.Color {
	if !(image.Point{x, y}).In(p.Rect) {
		return color.RGBA{}
	}
	px := bo.Uint16(p.Pix[p.PixOffset(x, y):])
	r, g, b := rgb565ToRGB888(px)
	return color.RGBA{A: 0xff, R: r, G: g, B: b}
}

var bo = binary.BigEndian

func (p *Image) SetRGBA64(x, y int, c color.RGBA64) {
	if !(image.Point{x, y}).In(p.Rect) {
		return
	}
	rgb16 := rgb888ToRGB565(uint8(c.R>>8), uint8(c.G>>8), uint8(c.B>>8))
	bo.PutUint16(p.Pix[p.PixOffset(x, y):], rgb16)
}

func (p *Image) RGBA64At(x, y int) color.RGBA64 {
	if !(image.Point{x, y}).In(p.Rect) {
		return color.RGBA64{}
	}
	px := bo.Uint16(p.Pix[p.PixOffset(x, y):])
	r, g, b := rgb565ToRGB888(px)
	r16 := uint16(r)
	r16 |= r16 << 8
	g16 := uint16(g)
	g16 |= g16 << 8
	b16 := uint16(b)
	b16 |= b16 << 8
	return color.RGBA64{A: 0xffff, R: r16, G: g16, B: b16}
}

func (p *Image) DrawOver(dr image.Rectangle, src image.Image, sp image.Point) {
	dr = dr.Intersect(p.Rect)
	// Optimize special cases.
	switch src := src.(type) {
	case *image.Uniform:
		if src.Opaque() {
			rgb := colorToRGB565(src.C)
			for y := 0; y < dr.Dy(); y++ {
				for x := 0; x < dr.Dx(); x++ {
					po := p.PixOffset(dr.Min.X+x, dr.Min.Y+y)
					bo.PutUint16(p.Pix[po:], rgb)
				}
			}
			return
		}
	case *image.Gray:
		for y := 0; y < dr.Dy(); y++ {
			for x := 0; x < dr.Dx(); x++ {
				col := src.GrayAt(sp.X+x, sp.Y+y)
				po := p.PixOffset(dr.Min.X+x, dr.Min.Y+y)
				rgb16 := rgb888ToRGB565(col.Y, col.Y, col.Y)
				bo.PutUint16(p.Pix[po:], rgb16)
			}
		}
		return
	}

	// General case.
	draw.Draw(p, dr, src, sp, draw.Over)
}

func colorToRGB565(c color.Color) uint16 {
	r, g, b, _ := c.RGBA()
	return rgb888ToRGB565(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

func rgb888ToRGB565(r, g, b uint8) uint16 {
	u16 := uint16(b)>>3 | uint16(g&0xFC)<<3 | uint16(r&0xF8)<<8
	return ((u16 & 0xff) << 8) | (u16 >> 8)
}

func rgb565ToRGB888(rgb uint16) (r, g, b uint8) {
	rgb = ((rgb & 0xff) << 8) | (rgb >> 8)
	r = uint8(rgb>>8) & 0xf8
	r |= r >> 5
	g = uint8(rgb>>3) & 0xfc
	g |= g >> 6
	b = uint8(rgb << 3)
	b |= b | b>>5
	return
}
