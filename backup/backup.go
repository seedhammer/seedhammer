// package backup implements the SeedHammer backup scheme.
package backup

import (
	"fmt"
	"image"
	"math"
	"strings"

	qr "github.com/seedhammer/kortschak-qr"
	"seedhammer.com/engrave"
	"seedhammer.com/font/vector"
)

type Seed struct {
	Title             string
	Mnemonic          []string
	ShortestWord      int
	LongestWord       int
	QR                *qr.Code
	MasterFingerprint uint32
	Font              *vector.Face
}

type SeedString struct {
	Title             string
	Seed              string
	MasterFingerprint uint32
	Font              *vector.Face
}

type Text struct {
	Paragraphs []Paragraph
	Font       *vector.Face
}

type Paragraph struct {
	Text    string
	QR      *qr.Code
	QRScale int
}

const MaxTitleLen = 18

const outerMargin = 3
const innerMargin = 10

func TitleString(face *vector.Face, s string) string {
	s = strings.ToUpper(s)
	res := ""
	for _, r := range s {
		if _, _, valid := face.Decode(r); valid {
			res += string(r)
		}
		if len(res) == MaxTitleLen {
			break
		}
	}
	return res
}

func EngraveSeed(params engrave.Params, plate Seed) (engrave.Engraving, error) {
	var qrc *engrave.ConstantQRCmd
	if plate.QR != nil {
		var err error
		qrc, err = engrave.ConstantQR(plate.QR)
		if err != nil {
			return nil, err
		}
	}
	side := frontSideSeed(params, plate, qrc)
	return side, nil
}

func EngraveSeedString(params engrave.Params, plate SeedString) (engrave.Engraving, error) {
	seed := strings.ToUpper(plate.Seed)
	qrc, err := qr.Encode(seed, qr.M)
	if err != nil {
		return nil, err
	}
	qrCmd, err := engrave.ConstantQR(qrc)
	if err != nil {
		return nil, err
	}
	side := engraveSeedString(params, plate, qrCmd)
	return side, nil
}

const plateFontSize = 4.1
const plateFontSizeUR = 3.8
const plateSmallFontSize = 3.

const groupLen = 10

func engraveSeedString(params engrave.Params, plate SeedString, qrc *engrave.ConstantQRCmd) engrave.Engraving {
	pfs := params.F(plateFontSize)
	constant := engrave.NewConstantStringer(plate.Font, params, pfs)
	return func(yield func(engrave.Command) bool) {
		plateDims := image.Point{
			X: params.F(85),
			Y: params.F(85),
		}
		t := engrave.NewTransform(yield)

		const (
			maxCol1 = 16
			maxCol2 = 4
			qrScale = 3
		)
		seed := strings.ToUpper(plate.Seed)
		ngroups := (len(seed) + groupLen - 1) / groupLen
		endCol1 := min(ngroups, maxCol1)
		qrsz := qrc.Size * params.StrokeWidth * qrScale
		col1Height := max(qrsz, pfs*endCol1)

		// Engrave version, mfp and page.
		innerMargin := params.I(innerMargin)
		metaMargin := params.I(4)
		if plate.MasterFingerprint != 0 {
			mfp := strings.ToUpper(fmt.Sprintf("%.8x", plate.MasterFingerprint))
			offy := (plateDims.Y-col1Height)/2 - metaMargin
			mfpStr := engrave.String(plate.Font, params.F(plateSmallFontSize), mfp)
			mfpszX, mfpszY := mfpStr.Measure()
			t.Offset((plateDims.X-mfpszX)/2, offy-mfpszY)
			mfpStr.Engrave(t.Yield)
		}

		// Engrave column 1.
		off := t.Offset(innerMargin, (plateDims.Y-col1Height)/2)
		stringColumn(off, constant, plate.Font, pfs, seed, 0, endCol1)

		// Engrave (top of) column 2.
		endCol2 := min(ngroups, endCol1+maxCol2)
		off = t.Offset(params.I(44), (plateDims.Y-col1Height)/2)
		stringColumn(off, constant, plate.Font, pfs, seed, endCol1, endCol2)

		// Engrave seed QR.
		qrCmd := qrc.Engrave(params.StepperConfig, params.StrokeWidth, qrScale)
		t.Offset(params.I(60)-qrsz/2, (plateDims.Y-qrsz)/2)
		qrCmd(t.Yield)

		{
			// Engrave bottom of column 2.
			height := (ngroups - endCol2) * pfs
			off := t.Offset(params.I(44), (plateDims.Y+col1Height)/2-height)
			stringColumn(off, constant, plate.Font, pfs, seed, endCol2, ngroups)
		}

		// Engrave title.
		title := strings.ToUpper(plate.Title)
		{
			offy := (plateDims.Y+col1Height)/2 + metaMargin
			title := engrave.String(plate.Font, params.F(plateSmallFontSize), title)
			titleWidth, _ := title.Measure()
			t.Offset((plateDims.X-titleWidth)/2, offy)
			title.Engrave(t.Yield)
		}
	}
}

