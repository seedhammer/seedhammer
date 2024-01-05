package main

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"seedhammer.com/bezier"
	"seedhammer.com/bspline"
	"seedhammer.com/font/vector"
)

var (
	packageName = flag.String("package", "main", "package name")
	scale       = flag.Int("scale", 1, "scale font")
	precision   = flag.Int("prec", 16, "sampling precision")
	dump        = flag.String("dump", "", "dump to SVG")
	splice      = flag.Bool("splice", false, "spline lines to curve segments")
)

type Face struct {
	Metrics vector.Metrics
	// Index maps a character to its segment range.
	Index [unicode.MaxASCII]vector.Glyph
	// Spline knots encoded as line byte(0 or 1), int16(x-coord), int16(y-coord).
	Splines []byte
}

type segment struct {
	Op   SegmentOp
	Args [4]bezier.Point
}

type SegmentOp uint32

const (
	SegmentOpMoveTo SegmentOp = iota
	SegmentOpLineTo
	SegmentOpQuadTo
	SegmentOpCubeTo
)

func main() {
	flag.Parse()

	if flag.NArg() != 2 {
		fmt.Fprintf(os.Stderr, "usage: convert infile outfile\n")
		os.Exit(1)
	}

	infile, name := flag.Arg(0), flag.Arg(1)
	if err := run(infile, name); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", infile, err)
		os.Exit(1)
	}
}

