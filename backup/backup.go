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
	"golang.org/x/image/math/f32"
	"seedhammer.com/bc/fountain"
	"seedhammer.com/bc/ur"
	"seedhammer.com/bc/urtypes"
	"seedhammer.com/bip39"
	"seedhammer.com/engrave"
	"seedhammer.com/font"
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
	Title      string
	Descriptor urtypes.OutputDescriptor
	KeyIdx     int
	Mnemonic   bip39.Mnemonic
	Font       *font.Face
}

type Plate struct {
	Size  PlateSize
	Sides []engrave.Command
}

type measureProgram struct {
	Bounds image.Rectangle
}

func (m *measureProgram) Line(p f32.Vec2) {
	bounds := image.Rectangle{
		Min: image.Pt(int(math.Floor(float64(p[0]))), int(math.Floor(float64(p[1])))),
		Max: image.Pt(int(math.Ceil(float64(p[0]))), int(math.Ceil(float64(p[1])))),
	}
	m.Bounds = m.Bounds.Union(bounds)
}

func (m *measureProgram) Move(p f32.Vec2) {}

func measure(c engrave.Command) image.Rectangle {
	var measure measureProgram
	c.Engrave(&measure)
	return measure.Bounds
}

func dims(c engrave.Command) (engrave.Command, f32.Vec2) {
	b := measure(c)
	sz := b.Size()
	return c, f32.Vec2{float32(sz.X), float32(sz.Y)}
}

var ErrDescriptorTooLarge = errors.New("output descriptor is too large to backup")

const outerMargin = 3
const innerMargin = 10

func Engrave(strokeWidth float32, plate PlateDesc) (Plate, error) {
	for _, sz := range []PlateSize{SmallPlate, SquarePlate, LargePlate} {
		p := Plate{Size: sz}
		seedOnly := plate.Descriptor.Type == urtypes.UnknownScript
		switch {
		case seedOnly && len(plate.Mnemonic) > 12:
			p.Sides = append(p.Sides, seedBackSide(plate.Title, plate.Font, plate.Mnemonic, sz.Bounds().Size()))
		case !seedOnly:
			urs := splitUR(plate.Descriptor, plate.KeyIdx)
			p.Sides = append(p.Sides, descriptorSide(strokeWidth, plate.Font, urs, p.Size))
		}
		p.Sides = append(p.Sides, frontSide(strokeWidth, plate, p.Size))
		bounds := measure(engrave.Commands(p.Sides))
		dims := p.Size.Bounds().Size()
		safetyMargin := image.Pt(outerMargin, outerMargin)
		if !bounds.In(image.Rectangle{Min: safetyMargin, Max: dims.Sub(safetyMargin)}) {
			continue
		}
		off := p.Size.Bounds().Min
		for i, s := range p.Sides {
			p.Sides[i] = engrave.Offset(float32(off.X), float32(off.Y), s)
		}
		return p, nil
	}
	return Plate{}, ErrDescriptorTooLarge
}

// splitUR searches for the appropriate seqNum in the [UR] encoding
// that makes m-of-n backups recoverable regardless of
// which m-sized subset is used. To achieve that, we're exploiting the
// fact that the UR encoding of a fragment can contain multiple fragments,
// XOR'ed together.
//
// Schemes are implemented for backups where m == n - 1 and for 3-of-5.
//
// For m == n - 1, the data is split into m parts (seqLen in UR parlor), and m shares have parts
// assigned as follows:
//
//	1, 2, ..., m
//
// The final share contains the XOR of all m parts.
//
// The scheme can trivially recover the data when selecting the m shares each with 1
// part. For all other selections, one share will be missing, say k, but we'll have the
// final plate with every part XOR'ed together. So, k is derived by XOR'ing (canceling) every
// part other than k into the combined part.
//
// Example: a 2-of-3 setup will have data split into 2 parts, with the 3 shares assigned parts
// like so: 1, 2, 1 XOR 2. Selecting the first two plates, the data is trivially recovered;
// otherwise we have one part, say 1, and the combined part. The other part, 2, is then recovered
// by XOR'ing the one part with the combination: 1 XOR 1 XOR 2 = 2.
//
// For m == n - 2, the data is split into n + 1 parts, and each share will have two parts assigned.
//
// The assignment is as follows, where p1 and p2 denotes the two parts assigned to each share.
//
//	share    |    p1     |        p2
//	 1            1         (n+1) XOR n XOR 2
//	 2            2         (n+1) XOR 1 XOR 3
//	 3            3         (n+1) XOR 2 XOR 4
//	...          ...              ...
//	 n            n         (n+1) XOR n XOR 1
//
// That is, every share is assigned a part and the combination of the n+1 part with the neighbour
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
	case n == 5 && m == 3:
		// Optimal, but 2 parts per share. There doesn't seem to exist an
		// optimal scheme with 1 part per share.
		seqLen = n + 1
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
		if !reflect.DeepEqual(got, desc) {
			return false
		}
	}
	return true
}

