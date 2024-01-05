package engrave

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	qr "github.com/seedhammer/kortschak-qr"
	"seedhammer.com/bezier"
	"seedhammer.com/bip39"
	"seedhammer.com/bspline"
	"seedhammer.com/font/constant"
	"seedhammer.com/font/sh"
	"seedhammer.com/font/vector"
	"seedhammer.com/internal/golden"
	"seedhammer.com/seedqr"
)

var (
	update = flag.Bool("update", false, "update golden files")
	dump   = flag.String("dump", "", "dump original and new splines to directory")
)

func TestCSQR(t *testing.T) {
	mnemonic := make(bip39.Mnemonic, 24)
	for i := range mnemonic {
		mnemonic[i] = bip39.RandomWord()
	}
	mnemonic = mnemonic.FixChecksum()
	compact, err := qr.Encode(string(seedqr.CompactQR(mnemonic)), qr.Q)
	if err != nil {
		t.Fatal(err)
	}
	regular, err := qr.Encode(string(seedqr.QR(mnemonic)), qr.M)
	if err != nil {
		t.Fatal(err)
	}
	if compact.Size != regular.Size {
		t.Errorf("compact: %d, regular: %d", compact.Size, regular.Size)
	}
}

func TestConstantQR(t *testing.T) {
	rng := rand.New(rand.NewSource(44))
	genEntropy := func(n int) []byte {
		entropy := make([]byte, n)
		if _, err := io.ReadFull(rng, entropy); err != nil {
			t.Fatal(err)
		}
		return entropy
	}
	for n := 16; n <= 40; n++ {
		templateEntropy := genEntropy(n)
		qrc, err := qr.Encode(string(templateEntropy), qr.Q)
		if err != nil {
			t.Fatal(err)
		}
		cmd, err := ConstantQR(qrc)
		if err != nil {
			t.Fatalf("entropy: %x: %v", templateEntropy, err)
		}
		refProf := ProfileSpline(PlanEngraving(conf, cmd.Engrave(conf, strokeWidth, 3)))
		for i := range 100 {
			t.Run(fmt.Sprintf("n-%d-run-%d", n, i), func(t *testing.T) {
				t.Parallel()
				entropy := genEntropy(n)
				qrc, err := qr.Encode(string(entropy), qr.Q)
				if err != nil {
					t.Fatal(err)
				}
				cmd, err := ConstantQR(qrc)
				if err != nil {
					t.Fatalf("entropy: %x: %v", entropy, err)
				}
				prof := ProfileSpline(verifiedEngraving(t, conf, cmd.Engrave(conf, strokeWidth, 3)))
				if !prof.Equal(refProf) {
					t.Errorf("entropy: %x: engraving is not constant compared to %x", entropy, templateEntropy)
				}
				dim := qrc.Size
				want := bitmapForQR(qrc)
				got := newBitmap(dim, dim)
				posMarkers, alignMarkers := bitmapForQRStatic(dim)
				// Fill static markers.
				for _, p := range posMarkers {
					fillMarker(got, p, positionMarker)
				}
				for _, p := range alignMarkers {
					fillMarker(got, p, alignmentMarker)
				}
				start, _ := constantTimeStartEnd(dim)
				needle := start
				for _, m := range cmd.plan {
					needle = needle.Add(m.Point())
					got.Set(needle)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("entropy: %x: engraving plan doesn't match QR code", entropy)
				}
			})
		}
	}
}

const (
	mm             = 6400
	strokeWidth    = mm / 3
	speed          = 30 * mm
	engravingSpeed = 8 * mm
	accel          = 250 * mm
	jerk           = 2600 * mm
)

var (
	conf = StepperConfig{
		Speed:          speed,
		EngravingSpeed: engravingSpeed,
		Acceleration:   accel,
		Jerk:           jerk,
		TicksPerSecond: speed,
	}
)

func params() Params {
	return Params{
		Millimeter:    mm,
		StrokeWidth:   strokeWidth,
		StepperConfig: conf,
	}
}

