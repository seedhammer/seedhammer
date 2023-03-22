package rgb16

import (
	"math"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	for c := 0; c <= math.MaxUint16; c++ {
		rgb16 := uint16(c)
		r, g, b := rgb565ToRGB888(rgb16)
		got := rgb888ToRGB565(r, g, b)
		if rgb16 != got {
			t.Errorf("%.4x => %.2x, %.2x, %.2x => %.4x", c, r, g, b, got)
		}
	}
}
