package engrave

import (
	"io"
	"math/rand"
	"reflect"
	"testing"

	"github.com/skip2/go-qrcode"
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
			cmd, err := ConstantQR(1, 3, lvl, entropy)
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
			for i, p := range qrCmd.plan[1:] {
				if (i+1)%qrMoves == 0 {
					got.Set(p)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("entropy: %x: engraving plan doesn't match QR code", entropy)
			}
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