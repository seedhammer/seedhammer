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
	sr := uint32(src.R>>3) * 0xff
	sg := uint32(src.G>>2) * 0xff
	sb := uint32(src.B>>3) * 0xff
	sa := uint32(src.A)
	for y := 0; y < dr.Dy(); y++ {
		dstOff := dst.PixOffset(dr.Min.X, dr.Min.Y+y)
		dstPix := dstPix[dstOff : dstOff+maxx]
		maskOff := mask.PixOffset(maskOff.X, maskOff.Y+y)
		for x, dcol := range dstPix {
			i := maskOff + x
			a := uint32(alpha4.Val(i, maskPix[i/2]))
			const div = 0xf * 0xff
			a1 := div - a*sa
			dr := uint32(dcol >> 11)
			dg := uint32((dcol >> 5) & 0b111111)
			db := uint32(dcol & 0b11111)
			rr := (sr*a + dr*a1) / div
			rg := (sg*a + dg*a1) / div
			rb := (sb*a + db*a1) / div
			res := rgb565.Color(rr<<11 | rg<<5 | rb)
			dstPix[x] = res
		}
	}
}
