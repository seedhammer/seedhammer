package widget

import (
	"image/color"
	"testing"

	"seedhammer.com/font/poppins"
	"seedhammer.com/gui/op"
	"seedhammer.com/gui/text"
)

func BenchmarkLayout(b *testing.B) {
	bytes := any([]byte{'a', 'b'})
	var ops op.Ops
	for range b.N {
		format := "₿ %.2d%% %s %.8x %c %s %.32b"
		args := []any{120, "Hi", 0xcafe, 'B', bytes, 0b11101100}
		ops.Reset()
		Labelf(ops.Context(), text.Style{Face: poppins.Bold10}, color.NRGBA{}, format, args...)
	}
}

func TestAllocs(t *testing.T) {
	res := testing.Benchmark(BenchmarkLayout)
	allocs := res.AllocsPerOp()
	if allocs > 0 {
		t.Errorf("Layout allocates %d, expected none", allocs)
	}
}
