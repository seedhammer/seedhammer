package gui

import (
	"image/color"

	"seedhammer.com/font/comfortaa"
	"seedhammer.com/font/poppins"
	"seedhammer.com/gui/text"
)

var theme struct {
	overlayMask  uint8
	activeMask   uint8
	inactiveMask uint8
}

type Styles struct {
	title    text.Style
	subtitle text.Style
	body     text.Style
	lead     text.Style
	button   text.Style
	word     text.Style
	keyboard text.Style
	warning  text.Style
	nav      text.Style
	debug    text.Style
	progress text.Style
}

type Colors struct {
	Background color.NRGBA
	Text       color.NRGBA
	Primary    color.NRGBA
}

var (
	descriptorTheme Colors
	singleTheme     Colors
	engraveTheme    Colors
	cameraTheme     Colors
)

const leadingSize = 44

func init() {
	prim := rgb(0x02427d)
	descriptorTheme = Colors{
		Background: rgb(0x267f26),
		Text:       rgb(0xe9f2ea),
		Primary:    prim,
	}
	singleTheme = Colors{
		Background: rgb(0xdd9700),
		Text:       rgb(0xfbf4e8),
		Primary:    prim,
	}
	engraveTheme = Colors{
		Background: rgb(0xd1e83cb),
		Text:       rgb(0xdffffff),
		Primary:    prim,
	}
	cameraTheme = Colors{
		Text: rgb(0xfbf4e8),
	}
	theme.overlayMask = 0x55
	theme.activeMask = 0x55
	theme.inactiveMask = 0x55
}

func NewStyles() Styles {
	p := poppins.Regular()
	pBold := poppins.Bold()
	cmfBold := comfortaa.Bold()

	return Styles{
		title: text.Style{
			Face:          mustFace(pBold, 23),
			Alignment:     text.AlignCenter,
			LetterSpacing: -1,
			LineHeight:    0.75,
		},
		body: text.Style{
			Face:       mustFace(p, 16),
			LineHeight: 0.75,
		},
		debug: text.Style{
			Face: mustFace(pBold, 10),
		},
		warning: text.Style{
			Face:       mustFace(pBold, 23),
			LineHeight: 0.75,
			Alignment:  text.AlignCenter,
		},
		lead: text.Style{
			Face:       mustFace(p, 16),
			LineHeight: 0.9,
			Alignment:  text.AlignCenter,
		},
		subtitle: text.Style{
			Face:       mustFace(pBold, 16),
			LineHeight: 0.9,
		},
		nav: text.Style{
			Face: mustFace(pBold, 24),
		},
		button: text.Style{
			Face:       mustFace(pBold, 20),
			Alignment:  text.AlignCenter,
			LineHeight: 0.70,
		},
		word: text.Style{
			Face: mustFace(cmfBold, 17),
		},
		keyboard: text.Style{
			Face: mustFace(pBold, 16),
		},
		progress: text.Style{
			Face:          mustFace(pBold, 45),
			Alignment:     text.AlignCenter,
			LetterSpacing: -1,
		},
	}
}