func frontSideSeed(params engrave.Params, plate Seed, qrc *engrave.ConstantQRCmd) engrave.Engraving {
	return func(yield func(engrave.Command) bool) {
		plateDims := image.Point{
			X: params.F(85),
			Y: params.F(85),
		}
		t := engrave.NewTransform(yield)
		pfs := params.F(plateFontSize)
		constant := engrave.NewConstantStringer(plate.Font, params, pfs)

		const (
			maxCol1 = 16
			maxCol2 = 4
		)
		endCol1 := maxCol1
		if endCol1 > len(plate.Mnemonic) {
			endCol1 = len(plate.Mnemonic)
		}
		col1Height := pfs * endCol1

		// Engrave master fingerprint.
		innerMargin := params.I(innerMargin)
		metaMargin := params.I(4)
		if plate.MasterFingerprint != 0 {
			mfp := strings.ToUpper(fmt.Sprintf("%.8x", plate.MasterFingerprint))
			offy := (plateDims.Y-col1Height)/2 - metaMargin
			mfpStr := engrave.String(plate.Font, params.F(plateSmallFontSize), mfp)
			mfpszX, mfpszY := mfpStr.Measure()
			t.Offset((plateDims.X-mfpszX)/2, offy-mfpszY)
			mfpStr.Engrave(t.Yield)
		}

		// Engrave column 1.
		off := t.Offset(innerMargin, (plateDims.Y-col1Height)/2)
		wordColumn(off, constant, plate.Font, pfs, plate.Mnemonic, plate.ShortestWord, plate.LongestWord, 0, endCol1)

		// Engrave (top of) column 2.
		endCol2 := endCol1 + maxCol2
		if endCol2 > len(plate.Mnemonic) {
			endCol2 = len(plate.Mnemonic)
		}
		off = t.Offset(params.I(44), (plateDims.Y-col1Height)/2)
		wordColumn(off, constant, plate.Font, pfs, plate.Mnemonic, plate.ShortestWord, plate.LongestWord, endCol1, endCol2)

		// Engrave seed QR.
		if qrc != nil {
			const qrScale = 3
			qrCmd := qrc.Engrave(params.StepperConfig, params.StrokeWidth, qrScale)
			qrsz := qrc.Size * params.StrokeWidth * qrScale
			t.Offset(params.I(60)-qrsz/2, (plateDims.Y-qrsz)/2)
			qrCmd(t.Yield)
		}

		{
			// Engrave bottom of column 2.
			height := (len(plate.Mnemonic) - endCol2) * pfs
			off := t.Offset(params.I(44), (plateDims.Y+col1Height)/2-height)
			wordColumn(off, constant, plate.Font, pfs, plate.Mnemonic, plate.ShortestWord, plate.LongestWord, endCol2, len(plate.Mnemonic))
		}

		// Engrave title.
		title := strings.ToUpper(plate.Title)
		{
			offy := (plateDims.Y+col1Height)/2 + metaMargin
			title := engrave.String(plate.Font, params.F(plateSmallFontSize), title)
			titleWidth, _ := title.Measure()
			t.Offset((plateDims.X-titleWidth)/2, offy)
			title.Engrave(t.Yield)
		}
	}
}

