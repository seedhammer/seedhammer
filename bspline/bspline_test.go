package bspline

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"seedhammer.com/bezier"
)

var (
	update = flag.Bool("update", false, "update golden files")
	dump   = flag.String("dump", "", "dump SVG files to directory")
)

func TestInterpolator(t *testing.T) {
	splines := [][]bezier.Point{
		{
			bezier.Pt(1, 1),
			bezier.Pt(1, 1),
			bezier.Pt(1, 1),
			bezier.Pt(1, 1),
		},
		{
			bezier.Pt(10, 10),
			bezier.Pt(10, 10),
			bezier.Pt(50, 20),
			bezier.Pt(50, 20),
		},
		{
			bezier.Pt(20, 40),
			bezier.Pt(50, 100),
			bezier.Pt(140, -50),
			bezier.Pt(100, 30),
		},
		{
			bezier.Pt(50, 100),
			bezier.Pt(200, 30),
			bezier.Pt(40, 30),
			bezier.Pt(140, 100),
		},
	}
	img := image.NewAlpha(image.Rectangle{Max: image.Pt(150, 150)})
	for _, s := range splines {
		rasterizeUniformBSpline(img, s)
	}
	p := filepath.Join("testdata", "curves.png")
	if err := compareImages(p, *update, img); err != nil {
		t.Error(err)
	}
}

func TestMeasureBSpline(t *testing.T) {
	tests := []struct {
		spline []Knot
		bounds Bounds
	}{
		{
			[]Knot{
				{Ctrl: bezier.Pt(0, 0)},
				{Ctrl: bezier.Pt(0, 0)},
				{Ctrl: bezier.Pt(0, 0)},
				{T: 1, Ctrl: bezier.Pt(1, 0)},
				{T: 1, Ctrl: bezier.Pt(100, 10)},
				{T: 1, Ctrl: bezier.Pt(10, 30)},
				{T: 1, Ctrl: bezier.Pt(60, 30), Engrave: true},
				{T: 1, Ctrl: bezier.Pt(50, 10)},
				{T: 1, Ctrl: bezier.Pt(0, 0)},
				{Ctrl: bezier.Pt(0, 0)},
				{Ctrl: bezier.Pt(0, 0)},
			},
			Bounds{Min: bezier.Pt(10, 10), Max: bezier.Pt(100, 30)},
		},
	}
	for _, test := range tests {
		attrs := Measure(slices.Values(test.spline))
		if got := attrs.Bounds; got != test.bounds {
			t.Errorf("measured spline bounds to %+v, want %+v", got, test.bounds)
		}
	}
}

func TestKinematics(t *testing.T) {
	tests := []struct {
		spline       []Knot
		velocity     []bezier.Point
		acceleration []bezier.Point
		jerk         []bezier.Point
	}{
		{
			[]Knot{
				{Ctrl: bezier.Pt(0, 100), T: 0},
				{Ctrl: bezier.Pt(0, 100), T: 0},
				{Ctrl: bezier.Pt(0, 100), T: 0},
				{Ctrl: bezier.Pt(0, 100), T: 1},
				{Ctrl: bezier.Pt(80, 100), T: 1},
				{Ctrl: bezier.Pt(80, 100), T: 1},
				{Ctrl: bezier.Pt(80, 100), T: 0},
				{Ctrl: bezier.Pt(80, 100), T: 0},
			},
			[]bezier.Point{{X: 0, Y: 0}, {X: 0, Y: 0}, {X: 80, Y: 0}, {X: 0, Y: 0}, {X: 0, Y: 0}},
			[]bezier.Point{{X: 0, Y: 0}, {X: 80, Y: 0}, {X: -80, Y: 0}, {X: 0, Y: 0}},
			[]bezier.Point{{X: 80, Y: 0}, {X: -160, Y: 0}, {X: 80, Y: 0}},
		},
		{
			[]Knot{
				{Ctrl: bezier.Pt(10, 20), T: 0},
				{Ctrl: bezier.Pt(10, 20), T: 0},
				{Ctrl: bezier.Pt(10, 20), T: 0},
				{Ctrl: bezier.Pt(5, 30), T: 2},
				{Ctrl: bezier.Pt(50, 25), T: 3},
				{Ctrl: bezier.Pt(200, -100), T: 1},
				{Ctrl: bezier.Pt(-200, -200), T: 4},
				{Ctrl: bezier.Pt(80, 100), T: 2},
				{Ctrl: bezier.Pt(80, 100), T: 0},
				{Ctrl: bezier.Pt(80, 100), T: 0},
			},
			[]bezier.Point{{0, 0}, {-3, 6}, {22, -2}, {56, -46}, {-171, -42}, {140, 150}, {0, 0}},
			[]bezier.Point{{-3, 6}, {10, -3}, {17, -22}, {-90, 1}, {103, 64}, {-140, -150}},
			[]bezier.Point{{6, -4}, {2, -6}, {-107, 23}, {48, 15}, {-121, -107}},
		},
	}
	for _, test := range tests {
		vel, accel, jerk := extractKinematics(t, test.spline)
		if !slices.Equal(vel, test.velocity) {
			t.Errorf("got velocity %v, expected %v", vel, test.velocity)
		}
		if !slices.Equal(accel, test.acceleration) {
			t.Errorf("got acceleration %v, expected %v", accel, test.acceleration)
		}
		if !slices.Equal(jerk, test.jerk) {
			t.Errorf("got jerk %v, expected %v", jerk, test.jerk)
		}
	}
}

