// package backup implements the SeedHammer backup scheme.
package backup

import (
	"errors"
	"fmt"
	"image"
	"math"
	"strings"

	"github.com/seedhammer/kortschak-qr"
	"seedhammer.com/bip39"
	"seedhammer.com/engrave"
	"seedhammer.com/font/vector"
	"seedhammer.com/seedqr"
)

type PlateSize int

const (
	SquarePlate PlateSize = iota
	LargePlate
)

func (p PlateSize) Dims() image.Point {
	switch p {
	case SquarePlate:
		return image.Pt(85, 85)
	case LargePlate:
		return image.Pt(85, 134)
	}
	panic("unreachable")
}

type Seed struct {
	Title             string
	Mnemonic          bip39.Mnemonic
	MasterFingerprint uint32
	Font              *vector.Face
	Size              PlateSize
}

type Text struct {
	Data []string
	Font *vector.Face
	Size PlateSize
}

var ErrTooLarge = errors.New("backup: data does not fit plate")

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

func planFits(plan engrave.Plan, scale int, size PlateSize) error {
	sz := size.Dims().Mul(scale)
	bounds := engrave.Measure(plan)
	safetyMargin := image.Pt(outerMargin*scale, outerMargin*scale)
	if !bounds.In(image.Rectangle{Min: safetyMargin, Max: sz.Sub(safetyMargin)}) {
		return ErrTooLarge
	}
	return nil
}

func EngraveSeed(params engrave.Params, plate Seed) (engrave.Plan, error) {
	sz := plate.Size.Dims().Mul(params.Millimeter)
	qrc, err := qr.Encode(string(seedqr.QR(plate.Mnemonic)), qr.M)
	if err != nil {
		return nil, err
	}
	side, err := frontSideSeed(params, plate, qrc, sz)
	if err != nil {
		return nil, err
	}
	if err := planFits(side, params.Millimeter, plate.Size); err != nil {
		return nil, err
	}
	return side, nil
}

func EngraveText(params engrave.Params, plate Text) (engrave.Plan, error) {
	sz := plate.Size.Dims().Mul(params.Millimeter)
	urQRs := make([]*qr.Code, 0, len(plate.Data))
	for _, s := range plate.Data {
		qrcode, err := qr.Encode(s, qr.M)
		if err != nil {
			return nil, err
		}
		urQRs = append(urQRs, qrcode)
	}
	side := textSide(params, plate.Font, plate.Data, urQRs, plate.Size, sz)
	if err := planFits(side, params.Millimeter, plate.Size); err != nil {
		return nil, err
	}
	return side, nil
}

const plateFontSize = 4.1
const plateFontSizeUR = 3.8
const plateSmallFontSize = 3.

func frontSideSeed(params engrave.Params, plate Seed, qrc *qr.Code, plateDims image.Point) (engrave.Plan, error) {
	constant := engrave.NewConstantStringer(plate.Font)
	var cmds []engrave.Plan
	cmd := func(c engrave.Plan) {
		cmds = append(cmds, c)
	}

	maxCol1 := 16
	maxCol2 := 4
	endCol1 := maxCol1
	if endCol1 > len(plate.Mnemonic) {
		endCol1 = len(plate.Mnemonic)
	}
	pfs := params.F(plateFontSize)
	col1 := wordColumn(constant, plate.Font, pfs, plate.Mnemonic, 0, endCol1)
	col1Height := pfs * endCol1

	// Engrave version, mfp and page.
	innerMargin := params.I(innerMargin)
	metaMargin := params.I(4)
	mfp := strings.ToUpper(fmt.Sprintf("%.8x", plate.MasterFingerprint))
	{
		offy := (plateDims.Y-col1Height)/2 - metaMargin
		mfpStr := engrave.String(plate.Font, params.F(plateSmallFontSize), mfp)
		mfpsz := mfpStr.Measure()
		cmd(engrave.Offset((plateDims.X-mfpsz.X)/2, offy-mfpsz.Y, mfpStr.Engrave()))
	}

	// Engrave column 1.
	cmd(engrave.Offset(innerMargin, (plateDims.Y-col1Height)/2, col1))

	// Engrave (top of) column 2.
	endCol2 := endCol1 + maxCol2
	if endCol2 > len(plate.Mnemonic) {
		endCol2 = len(plate.Mnemonic)
	}
	col2 := wordColumn(constant, plate.Font, params.F(plateFontSize), plate.Mnemonic, endCol1, endCol2)
	cmd(engrave.Offset(params.I(44), (plateDims.Y-col1Height)/2, col2))

	// Engrave seed QR.
	const qrScale = 3
	qrCmd, err := engrave.ConstantQR(params.StrokeWidth, qrScale, qrc)
	if err != nil {
		return nil, err
	}
	qrsz := qrc.Size * params.StrokeWidth * qrScale
	cmd(engrave.Offset(params.I(60)-qrsz/2, (plateDims.Y-qrsz)/2, qrCmd))

	{
		// Engrave bottom of column 2.
		fs := params.F(plateFontSize)
		col2 := wordColumn(constant, plate.Font, fs, plate.Mnemonic, endCol2, len(plate.Mnemonic))
		height := (len(plate.Mnemonic) - endCol2) * fs
		cmd(engrave.Offset(params.I(44), (plateDims.Y+col1Height)/2-height, col2))
	}

	// Engrave title.
	title := strings.ToUpper(plate.Title)
	{
		offy := (plateDims.Y+col1Height)/2 + metaMargin
		title := engrave.String(plate.Font, params.F(plateSmallFontSize), title)
		titlesz := title.Measure()
		cmd(engrave.Offset((plateDims.X-titlesz.X)/2, offy, title.Engrave()))
	}
	all := engrave.Commands(cmds...)
	if plate.Size == LargePlate {
		// Avoid the middle holes.
		return engrave.Offset(0, params.F(24.5), all), nil
	}
	return all, nil
}

