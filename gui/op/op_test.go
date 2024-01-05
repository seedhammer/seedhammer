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
	bounds := image.Rect(0, 0, 100, 100)
	fb := rgb565.New(bounds)
	mask := image.NewAlpha(bounds)
	ops := new(Ops)
	b.ResetTimer()
	for range b.N {
		ops.Reset()
		ctx := ops.Context()
		Offset(ctx, image.Pt(50, 50))
		ColorOp(ctx, color.NRGBA{})
		ops.Clip(fb.Bounds())
		ops.Draw(fb, mask)
	}
}
