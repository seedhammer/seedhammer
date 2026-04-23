package op

import (
	"image"
	"image/color"
	"slices"
	"testing"

	"seedhammer.com/image/alpha4"
	"seedhammer.com/image/rgb565"
)

func TestDrawAlphaUniformOver(t *testing.T) {
	dst := rgb565.New(image.Rect(0, 0, 2, 2))
	src := color.RGBA{R: 0xde, G: 0xad, B: 0xbe, A: 0xef}
	mask := alpha4.New(alpha4.Rect(dst.Rect))
	maskOff := image.Point{}
	dst.SetRGB565(0, 0, 0b11111_111111_11111)
	dst.SetRGB565(1, 0, 0b00000_000000_00000)
	dst.SetRGB565(0, 1, 0b10101_110011_11001)
	dst.SetRGB565(1, 1, 0b00001_000001_00001)
	mask.SetAlpha4(0, 0, 0b1111)
	mask.SetAlpha4(1, 0, 0b0001)
	mask.SetAlpha4(0, 1, 0b0000)
	mask.SetAlpha4(1, 1, 0b1001)
	drawAlphaUniformOver(dst, dst.Rect, src, mask, maskOff)
	want := []rgb565.Color{
		0b11100_101110_11000,
		0b00001_000010_00001,
		0b10101_110011_11001,
		0b10000_011010_01110,
	}
	if !slices.Equal(dst.Pix, want) {
		t.Fatalf("image:\n%.16b\nexpected:\n%.16b\n", dst.Pix, want)
	}
}