func wordColumn(t engrave.Transform, constant *engrave.ConstantStringer, font *vector.Face, fontSize int, mnemonic []string, shortest, longest, start, end int) {
	y := 0
	for i := start; i < end; i++ {
		num := engrave.String(font, fontSize, fmt.Sprintf("%2d ", i+1))
		width, _ := num.Measure()
		w := mnemonic[i]
		word := strings.ToUpper(w)
		t.Offset(0, y)
		num.Engrave(t.Yield)
		t.Offset(width, y)
		constant.PaddedString(t.Yield, word, shortest, longest)
		y += fontSize
	}
}

func stringColumn(t engrave.Transform, constant *engrave.ConstantStringer, font *vector.Face, fontSize int, s string, start, end int) {
	y := 0
	for i := start; i < end; i++ {
		word := s[i*groupLen:]
		word = word[:min(len(word), groupLen)]
		constant.String(t.Offset(0, y).Yield, word)
		y += fontSize
	}
}

func EngraveText(params engrave.Params, plate Text) engrave.Engraving {
	return func(yield func(engrave.Command) bool) {
		t := engrave.NewTransform(yield)
		fontSize := params.F(plateFontSizeUR)
		fnt := plate.Font

		// Compute character width, assuming the font is fixed width.
		charWidthf, _, ok := fnt.Decode('W')
		if !ok {
			panic("W not in font")
		}
		charWidth := int(float32(charWidthf*fontSize) / float32(fnt.Metrics().Height))
		margin := params.I(outerMargin)
		innerMargin := params.I(innerMargin)
		holeChars := int(math.Ceil(float64(innerMargin-margin) / float64(charWidth)))
		holeLines := int(math.Ceil(float64(innerMargin-margin) / float64(fontSize)))
		plateDims := image.Point{
			X: params.F(85),
			Y: params.F(85),
		}
		width := plateDims.X - 2*margin
		charPerLine := int(width / charWidth)
		offy := params.I(outerMargin)
		for i, p := range plate.Paragraphs {
			qrLines := 0
			charPerQRLine := 0
			qrsz := 0
			qrBorder := params.I(2)
			var qr engrave.Engraving
			if p.QR != nil {
				qrScale := p.QRScale
				if qrScale == 0 {
					qrScale = 2
				}
				qr = engrave.QR(params.StrokeWidth, qrScale, p.QR)
				qrsz = p.QR.Size * params.StrokeWidth * qrScale
				charPerQRLine = (width - 2*qrBorder - qrsz) / charWidth
				qrLines = (qrsz + 2*qrBorder + fontSize - 1) / fontSize
			}
			lineno := 0
			txt := p.Text
			for len(txt) > 0 {
				n := charPerLine
				offx := 0
				isQRLine := holeLines <= lineno && lineno < holeLines+qrLines
				if isQRLine {
					n = charPerQRLine
				}
				// Avoid screw holes on the smaller plates on the first and last lines.
				holeLine := offy+lineno*fontSize < innerMargin ||
					offy+(lineno+1)*fontSize > plateDims.Y-innerMargin
				if holeLine {
					if !isQRLine {
						// End of line.
						n -= holeChars
					}
					// Beginning of line.
					n -= holeChars
					offx = holeChars * charWidth
				}
				if n < 1 {
					n = 1
				}
				if l := len(txt); n > l {
					n = l
				}
				s := txt[:n]
				txt = txt[n:]
				t.Offset(offx+margin, offy+lineno*fontSize)
				engrave.String(fnt, fontSize, s).Engrave(t.Yield)
				lineno++
			}
			if qr != nil {
				qrx := plateDims.X - qrsz - margin - qrBorder
				qry := offy + holeLines*fontSize + (qrLines*fontSize-qrsz)/2
				if len(p.Text) == 0 {
					// Center QR.
					qrx, qry = (plateDims.X-qrsz)/2, (plateDims.Y-qrsz)/2
				}
				t.Offset(qrx, qry)
				qr(t.Yield)
			}
			offy += lineno * fontSize
			if i != len(plate.Paragraphs)-1 {
				// Space UR sections.
				offy += params.I(1)
			}
		}
	}
}