func wordColumn(constant *engrave.ConstantStringer, font *vector.Face, fontSize int, mnemonic bip39.Mnemonic, start, end int) engrave.Plan {
	var cmds []engrave.Plan
	y := 0
	for i := start; i < end; i++ {
		num := engrave.String(font, fontSize, fmt.Sprintf("%2d ", i+1))
		d := num.Measure()
		w := mnemonic[i]
		word := strings.ToUpper(bip39.LabelFor(w))
		txt := constant.String(word, fontSize, bip39.LongestWord)
		cmds = append(cmds,
			engrave.Offset(0, y, num.Engrave()),
			engrave.Offset(d.X, y, txt),
		)
		y += fontSize
	}
	return engrave.Commands(cmds...)
}

func textSide(params engrave.Params, fnt *vector.Face, urs []string, urQRs []*qr.Code, size PlateSize, plateDims image.Point) engrave.Plan {
	var cmds []engrave.Plan
	cmd := func(c engrave.Plan) {
		cmds = append(cmds, c)
	}
	fontSize := params.F(plateFontSizeUR)
	str := func(s string) engrave.Plan {
		return engrave.String(fnt, fontSize, s).Engrave()
	}

	// Compute character width, assuming the font is fixed width.
	charWidthf, _, ok := fnt.Decode('W')
	if !ok {
		panic("W not in font")
	}
	charWidth := int(float32(charWidthf*fontSize) / float32(fnt.Metrics().Height))
	margin := params.I(outerMargin)
	innerMargin := params.I(innerMargin)
	if size == LargePlate {
		margin = innerMargin
	}
	holeChars := int(math.Ceil(float64(innerMargin-margin) / float64(charWidth)))
	holeLines := int(math.Ceil(float64(innerMargin-margin) / float64(fontSize)))
	width := plateDims.X - 2*margin
	charPerLine := int(width / charWidth)
	offy := params.I(outerMargin)
	for i, ur := range urs {
		qrcode := urQRs[i]
		const qrScale = 2
		qr := engrave.QR(params.StrokeWidth, qrScale, qrcode)
		qrsz := qrcode.Size * params.StrokeWidth * qrScale
		qrBorder := params.I(2)
		charPerQRLine := (width - 2*qrBorder - qrsz) / charWidth
		qrLines := (qrsz + 2*qrBorder + fontSize - 1) / fontSize
		qrLineStart := holeLines
		lineno := 0
		for len(ur) > 0 {
			n := charPerLine
			offx := 0
			isQRLine := qrLineStart <= lineno && lineno < qrLineStart+qrLines
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
			if n > len(ur) {
				n = len(ur)
			}
			s := ur[:n]
			ur = ur[n:]
			cmd(engrave.Offset(offx+margin, offy+lineno*fontSize, str(s)))
			lineno++
		}
		qrx := plateDims.X - qrsz - margin - qrBorder
		qry := qrLineStart*fontSize + (qrLines*fontSize-qrsz)/2
		cmd(engrave.Offset(qrx, offy+qry, qr))
		offy += lineno * fontSize
		if i != len(urs)-1 {
			// Space UR sections.
			offy += params.I(1)
		}
	}

	return engrave.Commands(cmds...)
}
