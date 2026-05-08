package op

import (
	"image"
	"image/color"

	"seedhammer.com/font/bitmap"
	"seedhammer.com/image/alpha4"
)

type rgbaUniform struct {
	C color.RGBA
}

type roundedRect struct {
	bounds image.Rectangle
	r      int
}

type roundedOutline struct {
	bounds image.Rectangle
	r, lw  int
}

type glyph struct {
	face *bitmap.Face
	g    alpha4.Image
	r    rune
}

var glyphImage = RegisterParameterizedImage(func() ParameterizedImage {
	img := new(glyph)
	return func(args ImageArguments) image.Image {
		img.face = args.Refs[0].(*bitmap.Face)
		img.r = rune(args.Args[0])
		img.g, _, _ = img.face.Glyph(img.r)
		return img
	}
})

var uniformImage = RegisterParameterizedImage(func() ParameterizedImage {
	u := new(rgbaUniform)
	return func(args ImageArguments) image.Image {
		u.C = colorFromArgs(args)
		return u
	}
})

var roundedOutlineImage = RegisterParameterizedImage(func() ParameterizedImage {
	img := new(roundedOutline)
	return func(args ImageArguments) image.Image {
		img.bounds = args.Bounds
		img.r = int(int32(args.Args[0]))
		img.lw = int(int32(args.Args[1]))
		return img
	}
})

var roundedRectImage = RegisterParameterizedImage(func() ParameterizedImage {
	img := new(roundedRect)
	return func(args ImageArguments) image.Image {
		img.bounds = args.Bounds
		img.r = int(int32(args.Args[0]))
		return img
	}
})

func (img *glyph) ColorModel() color.Model {
	return color.AlphaModel
}

func (img *glyph) Bounds() image.Rectangle {
	return img.g.Bounds()
}

func (img *glyph) At(x, y int) color.Color {
	return img.g.At(x, y)
}

func (img *glyph) RGBA64At(x, y int) color.RGBA64 {
	return img.g.RGBA64At(x, y)
}

func (img *roundedOutline) ColorModel() color.Model {
	return color.AlphaModel
}

func (img *roundedOutline) Bounds() image.Rectangle {
	return img.bounds
}

func (img *roundedOutline) At(x, y int) color.Color {
	a := roundedOutlineAlpha(img.bounds, img.r, img.lw, image.Pt(x, y))
	return color.Alpha{A: a}
}

func (img *roundedOutline) RGBA64At(x, y int) color.RGBA64 {
	a := roundedOutlineAlpha(img.bounds, img.r, img.lw, image.Pt(x, y))
	return color.RGBA64{A: uint16(a)<<8 | uint16(a)}
}

func (img *roundedRect) ColorModel() color.Model {
	return color.AlphaModel
}

func (img *roundedRect) Bounds() image.Rectangle {
	return img.bounds
}

func (img *roundedRect) At(x, y int) color.Color {
	a := roundedRectAlpha(img.bounds, img.r, image.Pt(x, y))
	return color.Alpha{A: a}
}

func (img *roundedRect) RGBA64At(x, y int) color.RGBA64 {
	a := roundedRectAlpha(img.bounds, img.r, image.Pt(x, y))
	return color.RGBA64{A: uint16(a)<<8 | uint16(a)}
}

func (u *rgbaUniform) ColorModel() color.Model {
	return color.RGBAModel
}

func (c *rgbaUniform) Bounds() image.Rectangle {
	return image.Rectangle{image.Point{-1e9, -1e9}, image.Point{1e9, 1e9}}
}

func (c *rgbaUniform) At(x, y int) color.Color { return c.C }

func (c *rgbaUniform) RGBA64At(x, y int) color.RGBA64 {
	r, g, b, a := uint16(c.C.R), uint16(c.C.G), uint16(c.C.B), uint16(c.C.A)
	return color.RGBA64{R: r | r<<8, G: g | g<<8, B: b | b<<8, A: a | a<<8}
}

func (c *rgbaUniform) Opaque() bool {
	return c.C.A == 0xff
}

const px = 1 << 8

//go:inline
func roundedOutlineAlpha(bounds image.Rectangle, r, lw int, p image.Point) uint8 {
	dist := roundedRectDist(bounds, r, p)
	outer := min(dist, px)
	inner := min(-dist-lw, px)
	a := 0xff * (px - max(outer, inner, 0)) / px
	return uint8(a)
}

//go:inline
func roundedRectAlpha(bounds image.Rectangle, r int, p image.Point) uint8 {
	dist := roundedRectDist(bounds, r, p)
	dist = max(min(dist, px), 0)
	return uint8(0xff * (px - dist) / px)
}

//go:inline
func roundedRectDist(bounds image.Rectangle, r int, p image.Point) int {
	b := bounds.Size().Sub(image.Pt(1, 1)).Mul(px).Div(2)
	// Center.
	p = p.Sub(bounds.Min).Mul(px).Sub(b)
	if p.X < 0 {
		p.X = -p.X
	}
	if p.Y < 0 {
		p.Y = -p.Y
	}
	q := p.Sub(b).Add(image.Pt(r, r))
	cq := image.Pt(max(q.X, 0), max(q.Y, 0))
	// Approximate l = √(cq.X²+cq.Y²) using a few iterations of Heron's method.
	S := cq.X*cq.X + cq.Y*cq.Y
	l := 0
	if S > 0 {
		l = 1 + r // Initial guess.
		l = (l + S/l) / 2
		l = (l + S/l) / 2
	}
	return min(max(q.X, q.Y), 0) - r + l
}

func colorFromArgs(args ImageArguments) color.RGBA {
	nrgba := args.Args[0]
	r := nrgba >> 24
	g := (nrgba >> 16) & 0xff
	b := (nrgba >> 8) & 0xff
	a := nrgba & 0xff
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)}
}
