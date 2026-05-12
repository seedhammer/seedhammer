package op

import (
	"image"
	"image/color"
	"testing"

	"seedhammer.com/image/rgb565"
)

func TestAllocs(t *testing.T) {
	res := testing.Benchmark(BenchmarkOps)
	if a := res.AllocsPerOp(); a > 0 {
		t.Errorf("got %d allocs, expected none", a)
	}
}

func BenchmarkOps(b *testing.B) {
	b.ReportAllocs()
	bounds := image.Rect(0, 0, 100, 100)
	fb := rgb565.New(bounds)
	mask := image.NewAlpha(bounds)
	d := new(Drawer)
	buf := new(Buffer)
	for b.Loop() {
		d.Reset()
		buf.Reset()
		d.Draw(fb, mask,
			Color(buf, color.RGBA{}).
				Offset(image.Pt(50, 50)),
		)
	}
}
