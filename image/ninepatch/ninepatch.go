// package ninepatch contains an image.Image implementation of stretchable
// images in 9-patch format.
package ninepatch

import (
	"image"
	"image/color"

	"seedhammer.com/gui/op"
)

type Template struct {
	inner   image.Rectangle
	padding image.Rectangle
	src     image.RGBA64Image
	srcb    image.Rectangle
	img     op.Image
}

func New(src image.RGBA64Image) *Template {
	inner := nineBounds(src, 0, 0)
	b := src.Bounds()
	padding := nineBounds(src, b.Max.Y-1, b.Max.X-1)
	srcb := src.Bounds()
	np := &Template{
		inner:   inner,
		padding: padding,
		src:     src,
		srcb: image.Rectangle{
			Min: srcb.Min.Add(image.Pt(1, 1)),
			Max: srcb.Max.Sub(image.Pt(1, 1)),
		},
	}
	np.img = op.RegisterParameterizedImage(np.at)
	return np
}

func nineBounds(img image.RGBA64Image, row, col int) image.Rectangle {
	var res image.Rectangle
	b := img.Bounds()
	for x := b.Min.X + 1; x < b.Max.X-1; x++ {
		c := img.RGBA64At(x, row)
		_, _, _, a := c.RGBA()
		if a != 0 {
			res.Min.X = x
			res.Max.X = x
			break
		}
	}
	for y := b.Min.Y + 1; y < b.Max.Y-1; y++ {
		c := img.RGBA64At(col, y)
		_, _, _, a := c.RGBA()
		if a != 0 {
			res.Min.Y = y
			res.Max.Y = y
			break
		}
	}
	for x := b.Max.X - 2; x > res.Min.X; x-- {
		c := img.RGBA64At(x, row)
		_, _, _, a := c.RGBA()
		if a != 0 {
			res.Max.X = x + 1
			break
		}
	}
	for y := b.Max.Y - 2; y > res.Min.Y; y-- {
		c := img.RGBA64At(col, y)
		_, _, _, a := c.RGBA()
		if a != 0 {
			res.Max.Y = y + 1
			break
		}
	}
	return res
}

func (n *Template) Padding() (int, int, int, int) {
	b := n.src.Bounds()
	return n.padding.Min.Y, b.Max.X - n.padding.Max.X,
		b.Max.Y - n.padding.Max.Y, n.padding.Min.X
}

func (n *Template) Bounds(r image.Rectangle) image.Rectangle {
	top, end, bottom, start := n.Padding()
	r.Min.X -= start
	r.Max.X += end
	r.Min.Y -= top
	r.Max.Y += bottom
	return r
}

func (n *Template) Add(ops op.Ctx, r image.Rectangle, mask bool) image.Rectangle {
	r = n.Bounds(r)
	op.ParamImageOp(ops, n.img, mask, r, nil, nil)
	return r
}

func (n *Template) at(args op.ImageArguments, x, y int) color.RGBA64 {
	x -= args.Bounds.Min.X
	y -= args.Bounds.Min.Y
	x += n.srcb.Min.X
	y += n.srcb.Min.Y
	x = adjust(x, args.Bounds.Dx(), n.inner.Min.X, n.inner.Max.X, n.srcb.Min.X, n.srcb.Max.X)
	y = adjust(y, args.Bounds.Dy(), n.inner.Min.Y, n.inner.Max.Y, n.srcb.Min.Y, n.srcb.Max.Y)

	return n.src.RGBA64At(x, y)
}

func adjust(v, sz, innerMin, innerMax, srcMin, srcMax int) int {
	limit := 0
	len := innerMin
	if v >= limit+len {
		limit += len
		innersz := innerMax - innerMin
		startsz := innerMin - srcMin
		endsz := srcMax - innerMax
		outerw := sz - startsz - endsz
		stretch := outerw - innersz
		len := stretch / 2
		if v >= limit+len {
			limit += len
			len := innersz
			if v >= limit+len {
				limit += len
				len := stretch - stretch/2
				if v >= limit+len {
					limit += len
					v = v - limit + innerMax
				} else {
					v = innerMax - 1
				}
			} else {
				v = v - limit + innerMin
			}
		} else {
			v = innerMin
		}
	}
	return v
}
