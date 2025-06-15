package engrave

import (
	"bytes"
	"flag"
	"image"
	"image/png"
	"io"
	"iter"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/seedhammer/kortschak-qr"
	"seedhammer.com/bip39"
	"seedhammer.com/font/constant"
	"seedhammer.com/seedqr"
)

var update = flag.Bool("update", false, "update golden files")

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
	for n := 16; n <= 32; n++ {
		var prev Plan
		var prevEntropy []byte
		for i := 0; i < 100; i++ {
			entropy := make([]byte, n)
			if _, err := io.ReadFull(rng, entropy); err != nil {
				t.Fatal(err)
			}
			qrc, err := qr.Encode(string(entropy), qr.Q)
			if err != nil {
				t.Fatal(err)
			}
			cmd, err := constantQR(qrc)
			if err != nil {
				t.Fatalf("entropy: %x: %v", entropy, err)
			}
			engraving := cmd.engrave(1, 3)
			if prev != nil {
				if !constantEqual(prev, engraving) {
					t.Errorf("entropy: %x: engraving is not constant compared to %x", entropy, prevEntropy)
				}
			}
			prev = engraving
			prevEntropy = entropy
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
			start, end := constantTimeStartEnd(dim)
			needle := start
			for i, m := range cmd.plan {
				for j := range qrMovesPerModule {
					needle = needle.Add(m.Get(j))
				}
				// Skip end point.
				if i < len(cmd.plan)-1 {
					got.Set(needle)
				}
			}
			if needle != end {
				t.Errorf("entropy: %x: engraving plan ends in %v, expected %v", entropy, needle, end)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("entropy: %x: engraving plan doesn't match QR code", entropy)
			}
		}
	}
}

func TestQRMoves(t *testing.T) {
	for y := -qrMovesPerModule; y <= qrMovesPerModule; y++ {
		for x := -qrMovesPerModule; x <= qrMovesPerModule; x++ {
			target := image.Pt(x, y)
			moves := constantQRMoves(target)
			var p image.Point
			for i := range qrMovesPerModule {
				p = p.Add(moves.Get(i))
			}
			if p != target {
				t.Errorf("%v: constant QR moved to %v", target, p)
			}
		}
	}
}

func TestConstantFont(t *testing.T) {
	f := constant.Font
	s := NewConstantStringer(f)
	h := int(f.Metrics().Height)
	em := h * 10
	bounds := image.Rectangle{Max: image.Pt(em*len(alphabet), em)}
	got := image.NewAlpha(bounds)
	Rasterize(got, s.String(alphabet, em, len(alphabet)))
	golden := filepath.Join("testdata", "alphabet.png")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o700); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, got); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o640); err != nil {
			t.Fatal(err)
		}
		return
	}
	gf, err := os.Open(golden)
	if err != nil {
		t.Fatal(err)
	}
	defer gf.Close()
	want, _, err := image.Decode(gf)
	if err != nil {
		t.Fatal(err)
	}
	if w, g := want.Bounds().Size(), got.Bounds().Size(); w != g {
		t.Fatalf("golden image bounds mismatch: got %v, want %v", g, w)
	}
	mismatches := 0
	pixels := 0
	width, height := want.Bounds().Dx(), want.Bounds().Dy()
	gotOff := bounds.Min
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			wanty16, _, _, _ := want.At(x, y).RGBA()
			want := wanty16 != 0
			got := got.AlphaAt(gotOff.X+x, gotOff.Y+y).A != 0
			if want {
				pixels++
			}
			if got != want {
				mismatches++
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("%d/%d pixels golden image mismatches", mismatches, pixels)
	}
}

func TestConstantWords(t *testing.T) {
	const em = 1000
	s := NewConstantStringer(constant.Font)
	var prev Plan
	for r := bip39.Word(0); r < bip39.NumWords; r++ {
		w := strings.ToUpper(bip39.LabelFor(r))
		cmd := s.String(w, em, bip39.LongestWord)
		if prev != nil {
			if !constantEqual(prev, cmd) {
				t.Errorf("%s: not constant", w)
			}
		}
		prev = cmd
		moves := measureMoves(cmd)
		bounds := image.Rect(0, 0, em*len(w), em)
		if !moves.In(bounds) {
			t.Errorf("%s: movement bounds %v are not inside bounds %v", w, moves, bounds)
		}
	}
}

func FuzzConstantQR(f *testing.F) {
	f.Fuzz(func(t *testing.T, entropy []byte) {
		if len(entropy) < 16 {
			return
		}
		if len(entropy) > 32 {
			entropy = entropy[:32]
		}
		qrcq, err := qr.Encode(string(entropy), qr.Q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ConstantQR(1, 3, qrcq); err != nil {
			t.Fatalf("entropy: %x: %v", entropy, err)
		}
		qrcl, err := qr.Encode(string(entropy), qr.L)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ConstantQR(1, 3, qrcl); err != nil {
			t.Fatalf("entropy: %x: %v", entropy, err)
		}
	})
}

func measureMoves(p Plan) image.Rectangle {
	inf := image.Rectangle{Min: image.Pt(1e6, 1e6), Max: image.Pt(-1e6, -1e6)}
	bounds := inf
	for cmd := range p {
		if cmd.Line {
			continue
		}
		p := cmd.Coord
		if p.X < bounds.Min.X {
			bounds.Min.X = p.X
		} else if p.X > bounds.Max.X {
			bounds.Max.X = p.X
		}
		if p.Y < bounds.Min.Y {
			bounds.Min.Y = p.Y
		} else if p.Y > bounds.Max.Y {
			bounds.Max.Y = p.Y
		}
	}
	if bounds == inf {
		bounds = image.Rectangle{}
	}
	return bounds
}

// constantEqual reports whether two lists of commands are
// equal except for directions. That is, the commands are
// pairwise equal in type (move or line) and Manhattan
// distance.
func constantEqual(p1, p2 Plan) bool {
	cmds1, close := iter.Pull[Command](p1)
	defer close()
	cmds2, close := iter.Pull[Command](p2)
	defer close()
	idx := 0
	var pen1, pen2 image.Point
	for {
		cmd1, ok1 := cmds1()
		cmd2, ok2 := cmds2()
		if ok1 != ok2 {
			return false // Different lengths.
		}
		if !ok1 {
			return true
		}
		if cmd1.Line != cmd2.Line {
			return false
		}
		if d1, d2 := ManhattanDist(pen1, cmd1.Coord), ManhattanDist(pen2, cmd2.Coord); d1 != d2 {
			return false
		}
		pen1, pen2 = cmd1.Coord, cmd2.Coord
		idx++
	}
}
