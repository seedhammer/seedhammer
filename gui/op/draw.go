package op

import (
	"image"

	"image/color"

	"golang.org/x/image/draw"
	"seedhammer.com/image/alpha4"
	"seedhammer.com/image/paletted"
	"seedhammer.com/image/rgb565"
)

func drawMask(dst draw.Image, dr image.Rectangle, src image.Image, pos image.Point, mask image.Image, maskOff image.Point, op draw.Op) {
	// Optimize special cases.
	switch dst := dst.(type) {
	case *rgb565.Image:
		switch mask := mask.(type) {
		case nil:
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
							for x := range maxx {
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
						dstPix := dstPix[dstOff : dstOff+maxx]
						srcOff := src.PixOffset(pos.X, pos.Y+y)
						srcPix := srcPix[srcOff : srcOff+maxx]
						for x := range maxx {
							srcCol, a := p.At(srcPix[x])
							dstCol := dstPix[x]
							// The following call is inlined manually:
							//
							// dstPix[x] = blend565(dstCol, srcCol, a)
							{
								dr, dg, db := rgb565.RGB565ToRGB888(dstCol)
								sr, sg, sb := rgb565.RGB565ToRGB888(srcCol)
								a1 := uint16(255 - a)
								r, g, b := uint8(uint16(dr)*a1/255)+sr, uint8(uint16(dg)*a1/255)+sg, uint8(uint16(db)*a1/255)+sb
								dstPix[x] = rgb565.RGB888ToRGB565(r, g, b)
							}
						}
					}
					return
				}
			}
			dst.Draw(dr, src, pos, op)
			return
		case *genImage:
			switch mask.gen.id {
			case glyphImage.id:
				switch src := src.(type) {
				case *genImage:
					switch src.gen.id {
					case uniformImage.id:
						switch op {
						case draw.Over:
							face, r := decodeGlyphImage(mask.ImageArguments)
							mask, _, _ := face.Glyph(r)
							src := colorFromArgs(src.ImageArguments)
							drawAlphaUniformOver(dst, dr, src, &mask, maskOff)
							return
						}
					}
				}
			}
		case *alpha4.Image:
			switch src := src.(type) {
			case *genImage:
				switch src.gen.id {
				case uniformImage.id:
					switch op {
					case draw.Over:
						src := colorFromArgs(src.ImageArguments)
						drawAlphaUniformOver(dst, dr, src, mask, maskOff)
						return
					}
				}
			}
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

func drawAlphaUniformOver(dst *rgb565.Image, dr image.Rectangle, src color.RGBA, mask *alpha4.Image, maskOff image.Point) {
	maxx := dr.Dx()
	dstPix := dst.Pix
	maskPix := mask.Pix
	for y := 0; y < dr.Dy(); y++ {
		dstOff := dst.PixOffset(dr.Min.X, dr.Min.Y+y)
		dstPix := dstPix[dstOff : dstOff+maxx]
		maskOff := mask.PixOffset(maskOff.X, maskOff.Y+y)
		for x := range maxx {
			i := maskOff + x
			a := alpha4.Val(i, maskPix[i/2])
			a16 := uint16(a)
			srcCol := color.RGBA{
				R: uint8(uint16(src.R) * a16 / 255),
				G: uint8(uint16(src.G) * a16 / 255),
				B: uint8(uint16(src.B) * a16 / 255),
				A: uint8(uint16(src.A) * a16 / 255),
			}
			dstCol := dstPix[x]
			// The following call is inlined manually for performance:
			//
			// dstPix[x] = blend888(dstCol, srcCol)
			{
				dr, dg, db := rgb565.RGB565ToRGB888(dstCol)
				a1 := uint16(255 - srcCol.A)
				r, g, b := uint8(uint16(dr)*a1/255)+srcCol.R, uint8(uint16(dg)*a1/255)+srcCol.G, uint8(uint16(db)*a1/255)+srcCol.B
				dstPix[x] = rgb565.RGB888ToRGB565(r, g, b)
			}
		}
	}
}

func blend888(d rgb565.Color, s color.RGBA) rgb565.Color {
	dr, dg, db := rgb565.RGB565ToRGB888(d)
	a1 := uint16(255 - s.A)
	r, g, b := uint8(uint16(dr)*a1/255)+s.R, uint8(uint16(dg)*a1/255)+s.G, uint8(uint16(db)*a1/255)+s.B
	return rgb565.RGB888ToRGB565(r, g, b)
}

func blend565(d, s rgb565.Color, a uint8) rgb565.Color {
	dr, dg, db := rgb565.RGB565ToRGB888(d)
	sr, sg, sb := rgb565.RGB565ToRGB888(s)
	a1 := uint16(255 - a)
	r, g, b := uint8(uint16(dr)*a1/255)+sr, uint8(uint16(dg)*a1/255)+sg, uint8(uint16(db)*a1/255)+sb
	return rgb565.RGB888ToRGB565(r, g, b)
}
