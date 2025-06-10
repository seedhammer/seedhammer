// package backup implements the SeedHammer backup scheme.
package backup

import (
	"errors"
	"fmt"
	"image"
	"math"
	"math/bits"
	"reflect"
	"strings"

	"github.com/kortschak/qr"
	"seedhammer.com/bc/fountain"
	"seedhammer.com/bc/ur"
	"seedhammer.com/bc/urtypes"
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
	KeyIdx            int
	Mnemonic          bip39.Mnemonic
	Keys              int
	MasterFingerprint uint32
	Font              *vector.Face
	Size              PlateSize
}

type Descriptor struct {
	Descriptor urtypes.OutputDescriptor
	KeyIdx     int
	Font       *vector.Face
	Size       PlateSize
}

var ErrDescriptorTooLarge = errors.New("output descriptor is too large to backup")

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
		return ErrDescriptorTooLarge
	}
	return nil
}

func EngraveSeed(params engrave.Params, plate Seed) (engrave.Plan, error) {
	sz := plate.Size.Dims().Mul(params.Millimeter)
	side, err := frontSideSeed(params, plate, sz)
	if err != nil {
		return nil, err
	}
	if err := planFits(side, params.Millimeter, plate.Size); err != nil {
		return nil, err
	}
	return side, nil
}

func EngraveDescriptor(params engrave.Params, plate Descriptor) (engrave.Plan, error) {
	sz := plate.Size.Dims().Mul(params.Millimeter)
	urs := splitUR(plate.Descriptor, plate.KeyIdx)
	side, err := descriptorSide(params, plate.Font, urs, plate.Size, sz)
	if err != nil {
		return nil, err
	}
	if err := planFits(side, params.Millimeter, plate.Size); err != nil {
		return nil, err
	}
	return side, nil
}

// splitUR searches for the appropriate seqNum in the [UR] encoding
// that makes m-of-n backups recoverable regardless of
// which m-sized subset is used. To achieve that, we're exploiting the
// fact that the UR encoding of a fragment can contain multiple fragments,
// xor'ed together.
//
// Schemes are implemented for backups where m == n - 1 and for 3-of-5.
//
// For m == n - 1, the data is split into m parts (seqLen in UR parlor), and m shares have parts
// assigned as follows:
//
//	1, 2, ..., m
//
// The final share contains the xor of all m parts.
//
// The scheme can trivially recover the data when selecting the m shares each with 1
// part. For all other selections, one share will be missing, say k, but we'll have the
// final plate with every part xor'ed together. So, k is derived by xor'ing (canceling) every
// part other than k into the combined part.
//
// Example: a 2-of-3 setup will have data split into 2 parts, with the 3 shares assigned parts
// like so: 1, 2, 1 ⊕ 2. Selecting the first two plates, the data is trivially recovered;
// otherwise we have one part, say 1, and the combined part. The other part, 2, is then recovered
// by xor'ing the one part with the combination: 1 ⊕ 1 ⊕ 2 = 2.
//
// For 3-of-5, the data is split into 6 parts, and each share will have two parts assigned.
//
// The assignment is as follows, where p1 and p2 denotes the two parts assigned to each share.
//
//	share    |    p1     |        p2
//	 1            1         6 ⊕ 5 ⊕ 2
//	 2            2         6 ⊕ 1 ⊕ 3
//	 3            3         6 ⊕ 2 ⊕ 4
//	 4            4         6 ⊕ 3 ⊕ 5
//	 5            5         6 ⊕ 4 ⊕ 1
//
// That is, every share is assigned a part and the combination of the 6 part with the neighbour
// parts.
//
// [UR]: https://github.com/BlockchainCommons/Research/blob/master/papers/bcr-2020-005-ur.md
func splitUR(desc urtypes.OutputDescriptor, keyIdx int) (urs []string) {
	var shares [][]int
	var seqLen int
	m, n := desc.Threshold, len(desc.Keys)
	switch {
	case n-m <= 1:
		// Optimal: 1 part per share, seqLen m.
		seqLen = m
		if keyIdx < m {
			shares = [][]int{{keyIdx}}
		} else {
			all := make([]int, 0, m)
			for i := 0; i < m; i++ {
				all = append(all, i)
			}
			shares = [][]int{all}
		}
	case n == 4 && m == 2:
		// Optimal, but 2 parts per share.
		seqLen = m * 2
		switch keyIdx {
		case 0:
			shares = [][]int{{0}, {1}}
		case 1:
			shares = [][]int{{2}, {3}}
		case 2:
			shares = [][]int{{0, 2}, {1, 3}}
		case 3:
			shares = [][]int{{0, 2, 1}, {1, 3, 2}}
		}
	case n == 5 && m == 3:
		// Optimal, but 2 parts per share. There doesn't seem to exist an
		// optimal scheme with 1 part per share.
		seqLen = m * 2
		second := []int{
			n,
			(keyIdx + n - 1) % n,
			(keyIdx + 1) % n,
		}
		shares = [][]int{{keyIdx}, second}
	default:
		// Fallback: every share contains the complete data. It's only optimal
		// for 1-of-n backups.
		seqLen = 1
		shares = [][]int{{0}}
	}
	data := desc.Encode()
	check := fountain.Checksum(data)
	for _, frag := range shares {
		seqNum := fountain.SeqNumFor(seqLen, check, frag)
		qr := strings.ToUpper(ur.Encode("crypto-output", data, seqNum, seqLen))
		urs = append(urs, qr)
	}
	return
}

