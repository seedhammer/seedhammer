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
	testGolden(t, "rounded-rect", 100, 60, func(ctx Ctx) {
		outer := image.Rect(11, 12, 41, 52)
		inner := image.Rect(21, 22, 31, 42)
		ColorOp(ctx, argb(0xde11adbe))

		RoundedRect(ctx, outer, 10)
		ColorOp(ctx, argb(0xf03530aa))

		RoundedRect(ctx, inner, 4)
		ColorOp(ctx, argb(0xffffffff))

		Offset(ctx, image.Pt(50, 0))
		RoundedOutline(ctx, outer, 10, 1)
		ColorOp(ctx, argb(0xf1a2040a))

		Offset(ctx, image.Pt(50, 0))
		RoundedOutline(ctx, inner, 4, 1)
		ColorOp(ctx, argb(0xff000000))

		Offset(ctx, image.Pt(25, 0))
		ClipOp(image.Rect(22, 23, 30, 41)).Add(ctx)
		RoundedRect(ctx, inner, 4)
		ColorOp(ctx, argb(0xff00ffff))
	})
}

func TestClip(t *testing.T) {
	testGolden(t, "alpha-mask", 100, 60, func(ctx Ctx) {
		r := image.Rect(1, 2, 31, 42)
		ColorOp(ctx, argb(0xde11adbe))

		m := ctx.Begin()
		ClipOp(r).Add(m)
		ColorOp(m, argb(0xf03530aa))
		c := ctx.End()
		Offset(ctx, image.Pt(10, 10))
		c.Add(ctx)

		m = ctx.Begin()
		Offset(ctx, image.Pt(10, 10))
		ClipOp(r).Add(m)
		ColorOp(m, argb(0xf1a2040a))
		c = ctx.End()
		Offset(ctx, image.Pt(50, 0))
		c.Add(ctx)
	})
}

func TestImageMask(t *testing.T) {
	testGolden(t, "image-mask", 80, 50, func(ctx Ctx) {
		ColorOp(ctx, argb(0xde11adbe))

		Offset(ctx, image.Pt(10, 10))
		ImageOp(ctx, assets.IconDiscard, true)
		ColorOp(ctx, argb(0xffdfcf0f))

		fnt := poppins.Bold25
		m := fnt.Metrics()
		Offset(ctx, image.Pt(50, m.Ascent.Round()+10))
		GlyphOp(ctx, fnt, 'R')
		ColorOp(ctx, argb(0xffdfcf0f))
	})
}

func TestPaletted(t *testing.T) {
	testGolden(t, "paletted", 150, 150, func(ctx Ctx) {
		ColorOp(ctx, argb(0xde11adbe))

		Offset(ctx, image.Pt(10, 10))
		AlphaOp(ctx, 0xcc)
		AlphaOp(ctx, 0xaa)
		ImageOp(ctx, assets.Hammer, false)
	})
}

func testGolden(t *testing.T, name string, w, h int, f func(ctx Ctx)) {
	t.Helper()

	var ops Ops
	ctx := ops.Context()

	f(ctx)

	dr := image.Rect(0, 0, w, h)
	fb := rgb565.New(dr)
	mask := image.NewAlpha(dr)
	ops.Draw(fb, mask)
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
