package saver

import (
	"image"
	"image/draw"
	"testing"

	"seedhammer.com/image/rgb565"
)

func TestAllocs(t *testing.T) {
	res := testing.Benchmark(func(b *testing.B) {
		scr := new(dummyScreen)
		scr.img = rgb565.New(image.Rectangle{Max: scr.DisplaySize()})
		s := new(State)
		b.StartTimer()
		for range b.N {
			s.Draw(scr)
		}
	})
	if a := res.AllocsPerOp(); a > 0 {
		t.Errorf("got %d allocations (%d bytes), expected %d", a, res.AllocedBytesPerOp(), 0)
	}
}

type dummyScreen struct {
	img *rgb565.Image
	d   image.Rectangle
}

func (s *dummyScreen) DisplaySize() image.Point {
	return image.Pt(200, 200)
}

func (s *dummyScreen) Dirty(r image.Rectangle) error {
	r = r.Intersect(image.Rectangle{Max: s.DisplaySize()})
	s.d = s.d.Union(r)
	return nil
}

func (s *dummyScreen) NextChunk() (draw.RGBA64Image, bool) {
	if s.d.Empty() {
		return nil, false
	}
	s.d = image.Rectangle{}
	return s.img, true
}
