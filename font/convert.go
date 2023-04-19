//go:build ignore

package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/f32"
	"golang.org/x/image/math/fixed"
	"seedhammer.com/affine"
	sfont "seedhammer.com/font"
)

var packageName = flag.String("package", "main", "package name")

func main() {
	flag.Parse()

	if flag.NArg() != 2 {
		fmt.Fprintf(os.Stderr, "usage: convert infile outfile\n")
		os.Exit(1)
	}

	infile := flag.Arg(0)
	data, err := os.ReadFile(infile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	conv, err := convert(filepath.Ext(infile), data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse %q: %v\n", infile, err)
		os.Exit(1)
	}
	var output bytes.Buffer
	fname := filepath.Base(infile)
	ext := filepath.Ext(fname)
	fname = fname[:len(fname)-len(ext)]
	fmt.Fprintf(&output, "// Code generated DO NOT EDIT.\npackage %s\n", *packageName)
	fmt.Fprintf(&output, "import \"seedhammer.com/font\"\nvar Font = %#v\n", *conv)
	formatted, err := format.Source(output.Bytes())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to format output: %v\n", err)
		os.Exit(2)
	}
	if err := os.WriteFile(flag.Arg(1), formatted, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func convert(ext string, data []byte) (*sfont.Face, error) {
	switch ext {
	case ".ttf":
		face, err := sfnt.Parse(data)
		if err != nil {
			return nil, err
		}
		return convertFont(face)
	case ".svg":
		face, err := convertSVG(data)
		if err != nil {
			return nil, err
		}
		return face, nil
	default:
		return nil, errors.New("unsupported file type")
	}
}

type MetaData struct {
	Advance, Height, Baseline float64
}

func convertSVG(svg []byte) (*sfont.Face, error) {
	d := xml.NewDecoder(bytes.NewReader(svg))
	for {
		root, err := d.Token()
		if err != nil {
			return nil, err
		}
		t, ok := root.(xml.StartElement)
		if !ok {
			continue
		}
		if !ok || t.Name.Local != "svg" {
			return nil, errors.New("missing <svg> root element")
		}
		var face sfont.Face
		meta, err := parseMeta(svg)
		if err != nil {
			return nil, err
		}
		ascent := meta.Baseline
		face.Metrics.Ascent = float32(ascent)
		face.Metrics.Height = float32(meta.Height)
		adv := meta.Advance
		face.Index[' '] = sfont.Glyph{
			Advance: float32(adv),
		}
		err = parseChars(&face, d, adv, ascent)
		return &face, err
	}
}

func parseMeta(data []byte) (*MetaData, error) {
	type Line struct {
		ID string  `xml:"id,attr"`
		X1 float64 `xml:"x1,attr"`
		Y1 float64 `xml:"y1,attr"`
		X2 float64 `xml:"x2,attr"`
		Y2 float64 `xml:"y2,attr"`
	}
	type SVG struct {
		XMLName xml.Name `xml:"svg"`
		Lines   []Line   `xml:"line"`
	}
	var svg SVG
	if err := xml.Unmarshal(data, &svg); err != nil {
		return nil, err
	}
	var meta MetaData
	for _, line := range svg.Lines {
		switch line.ID {
		case "advance":
			meta.Advance = line.X2 - line.X1
		case "height":
			meta.Height = line.Y2 - line.Y1
		case "baseline":
			meta.Baseline = line.Y1
		}
	}
	return &meta, nil
}

func findAttr(e xml.StartElement, name string) (string, bool) {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

func parseChars(face *sfont.Face, d *xml.Decoder, adv, ascent float64) error {
	offx := 0.
	for {
		t, err := d.Token()
		if err != nil {
			if err != io.EOF {
				return err
			}
			break
		}
		e, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch e.Name.Local {
		case "style":
			if err := d.Skip(); err != nil {
				return err
			}
			continue
		}
		id, _ := findAttr(e, "id")
		switch id {
		case "advance", "height", "baseline", "size":
			// Skip anonymous and meta-data elements.
			if err := d.Skip(); err != nil {
				return err
			}
			continue
		}
		r, ok := mapChar(id)
		if !ok {
			return fmt.Errorf("unknown character id: %q", id)
		}
		idxStart := len(face.Segments)
		if err := parseSegments(face, d, e, offx, -ascent); err != nil {
			return err
		}
		idxEnd := len(face.Segments)
		face.Index[r] = sfont.Glyph{
			Advance: float32(adv),
			Start:   uint16(idxStart),
			End:     uint16(idxEnd),
		}
		offx -= adv
	}
	return nil
}

func parseSegments(face *sfont.Face, d *xml.Decoder, e xml.StartElement, offx, offy float64) error {
	encode := func(op sfont.SegmentOp, args ...f32.Vec2) {
		face.Segments = append(face.Segments, uint32(op))
		for _, a := range args {
			face.Segments = append(face.Segments, math.Float32bits(a[0]), math.Float32bits(a[1]))
		}
	}
	switch n := e.Name.Local; n {
	case "g":
		for {
			t, err := d.Token()
			if err != nil {
				return err
			}
			switch t := t.(type) {
			case xml.StartElement:
				if err := parseSegments(face, d, t, offx, offy); err != nil {
					return err
				}
			case xml.EndElement:
				return nil
			}
		}
	case "line":
		var line struct {
			X1 float64 `xml:"x1,attr"`
			Y1 float64 `xml:"y1,attr"`
			X2 float64 `xml:"x2,attr"`
			Y2 float64 `xml:"y2,attr"`
		}
		if err := d.DecodeElement(&line, &e); err != nil {
			return err
		}
		line.X1 = line.X1 + offx
		line.Y1 = line.Y1 + offy
		line.X2 = line.X2 + offx
		line.Y2 = line.Y2 + offy
		encode(sfont.SegmentOpMoveTo, f32.Vec2{float32(line.X1), float32(line.Y1)})
		encode(sfont.SegmentOpLineTo, f32.Vec2{float32(line.X2), float32(line.Y2)})
		return nil
	case "polyline":
		points, ok := findAttr(e, "points")
		if !ok {
			return errors.New("missing points attribute for <polyline>")
		}
		points = strings.TrimSpace(points)
		coords := strings.Split(points, " ")
		for i, c := range coords {
			var x, y float64
			if _, err := fmt.Sscanf(c, "%f,%f", &x, &y); err != nil {
				return fmt.Errorf("invalid coordinates %q in <polyline>:", c)
			}
			x = x + offx
			y = y + offy
			op := sfont.SegmentOpLineTo
			if i == 0 {
				op = sfont.SegmentOpMoveTo
			}
			encode(op, f32.Vec2{float32(x), float32(y)})
		}
		return d.Skip()
	case "path":
		cmds, ok := findAttr(e, "d")
		if !ok {
			return errors.New("missing d attribute for <path>")
		}
		cmds = strings.TrimSpace(cmds)
		pen := f32.Vec2{float32(offx), float32(offy)}
		initPoint := pen
		ctrl2 := pen
		for {
			cmds = strings.TrimLeft(cmds, " ,\t\n")
			if len(cmds) == 0 {
				break
			}
			orig := cmds
			op := rune(cmds[0])
			cmds = cmds[1:]
			switch op {
			case 'M', 'm', 'V', 'v', 'L', 'l', 'H', 'h', 'C', 'c', 'S', 's':
			case 'Z', 'z':
				if pen != initPoint {
					encode(sfont.SegmentOpLineTo, initPoint)
					pen = initPoint
				}
				ctrl2 = initPoint
				continue
			default:
				return fmt.Errorf("unknown <path> command %s in %q", string(op), orig)
			}
			var coords []float64
			for {
				cmds = strings.TrimLeft(cmds, " ,\t\n")
				if len(cmds) == 0 {
					break
				}
				n, x, ok := parseFloat(cmds)
				if !ok {
					break
				}
				cmds = cmds[n:]
				coords = append(coords, x)
			}
			rel := unicode.IsLower(op)
			newPen := pen
			switch unicode.ToLower(op) {
			case 'h':
				for _, x := range coords {
					p := f32.Vec2{float32(x), pen[1]}
					if rel {
						p[0] += pen[0]
					} else {
						p[0] += float32(offx)
					}
					encode(sfont.SegmentOpLineTo, p)
					newPen = p
				}
				pen = newPen
				ctrl2 = newPen
				continue
			case 'v':
				for _, y := range coords {
					p := f32.Vec2{pen[0], float32(y)}
					if rel {
						p[1] += pen[1]
					} else {
						p[1] += float32(offy)
					}
					encode(sfont.SegmentOpLineTo, p)
					newPen = p
				}
				pen = newPen
				ctrl2 = newPen
				continue
			}
			if len(coords)%2 != 0 {
				return fmt.Errorf("odd number of coordinates in <path> data: %q", orig)
			}
			var off f32.Vec2
			if rel {
				// Relative command.
				off = pen
			} else {
				off[0] = float32(offx)
				off[1] = float32(offy)
			}
			var points []f32.Vec2
			for i := 0; i < len(coords); i += 2 {
				p := f32.Vec2{float32(coords[i]), float32(coords[i+1])}
				p = affine.Add(p, off)
				points = append(points, p)
			}
			newCtrl2 := ctrl2
			switch op := unicode.ToLower(op); op {
			case 'm', 'l':
				sop := sfont.SegmentOpMoveTo
				if op == 'l' {
					sop = sfont.SegmentOpLineTo
				}
				for _, p := range points {
					encode(sop, p)
					newPen = p
				}
				if op == 'm' {
					initPoint = newPen
				}
			case 'c':
				for i := 0; i < len(points); i += 3 {
					p1, p2, p3 := points[i], points[i+1], points[i+2]
					encode(sfont.SegmentOpCubeTo, p1, p2, p3)
					newPen = p3
					newCtrl2 = p2
				}
			case 's':
				for i := 0; i < len(points); i += 2 {
					p2, p3 := points[i], points[i+1]
					// Compute p1 by reflecting p2 on to the line that contains pen and p2.
					p1 := affine.Sub(affine.Scale(pen, 2), ctrl2)
					encode(sfont.SegmentOpCubeTo, p1, p2, p3)
					newPen = p3
					newCtrl2 = p2
				}
			}
			pen = newPen
			ctrl2 = newCtrl2
		}
		return d.Skip()
	default:
		return fmt.Errorf("unsupported element: <%s>", n)
	}
}

func parseFloat(s string) (int, float64, bool) {
	n := 0
	if len(s) > 0 && s[0] == '-' {
		n++
	}
	for ; n < len(s); n++ {
		if !(unicode.IsDigit(rune(s[n])) || s[n] == '.') {
			break
		}
	}
	f, err := strconv.ParseFloat(s[:n], 64)
	return n, f, err == nil
}

func mapChar(id string) (rune, bool) {
	var r rune
	switch {
	case len(id) == 1:
		r = rune(id[0])
	default:
		switch id {
		case "zero":
			r = '0'
		case "one":
			r = '1'
		case "two":
			r = '2'
		case "three":
			r = '3'
		case "four":
			r = '4'
		case "five":
			r = '5'
		case "six":
			r = '6'
		case "seven":
			r = '7'
		case "eight":
			r = '8'
		case "nine":
			r = '9'
		case "colon":
			r = ':'
		case "comma":
			r = ','
		case "slash":
			r = '/'
		case "apostrophe":
			r = '\''
		case "dash":
			r = '-'
		case "period":
			r = '.'
		case "leftparen":
			r = '('
		case "rightparen":
			r = ')'
		case "leftbracket":
			r = '['
		case "rightbracket":
			r = ']'
		case "leftcurlybrace":
			r = '{'
		case "rightcurlybrace":
			r = '}'
		case "hash":
			r = '#'
		case "star":
			r = '*'
		case "at":
			r = '@'
		default:
			return 0, false
		}
	}
	return r, true
}

func convertFont(f *sfnt.Font) (*sfont.Face, error) {
	const prec = 1 << 15
	ppem := fixed.I(prec)
	var buf sfnt.Buffer
	metrics, err := f.Metrics(&buf, ppem, font.HintingFull)
	if err != nil {
		return nil, err
	}

	tof := func(v fixed.Int26_6) float32 {
		return float32(v.Round()) / prec
	}
	face := &sfont.Face{
		Metrics: sfont.Metrics{
			Ascent: tof(metrics.Ascent),
			Height: tof(metrics.Height),
		},
	}
	encode := func(op sfont.SegmentOp, args ...fixed.Point26_6) {
		face.Segments = append(face.Segments, uint32(op))
		for _, a := range args {
			x, y := tof(a.X), tof(a.Y)
			face.Segments = append(face.Segments, math.Float32bits(x), math.Float32bits(y))
		}
	}
	for ch := range face.Index {
		gidx, err := f.GlyphIndex(&buf, rune(ch))
		if err != nil {
			return nil, err
		}
		segs, err := f.LoadGlyph(&buf, gidx, ppem, nil)
		if err != nil {
			return nil, err
		}
		idxStart := len(face.Segments)
		for _, seg := range segs {
			switch seg.Op {
			case sfnt.SegmentOpMoveTo:
				encode(sfont.SegmentOpMoveTo, seg.Args[:1]...)
			case sfnt.SegmentOpLineTo:
				encode(sfont.SegmentOpLineTo, seg.Args[:1]...)
			case sfnt.SegmentOpQuadTo:
				encode(sfont.SegmentOpQuadTo, seg.Args[:2]...)
			case sfnt.SegmentOpCubeTo:
				encode(sfont.SegmentOpCubeTo, seg.Args[:3]...)
			}
		}
		idxEnd := len(face.Segments)
		adv, err := f.GlyphAdvance(&buf, gidx, ppem, font.HintingFull)
		if err != nil {
			return nil, err
		}
		face.Index[ch] = sfont.Glyph{
			Advance: tof(adv),
			Start:   uint16(idxStart),
			End:     uint16(idxEnd),
		}
	}
	return face, nil
}