const plateFontSize = 5.
const plateFontSizeUR = 4.1
const plateSmallFontSize = 3.5

func frontSide(strokeWidth float32, plate PlateDesc, size PlateSize) engrave.Command {
	var cmds engrave.Commands
	cmd := func(c engrave.Command) {
		cmds = append(cmds, c)
	}
	plateDimsI := size.Bounds().Size()
	plateDims := f32.Vec2{float32(plateDimsI.X), float32(plateDimsI.Y)}

	maxCol1 := 16
	maxCol2 := 4
	seedOnly := plate.Descriptor.Type == urtypes.UnknownScript
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
	col1, col1b := dims(wordColumn(plate.Font, plate.Mnemonic, 0, endCol1))

	// Engrave version, mfp and page.
	const version = "v1"
	const margin = outerMargin
	const metaMargin = 4
	page := fmt.Sprintf("%d/%d", plate.KeyIdx+1, len(plate.Descriptor.Keys))
	switch size {
	case SmallPlate:
		pagec, _ := dims(engrave.String(plate.Font, plateSmallFontSize, page))
		cmd(engrave.Offset(margin, plateDims[1]-innerMargin, engrave.Rotate(-math.Pi/2, pagec)))
		mfp := fmt.Sprintf("%.8x", plate.Descriptor.Keys[plate.KeyIdx].MasterFingerprint)
		mfpc, sz := dims(engrave.Rotate(-math.Pi/2, engrave.String(plate.Font, plateSmallFontSize, mfp)))
		cmd(engrave.Offset(margin, (plateDims[1]+sz[1])/2, mfpc))
		txt, sz := dims(engrave.Rotate(-math.Pi/2, engrave.String(plate.Font, plateSmallFontSize, version)))
		cmd(engrave.Offset(margin, innerMargin+sz[1], txt))
	default:
		offy := (plateDims[1]-col1b[1])/2 - metaMargin
		pagec, sz := dims(engrave.String(plate.Font, plateSmallFontSize, page))
		cmd(engrave.Offset(innerMargin, offy-sz[1], pagec))
		mfp := fmt.Sprintf("%.8x", plate.Descriptor.Keys[plate.KeyIdx].MasterFingerprint)
		mfpc, sz := dims(engrave.String(plate.Font, plateSmallFontSize, mfp))
		cmd(engrave.Offset((plateDims[0]-sz[0])/2, offy-sz[1], mfpc))
		txt, sz := dims(engrave.String(plate.Font, plateSmallFontSize, version))
		cmd(engrave.Offset(plateDims[0]-sz[0]-innerMargin, offy-sz[1], txt))
	}

	// Engrave column 1.
	cmd(engrave.Offset(innerMargin, (plateDims[1]-col1b[1])/2, col1))

	// Engrave (top of) column 2.
	endCol2 := endCol1 + maxCol2
	if endCol2 > len(plate.Mnemonic) {
		endCol2 = len(plate.Mnemonic)
	}
	cmd(engrave.Offset(44, (plateDims[1]-col1b[1])/2, wordColumn(plate.Font, plate.Mnemonic, endCol1, endCol2)))

	// Engrave seed QR.
	qr, sz := dims(engrave.QR(strokeWidth, 3, qrcode.High, seedqr.CompactQR(plate.Mnemonic)))
	cx, cy := float32(60), plateDims[1]/2
	cmd(engrave.Offset(cx-sz[0]/2, cy-sz[1]/2, qr))

	if size != SmallPlate {
		// Engrave bottom of column 2.
		col2, col2b := dims(wordColumn(plate.Font, plate.Mnemonic, endCol2, len(plate.Mnemonic)))
		cmd(engrave.Offset(44, (plateDims[1]+col1b[1])/2-col2b[1], col2))
	}

	// Engrave title.
	switch size {
	case SmallPlate:
		title, sz := dims(engrave.Rotate(-math.Pi/2, engrave.String(plate.Font, plateSmallFontSize, plate.Title)))
		cmd(engrave.Offset(plateDims[0]-margin-sz[0], (plateDims[1]+sz[1])/2, title))
	default:
		offy := (plateDims[1]+col1b[1])/2 + metaMargin
		title, sz := dims(engrave.String(plate.Font, plateSmallFontSize, plate.Title))
		cmd(engrave.Offset((plateDims[0]-sz[0])/2, offy, title))
	}
	if size == LargePlate {
		// Avoid the middle holes.
		return engrave.Offset(0, 24.5, cmds)
	}
	return cmds
}

