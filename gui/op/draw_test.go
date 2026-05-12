package op

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"seedhammer.com/font/poppins"
	"seedhammer.com/gui/assets"
	"seedhammer.com/image/alpha4"
	"seedhammer.com/image/rgb565"
)

var (
	update = flag.Bool("update", false, "update golden images")
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

func TestRoundedRect(t *testing.T) {
	testGolden(t, "rounded-rect", 100, 60, func(b *Buffer) Op {
		outer := image.Rect(11, 12, 41, 52)
		inner := image.Rect(21, 22, 31, 42)

		l := Layer(
			Compose(
				Color(b, argb(0xff000000)),
				RoundedOutline2(b, inner, 4, 1),
			),

			Compose(
				Color(b, argb(0xf1a2040a)),
				RoundedOutline2(b, outer, 10, 1),
			),
		)
		return Layer(
			Compose(
				Color(b, argb(0xff00ffff)),
				RoundedRect2(b, inner, 4),
			).Clip(image.Rect(22, 23, 30, 41)).
				Offset(image.Pt(25, 0)),

			l.Offset(image.Pt(50, 0)),

			Compose(
				Color(b, argb(0xffffffff)),
				RoundedRect2(b, inner, 4),
			),

			Compose(
				Color(b, argb(0xf03530aa)),
				RoundedRect2(b, outer, 10),
			),

			Color(b, argb(0xde11adbe)),
		)
	})
}

func TestClip(t *testing.T) {
	testGolden(t, "alpha-mask", 100, 60, func(b *Buffer) Op {
		r := image.Rect(1, 2, 31, 42)

		c1 := Color(b, argb(0xf03530aa)).
			Clip(r).
			Offset(image.Pt(10, 10))

		c2 := Color(b, argb(0xf1a2040a)).
			Clip(r).
			Offset(image.Pt(10, 10)).
			Offset(image.Pt(50, 0))
		background := Color(b, argb(0xde11adbe))
		return Layer(c2, c1, background)
	})
}

func TestImageMask(t *testing.T) {
	testGolden(t, "image-mask", 80, 50, func(b *Buffer) Op {
		fnt := poppins.Bold25
		m := fnt.Metrics()
		return Layer(
			Compose(
				Color(b, argb(0xffdfcf0f)),
				Glyph(b, fnt, 'R'),
			).Offset(image.Pt(50, m.Ascent.Round()+10)),
			Compose(
				Color(b, argb(0xffdfcf0f)),
				Mask(b, assets.IconDiscard),
			).Offset(image.Pt(10, 10)),
			Color(b, argb(0xde11adbe)),
		)
	})
}

func TestPaletted(t *testing.T) {
	testGolden(t, "paletted", 150, 150, func(b *Buffer) Op {
		return Layer(
			Compose(
				Image(b, assets.Hammer),
				_Alpha(b, 0xaa),
				_Alpha(b, 0xcc),
			).Offset(image.Pt(10, 10)),
			Color(b, argb(0xde11adbe)),
		)
	})
}

func testGolden(t *testing.T, name string, w, h int, f func(b *Buffer) Op) {
	t.Helper()

	op := f(new(Buffer))

	dr := image.Rect(0, 0, w, h)
	fb := rgb565.New(dr)
	mask := image.NewAlpha(dr)
	d := new(Drawer)
	d.Draw(fb, mask, op)
	if err := os.MkdirAll("testdata", 0o770); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", name+".png")
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, fb); err != nil {
		t.Fatal(err)
	}
	imgBytes := buf.Bytes()
	if *update {
		if err := os.WriteFile(path, imgBytes, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gf, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer gf.Close()
	golden, err := png.Decode(gf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := png.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err := compareImages(got, golden); err != nil {
		n := filepath.Join(t.ArtifactDir(), name+"-mismatch.png")
		if err := os.WriteFile(n, imgBytes, 0o660); err != nil {
			t.Error(err)
		}
		t.Error(err)
	}
}

func compareImages(img, golden image.Image) error {
	b1, b2 := img.Bounds(), golden.Bounds()
	if b1 != b2 {
		return fmt.Errorf("got dimensions %v, expected %v", b1, b2)
	}
	mismatches := 0
	for y := b1.Min.Y; y < b1.Max.Y; y++ {
		for x := b1.Min.X; x < b1.Max.X; x++ {
			p1, p2 := img.At(x, y), golden.At(x, y)
			if p1 != p2 {
				mismatches++
			}
		}
	}
	if mismatches > 0 {
		return fmt.Errorf("%d pixels mismatch", mismatches)
	}
	return nil
}

func argb(c uint32) color.RGBA {
	return color.RGBA{A: uint8(c >> 24), R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
}
