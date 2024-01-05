package golden

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"seedhammer.com/bezier"
	"seedhammer.com/bspline"
)

func CompareBSpline(path string, update bool, dumpDir string, strokeWidth int, bounds bspline.Bounds, spline bspline.Curve) error {
	bpath := filepath.Base(path)
	if dumpDir != "" {
		fpath := filepath.Join(dumpDir, bpath+".svg")
		if err := dumpSVG(fpath, strokeWidth, bounds, spline); err != nil {
			return err
		}
	}
	if update {
		buf := new(bytes.Buffer)
		w, err := gzip.NewWriterLevel(buf, gzip.BestCompression)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		w.Write(encodeBSpline(spline))
		if err := w.Close(); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return os.WriteFile(path, buf.Bytes(), 0o640)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	golden, err := decodeBSpline(b)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	knots := slices.Collect(spline)
	mismatches := 0
	for i := range min(len(knots), len(golden)) {
		if k1, k2 := knots[i], golden[i]; !knotsCloseEnough(k1, k2) {
			mismatches++
		}
	}
	if mismatches > 0 || len(knots) != len(golden) {
		if dumpDir != "" {
			orig := slices.Values(golden)
			fpath := filepath.Join(dumpDir, bpath+".orig.svg")
			if err := dumpSVG(fpath, strokeWidth, bounds, orig); err != nil {
				return err
			}
		}
		return fmt.Errorf("spline lengths %d, %d, with %d/%d knot mismatches", len(knots), len(golden), mismatches, len(golden))
	}
	return nil
}

func knotsCloseEnough(k1, k2 bspline.Knot) bool {
	return k1.Engrave == k2.Engrave && k1.T == k2.T &&
		pointsCloseEnough(k1.Ctrl, k2.Ctrl)
}

func pointsCloseEnough(p1, p2 bezier.Point) bool {
	const epsilon = 1
	diff := p2.Sub(p1)
	d := max(diff.X, diff.Y, -diff.X, -diff.Y)
	return d <= epsilon
}

// decodeBSpline decodes a spline from its binary form.
func decodeBSpline(enc []byte) ([]bspline.Knot, error) {
	var knots []bspline.Knot
	engrave := false
	var lastp bezier.Point
	nKnots := byte(0)
	for len(enc) > 0 {
		if nKnots == 0 {
			b := enc[0]
			enc = enc[1:]
			nKnots = b & 0b111_1111
			engrave = b>>7 != 0
		}
		nKnots--
		x, n := binary.Varint(enc)
		if n < 0 {
			return nil, errors.New("truncated spline")
		}
		enc = enc[n:]
		y, n := binary.Varint(enc)
		if n < 0 {
			return nil, errors.New("truncated spline")
		}
		enc = enc[n:]
		t, n := binary.Varint(enc)
		if n < 0 {
			return nil, errors.New("truncated spline")
		}
		enc = enc[n:]
		p := bezier.Point{X: int(x), Y: int(y)}.Add(lastp)
		k := bspline.Knot{Ctrl: p, T: uint(t), Engrave: engrave}
		lastp = p
		knots = append(knots, k)
	}
	return knots, nil
}

// encodeBSpline encodes a spline into a compact binary form.
func encodeBSpline(spline bspline.Curve) []byte {
	var buf []byte
	var lastk bspline.Knot
	lastHeader := 0
	// Initial header.
	buf = append(buf, 0)
	for k := range spline {
		// Load header.
		h := buf[lastHeader]
		engraveBit := byte(0)
		if k.Engrave {
			engraveBit = 1
		}
		// Check whether knot fits in current header.
		if h&0b111_1111 == 0b111_1111 || h>>7 != engraveBit {
			lastHeader = len(buf)
			h = engraveBit << 7
			buf = append(buf, h)
		}
		// Increment count and store.
		h++
		buf[lastHeader] = h
		d := k.Ctrl.Sub(lastk.Ctrl)
		lastk = k
		buf = binary.AppendVarint(buf, int64(d.X))
		buf = binary.AppendVarint(buf, int64(d.Y))
		buf = binary.AppendVarint(buf, int64(k.T))
	}
	return buf
}

func Vectorize(f io.Writer, strokeWidth int, bounds bspline.Bounds, spline bspline.Curve) error {
	const (
		margin = 20
		dots   = false
	)
	out := bufio.NewWriter(f)

	w, h := bounds.Dx()+2*margin, bounds.Dy()+2*margin
	aspect := w / h
	fmt.Fprintf(out, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"%d %d %d %d\" width=\"%d\" height=\"%d\">\n",
		bounds.Min.X, bounds.Min.Y-margin, w, h, 600*aspect, 600)

	fmt.Fprintf(out, `<defs><style>
		.spline { fill: none; stroke: #000; stroke-width: %d; stroke-linejoin: round; stroke-linecap: round; }
	</style></defs>`, strokeWidth)
	fmt.Fprint(out, `<path class="spline" d="`)
	type circle struct {
		pos    bezier.Point
		fill   string
		radius float64
	}
	var circles []circle
	var seg bspline.Segment
	first := true
	for k := range spline {
		c, dt, line := seg.Knot(k)
		if dt == 0 {
			continue
		}
		if line {
			circles = append(circles, circle{k.Ctrl, "blue", 1.2})
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

	fmt.Fprintln(out, `" />`)
	if dots {
		for _, c := range circles {
			fmt.Fprintf(out, `<circle cx="%d" cy="%d" r="%d" fill="%s"/>`, c.pos.X, c.pos.Y, strokeWidth/2, c.fill)
		}
	}
	fmt.Fprintln(out, "</svg>")
	return out.Flush()
}

func dumpSVG(f string, strokeWidth int, bounds bspline.Bounds, spline bspline.Curve) error {
	buf := new(bytes.Buffer)
	Vectorize(buf, strokeWidth, bounds, spline)
	return os.WriteFile(f, buf.Bytes(), 0o640)
}