func run(infile, name string) error {
	in, err := os.ReadFile(infile)
	if err != nil {
	}
	conv, runeToSegs, err := convert(in)
	if err != nil {
		return err
	}
	samples, err := buildBSplines(conv, runeToSegs)
	if err != nil {
		return err
	}
	gosrc, data, err := generate(name, conv)
	if err != nil {
		return err
	}
	if err := os.WriteFile(name+".go", gosrc, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(name+".bin", data, 0o600); err != nil {
		return err
	}
	if f := *dump; f != "" {
		svg := dumpSVG(vector.NewFace(data), samples)
		if err := os.WriteFile(f, svg, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func buildBSplines(f *Face, runeToSegs map[rune][]segment) (samples map[rune][]bezier.Point, err error) {
	// Spline optimization is slow, so smear out the work across
	// available CPUs.
	var index [unicode.MaxASCII][]byte
	type job struct {
		r    rune
		segs []segment
	}
	type result struct {
		r       rune
		samples []bezier.Point
		spline  []byte
		err     error
	}
	samples = make(map[rune][]bezier.Point)
	jobs := make(chan job)
	res := make(chan result, len(runeToSegs))
	defer close(res)
	prec := max(1, f.Metrics.Height/(*precision))
	for range runtime.NumCPU() {
		go func() {
			for {
				j, ok := <-jobs
				if !ok {
					return
				}
				s, spline, err := segmentsToBSpline(j.segs, prec)
				res <- result{j.r, s, encodeBSpline(spline), err}
			}
		}()
	}
	for r, segs := range runeToSegs {
		jobs <- job{r, segs}
	}
	for range cap(res) {
		r := <-res
		if err := r.err; err != nil {
			return nil, fmt.Errorf("%q: %w", string(r.r), err)
		}
		samples[r.r] = r.samples
		index[r.r] = r.spline
	}
	for r, spline := range index {
		if len(spline) == 0 {
			continue
		}
		f.Index[r].Start = len(f.Splines)
		f.Splines = append(f.Splines, spline...)
		f.Index[r].End = len(f.Splines)
	}
	return samples, nil
}

func encodeBSpline(spline []vector.Knot) []byte {
	knots := make([]byte, 0, len(spline)*vector.IndexElemSize)
	for _, k := range spline {
		l := byte(0)
		if k.Line {
			l = 1
		}
		x, y := int16(k.Ctrl.X), int16(k.Ctrl.Y)
		if int(x) != k.Ctrl.X || int(y) != k.Ctrl.Y {
			panic(fmt.Errorf("spline knot coordinates out of range: %v", k.Ctrl))
		}
		knots = append(knots, l)
		knots = bo.AppendUint16(knots, uint16(x))
		knots = bo.AppendUint16(knots, uint16(y))
	}
	return knots
}

func segmentsToBSpline(segs []segment, prec int) (allSamples []bezier.Point, spline []vector.Knot, err error) {
	var samples []bezier.Point
	var interpolateErr error
	flushSamples := func(line bool) {
		if interpolateErr != nil || len(samples) == 0 {
			return
		}
		for i, s := range samples[:len(samples)-1] {
			s2 := samples[i+1]
			if s == s2 {
				interpolateErr = fmt.Errorf("overlapping sampling point %v", s)
				return
			}
		}

		uspline, err := bspline.InterpolatePoints(samples)
		allSamples = append(allSamples, samples...)
		samples = samples[:0]
		if err != nil {
			interpolateErr = err
			return
		}
		for _, k := range uspline[3:] {
			spline = append(spline, vector.Knot{
				Ctrl: k,
				Line: line,
			})
		}
	}
	appendBezier := func(c bezier.Cubic) {
		if len(samples) == 0 {
			samples = append(samples, c.C0)
		}
		samples = bezier.Sample(samples, c, prec)
	}
	p0 := bezier.Point{}
	for i, s := range segs {
		switch s.Op {
		case SegmentOpMoveTo:
			flushSamples(true)
			p1 := s.Args[0]
			if n := len(spline); n > 0 {
				spline[n-1].Line = false
			}
			k := vector.Knot{Ctrl: p1}
			spline = append(spline, k, k, k)
			p0 = p1
		case SegmentOpLineTo:
			p1 := s.Args[0]
			c := bezier.Cubic{
				C0: p0,
				C1: p0.Mul(2).Add(p1).Div(3),
				C2: p1.Mul(2).Add(p0).Div(3),
				C3: p1,
			}
			p0 = p1
			// If this line is part of a longer shape,
			// append it as a (straight) curve segment.
			if *splice && (i >= 0 && segs[i-1].Op != SegmentOpMoveTo ||
				i < len(segs)-1 && segs[i+1].Op != SegmentOpMoveTo) {
				appendBezier(c)
				break
			}
			flushSamples(true)
			if n := len(spline); n > 0 {
				spline[n-1].Line = true
			}
			k := vector.Knot{Ctrl: p1, Line: true}
			spline = append(spline, k, k, k)
		case SegmentOpCubeTo:
			p1 := s.Args[0]
			p2 := s.Args[1]
			p3 := s.Args[2]
			c := bezier.Cubic{
				C0: p0, C1: p1, C2: p2, C3: p3,
			}
			p0 = p3
			appendBezier(c)
		default:
			panic("unknown segment type")
		}
	}
	flushSamples(true)
	return allSamples, spline, interpolateErr
}

var bo = binary.LittleEndian

func generate(name string, conv *Face) (gosrc []byte, data []byte, err error) {
	var output bytes.Buffer
	fmt.Fprintf(&output, "// Code generated by seedhammer.com/cmd/vectorfont; DO NOT EDIT.\npackage %s\n", *packageName)
	fmt.Fprintf(&output, "import (\n")
	fmt.Fprintf(&output, "    _ \"embed\"\n")
	fmt.Fprintf(&output, "    \"unsafe\"\n")
	fmt.Fprintf(&output, "    \"seedhammer.com/font/vector\"\n")
	fmt.Fprintf(&output, ")\n\n")
	fmt.Fprintf(&output, "var Font = vector.NewFace(unsafe.Slice(unsafe.StringData(%sData), len(%[1]sData)))\n\n", name)
	fmt.Fprintf(&output, "//go:embed %s.bin\n", name)
	fmt.Fprintf(&output, "var %sData string\n", name)
	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, nil, err
	}

	ascent := uint16(conv.Metrics.Ascent)
	if int(ascent) != conv.Metrics.Ascent {
		return nil, nil, errors.New("ascent overflows uint16")
	}
	height := uint16(conv.Metrics.Height)
	if int(height) != conv.Metrics.Height {
		return nil, nil, errors.New("height overflows uint16")
	}
	data = bo.AppendUint16(data, ascent)
	data = bo.AppendUint16(data, height)
	for _, g := range conv.Index {
		adv := uint16(g.Advance)
		if int(adv) != g.Advance {
			return nil, nil, errors.New("advance overflows")
		}
		data = bo.AppendUint16(data, adv)
		start, end := g.Start+vector.OffSplines, g.End+vector.OffSplines
		s16, e16 := uint16(start), uint16(end)
		if int(s16) != start || int(e16) != end {
			return nil, nil, errors.New("spline offset overflows uint16")
		}
		data = bo.AppendUint16(data, s16)
		data = bo.AppendUint16(data, e16)
	}
	if len(data) != vector.OffSplines {
		panic("miscalculated spline offset")
	}
	data = append(data, conv.Splines...)
	return formatted, data, nil
}

type MetaData struct {
	Advance, Height, Baseline int
}

func convert(svg []byte) (*Face, map[rune][]segment, error) {
	d := xml.NewDecoder(bytes.NewReader(svg))
	for {
		root, err := d.Token()
		if err != nil {
			return nil, nil, err
		}
		t, ok := root.(xml.StartElement)
		if !ok {
			continue
		}
		if !ok || t.Name.Local != "svg" {
			return nil, nil, errors.New("missing <svg> root element")
		}
		face := new(Face)
		meta, err := parseMeta(svg)
		if err != nil {
			return nil, nil, err
		}
		face.Metrics.Ascent = meta.Baseline
		face.Metrics.Height = meta.Height
		face.Index[' '] = vector.Glyph{
			Advance: meta.Advance,
		}
		chars, err := parseChars(face, d, meta.Advance, meta.Baseline)
		return face, chars, err
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
	sf := float64(*scale)
	return int(math.Round(v * sf))
}

func parseChars(face *Face, d *xml.Decoder, adv, ascent int) (map[rune][]segment, error) {
	offx := 0
	runeToSegs := make(map[rune][]segment)
	for {
		t, err := d.Token()
		if err != nil {
			if err != io.EOF {
				return nil, err
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
				return nil, err
			}
			continue
		}
		id, _ := findAttr(e, "id")
		switch id {
		case "advance", "height", "baseline", "size":
			// Skip anonymous and meta-data elements.
			if err := d.Skip(); err != nil {
				return nil, err
			}
			continue
		}
		r, ok := mapChar(id)
		if !ok {
			return nil, fmt.Errorf("unknown character id: %q", id)
		}
		segs, err := parseSegments(face, d, e, offx, -ascent, nil)
		if err != nil {
			return nil, err
		}
		if segs2 := optimizeSegments(segs); len(segs2) > 0 {
			runeToSegs[r] = segs2
		}
		face.Index[r] = vector.Glyph{
			Advance: adv,
		}
		offx -= adv
	}
	return runeToSegs, nil
}

func optimizeSegments(segs []segment) []segment {
	var opt []segment
	var p0, pMinusOne bezier.Point
skip:
	for _, s := range segs {
		var pNext bezier.Point
		for {
			switch s.Op {
			case SegmentOpMoveTo, SegmentOpLineTo:
				p1 := s.Args[0]
				if p1 == p0 {
					continue skip
				}
				pNext = p1
				// Merge colinear segments of the same type.
				if len(opt) > 0 {
					if prevSeg := opt[len(opt)-1]; prevSeg.Op == s.Op {
						if onSegment(p0, pMinusOne, p1) {
							opt[len(opt)-1].Args[0] = p1
							continue skip
						}
					}
				}
			case SegmentOpQuadTo:
				p12 := s.Args[0]
				p3 := s.Args[1]
				// Expand to cubic.
				p1 := mix(p12, p0, 1.0/3.0)
				p2 := mix(p12, p3, 1.0/3.0)
				s.Op = SegmentOpCubeTo
				copy(s.Args[:], []bezier.Point{
					p1, p2, p3,
				})
				continue
			case SegmentOpCubeTo:
				p1 := s.Args[0]
				p2 := s.Args[1]
				p3 := s.Args[2]
				// Check whether the segment degenerates into
				// a line, which is equivalent to checking whether
				// the two inner control points lie on the line segment
				// of the endpoints.
				if onSegment(p1, p0, p3) && onSegment(p2, p0, p3) {
					s.Op = SegmentOpLineTo
					s.Args[0] = p3
					continue
				}
				pNext = p3
			}
			break
		}
		opt = append(opt, s)
		p0, pMinusOne = pNext, p0
	}
	return opt
}

// onSegment checks if point p lies on the segment between a and b.
func onSegment(p, a, b bezier.Point) bool {
	// Check collinearity using the cross product.
	if cross := (p.Y-a.Y)*(b.X-a.X) - (p.X-a.X)*(b.Y-a.Y); cross != 0 {
		return false
	}

	// p must also lie in the bounding box with a and b as corners.
	return p.X >= min(a.X, b.X) && p.X <= max(a.X, b.X) &&
		p.Y >= min(a.Y, b.Y) && p.Y <= max(a.Y, b.Y)
}

func parseSegments(face *Face, d *xml.Decoder, e xml.StartElement, offx, offy int, segs []segment) ([]segment, error) {
	encode := func(op SegmentOp, args ...bezier.Point) {
		seg := segment{Op: op}
		if len(args) > len(seg.Args) {
			panic("too many arguments")
		}
		copy(seg.Args[:], args)
		segs = append(segs, seg)
	}
	switch n := e.Name.Local; n {
	case "g":
		for {
			t, err := d.Token()
			if err != nil {
				return segs, err
			}
			switch t := t.(type) {
			case xml.StartElement:
				segs, err = parseSegments(face, d, t, offx, offy, segs)
				if err != nil {
					return segs, err
				}
			case xml.EndElement:
				return segs, nil
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
			return segs, err
		}
		encode(SegmentOpMoveTo, bezier.Pt(mustInt(line.X1)+offx, mustInt(line.Y1)+offy))
		encode(SegmentOpLineTo, bezier.Pt(mustInt(line.X2)+offx, mustInt(line.Y2)+offy))
		return segs, nil
	case "polyline":
		points, ok := findAttr(e, "points")
		if !ok {
			return segs, errors.New("missing points attribute for <polyline>")
		}
		points = strings.TrimSpace(points)
		coords := strings.Split(points, " ")
		for i, c := range coords {
			var x, y float64
			if _, err := fmt.Sscanf(c, "%f,%f", &x, &y); err != nil {
				return segs, fmt.Errorf("invalid coordinates %q in <polyline>:", c)
			}
			op := SegmentOpLineTo
			if i == 0 {
				op = SegmentOpMoveTo
			}
			encode(op, bezier.Pt(mustInt(x)+offx, mustInt(y)+offy))
		}
		return segs, d.Skip()
	case "path":
		cmds, ok := findAttr(e, "d")
		if !ok {
			return segs, errors.New("missing d attribute for <path>")
		}
		cmds = strings.TrimSpace(cmds)
		pen := bezier.Pt(offx, offy)
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
					encode(SegmentOpLineTo, initPoint)
					pen = initPoint
				}
				ctrl2 = initPoint
				continue
			default:
				return segs, fmt.Errorf("unknown <path> command %s in %q", string(op), orig)
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
					p := bezier.Pt(x, pen.Y)
					if rel {
						p.X += pen.X
					} else {
						p.X += offx
					}
					encode(SegmentOpLineTo, p)
					newPen = p
				}
				pen = newPen
				ctrl2 = newPen
				continue
			case 'v':
				for _, y := range coords {
					p := bezier.Pt(pen.X, y)
					if rel {
						p.Y += pen.Y
					} else {
						p.Y += offy
					}
					encode(SegmentOpLineTo, p)
					newPen = p
				}
				pen = newPen
				ctrl2 = newPen
				continue
			}
			if len(coords)%2 != 0 {
				return segs, fmt.Errorf("odd number of coordinates in <path> data: %q", orig)
			}
			var off bezier.Point
			if rel {
				// Relative command.
				off = pen
			} else {
				off = bezier.Pt(offx, offy)
			}
			var points []bezier.Point
			for i := 0; i < len(coords); i += 2 {
				p := bezier.Pt(coords[i], coords[i+1])
				p = p.Add(off)
				points = append(points, p)
			}
			newCtrl2 := ctrl2
			switch op := unicode.ToLower(op); op {
			case 'm', 'l':
				sop := SegmentOpMoveTo
				if op == 'l' {
					sop = SegmentOpLineTo
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
					encode(SegmentOpCubeTo, p1, p2, p3)
					newPen = p3
					newCtrl2 = p2
				}
			case 's':
				for i := 0; i < len(points); i += 2 {
					p2, p3 := points[i], points[i+1]
					// Compute p1 by reflecting p2 on to the line that contains pen and p2.
					p1 := pen.Mul(2).Sub(ctrl2)
					encode(SegmentOpCubeTo, p1, p2, p3)
					newPen = p3
					newCtrl2 = p2
				}
			}
			pen = newPen
			ctrl2 = newCtrl2
		}
		return segs, d.Skip()
	default:
		return segs, fmt.Errorf("unsupported element: <%s>", n)
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
		case "semicolon":
			r = ';'
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
		case "lt":
			r = '<'
		case "gt":
			r = '>'
		default:
			return 0, false
		}
	}
	return r, true
}

