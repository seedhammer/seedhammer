// Package paletted implements a [image.Paletted] replacement with
// a more compact encoding.
package paletted

import (
	"image"
	"image/color"

	"seedhammer.com/image/rgb565"
)

// Image is like [image.Paletted] with a more efficient
// palette.
type Image struct {
	Pix     []uint8
	Rect    Rectangle
	Palette Palette
}

type Rectangle struct {
	MinX, MinY int16
	MaxX, MaxY int16
}

// Palette is a list of RGBA colors encoded as a byte slice.
type Palette []byte

func (p Palette) At(i uint8) (rgb565.Color, uint8) {
	col := p[int(i)*3 : (int(i)+1)*3]
	return rgb565.Color{B0: col[0], B1: col[1]}, col[2]
}

func (p *Image) Bounds() image.Rectangle {
	return p.Rect.Rect()
}

func (r Rectangle) Rect() image.Rectangle {
	return image.Rect(int(r.MinX), int(r.MinY), int(r.MaxX), int(r.MaxY))
}

func (p *Image) PixOffset(x, y int) int {
	r := p.Rect
	return (y-int(r.MinY))*int(r.MaxX-r.MinX) + (x-int(r.MinX))*1
}

func (p *Image) At(x, y int) color.Color {
	return p.RGBA64At(x, y)
}

func (p *Image) ColorModel() color.Model { panic("not implemented") }

func (p *Image) RGBA64At(x, y int) color.RGBA64 {
	if len(p.Palette) == 0 {
		return color.RGBA64{}
	}
	if !(image.Point{X: x, Y: y}.In(p.Bounds())) {
		return color.RGBA64{}
	}
	i := p.PixOffset(x, y)
	c, a := p.Palette.At(p.Pix[i])
	r, g, b := rgb565.RGB565ToRGB888(c)
	rgba := color.RGBA{R: r, G: g, B: b, A: a}
	r16, g16, b16, a16 := rgba.RGBA()
	return color.RGBA64{
		uint16(r16),
		uint16(g16),
		uint16(b16),
		uint16(a16),
	}
}
