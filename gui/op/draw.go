package op

import (
	"image"

	"golang.org/x/image/draw"
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
						for y := 0; y < dr.Dy(); y++ {
							poff := dst.PixOffset(dr.Min.X, dr.Min.Y+y)
							pix := dst.Pix[poff:]
							max := dr.Dx()
							_ = pix[max-1]
							for x := 0; x < max; x++ {
								pix[x] = rgb
							}
						}
						return
					}
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