func mix(p1, p2 bezier.Point, a float64) bezier.Point {
	return bezier.Point{
		X: int(math.Round(float64(p1.X)*(1.-a) + float64(p2.X)*a)),
		Y: int(math.Round(float64(p1.Y)*(1.-a) + float64(p2.Y)*a)),
	}
}

func expandUniformBSpline(spline vector.UniformBSpline) []bspline.Knot {
	var knots []bspline.Knot
	var last bezier.Point
	for {
		c, ok := spline.Next()
		if !ok {
			break
		}
		t := uint(1)
		if c.Ctrl == last {
			t = 0
		}
		last = c.Ctrl
		k := bspline.Knot{
			Engrave: c.Line,
			Ctrl:    c.Ctrl,
			T:       t,
		}
		knots = append(knots, k)
	}
	return knots
}

func dumpSVG(f *vector.Face, samples map[rune][]bezier.Point) []byte {
	out := new(bytes.Buffer)

	var alphabet []rune
	width := 0
	for r := range rune(unicode.MaxASCII) {
		if adv, _, ok := f.Decode(r); ok {
			alphabet = append(alphabet, r)
			width += adv
		}
	}
	const margin = 20
	m := f.Metrics()
	w, h := width+2*margin, m.Height+2*margin
	aspect := w / h
	fmt.Fprintf(out, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"%d %d %d %d\" width=\"%d\" height=\"%d\">\n",
		-margin, -m.Ascent-margin, w, h, 200*aspect, 200)

	fmt.Fprintf(out, `<defs><style>
		.spline { fill: none; stroke: #0984e3; stroke-width: %g; stroke-linecap: round; }
	</style></defs>`, float64(m.Height)/100)
	x := 0
	fmt.Fprint(out, `<path class="spline" d="`)
	type circle struct {
		pos    bezier.Point
		fill   string
		radius float64
	}
	var circles []circle
	for _, r := range alphabet {
		for _, s := range samples[r] {
			c := s.Add(bezier.Pt(x, 0))
			circles = append(circles, circle{c, "black", 1.5})
		}
		adv, uspline, _ := f.Decode(r)
		spline := expandUniformBSpline(uspline)

		var seg bspline.Segment
		first := true
		for _, k := range spline {
			c, dt, line := seg.Knot(k)
			if dt == 0 {
				continue
			}
			circles = append(circles, circle{k.Ctrl.Add(bezier.Pt(x, 0)), "blue", 1.2})
			c = c.Add(bezier.Pt(x, 0))
			if line {
				if first {
					first = false
					circles = append(circles, circle{c.C0, "red", 1})
				}
				circles = append(circles, circle{c.C3, "red", 1})
				fmt.Fprintf(out, " C %d %d, %d %d, %d %d",
					c.C1.X, c.C1.Y, c.C2.X, c.C2.Y, c.C3.X, c.C3.Y)
			} else {
				first = true
				fmt.Fprintf(out, " M %d %d", c.C3.X, c.C3.Y)
			}
		}

		x += adv
	}
	fmt.Fprintln(out, `" />`)
	for _, c := range circles {
		fmt.Fprintf(out, `<circle cx="%d" cy="%d" r="%g" fill="%s"/>`, c.pos.X, c.pos.Y, float64(m.Height)/200*c.radius, c.fill)
	}
	fmt.Fprintln(out, "</svg>")
	return out.Bytes()
}