func TestInterpolatePoints(t *testing.T) {
	tests := [][]bezier.Point{
		{{0, 0}, {1000, 1000}},
		{{100, 100}, {400, 100}, {700, 100}, {1000, 100}, {1000, 400}, {1000, 700}, {1000, 1000}, {700, 1000}, {400, 1000}, {100, 1000}, {100, 700}, {100, 400}, {100, 100}},
		{{X: 2900, Y: -2300}, {X: 2605, Y: -2506}, {X: 2269, Y: -2637}, {X: 1909, Y: -2676}, {X: 1550, Y: -2607}, {X: 1244, Y: -2417}, {X: 1100, Y: -2100}, {X: 1231, Y: -1709}, {X: 1585, Y: -1501}, {X: 2000, Y: -1400}, {X: 2342, Y: -1335}, {X: 2665, Y: -1231}, {X: 2916, Y: -1025}, {X: 3000, Y: -700}, {X: 2846, Y: -328}, {X: 2509, Y: -123}, {X: 2111, Y: -47}, {X: 1714, Y: -73}, {X: 1337, Y: -189}, {X: 1000, Y: -400}},
	}
	for i, waypoints := range tests {
		uspline, v, a, j, err := interpolatePoints(waypoints)
		if err != nil {
			t.Fatal(err)
		}
		cuspline := expandUniformBSpline(uspline)
		if dir := *dump; dir != "" {
			path := filepath.Join(dir, fmt.Sprintf("dump%d.svg", i))
			if err := dumpBSplineToSVG(path, cuspline, waypoints); err != nil {
				t.Fatal(err)
			}
		}
		var seg Segment
		// Test that the spline passes through every line segment
		// spanned by waypoints.
		for i, k := range cuspline {
			b, _, _ := seg.Knot(k)
			if i < 6 {
				// Skip starting points.
				continue
			}
			p := b.C0
			w0, w1 := waypoints[i-6], waypoints[i-5]
			if !closeToSegment(p, w0, w1) {
				t.Errorf("spline passes through %v, which is not on the line segment %v-%v", p, w0, w1)
			}
		}
		// Test that the optimization goal matches the kinematics.
		wantv, wanta, wantj := extractKinematics(t, cuspline)
		if !pointsNearlyEqual(v, wantv) {
			t.Errorf("got velocities\n%v, expected\n%v", v, wantv)
		}
		if !pointsNearlyEqual(a, wanta) {
			t.Errorf("got accelerations\n%v, expected\n%v", a, wanta)
		}
		if !pointsNearlyEqual(j, wantj) {
			t.Errorf("got jerks\n%v, expected\n%v", j, wantj)
		}
	}
}

