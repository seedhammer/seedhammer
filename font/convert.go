//go:build ignore

package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"image"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

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
	Advance, Height, Baseline int
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
		face.Metrics.Ascent = ascent
		face.Metrics.Height = meta.Height
		adv := meta.Advance
		face.Index[' '] = sfont.Glyph{
			Advance: adv,
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
			meta.Advance = mustInt(line.X2 - line.X1)
		case "height":
			meta.Height = mustInt(line.Y2 - line.Y1)
		case "baseline":
			meta.Baseline = mustInt(line.Y1)
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

func mustInt(v float64) int {
	i := int(v)
	if float64(i) != v {
		panic("non-integer floating point number")
	}
	return i
}

func parseChars(face *sfont.Face, d *xml.Decoder, adv, ascent int) error {
	offx := 0
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
			Advance: adv,
			Start:   uint16(idxStart),
			End:     uint16(idxEnd),
		}
		offx -= adv
	}
	return nil
}

func parseSegments(face *sfont.Face, d *xml.Decoder, e xml.StartElement, offx, offy int) error {
	encode := func(op sfont.SegmentOp, args ...image.Point) {
		face.Segments = append(face.Segments, uint32(op))
		for _, a := range args {
			face.Segments = append(face.Segments, uint32(a.X), uint32(a.Y))
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
		encode(sfont.SegmentOpMoveTo, image.Pt(mustInt(line.X1)+offx, mustInt(line.Y1)+offy))
		encode(sfont.SegmentOpLineTo, image.Pt(mustInt(line.X2)+offx, mustInt(line.Y2)+offy))
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
			op := sfont.SegmentOpLineTo
			if i == 0 {
				op = sfont.SegmentOpMoveTo
			}
			encode(op, image.Pt(mustInt(x)+offx, mustInt(y)+offy))
		}
		return d.Skip()
	case "path":
		cmds, ok := findAttr(e, "d")
		if !ok {
			return errors.New("missing d attribute for <path>")
		}
		cmds = strings.TrimSpace(cmds)
		pen := image.Pt(offx, offy)
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
			var coords []int
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
				coords = append(coords, mustInt(x))
			}
			rel := unicode.IsLower(op)
			newPen := pen
			switch unicode.ToLower(op) {
			case 'h':
				for _, x := range coords {
					p := image.Pt(x, pen.Y)
					if rel {
						p.X += pen.X
					} else {
						p.X += offx
					}
					encode(sfont.SegmentOpLineTo, p)
					newPen = p
				}
				pen = newPen
				ctrl2 = newPen
				continue
			case 'v':
				for _, y := range coords {
					p := image.Pt(pen.X, y)
					if rel {
						p.Y += pen.Y
					} else {
						p.Y += offy
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
			var off image.Point
			if rel {
				// Relative command.
				off = pen
			} else {
				off = image.Pt(offx, offy)
			}
			var points []image.Point
			for i := 0; i < len(coords); i += 2 {
				p := image.Pt(coords[i], coords[i+1])
				p = p.Add(off)
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
			case 'c', 's':
				return errors.New("cubic splines not supported")
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
