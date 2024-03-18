//go:build ignore

// gen converts the PNG assets to embedded image literals.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"image"
	"image/draw"
	_ "image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	simage "seedhammer.com/image"
	"seedhammer.com/image/rgb565"
)

func main() {
	pngs, err := filepath.Glob("*.png")
	if err != nil {
		log.Fatal(err)
	}
	// out is the generated embed.go file.
	out := new(bytes.Buffer)
	// data is the binary embed.bin containing image data.
	fmt.Fprintf(out, "// Code generated by gui/assets/gen.go; DO NOT EDIT.\n")
	fmt.Fprintf(out, "package assets\n\n")
	fmt.Fprintf(out, "import (\n")
	fmt.Fprintf(out, "_ \"embed\"\n")
	fmt.Fprintf(out, "\"unsafe\"\n\n")
	fmt.Fprintf(out, "\"seedhammer.com/image/ninepatch\"\n")
	fmt.Fprintf(out, "\"seedhammer.com/image/paletted\"\n\n")
	fmt.Fprintf(out, ")\n\n")
	fmt.Fprintf(out, "var (\n")
	for _, p := range pngs {
		r, err := os.Open(p)
		if err != nil {
			log.Fatal(err)
		}
		img, _, err := image.Decode(r)
		r.Close()
		if err != nil {
			log.Fatal(err)
		}
		name := p[:len(p)-len(filepath.Ext(p))]
		ninePatchPrefix, ninePatchSuffix := "", ""
		if ext := filepath.Ext(name); ext == ".9" {
			name = name[:len(name)-len(ext)]
			ninePatchPrefix, ninePatchSuffix = "ninepatch.New(", ")"
		}
		goName := filenameToGoName(name)
		fmt.Fprintf(out, "%s = %s&", goName, ninePatchPrefix)
		data := new(bytes.Buffer)
		switch img := img.(type) {
		case *image.Paletted:
			r := simage.Crop(img)
			crop := image.NewPaletted(r, img.Palette)
			draw.Draw(crop, crop.Rect, img, crop.Rect.Min, draw.Src)
			img = crop
			data.Write(img.Pix)
			start := data.Len()
			// Write palette.
			for _, c := range img.Palette {
				r, g, b, a := c.RGBA()
				rgb565 := rgb565.RGB888ToRGB565(uint8(r>>8), uint8(g>>8), uint8(b>>8))
				data.Write([]byte{rgb565[0], rgb565[1], uint8(a >> 8)})
			}
			b := img.Rect
			fmt.Fprintf(out, "paletted.Image{\n")
			fmt.Fprintf(out, "Pix: unsafe.Slice(unsafe.StringData(%sData[:%d]), len(%[1]sData[:%[2]d])),\n", goName, start)
			fmt.Fprintf(out, "Rect: paletted.Rectangle{MinX: %d, MinY: %d, MaxX: %d, MaxY: %d},\n", b.Min.X, b.Min.Y, b.Max.X, b.Max.Y)
			fmt.Fprintf(out, "Palette: paletted.Palette(unsafe.Slice(unsafe.StringData(%sData[%d:]), len(%[1]sData[%[2]d:]))),\n", goName, start)
		case *image.NRGBA:
			// Force efficient indexed images for now.
			log.Fatal("only indexed images are supported")

			// Convert to alpha pre-multiplied RGBA.
			rgba := image.NewRGBA(img.Bounds())
			draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
			data.Write(rgba.Pix)
			fmt.Fprintf(out, "image.RGBA{\n")
			fmt.Fprintf(out, "Pix: unsafe.Slice(unsafe.StringData(%sData), len(%[1]sData)),\n", goName)
			fmt.Fprintf(out, "Stride: %#v,\n", rgba.Stride)
			fmt.Fprintf(out, "Rect: %#v,\n", rgba.Rect)
		default:
			log.Fatalf("unsupported image format for %q: %T\n", p, img)
		}
		fmt.Fprintf(out, "}%s\n", ninePatchSuffix)
		binName := fmt.Sprintf("%s.bin", name)
		fmt.Fprintf(out, "//go:embed %s\n", binName)
		fmt.Fprintf(out, "%sData string\n\n", goName)
		if err := os.WriteFile(binName, data.Bytes(), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Fprintf(out, ")\n\n")
	src, err := format.Source(out.Bytes())
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("embed.go", src, 0o644); err != nil {
		log.Fatal(err)
	}
}

func filenameToGoName(n string) string {
	var name strings.Builder
	toTitle := true
	for _, ch := range n {
		if !unicode.IsLetter(ch) && !unicode.IsDigit(ch) {
			toTitle = true
			continue
		}
		if toTitle {
			toTitle = false
			ch = unicode.ToTitle(ch)
		}
		name.WriteRune(ch)
	}
	return name.String()
}