func TestFonts(t *testing.T) {
	fonts := []struct {
		name string
		face *vector.Face
	}{
		{
			"sh",
			sh.Font,
		},
	}
	const em = 7 * mm
	for _, f := range fonts {
		t.Run(f.name, func(t *testing.T) {
			var alphabet []rune
			width := 0
			for r := range rune(unicode.MaxASCII) {
				if adv, _, ok := f.face.Decode(r); ok {
					alphabet = append(alphabet, r)
					width += adv
				}
			}
			plan := func(yield func(Command) bool) {
				String(f.face, em, string(alphabet)).Engrave(yield)
			}
			p := filepath.Join("testdata", "font-"+f.name+".bin")
			spline := verifiedEngraving(t, conf, plan)
			m := f.face.Metrics()
			bounds := bspline.Bounds{Max: bezier.Pt(width*em/m.Height, em)}
			if err := golden.CompareBSpline(p, *update, *dump, strokeWidth, bounds, spline); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConstantFont(t *testing.T) {
	f := constant.Font
	const em = 7 * mm
	s := NewConstantStringer(f, params(), em)
	plan := func(yield func(Command) bool) {
		s.String(yield, constantAlphabet)
	}
	p := filepath.Join("testdata", "font-constant.bin")
	spline := verifiedEngraving(t, conf, plan)
	width, height := String(f, em, constantAlphabet).Measure()
	bounds := bspline.Bounds{
		Max: bezier.Pt(width, height),
	}
	if err := golden.CompareBSpline(p, *update, *dump, strokeWidth, bounds, spline); err != nil {
		t.Fatal(err)
	}
}

func TestConstantWords(t *testing.T) {
	const em = 1000
	s := NewConstantStringer(constant.Font, params(), em)
	w := strings.ToUpper(bip39.LabelFor(bip39.Word(0)))
	plan := func(yield func(Command) bool) {
		s.PaddedString(yield, w, bip39.ShortestWord, bip39.LongestWord)
	}
	refProf := ProfileSpline(PlanEngraving(conf, plan))
	for w := bip39.Word(1); w < bip39.NumWords; w++ {
		t.Run(bip39.LabelFor(w), func(t *testing.T) {
			t.Parallel()
			w := strings.ToUpper(bip39.LabelFor(w))
			cmd := func(yield func(Command) bool) {
				s.PaddedString(yield, w, bip39.ShortestWord, bip39.LongestWord)
			}
			spline := PlanEngraving(conf, cmd)
			prof := ProfileSpline(spline)
			if !refProf.Equal(prof) {
				t.Errorf("%s: not constant", w)
			}
		})
	}
}

func TestSCurveSpecialCases(t *testing.T) {
	tests := []struct {
		phases           int
		dist             int
		vlim, alim, jlim uint
	}{
		// Provoke 5-phase curve with fast acceleration.
		{5, 65601, 30 * mm, 269 * mm, 2400 * mm},
	}
	for _, test := range tests {
		gotPhases := testSCurve(t, test.vlim, test.alim, test.jlim, test.dist)
		if gotPhases != test.phases {
			t.Errorf("dist %d got %d phases, expected %d", test.dist, gotPhases, test.phases)
		}
	}
}

func TestSCurveExhaustive(t *testing.T) {
	maxPhases := 0
	// Short distances load to inaccurate kinematic calculations.
	// Skip them
	const shortDist = 100
	for dist := range 70000 {
		if dist < shortDist {
			continue
		}
		vlim, alim, jlim := conf.Speed, conf.Acceleration, conf.Jerk
		nphases := testSCurve(t, vlim, alim, jlim, dist)
		maxPhases = max(maxPhases, nphases)
	}
	if maxPhases != 7 {
		t.Errorf("exhaustive test reached %d phases, expected 7", maxPhases)
	}
}

func testSCurve(t *testing.T, vlim, alim, jlim uint, dist int) int {
	t.Helper()

	// ε is the percentage slack in comparing kinetic values.
	const ε = 5
	lessEq := func(ref, val uint) bool {
		return val <= ref*(100+ε)/100
	}
	equal := func(ref, val uint) bool {
		return lessEq(ref, val) && val >= ref*(100-ε)/100
	}
	tps := conf.TicksPerSecond
	sc := computeSCurve(uint(dist), vlim, alim, jlim, tps)
	var spline []bspline.Knot
	spline = append(spline, bspline.Knot{}, bspline.Knot{})
	for _, phase := range sc {
		if phase.Duration == 0 {
			continue
		}
		p := int(phase.Position)
		spline = append(spline, bspline.Knot{
			Ctrl: bezier.Point{X: p, Y: p},
			T:    phase.Duration,
		})
	}
	e := bspline.Knot{Ctrl: bezier.Pt(dist, dist)}
	spline = append(spline, e, e)
	var kin bspline.Kinematics
	nphases := len(spline) - 4
	maxv, maxa, maxj := uint(0), uint(0), uint(0)
	var prof strings.Builder
	for i, k := range spline {
		kin.Knot(k.T, k.Ctrl, uint(conf.TicksPerSecond))
		v, a, j := kin.Max()
		maxv, maxa, maxj = max(maxv, v), max(maxa, a), max(maxj, j)
		if i < 4 {
			continue
		}
		switch {
		case equal(jlim, j):
			prof.WriteByte('j')
		case equal(alim, a):
			prof.WriteByte('a')
		case equal(vlim, v):
			prof.WriteByte('v')
		default:
			prof.WriteByte('-')
		}
	}
	if !lessEq(vlim, maxv) || !lessEq(alim, maxa) || !lessEq(jlim, maxj) {
		t.Errorf("dist: %d violates kinetic constraints (%d, %d, %d) > (%d, %d, %d)", dist,
			maxv, maxa, maxj, vlim, alim, jlim)
	}
	var wantProfile string
	switch nphases {
	case 7:
		wantProfile = "jajvjaj"
	case 6:
		wantProfile = "jajjaj"
	case 5:
		wantProfile = "jjvjj"
	case 4:
		wantProfile = "jjjj"
	}
	if got := prof.String(); got != wantProfile {
		t.Errorf("dist: %d got kinetic profile %q, expected %q", dist, got, wantProfile)
	}
	return nphases
}

func FuzzConstantQR(f *testing.F) {
	f.Fuzz(func(t *testing.T, entropy []byte) {
		if len(entropy) < 16 {
			return
		}
		if m := 40; len(entropy) > m {
			entropy = entropy[:m]
		}
		qrcq, err := qr.Encode(string(entropy), qr.Q)
		if err != nil {
			t.Fatal(err)
		}
		qrcqCmd, err := ConstantQR(qrcq)
		if err != nil {
			t.Fatalf("entropy: %x: %v", entropy, err)
		}
		qrcqCmd.Engrave(conf, strokeWidth, 3)
		qrcl, err := qr.Encode(string(entropy), qr.L)
		if err != nil {
			t.Fatal(err)
		}
		qrclCmd, err := ConstantQR(qrcl)
		if err != nil {
			t.Fatalf("entropy: %x: %v", entropy, err)
		}
		qrclCmd.Engrave(conf, strokeWidth, 3)
	})
}

func BenchmarkEngraving(b *testing.B) {
	const em = 4.1 * mm
	fontBenchmark := func(f *vector.Face) Engraving {
		return func(yield func(Command) bool) {
			alphabet := new(strings.Builder)
			for r := range rune(unicode.MaxASCII) {
				if _, _, ok := f.Decode(r); ok {
					alphabet.WriteRune(r)
				}
			}
			String(f, em, alphabet.String()).Engrave(yield)
		}
	}
	const qrScale = 3
	constantQRBenchmark := func(data string) Engraving {
		qrc, err := qr.Encode(data, qr.Q)
		if err != nil {
			b.Fatal(err)
		}
		cmd, err := ConstantQR(qrc)
		if err != nil {
			b.Fatal(err)
		}
		return cmd.Engrave(conf, strokeWidth, qrScale)
	}
	qrBenchmark := func(data string) Engraving {
		qrc, err := qr.Encode(data, qr.Q)
		if err != nil {
			b.Fatal(err)
		}
		return QR(strokeWidth, qrScale, qrc)
	}
	benchmarks := []struct {
		name string
		plan Engraving
	}{
		{
			"simple",
			func(yield func(Command) bool) {
				yield(Move(bezier.Pt(85*mm, 85*mm)))
			},
		},
		{
			"font-sh",
			fontBenchmark(sh.Font),
		},
		{
			"font-constant",
			fontBenchmark(constant.Font),
		},
		{
			"bip39-word",
			func(yield func(Command) bool) {
				s := NewConstantStringer(constant.Font, params(), em)
				s.PaddedString(yield, "ZOO", bip39.ShortestWord, bip39.LongestWord)
			},
		},
		{
			"constant-qr-21",
			constantQRBenchmark(strings.Repeat("1", 20)),
		},
		{
			"constant-qr-25",
			constantQRBenchmark(strings.Repeat("1", 40)),
		},
		{
			"constant-qr-29",
			constantQRBenchmark(strings.Repeat("1", 70)),
		},
		{
			"constant-qr-33",
			constantQRBenchmark(strings.Repeat("1", 100)),
		},
		{
			"qr-69",
			qrBenchmark(strings.Repeat("1", 500)),
		},
	}
	for _, bench := range benchmarks {
		b.Run(bench.name, func(b *testing.B) {
			var dur time.Duration
			for b.Loop() {
				dur += TimePlan(conf, bench.plan)
			}
			b.ReportMetric(dur.Minutes()/float64(b.N), "min/op")
		})
	}
}

func verifiedEngraving(t *testing.T, conf StepperConfig, e Engraving) bspline.Curve {
	return func(yield func(bspline.Knot) bool) {
		var kin bspline.Kinematics
		// Engrave mode is shifted one knot.
		lastEngrave := false
		remaining := 10
		for k := range PlanEngraving(conf, e) {
			kin.Knot(k.T, k.Ctrl, uint(conf.TicksPerSecond))
			limv := conf.Speed
			if lastEngrave {
				limv = conf.EngravingSpeed
			}
			lastEngrave = k.Engrave
			lima, limj := conf.Acceleration, conf.Jerk
			const slack = 101
			v, a, j := kin.Max()
			if v > limv*slack/100 || a > lima*slack/100 || j > limj*slack/100 {
				tof := func(v int) float64 {
					return float64(v) / mm
				}
				vx, vy := tof(kin.Velocity.X), tof(kin.Velocity.Y)
				ax, ay := tof(kin.Acceleration.X), tof(kin.Acceleration.Y)
				jx, jy := tof(kin.Jerk.X), tof(kin.Jerk.Y)
				t.Errorf("kinematics violation: v=(%g %g) a=(%g %g) j=(%g %g)) limits (v=%g, a=%g, j=%g)",
					vx, vy, ax, ay, jx, jy, tof(int(limv)), tof(int(lima)), tof(int(limj)))
				if remaining == 0 {
					t.Fatal("too many kinematics violations")
				}
				remaining--
			}
			if !yield(k) {
				return
			}
		}
	}
}
