package comfortaa

import (
	_ "embed"

	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

func Regular() *sfnt.Font {
	return must(regular)
}

func Bold() *sfnt.Font {
	return must(bold)
}

func must(ttf []byte) *sfnt.Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return f
}

//go:embed Comfortaa-Regular.ttf
var regular []byte

//go:embed Comfortaa-Bold.ttf
var bold []byte
