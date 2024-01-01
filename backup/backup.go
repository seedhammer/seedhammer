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

	"github.com/skip2/go-qrcode"
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
	SmallPlate PlateSize = iota
	SquarePlate
	LargePlate
)

func (p PlateSize) Bounds() image.Rectangle {
	w, h := p.dims()
	x, y := p.offset()
	return image.Rect(x, y, x+w, y+h)
}

func (p PlateSize) dims() (int, int) {
	switch p {
	case SmallPlate:
		return 85, 55
	case SquarePlate:
		return 85, 85
	case LargePlate:
		return 85, 134
	}
	panic("unreachable")
}

func (p PlateSize) offset() (int, int) {
	const x = 97
	switch p {
	case SquarePlate:
		return x, 49
	default:
		return x, 0
	}
}

type PlateDesc struct {
	Descriptor urtypes.OutputDescriptor
	KeyIdx     int
	Mnemonic   bip39.Mnemonic
	Font       *vector.Face
}

type Plate struct {
	Size  PlateSize
	Sides []engrave.Command
}

func dims(c engrave.Command) (engrave.Command, image.Point) {
	b := engrave.Measure(c)
	return engrave.Offset(-b.Min.X, -b.Min.Y, c), b.Size()
}

var ErrDescriptorTooLarge = errors.New("output descriptor is too large to backup")

const MaxTitleLen = 18

const outerMargin float32 = 3
const innerMargin float32 = 10

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

