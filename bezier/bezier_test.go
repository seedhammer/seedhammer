package bezier

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"testing"
)

func TestBezierAccuracy(t *testing.T) {
	curves := []struct {
		steps uint
		b     Cubic
	}{
		{
			671998,
			Cubic{Point{213333, 170666}, Point{170666, 192000}, Point{277333, 192000}, Point{319999, 170666}},
		},
		{
			0,
			Cubic{Point{0, 0}, Point{0, 0}, Point{0, 0}, Point{0, -71}},
		},
		{
			one,
			Cubic{Point{math.MinInt16, math.MinInt16}, Point{0, 0}, Point{0, 0}, Point{math.MaxInt16, math.MaxInt16}},
		},
	}
	for _, test := range curves {
		verifyBezierSmoothness(t, test.b, test.steps)
	}
}

func FuzzInterpolatorAccuracy(f *testing.F) {
	f.Fuzz(func(t *testing.T, c0x, c0y, c1x, c1y, c2x, c2y, c3x, c3y int, ticks int32) {
		b := Cubic{
			C0: Pt(c0x, c0y),
			C1: Pt(c1x, c1y),
			C2: Pt(c2x, c2y),
			C3: Pt(c3x, c3y),
		}
		verifyBezierSmoothness(t, b, uint(ticks))
	})
}

func verifyBezierSmoothness(t *testing.T, b Cubic, ticks uint) {
	t.Helper()
	var dir [2]int
	pos := b.C0
	if ticks == 0 {
		pos = b.C3
	}
	errors := 10
	step := 0
	reportErr := func(f string, args ...any) {
		t.Helper()
		if errors == 0 {
			return
		}
		err := fmt.Errorf(f, args...)
		t.Errorf("step %d/%d of %v: %v", step, ticks, b, err)
		errors--
	}
	nturns := [2]int{}
	in := new(Interpolator)
	in.Segment(b, ticks)
	for in.Step() {
		step++
		newPos := in.Position()
		step := newPos.Sub(pos)
		pos = newPos
		step.X = max(min(step.X, 1), -1)
		step.Y = max(min(step.Y, 1), -1)

		for axis, step := range []int{step.X, step.Y} {
			if step == 0 {
				continue
			}
			// Detect turns.
			if dir[axis] != 0 && step != dir[axis] {
				nturns[axis]++
			}
			dir[axis] = step
		}
	}
	for axis, nturns := range nturns {
		// A third degree bézier has at most 2 turns.
		if nturns > 2 {
			reportErr("axis %d: %d turns, expected <= 2", axis, nturns)
		}
	}
	if end := b.C3; pos != end {
		reportErr("ended in %v, expected %v", pos, end)
	}
}

// quadBezier represents a second degree Bézier curve.
type quadBezier struct {
	C0, C1, C2 Point
}

func velocityCurve(b Cubic) quadBezier {
	// Derived quadratic velocity curve:
	//
	// 3{(1−t)²(C1−C0)+2t(1−t)(C2−C1)+t²(C3−C2)}
	return quadBezier{
		C0: b.C1.Sub(b.C0).Mul(3),
		C1: b.C2.Sub(b.C1).Mul(3),
		C2: b.C3.Sub(b.C2).Mul(3),
	}
}

func bezierMaxVelocity(b Cubic) uint {
	c := velocityCurve(b)
	v0, v1, v2 := c.C0, c.C1, c.C2
	return uint(max(
		v0.X, v1.X, v2.X,
		-v0.X, -v1.X, -v2.X,
		v0.Y, v1.Y, v2.Y,
		-v0.Y, -v1.Y, -v2.Y,
	))
}

func compareImages(imgPath string, update bool, img image.Image) error {
	if update {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return err
		}
		return os.WriteFile(imgPath, buf.Bytes(), 0o640)
	}
	f, err := os.Open(imgPath)
	if err != nil {
		return err
	}
	want, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	f.Close()
	if w, g := want.Bounds().Size(), img.Bounds().Size(); w != g {
		return fmt.Errorf("golden image bounds mismatch: got %v, want %v", g, w)
	}
	mismatches := 0
	pixels := 0
	width, height := want.Bounds().Dx(), want.Bounds().Dy()
	gotOff := img.Bounds().Min
	for y := range height {
		for x := range width {
			wanta, _, _, _ := want.At(x, y).RGBA()
			want := wanta != 0
			gota, _, _, _ := img.At(gotOff.X+x, gotOff.Y+y).RGBA()
			got := gota != 0
			if want {
				pixels++
			}
			if got != want {
				mismatches++
			}
		}
	}
	if mismatches > 0 {
		return fmt.Errorf("%d/%d pixels golden image mismatches", mismatches, pixels)
	}
	return nil
}