func Recoverable(desc urtypes.OutputDescriptor) bool {
	var shares [][]string
	for k := range desc.Keys {
		shares = append(shares, splitUR(desc, k))
	}
	// Count to all bit patterns of n length, choose the ones with
	// m bits.
	allPerm := uint64(1)<<len(desc.Keys) - 1
	for c := uint64(1); c <= allPerm; c++ {
		if bits.OnesCount64(c) != desc.Threshold {
			continue
		}
		c := c
		d := new(ur.Decoder)
		for c != 0 {
			share := bits.TrailingZeros64(c)
			c &^= 1 << share
			for _, ur := range shares[share] {
				d.Add(ur)
			}
		}
		typ, enc, err := d.Result()
		if err != nil {
			return false
		}
		if enc == nil {
			return false
		}
		got, err := urtypes.Parse(typ, enc)
		if err != nil {
			return false
		}
		gotDesc := got.(urtypes.OutputDescriptor)
		gotDesc.Title = desc.Title
		if !reflect.DeepEqual(gotDesc, desc) {
			return false
		}
	}
	return true
}

const plateFontSize = 4.1
const plateFontSizeUR = 3.8
const plateSmallFontSize = 3.

func frontSideSeed(params engrave.Params, plate Seed, plateDims image.Point) (engrave.Plan, error) {
	constant := engrave.NewConstantStringer(plate.Font, params.F(plateFontSize), bip39.ShortestWord, bip39.LongestWord)
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
	const version = "V1"
	innerMargin := params.I(innerMargin)
	metaMargin := params.I(4)
	page := fmt.Sprintf("%d/%d", plate.KeyIdx+1, plate.Keys)
	mfp := strings.ToUpper(fmt.Sprintf("%.8x", plate.MasterFingerprint))
	{
		offy := (plateDims.Y-col1Height)/2 - metaMargin
		pageStr := engrave.String(plate.Font, params.F(plateSmallFontSize), page)
		pagesz := pageStr.Measure()
		cmd(engrave.Offset(innerMargin, offy-pagesz.Y, pageStr.Engrave()))
		mfpStr := engrave.String(plate.Font, params.F(plateSmallFontSize), mfp)
		mfpsz := mfpStr.Measure()
		cmd(engrave.Offset((plateDims.X-mfpsz.X)/2, offy-mfpsz.Y, mfpStr.Engrave()))
		txtStr := engrave.String(plate.Font, params.F(plateSmallFontSize), version)
		txtsz := txtStr.Measure()
		cmd(engrave.Offset(plateDims.X-txtsz.X-innerMargin, offy-txtsz.Y, txtStr.Engrave()))
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
	qrc, err := qr.Encode(string(seedqr.QR(plate.Mnemonic)), qr.M)
	if err != nil {
		return nil, err
	}
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
		txt := constant.String(word)
		cmds = append(cmds,
			engrave.Offset(0, y, num.Engrave()),
			engrave.Offset(d.X, y, txt),
		)
		y += fontSize
	}
	return engrave.Commands(cmds...)
}

func descriptorSide(params engrave.Params, fnt *vector.Face, urs []string, size PlateSize, plateDims image.Point) (engrave.Plan, error) {
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
		qrcode, err := qr.Encode(ur, qr.M)
		if err != nil {
			return nil, err
		}
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

	return engrave.Commands(cmds...), nil
}
