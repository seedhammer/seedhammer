package engrave

import (
	"image"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/kortschak/qr"
	"seedhammer.com/bip39"
	"seedhammer.com/font/constant"
)

func TestConstantQR(t *testing.T) {
	rng := rand.New(rand.NewSource(44))
	for i := 0; i < 100; i++ {
		for n := 16; n <= 32; n++ {
			entropy := make([]byte, n)
			if _, err := io.ReadFull(rng, entropy); err != nil {
				t.Fatal(err)
			}
			lvl := qr.Q
			cmd, err := constantQR(7, 4, lvl, entropy)
			if err != nil {
				t.Fatalf("entropy: %x: %v", entropy, err)
			}
			qrc, err := qr.Encode(string(entropy), lvl)
			if err != nil {
				t.Fatal(err)
			}
			dim := qrc.Size
			want := bitmapForQR(qrc)
			_, _, got := bitmapForQRStatic(dim)
			for _, p := range cmd.plan {
				got.Set(p)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("entropy: %x: engraving plan doesn't match QR code", entropy)
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
		if _, err := ConstantQR(1, 3, qr.Q, entropy); err != nil {
			t.Fatalf("entropy: %x: %v", entropy, err)
		}
		if _, err := ConstantQR(1, 3, qr.L, entropy); err != nil {
			t.Fatalf("entropy: %x: %v", entropy, err)
		}
	})
}

func measureMoves(p Plan) image.Rectangle {
	inf := image.Rectangle{Min: image.Pt(1e6, 1e6), Max: image.Pt(-1e6, -1e6)}
	bounds := inf
	p(func(cmd Command) {
		if cmd.Line {
			return
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
	})
	if bounds == inf {
		bounds = image.Rectangle{}
	}
	return bounds
}