func TestExprConst(t *testing.T) {
	tests := []struct {
		name string
		want float64
		expr expr
	}{
		{"0", 0, constExpr(0)},
		{"1", 1, constExpr(1)},
		{"0*10", 0, constExpr(0).Mul(10)},
		{"1*10", 10, constExpr(1).Mul(10)},
		{"10/2", 2, constExpr(10).Div(5)},
		{"10+2", 12, constExpr(10).Add(constExpr(2))},
		{"10-10", 0, constExpr(10).Sub(constExpr(10))},
		{"[1,1]-[0,1]", 1, varExpr(0).Add(varExpr(1)).Sub(varExpr(1))},
		{"[10,0,10,1]-[1,0,10,1]", 9, constExpr(10).Add(varExpr(2).Mul(10)).Add(varExpr(3)).
			Sub(constExpr(1).Add(varExpr(2).Mul(10)).Add(varExpr(3)))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.expr; got.Const() != test.want {
				t.Errorf("%s == %s", test.name, got)
			}
		})
	}
}

func TestExprVar(t *testing.T) {
	tests := []struct {
		name string
		want []float64
		expr expr
	}{
		{"[1]", []float64{1}, varExpr(0)},
		{"[0,1]", []float64{0, 1}, varExpr(1)},
		{"[1,0]+[0,1]", []float64{1, 1}, varExpr(0).Add(varExpr(1))},
		{"[10,0]+[0,1]", []float64{10, 1}, constExpr(10).Add(varExpr(1))},
		{"[10,0,10,1]+[1,0,10,1]", []float64{11, 0, 20, 2}, constExpr(10).Add(varExpr(2).Mul(10)).Add(varExpr(3)).
			Add(constExpr(1).Add(varExpr(2).Mul(10)).Add(varExpr(3)))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := test.expr.Explode()
			if got := test.expr; !slices.Equal(r, test.want) {
				t.Errorf("%s == %s", test.name, got)
			}
		})
	}
}

// closeToSegment checks if point p lies on the segment between a and b.
func closeToSegment(p, a, b bezier.Point) bool {
	const slack = 2

	// Compute distance from p to the line passing a and b.
	nom := (b.Y-a.Y)*p.X - (b.X-a.X)*p.Y + b.X*a.Y - b.Y*a.X
	nom = max(nom, -nom)
	denom := math.Hypot(float64(b.Y-a.Y), float64(b.X-a.X))
	dist := int(math.Round(float64(nom) / denom))
	if dist > slack {
		return false
	}

	// p must also lie in the bounding box with a and b as corners.
	return p.X >= min(a.X, b.X)-slack && p.X <= max(a.X, b.X)+slack &&
		p.Y >= min(a.Y, b.Y)-slack && p.Y <= max(a.Y, b.Y)+slack
}

// expandUniformBSpline expands a uniform B-spline to its non-uniform
// representation.
func expandUniformBSpline(bspline []bezier.Point) []Knot {
	knots := make([]Knot, 0, len(bspline))
	for i, c := range bspline {
		k := Knot{
			Ctrl: c,
		}
		if i > 2 && i < len(bspline)-2 {
			k.T = 1
		}
		knots = append(knots, k)
	}
	return knots
}

func uniformBSpline(uspline []bezier.Point) []Knot {
	s := uspline[0]
	e := uspline[len(uspline)-1]
	// Clamp.
	clamped := append([]bezier.Point{s, s}, uspline...)
	clamped = append(clamped, e, e)
	spline := expandUniformBSpline(clamped)
	maxv, _, _ := ComputeKinematics(spline, 1)
	for i := range spline[3 : len(spline)-2] {
		spline[i+3].T = maxv
	}
	return spline
}