func Engrave(scale, strokeWidth float32, plate PlateDesc) (Plate, error) {
	scalef := func(v float32) int {
		return int(math.Round(float64(v * scale)))
	}
	sw := scalef(strokeWidth)
	constant := engrave.NewConstantStringer(plate.Font, scalef(plateFontSize), bip39.ShortestWord, bip39.LongestWord)
	for _, sz := range []PlateSize{SmallPlate, SquarePlate, LargePlate} {
		p := Plate{Size: sz}
		seedOnly := plate.Descriptor.Script == urtypes.UnknownScript
		b := p.Size.Bounds()
		b = image.Rect(
			scalef(float32(b.Min.X)), scalef(float32(b.Min.Y)),
			scalef(float32(b.Max.X)), scalef(float32(b.Max.Y)),
		)
		switch {
		case seedOnly && len(plate.Mnemonic) > 12:
			p.Sides = append(p.Sides, seedBackSide(scalef, constant, plate.Descriptor.Title, plate.Font, plate.Mnemonic, b.Size()))
		case !seedOnly:
			urs := splitUR(plate.Descriptor, plate.KeyIdx)
			side, err := descriptorSide(scalef, sw, plate.Font, urs, p.Size, b.Size())
			if err != nil {
				return p, err
			}
			p.Sides = append(p.Sides, side)
		}
		side, err := frontSide(scalef, constant, sw, plate, p.Size, b.Size())
		if err != nil {
			return p, err
		}
		p.Sides = append(p.Sides, side)
		bounds := engrave.Measure(engrave.Commands(p.Sides))
		safetyMargin := image.Pt(scalef(outerMargin), scalef(outerMargin))
		if !bounds.In(image.Rectangle{Min: safetyMargin, Max: b.Size().Sub(safetyMargin)}) {
			continue
		}
		for i, s := range p.Sides {
			p.Sides[i] = engrave.Offset(b.Min.X, b.Min.Y, s)
		}
		return p, nil
	}
	return Plate{}, ErrDescriptorTooLarge
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

func frontSide(scale func(float32) int, constant *engrave.ConstantStringer, strokeWidth int, plate PlateDesc, size PlateSize, plateDims image.Point) (engrave.Command, error) {
	var cmds engrave.Commands
	cmd := func(c engrave.Command) {
		cmds = append(cmds, c)
	}

	maxCol1 := 16
	maxCol2 := 4
	seedOnly := plate.Descriptor.Script == urtypes.UnknownScript
	switch {
	case seedOnly && size == SmallPlate:
		// 12 words on this side, the rest on the other.
		maxCol1 = 12
		maxCol2 = 0
	}
	endCol1 := maxCol1
	if endCol1 > len(plate.Mnemonic) {
		endCol1 = len(plate.Mnemonic)
	}
	col1, col1b := dims(wordColumn(constant, plate.Font, scale(plateFontSize), plate.Mnemonic, 0, endCol1))

	// Engrave version, mfp and page.
	const version = "V1"
	margin := scale(outerMargin)
	innerMargin := scale(innerMargin)
	metaMargin := scale(4)
	page := fmt.Sprintf("%d/%d", plate.KeyIdx+1, len(plate.Descriptor.Keys))
	mfp := strings.ToUpper(fmt.Sprintf("%.8x", plate.Descriptor.Keys[plate.KeyIdx].MasterFingerprint))
	switch size {
	case SmallPlate:
		pagec, _ := dims(engrave.String(plate.Font, scale(plateSmallFontSize), page))
		cmd(engrave.Offset(margin, plateDims.Y-innerMargin, engrave.Rotate(-math.Pi/2, pagec)))
		mfpc, sz := dims(engrave.Rotate(-math.Pi/2, engrave.String(plate.Font, scale(plateSmallFontSize), mfp)))
		cmd(engrave.Offset(margin, (plateDims.Y-sz.Y)/2, mfpc))
		txt, sz := dims(engrave.Rotate(-math.Pi/2, engrave.String(plate.Font, scale(plateSmallFontSize), version)))
		cmd(engrave.Offset(margin, innerMargin, txt))
	default:
		offy := (plateDims.Y-col1b.Y)/2 - metaMargin
		pagec, sz := dims(engrave.String(plate.Font, scale(plateSmallFontSize), page))
		cmd(engrave.Offset(innerMargin, offy-sz.Y, pagec))
		mfpc, sz := dims(engrave.String(plate.Font, scale(plateSmallFontSize), mfp))
		cmd(engrave.Offset((plateDims.X-sz.X)/2, offy-sz.Y, mfpc))
		txt, sz := dims(engrave.String(plate.Font, scale(plateSmallFontSize), version))
		cmd(engrave.Offset(plateDims.X-sz.X-innerMargin, offy-sz.Y, txt))
	}

	// Engrave column 1.
	cmd(engrave.Offset(innerMargin, (plateDims.Y-col1b.Y)/2, col1))

	// Engrave (top of) column 2.
	endCol2 := endCol1 + maxCol2
	if endCol2 > len(plate.Mnemonic) {
		endCol2 = len(plate.Mnemonic)
	}
	col2, _ := dims(wordColumn(constant, plate.Font, scale(plateFontSize), plate.Mnemonic, endCol1, endCol2))
	cmd(engrave.Offset(scale(44), (plateDims.Y-col1b.Y)/2, col2))

	// Engrave seed QR.
	qrCmd, err := engrave.ConstantQR(strokeWidth, 3, qrcode.High, seedqr.CompactQR(plate.Mnemonic))
	if err != nil {
		return nil, err
	}
	qr, sz := dims(qrCmd)
	cmd(engrave.Offset(scale(60)-sz.X/2, (plateDims.Y-sz.Y)/2, qr))

	if size != SmallPlate {
		// Engrave bottom of column 2.
		col2, col2b := dims(wordColumn(constant, plate.Font, scale(plateFontSize), plate.Mnemonic, endCol2, len(plate.Mnemonic)))
		cmd(engrave.Offset(scale(44), (plateDims.Y+col1b.Y)/2-col2b.Y, col2))
	}

	// Engrave title.
	title := strings.ToUpper(plate.Descriptor.Title)
	switch size {
	case SmallPlate:
		title, sz := dims(engrave.Rotate(-math.Pi/2, engrave.String(plate.Font, scale(plateSmallFontSize), title)))
		cmd(engrave.Offset(plateDims.X-margin-sz.X, (plateDims.Y-sz.Y)/2, title))
	default:
		offy := (plateDims.Y+col1b.Y)/2 + metaMargin
		title, sz := dims(engrave.String(plate.Font, scale(plateSmallFontSize), title))
		cmd(engrave.Offset((plateDims.X-sz.X)/2, offy, title))
	}
	if size == LargePlate {
		// Avoid the middle holes.
		return engrave.Offset(0, scale(24.5), cmds), nil
	}
	return cmds, nil
}

func wordColumn(constant *engrave.ConstantStringer, font *vector.Face, fontSize int, mnemonic bip39.Mnemonic, start, end int) engrave.Command {
	var cmds engrave.Commands
	y := 0
	for i := start; i < end; i++ {
		num := engrave.String(font, fontSize, fmt.Sprintf("%2d ", i+1))
		d := num.Measure()
		w := mnemonic[i]
		word := strings.ToUpper(bip39.LabelFor(w))
		txt := constant.String(word)
		cmds = append(cmds,
			engrave.Offset(0, y, num),
			engrave.Offset(d.X, y, txt),
		)
		y += d.Y
	}
	return cmds
}

func descriptorSide(scale func(float32) int, strokeWidth int, fnt *vector.Face, urs []string, size PlateSize, plateDims image.Point) (engrave.Command, error) {
	var cmds engrave.Commands
	cmd := func(c engrave.Command) {
		cmds = append(cmds, c)
	}
	fontSize := scale(plateFontSizeUR)
	str := func(s string) engrave.Command {
		return engrave.String(fnt, fontSize, s)
	}

	// Compute character width, assuming the font is fixed width.
	charWidthf, _, ok := fnt.Decode('W')
	if !ok {
		panic("W not in font")
	}
	charWidth := int(float32(charWidthf*fontSize) / float32(fnt.Metrics().Height))
	margin := scale(outerMargin)
	innerMargin := scale(innerMargin)
	if size == LargePlate {
		margin = innerMargin
	}
	holeChars := int(math.Ceil(float64(innerMargin-margin) / float64(charWidth)))
	holeLines := int(math.Ceil(float64(innerMargin-margin) / float64(fontSize)))
	width := plateDims.X - 2*margin
	charPerLine := int(width / charWidth)
	offy := scale(outerMargin)
	for i, ur := range urs {
		qrcmd, err := engrave.QR(strokeWidth, 2, qrcode.Medium, []byte(ur))
		if err != nil {
			return nil, err
		}
		qr, qrsz := dims(qrcmd)
		qrBorder := scale(2)
		charPerQRLine := (width - 2*qrBorder - qrsz.X) / charWidth
		qrLines := (qrsz.Y + 2*qrBorder + fontSize - 1) / fontSize
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
		qrx := plateDims.X - qrsz.X - margin - qrBorder
		qry := qrLineStart*fontSize + (qrLines*fontSize-qrsz.Y)/2
		cmd(engrave.Offset(qrx, offy+qry, qr))
		offy += lineno * fontSize
		if i != len(urs)-1 {
			// Space UR sections.
			offy += scale(1)
		}
	}

	return cmds, nil
}

func seedBackSide(scale func(float32) int, constant *engrave.ConstantStringer, title string, font *vector.Face, plate bip39.Mnemonic, size image.Point) engrave.Command {
	var cmds engrave.Commands
	cmd := func(c engrave.Command) {
		cmds = append(cmds, c)
	}
	const col1Words = 18
	col1, col1b := dims(wordColumn(constant, font, scale(plateFontSize), plate, 12, col1Words))
	y := (size.Y - col1b.Y) / 2
	cmd(engrave.Offset(scale(9), y, col1))
	col2 := wordColumn(constant, font, scale(plateFontSize), plate, col1Words, len(plate))
	cmd(engrave.Offset(scale(44), y, col2))
	return cmds
}
