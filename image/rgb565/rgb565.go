// Package rgb565 contains an [image.RGBA64Image] implementation of a 16-bit
// RGB565 image.
package rgb565

import (
	"image"
	"image/color"
	"image/draw"
)

type Image struct {
	Pix    []Color
	Stride int
	Rect   image.Rectangle
}

type Color [2]byte

func New(r image.Rectangle) *Image {
	return &Image{
		Pix:    make([]Color, r.Dx()*r.Dy()),
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
	p.Pix[p.PixOffset(x, y)] = colorToRGB565(c)
}

func (p *Image) PixOffset(x, y int) int {
	off := image.Pt(x, y).Sub(p.Rect.Min)
	return off.Y*p.Stride + off.X
}

func (p *Image) SubImage(r image.Rectangle) image.Image {
	r = r.Intersect(p.Rect)
	if r.Empty() {
		return new(Image)
	}
	start := p.PixOffset(r.Min.X, r.Min.Y)
	end := p.PixOffset(r.Max.X, r.Max.Y-1)
	return &Image{
		Pix:    p.Pix[start:end],
		Stride: p.Stride,
		Rect:   r,
	}
}

func (p *Image) At(x, y int) color.Color {
	if !(image.Point{x, y}).In(p.Rect) {
		return color.RGBA{}
	}
	px := p.Pix[p.PixOffset(x, y)]
	r, g, b := RGB565ToRGB888(px)
	return color.RGBA{A: 0xff, R: r, G: g, B: b}
}

func (p *Image) SetRGBA64(x, y int, c color.RGBA64) {
	if !(image.Point{x, y}).In(p.Rect) {
		return
	}
	rgb16 := RGB888ToRGB565(uint8(c.R>>8), uint8(c.G>>8), uint8(c.B>>8))
	p.Pix[p.PixOffset(x, y)] = rgb16
}

func (p *Image) RGBA64At(x, y int) color.RGBA64 {
	if !(image.Point{x, y}).In(p.Rect) {
		return color.RGBA64{}
	}
	px := p.Pix[p.PixOffset(x, y)]
	r, g, b := RGB565ToRGB888(px)
	r16 := uint16(r)
	r16 |= r16 << 8
	g16 := uint16(g)
	g16 |= g16 << 8
	b16 := uint16(b)
	b16 |= b16 << 8
	return color.RGBA64{A: 0xffff, R: r16, G: g16, B: b16}
}

func (p *Image) Draw(dr image.Rectangle, src image.Image, sp image.Point, op draw.Op) {
	dr = dr.Intersect(p.Rect)
	// Optimize special cases.
	switch src := src.(type) {
	case *image.Uniform:
		if src.Opaque() || op == draw.Src {
			rgb := colorToRGB565(src.C)
			for y := 0; y < dr.Dy(); y++ {
				for x := 0; x < dr.Dx(); x++ {
					p.Pix[p.PixOffset(dr.Min.X+x, dr.Min.Y+y)] = rgb
				}
			}
			return
		}
	case *image.Gray:
		for y := 0; y < dr.Dy(); y++ {
			for x := 0; x < dr.Dx(); x++ {
				col := src.GrayAt(sp.X+x, sp.Y+y)
				po := p.PixOffset(dr.Min.X+x, dr.Min.Y+y)
				p.Pix[po] = RGB888ToRGB565(col.Y, col.Y, col.Y)
			}
		}
		return
	}

	// General case.
	draw.Draw(p, dr, src, sp, op)
}

func colorToRGB565(c color.Color) Color {
	r, g, b, _ := c.RGBA()
	return RGB888ToRGB565(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

func RGB888ToRGB565(r, g, b uint8) Color {
	u16 := uint16(b)>>3 | uint16(g&0xFC)<<3 | uint16(r&0xF8)<<8
	return Color{byte(u16), byte(u16 >> 8)}
}

func RGB565ToRGB888(rgb Color) (r, g, b uint8) {
	c := uint16(rgb[1])<<8 | uint16(rgb[0])
	r = uint8(c>>8) & 0xf8
	r |= r >> 5
	g = uint8(c>>3) & 0xfc
	g |= g >> 6
	b = uint8(c << 3)
	b |= b | b>>5
	return
}