func rasterizeUniformBSpline(img draw.Image, uspline []bezier.Point) {
	spline := uniformBSpline(uspline)
	var in bezier.Interpolator
	dot := func(pos bezier.Point) {
		img.Set(int(pos.X), int(pos.Y), color.Alpha{A: 255})
	}
	dot(uspline[0])
	var seg Segment
	for _, k := range spline {
		c, ticks, _ := seg.Knot(k)
		in.Segment(c, uint(ticks))
		for in.Step() {
			dot(in.Position())
		}
	}
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

func dumpBSplineToSVG(filename string, spline []Knot, waypoints []bezier.Point) (err error) {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	minX, minY, maxX, maxY := math.MaxInt32, math.MaxInt32, math.MinInt32, math.MinInt32
	for _, p := range spline {
		minX = min(minX, p.Ctrl.X)
		minY = min(minY, p.Ctrl.Y)
		maxX = max(maxX, p.Ctrl.X)
		maxY = max(maxY, p.Ctrl.Y)
	}
	for _, p := range waypoints {
		minX = min(minX, p.X)
		minY = min(minY, p.Y)
		maxX = max(maxX, p.X)
		maxY = max(maxY, p.Y)
	}
	const margin = 20
	fmt.Fprintf(f, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"%d %d %d %d\" width=\"800\" height=\"800\">\n",
		minX-margin, minY-margin, (maxX-minX)+2*margin, (maxY-minY)+2*margin)

	fmt.Fprintln(f, `<defs><style>
		.waypoint { fill: #d63031; r: 3; } 
		.spline { fill: none; stroke: #0984e3; stroke-width: 2; }
		.control-point { fill: #00ee10; stroke: none; }
		.control-point { fill: #ccb894; stroke: none; }
		.control-polygon { stroke: #00b894; stroke-width: 1; stroke-dasharray: 4,2; fill: none; opacity: 0.5; }
	</style></defs>`)

	fmt.Fprintf(f, `<rect x="%d" y="%d" width="%d" height="%d" fill="white" />`, minX-margin, minY-margin, (maxX-minX)+2*margin, (maxY-minY)+2*margin)

	fmt.Fprint(f, `<polyline points="`)
	for _, w := range waypoints {
		fmt.Fprintf(f, "%d,%d ", w.X, w.Y)
	}
	fmt.Fprintln(f, `" class="control-polygon" />`)

	fmt.Fprint(f, `<path class="spline" d="`)
	var seg Segment
	for i, k := range spline {
		c, _, _ := seg.Knot(k)
		if i < 3 {
			continue
		}
		if i == 3 {
			// Move to start of curve
			fmt.Fprintf(f, "M %d %d", c.C0.X, c.C0.Y)
		}
		fmt.Fprintf(f, " C %d %d, %d %d, %d %d",
			c.C1.X, c.C1.Y, c.C2.X, c.C2.Y, c.C3.X, c.C3.Y)
	}
	fmt.Fprintln(f, `" />`)

	for _, p := range spline {
		fmt.Fprintf(f, `<circle cx="%d" cy="%d" r="4.5" class="control-point" />`+"\n", p.Ctrl.X, p.Ctrl.Y)
	}
	for _, p := range waypoints {
		fmt.Fprintf(f, `<circle cx="%d" cy="%d" r="5.5" class="waypoint" />`+"\n", p.X, p.Y)
	}

	fmt.Fprintln(f, "</svg>")
	return nil
}

func extractKinematics(t *testing.T, spline []Knot) (v, a, j []bezier.Point) {
	var kin Kinematics
	var vel, accel, jerk []bezier.Point
	var maxv, maxa, maxj uint
	for i, k := range spline {
		kin.Knot(k.T, k.Ctrl, 1)
		v := kin.Velocity
		absv := uint(max(v.X, -v.X, v.Y, -v.Y))
		maxv = max(maxv, absv)
		a := kin.Acceleration
		absa := uint(max(a.X, -a.X, a.Y, -a.Y))
		maxa = max(maxa, absa)
		j := kin.Jerk
		absj := uint(max(j.X, -j.X, j.Y, -j.Y))
		maxj = max(maxj, absj)
		mv, ma, mj := kin.Max()
		if mv != absv || ma != absa || mj != absj {
			t.Errorf("got absolute kinematics (%d, %d, %d), expected (%d, %d, %d)", mv, ma, mj, absv, absa, absj)
		}
		if i >= 3 {
			vel = append(vel, v)
			if i >= 4 {
				accel = append(accel, a)
				if i >= 5 {
					jerk = append(jerk, j)
				}
			}
		}
	}
	gotv, gota, gotj := ComputeKinematics(spline, 1)
	if gotv != uint(maxv) || gota != uint(maxa) || gotj != uint(maxj) {
		t.Errorf("got kinematics (v, a, j) = (%d, %d, %d), expected (%d, %d, %d)", v, a, j, maxv, maxa, maxj)
	}
	return vel, accel, jerk
}

func pointsNearlyEqual(a, b []bezier.Point) bool {
	const slack = 4
	if len(a) != len(b) {
		return false
	}
	for i, pa := range a {
		pb := b[i]
		dx, dy := pa.X-pb.X, pa.Y-pb.Y
		dist := dx*dx + dy*dy
		if dist > slack*slack {
			return false
		}
	}
	return true
}
