package affine

import (
	"math"
	"testing"

	"golang.org/x/image/math/f32"
)

func eq(p1, p2 f32.Vec2) bool {
	tol := 1e-5
	dx, dy := p2[0]-p1[0], p2[1]-p1[1]
	return math.Abs(math.Sqrt(float64(dx*dx+dy*dy))) < tol
}

func TestTransformRotateAround(t *testing.T) {
	p := f32.Vec2{-1, -1}
	pt := Transform(Mul(Offsetting(f32.Vec2{1, 1}), Rotating(-math.Pi/2), Offsetting(f32.Vec2{-1, -1})), p)
	target := f32.Vec2{-1, 3}
	if !eq(pt, target) {
		t.Errorf("Rotate not as expected, got %v, want %v", pt, target)
	}
}
