package alpha4

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestExhausting(t *testing.T) {
	r := Rectangle{10, 10, 12, 12}
	img1 := image.NewAlpha(r.Rect())
	a4 := New(r)
	img2 := image.NewAlpha(img1.Rect)
	for a1 := byte(0); a1 <= 0b1111; a1++ {
		for a2 := byte(0); a2 <= 0b1111; a2++ {
			for a3 := byte(0); a3 <= 0b1111; a3++ {
				img1.SetAlpha(10, 10, color.Alpha{A: a1<<4 | a1})
				img1.SetAlpha(11, 10, color.Alpha{A: a2<<4 | a2})
				img1.SetAlpha(10, 11, color.Alpha{A: a3<<4 | a3})
				draw.Draw(a4, a4.Bounds(), img1, img1.Bounds().Min, draw.Src)
				draw.Draw(img2, img2.Bounds(), a4, a4.Bounds().Min, draw.Src)
				if !bytes.Equal(img1.Pix, img2.Pix) {
					t.Errorf("%.8b %.8b %.8b roundtripped to %.8b %.8b %.8b",
						img1.AlphaAt(10, 10), img1.AlphaAt(11, 10), img1.AlphaAt(10, 11),
						img2.AlphaAt(10, 10), img2.AlphaAt(11, 10), img2.AlphaAt(11, 10))
				}
			}
		}
	}
}