func wordColumn(font *font.Face, mnemonic bip39.Mnemonic, start, end int) engrave.Command {
	var b strings.Builder
	for i := start; i < end; i++ {
		w := mnemonic[i]
		word := strings.ToUpper(bip39.LabelFor(w))
		fmt.Fprintf(&b, "%2d:%-8s\n", i+1, word)
	}
	cmd := engrave.String(font, plateFontSize, b.String())
	cmd.LineHeight = .8
	return cmd
}

func descriptorSide(strokeWidth float32, fnt *font.Face, urs []string, size PlateSize) engrave.Command {
	var cmds engrave.Commands
	cmd := func(c engrave.Command) {
		cmds = append(cmds, c)
	}
	const fontSize = plateFontSizeUR
	str := func(s string) engrave.Command {
		return engrave.String(fnt, fontSize, s)
	}

	plateDimsI := size.Bounds().Size()
	plateDims := f32.Vec2{float32(plateDimsI.X), float32(plateDimsI.Y)}
	// Compute character width, assuming the font is fixed width.
	charWidth, _, ok := fnt.Decode('W')
	if !ok {
		panic("W not in font")
	}
	charWidth *= fontSize
	fontHeight := fnt.Metrics.Height * fontSize
	margin := float32(outerMargin)
	if size == LargePlate {
		margin = innerMargin
	}
	holeChars := int(math.Ceil(float64(innerMargin-margin) / float64(charWidth)))
	holeLines := int(math.Ceil(float64(innerMargin-margin) / float64(fontHeight)))
	width := plateDims[0] - 2*margin
	charPerLine := int(width / charWidth)
	offy := float32(outerMargin)
	for i, ur := range urs {
		qr, qrsz := dims(engrave.QR(strokeWidth, 2, qrcode.Medium, []byte(ur)))
		const qrBorder = 2
		charPerQRLine := int((width - 2*qrBorder - qrsz[0]) / charWidth)
		qrLines := int(math.Ceil(float64((qrsz[1] + 2*qrBorder) / fontHeight)))
		qrLineStart := holeLines
		lineno := 0
		for len(ur) > 0 {
			n := charPerLine
			offx := float32(0)
			isQRLine := qrLineStart <= lineno && lineno < qrLineStart+qrLines
			if isQRLine {
				n = charPerQRLine
			}
			// Avoid screw holes on the smaller plates on the first and last lines.
			holeLine := offy+float32(lineno)*fontHeight < innerMargin ||
				offy+float32(lineno+1)*fontHeight > plateDims[1]-innerMargin
			if holeLine {
				if !isQRLine {
					// End of line.
					n -= holeChars
				}
				// Beginning of line.
				n -= holeChars
				offx = float32(holeChars) * charWidth
			}
			if n < 1 {
				n = 1
			}
			if n > len(ur) {
				n = len(ur)
			}
			s := ur[:n]
			ur = ur[n:]
			cmd(engrave.Offset(offx+margin, offy+float32(lineno)*fontHeight, str(s)))
			lineno++
		}
		qrx := plateDims[0] - qrsz[0] - margin - qrBorder
		qry := (float32(qrLineStart)+float32(qrLines)/2)*fontHeight - qrsz[1]/2
		cmd(engrave.Offset(qrx, offy+qry, qr))
		offy += float32(lineno) * fontHeight
		if i != len(urs)-1 {
			// Space UR sections.
			offy += 1
		}
	}

	return cmds
}

func seedBackSide(title string, font *font.Face, plate bip39.Mnemonic, size image.Point) engrave.Command {
	var cmds engrave.Commands
	cmd := func(c engrave.Command) {
		cmds = append(cmds, c)
	}
	const col1Words = 18
	col1, col1b := dims(wordColumn(font, plate, 12, col1Words))
	y := (float32(size.Y) - col1b[1]) / 2
	cmd(engrave.Offset(9, y, col1))
	col2 := wordColumn(font, plate, col1Words, len(plate))
	cmd(engrave.Offset(44, y, col2))
	return cmds
}
