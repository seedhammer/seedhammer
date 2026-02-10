package layout

import (
	"image"
)

type Rectangle image.Rectangle

func (r Rectangle) Shrink(top, end, bottom, start int) Rectangle {
	r2 := Rectangle{
		Min: r.Min.Add(image.Pt(start, top)),
		Max: r.Max.Sub(image.Pt(end, bottom)),
	}
	if r2.Min.X > r.Max.X {
		r2.Min.X = r.Max.X
	}
	if r2.Max.X < r.Min.X {
		r2.Max.X = r.Min.X
	}
	if r2.Min.Y > r.Max.Y {
		r2.Min.Y = r.Max.Y
	}
	if r2.Max.Y < r.Min.Y {
		r2.Max.Y = r.Min.Y
	}
	return r2
}

func (r Rectangle) Center(sz image.Point) image.Point {
	off := r.Size().Sub(sz).Div(2)
	return r.Min.Add(off)
}

func (r Rectangle) E(sz image.Point) image.Point {
	return image.Point{
		X: r.Max.X - sz.X,
		Y: (r.Max.Y + r.Min.Y - sz.Y) / 2,
	}
}

func (r Rectangle) N(sz image.Point) image.Point {
	return image.Point{
		X: (r.Max.X + r.Min.X - sz.X) / 2,
		Y: r.Min.Y,
	}
}

func (r Rectangle) W(sz image.Point) image.Point {
	return image.Point{
		X: r.Min.X,
		Y: (r.Max.Y + r.Min.Y - sz.Y) / 2,
	}
}

func (r Rectangle) S(sz image.Point) image.Point {
	return image.Point{
		X: (r.Max.X + r.Min.X - sz.X) / 2,
		Y: r.Max.Y - sz.Y,
	}
}

func (r Rectangle) NW(sz image.Point) image.Point {
	return r.Min
}

func (r Rectangle) NE(sz image.Point) image.Point {
	return image.Point{
		X: r.Max.X - sz.X,
		Y: r.Min.Y,
	}
}

func (r Rectangle) SW(sz image.Point) image.Point {
	return image.Point{
		X: r.Min.X,
		Y: r.Max.Y - sz.Y,
	}
}

func (r Rectangle) SE(sz image.Point) image.Point {
	return r.Max.Sub(sz)
}

func (r Rectangle) Dx() int {
	return image.Rectangle(r).Dx()
}

func (r Rectangle) Dy() int {
	return image.Rectangle(r).Dy()
}

func (r Rectangle) Size() image.Point {
	return image.Rectangle(r).Size()
}

func (r Rectangle) CutTop(height int) (top Rectangle, bottom Rectangle) {
	cuty := min(r.Min.Y+height, r.Max.Y)
	return r.cutY(cuty)
}

func (r Rectangle) CutBottom(height int) (top Rectangle, bottom Rectangle) {
	cuty := max(r.Max.Y-height, r.Min.Y)
	return r.cutY(cuty)
}

func (r Rectangle) cutY(cuty int) (top Rectangle, bottom Rectangle) {
	top = Rectangle(image.Rect(r.Min.X, r.Min.Y, r.Max.X, cuty))
	bottom = Rectangle(image.Rect(r.Min.X, cuty, r.Max.X, r.Max.Y))
	return top, bottom
}

func (r Rectangle) CutStart(width int) (start Rectangle, end Rectangle) {
	cutx := min(r.Min.X+width, r.Max.X)
	return r.cutX(cutx)
}

func (r Rectangle) CutEnd(width int) (start Rectangle, end Rectangle) {
	cuty := max(r.Max.X-width, r.Min.X)
	return r.cutX(cuty)
}

func (r Rectangle) cutX(cutx int) (start Rectangle, end Rectangle) {
	start = Rectangle(image.Rect(r.Min.X, r.Min.Y, cutx, r.Max.Y))
	end = Rectangle(image.Rect(cutx, r.Min.Y, r.Max.X, r.Max.Y))
	return start, end
}

type Align struct {
	Size image.Point
}

func (h *Align) Add(sz image.Point) image.Point {
	if sz.Y > h.Size.Y {
		h.Size.Y = sz.Y
	}
	if sz.X > h.Size.X {
		h.Size.X = sz.X
	}
	return sz
}

func (h *Align) X(sz image.Point) int {
	return (h.Size.X - sz.X) / 2
}

func (h *Align) Y(sz image.Point) int {
	return (h.Size.Y - sz.Y) / 2
}
