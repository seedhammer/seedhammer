package engrave

import (
	"image"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/skip2/go-qrcode"
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
			lvl := qrcode.High
			cmd, err := ConstantQR(7, 4, lvl, entropy)
			if err != nil {
				t.Fatalf("entropy: %x: %v", entropy, err)
			}
			qrc, err := qrcode.New(string(entropy), lvl)
			if err != nil {
				t.Fatal(err)
			}
			qrc.DisableBorder = true
			bm := qrc.Bitmap()
			dim := len(bm)
			want := bitmapForBools(bm)
			_, _, got := bitmapForQRStatic(qrc.VersionNumber, dim)
			qrCmd := cmd.(constantQRCmd)
			for _, p := range qrCmd.plan[1 : len(qrCmd.plan)-1] {
				got.Set(p)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("entropy: %x: engraving plan doesn't match QR code", entropy)
			}
		}
	}
}

func TestConstantString(t *testing.T) {
	s := NewConstantStringer(&constant.Font, 1000, bip39.Shortest, bip39.Longest)
	for _, w := range bip39.Wordlist {
		w := strings.ToUpper(w)
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
		if _, err := ConstantQR(1, 3, qrcode.High, entropy); err != nil {
			t.Fatalf("entropy: %x: %v", entropy, err)
		}
		if _, err := ConstantQR(1, 3, qrcode.Low, entropy); err != nil {
			t.Fatalf("entropy: %x: %v", entropy, err)
		}
	})
}

type measureMovesProgram struct {
	bounds image.Rectangle
}

func (m *measureMovesProgram) Line(p image.Point) {}

func (m *measureMovesProgram) Move(p image.Point) {
	if p.X < m.bounds.Min.X {
		m.bounds.Min.X = p.X
	} else if p.X > m.bounds.Max.X {
		m.bounds.Max.X = p.X
	}
	if p.Y < m.bounds.Min.Y {
		m.bounds.Min.Y = p.Y
	} else if p.Y > m.bounds.Max.Y {
		m.bounds.Max.Y = p.Y
	}
}

func measureMoves(c Command) image.Rectangle {
	inf := image.Rectangle{Min: image.Pt(1e6, 1e6), Max: image.Pt(-1e6, -1e6)}
	measure := measureMovesProgram{
		bounds: inf,
	}
	c.Engrave(&measure)
	b := measure.bounds
	if b == inf {
		b = image.Rectangle{}
	}
	return b
}
