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
						rgb := rgb565.FromRGB888(col.R, col.G, col.B)
						maxx := dr.Dx()
						dstPix := dst.Pix
						for y := dr.Min.Y; y < dr.Max.Y; y++ {
							poff := dst.PixOffset(dr.Min.X, y)
							dstPix := dstPix[poff : poff+maxx]
							for x := range dstPix {
								dstPix[x] = rgb
							}
						}
						return
					}
				}
			case *paletted.Image:
				switch op {
				case draw.Over:
					drawAlphaPalettedOver(dst, dr, src, pos)
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
			case roundedRectImage.id:
				switch src := src.(type) {
				case *genImage:
					switch src.gen.id {
					case uniformImage.id:
						switch op {
						case draw.Over:
							bounds := mask.ImageArguments.Bounds
							cornerRadius := int(int32(mask.Args[0]))
							src := colorFromArgs(src.ImageArguments)
							drawRRectUniformOver(dst, dr, src, bounds.Sub(maskOff), cornerRadius)
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

func drawRRectUniformOver(dst *rgb565.Image, dr image.Rectangle, src color.RGBA, bounds image.Rectangle, cornerRadius int) {
	maxx := dr.Dx()
	dstPix := dst.Pix
	sr := uint32(src.R>>3) * 0xff
	sg := uint32(src.G>>2) * 0xff
	sb := uint32(src.B>>3) * 0xff
	sa := uint32(src.A)
	for y := 0; y < dr.Dy(); y++ {
		dstOff := dst.PixOffset(dr.Min.X, dr.Min.Y+y)
		dstPix := dstPix[dstOff : dstOff+maxx]
		for x, dcol := range dstPix {
			a8 := roundedRectAlpha(bounds, cornerRadius, image.Pt(x, y))
			a := uint32(a8)
			const div = 0xff * 0xff
			a1 := div - a*sa
			dr, dg, db := splitRGB565(dcol)
			rr := (sr*a + dr*a1) / div
			rg := (sg*a + dg*a1) / div
			rb := (sb*a + db*a1) / div
			res := combineRGB565(rr, rg, rb)
			dstPix[x] = res
		}
	}
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
			dr, dg, db := splitRGB565(dcol)
			rr := (sr*a + dr*a1) / div
			rg := (sg*a + dg*a1) / div
			rb := (sb*a + db*a1) / div
			res := combineRGB565(rr, rg, rb)
			dstPix[x] = res
		}
	}
}

func drawAlphaPalettedOver(dst *rgb565.Image, dr image.Rectangle, src *paletted.Image, pos image.Point) {
	p := src.Palette
	maxx := dr.Dx()
	dstPix := dst.Pix
	srcPix := src.Pix
	for y := 0; y < dr.Dy(); y++ {
		dstOff := dst.PixOffset(dr.Min.X, dr.Min.Y+y)
		dstPix := dstPix[dstOff : dstOff+maxx]
		srcOff := src.PixOffset(pos.X, pos.Y+y)
		srcPix := srcPix[srcOff : srcOff+maxx]
		for x, sp := range srcPix {
			srcCol, a := p.At(sp)
			a1 := uint32(255 - a)
			dr, dg, db := splitRGB565(dstPix[x])
			sr, sg, sb := splitRGB565(srcCol)
			rr, rg, rb := dr*a1/255+sr, dg*a1/255+sg, db*a1/255+sb
			dstPix[x] = combineRGB565(rr, rg, rb)
		}
	}
}

func combineRGB565(r, g, b uint32) rgb565.Color {
	return rgb565.Color(r<<11 | g<<5 | b)
}

func splitRGB565(c rgb565.Color) (uint32, uint32, uint32) {
	dr := uint32(c >> 11)
	dg := uint32((c >> 5) & 0b111111)
	db := uint32(c & 0b11111)
	return dr, dg, db
}
