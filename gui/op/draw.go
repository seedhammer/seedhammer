package op

import (
	"image"

	"golang.org/x/image/draw"
	"seedhammer.com/image/paletted"
	"seedhammer.com/image/rgb565"
)

func drawMask(dst draw.Image, dr image.Rectangle, src image.Image, pos image.Point, mask image.Image, maskOff image.Point, op draw.Op) {
	// Optimize special cases.
	switch dst := dst.(type) {
	case *rgb565.Image:
		if mask == nil {
			switch src := src.(type) {
			case *genImage:
				switch src.gen.id {
				case uniformImage.id:
					col := colorFromArgs(src.ImageArguments)
					if col.A == 255 || op == draw.Src {
						rgb := rgb565.RGB888ToRGB565(col.R, col.G, col.B)
						maxx := dr.Dx()
						dstPix := dst.Pix
						for y := dr.Min.Y; y < dr.Max.Y; y++ {
							poff := dst.PixOffset(dr.Min.X, y)
							dstPix := dstPix[poff : poff+maxx]
							for x := 0; x < maxx; x++ {
								dstPix[x] = rgb
							}
						}
						return
					}
				}
			case *paletted.Image:
				switch op {
				case draw.Over:
					p := src.Palette
					maxx := dr.Dx()
					dstPix := dst.Pix
					srcPix := src.Pix
					for y := 0; y < dr.Dy(); y++ {
						dstOff := dst.PixOffset(dr.Min.X, dr.Min.Y+y)
						srcOff := src.PixOffset(pos.X, pos.Y+y)
						dstPix := dstPix[dstOff : dstOff+maxx]
						srcPix := srcPix[srcOff : srcOff+maxx]
						for x := 0; x < maxx; x++ {
							srcCol, a := p.At(srcPix[x])
							dstCol := dstPix[x]
							dstPix[x] = blendRGB888(dstCol, srcCol, a)
						}
					}
					return
				}
			}
			dst.Draw(dr, src, pos, op)
			return
		}
	}

	// General case.
	draw.DrawMask(
		dst, dr,
		src, pos,
		mask, maskOff,
		op,
	)
}

func blendRGB888(d, s rgb565.Color, a uint8) rgb565.Color {
	dr, dg, db := rgb565.RGB565ToRGB888(d)
	sr, sg, sb := rgb565.RGB565ToRGB888(s)
	a1 := uint16(255 - a)
	r, g, b := uint8(uint16(dr)*a1/255)+sr, uint8(uint16(dg)*a1/255)+sg, uint8(uint16(db)*a1/255)+sb
	return rgb565.RGB888ToRGB565(r, g, b)
}
