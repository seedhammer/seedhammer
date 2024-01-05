package rgb565

import (
	"math"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	for c := 0; c <= math.MaxUint16; c++ {
		rgb16 := Color{B1: byte(c >> 8), B0: byte(c)}
		r, g, b := RGB565ToRGB888(rgb16)
		got := RGB888ToRGB565(r, g, b)
		if rgb16 != got {
			t.Errorf("%.4x => %.2x, %.2x, %.2x => %.4x", c, r, g, b, got)
		}
	}
}
