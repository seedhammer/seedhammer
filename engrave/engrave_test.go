package engrave

import (
	"image"
	"io"
	"iter"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/seedhammer/kortschak-qr"
	"seedhammer.com/bip39"
	"seedhammer.com/font/constant"
	"seedhammer.com/seedqr"
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

func TestConstantString(t *testing.T) {
	s := NewConstantStringer(constant.Font, 1000, bip39.ShortestWord, bip39.LongestWord)
	for i := bip39.Word(0); i < bip39.NumWords; i++ {
		w := strings.ToUpper(bip39.LabelFor(i))
		cmd := s.String(w)
		bounds := image.Rect(0, 0, s.longest*s.dims.X, s.dims.Y)
		moves := measureMoves(cmd)
		if !moves.In(bounds) {
			t.Errorf("%s movement bounds %v are not inside bounds %v", w, moves, bounds)
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
