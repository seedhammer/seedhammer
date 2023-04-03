// package engrave transforms shapes such as text and QR codes into
// line and move commands for use with an engraver.
package engrave

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sort"

	"github.com/skip2/go-qrcode"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/f32"
	"golang.org/x/image/math/fixed"
	"seedhammer.com/affine"
	"seedhammer.com/font"
)

type Command interface {
	Engrave(p Program)
}

type Commands []Command

func (c Commands) Engrave(p Program) {
	for _, c := range c {
		c.Engrave(p)
	}
}

// Program is an interface to output an engraving.
type Program interface {
	Move(p image.Point)
	Line(p image.Point)
}

type transformedProgram struct {
	prog  Program
	trans transform
}

func (t *transformedProgram) Move(p image.Point) {
	t.prog.Move(t.trans.transform(p))
}

func (t *transformedProgram) Line(p image.Point) {
	t.prog.Line(t.trans.transform(p))
}

type offsetProgram struct {
	prog Program
	off  image.Point
}

func (o *offsetProgram) Move(p image.Point) {
	o.prog.Move(p.Add(o.off))

}

func (o *offsetProgram) Line(p image.Point) {
	o.prog.Line(p.Add(o.off))
}

func roundCoord(p f32.Vec2) image.Point {
	return image.Point{
		X: int(math.Round(float64(p[0]))),
		Y: int(math.Round(float64(p[1]))),
	}
}

type transform [6]int

func (m transform) transform(p image.Point) image.Point {
	return image.Point{
		X: p.X*m[0] + p.Y*m[1] + m[2],
		Y: p.X*m[3] + p.Y*m[4] + m[5],
	}
}

func rotating(radians float64) transform {
	sin, cos := math.Sincos(float64(radians))
	s, c := int(math.Round(sin)), int(math.Round(cos))
	return transform{
		c, -s, 0,
		s, c, 0,
	}
}

func offsetting(x, y int) transform {
	return transform{
		1, 0, x,
		0, 1, y,
	}
}

type transformCmd struct {
	t   transform
	cmd Command
}

func (t transformCmd) Engrave(p Program) {
	p = &transformedProgram{
		prog:  p,
		trans: t.t,
	}
	t.cmd.Engrave(p)
}

func Offset(x, y int, cmd Command) Command {
	return transformCmd{
		t:   offsetting(x, y),
		cmd: cmd,
	}
}

func Rotate(radians float64, cmd Command) Command {
	return transformCmd{
		t:   rotating(radians),
		cmd: cmd,
	}
}

func QR(strokeWidth int, scale int, level qrcode.RecoveryLevel, content []byte) (Command, error) {
	qr, err := qrcode.New(string(content), level)
	if err != nil {
		return nil, err
	}
	qr.DisableBorder = true
	return qrCmd{
		strokeWidth: strokeWidth,
		scale:       scale,
		qr:          qr.Bitmap(),
	}, nil
}

type qrCmd struct {
	strokeWidth int
	scale       int
	qr          [][]bool
}

func (q qrCmd) Engrave(p Program) {
	for y, row := range q.qr {
		for i := 0; i < q.scale; i++ {
			draw := false
			var firstx int
			line := y*q.scale + i
			// Swap direction every other line.
			rev := line%2 != 0
			radius := q.strokeWidth / 2
			if rev {
				radius = -radius
			}
			drawLine := func(endx int) {
				start := image.Pt(firstx*q.scale*q.strokeWidth+radius, line*q.strokeWidth)
				end := image.Pt(endx*q.scale*q.strokeWidth-radius, line*q.strokeWidth)
				p.Move(start)
				p.Line(end)
				draw = false
			}
			for x := -1; x <= len(row); x++ {
				xl := x
				px := x
				if rev {
					xl = len(row) - 1 - x
					px = xl - 1
				}
				on := 0 <= px && px < len(row) && row[px]
				switch {
				case !draw && on:
					draw = true
					firstx = xl
				case draw && !on:
					drawLine(xl)
				}
			}
		}
	}
}

// qrMoves is the exact number of qrMoves before engraving
// a constant time QR module.
const qrMoves = 4

// constantTimeQRModeuls returns the exact number of modules in a constant
// time QR code, given its version.
func constantTimeQRModules(version int) int {
	// The numbers below are maximum numbers found through fuzzing.
	// Add a bit more to account for outliers not yet found.
	const extra = 5
	switch version {
	case 1:
		return 165 + extra
	case 2:
		return 263 + extra
	case 3:
		return 387 + extra
	}
	// Not supported, return a low number to force error.
	return 0
}

func constantTimeStartEnd(dim int) (start, end image.Point) {
	return image.Pt(8+qrMoves, dim-1-qrMoves), image.Pt(dim-1-3, 3)
}

func bitmapForBools(qr [][]bool) bitmap {
	bm := NewBitmap(len(qr), len(qr))
	for y, row := range qr {
		for x, module := range row {
			if module {
				bm.Set(image.Pt(x, y))
			}
		}
	}
	return bm
}

func bitmapForQRStatic(ver, dim int) ([]image.Point, []image.Point, bitmap) {
	engraved := NewBitmap(dim, dim)
	// First 3 position markers.
	posMarkers := []image.Point{
		{0, 0},
		{dim - 7, 0},
		{0, dim - 7},
	}
	for _, p := range posMarkers {
		fillPositionMarker(engraved, p)
	}
	// Ignore aligment markers.
	var alignMarkers []image.Point
	switch ver {
	case 2:
		alignMarkers = append(alignMarkers, image.Pt(16, 16))
	case 3:
		alignMarkers = append(alignMarkers, image.Pt(20, 20))
	}
	for _, p := range alignMarkers {
		fillAlignmentMarker(engraved, p)
	}
	return posMarkers, alignMarkers, engraved
}

// ConstantQR is like QR that engraves the QR code in a pattern independent of content,
// except for the QR code version (size).
func ConstantQR(strokeWidth, scale int, level qrcode.RecoveryLevel, content []byte) (Command, error) {
	qrc, err := qrcode.New(string(content), level)
	if err != nil {
		return nil, err
	}
	qrc.DisableBorder = true
	bm := qrc.Bitmap()
	dim := len(bm)
	qr := bitmapForBools(bm)
	// No need to engrave static features of the QR code.
	posMarkers, alignMarkers, engraved := bitmapForQRStatic(qrc.VersionNumber, dim)
	// Start in the lower-left corner.
	pos := image.Pt(0, dim-1)
	// Iterating forward.
	dir := 1
	start, end := constantTimeStartEnd(dim)
	modules := []image.Point{
		start,
	}
	waste := 0
	engrave := func(p image.Point) {
		modules = append(modules, p)
		if engraved.Get(p) {
			waste++
		} else {
			engraved.Set(p)
		}
	}
	move := func(p image.Point) error {
		// Find path to a module close enough to pos.
		visited := NewBitmap(dim, dim)
		needle := modules[len(modules)-1]
		path, ok := findPath(nil, visited, qr, engraved, p, needle)
		if !ok {
			return errors.New("QR modules spaced too far for constant time engraving")
		}
		for _, m := range path {
			engrave(m)
		}
		return nil
	}
	for pos.Y >= 0 {
		if qr.Get(pos) && !engraved.Get(pos) {
			needle := modules[len(modules)-1]
			dist := manhattanDist(pos, needle)
			if dist > qrMoves {
				if err := move(pos); err != nil {
					return nil, err
				}
			}
			engrave(pos)
		}
		// Advance to next module.
		if nextx := pos.X + dir; 0 <= nextx && nextx < dim {
			pos.X = nextx
			continue
		}
		// Row complete, advance to previous row.
		dir = -dir
		pos.Y--
	}
	if err := move(end); err != nil {
		return nil, err
	}
	nmod := constantTimeQRModules(qrc.VersionNumber)
	if len(modules) >= nmod {
		return nil, fmt.Errorf("too many version %d QR modules for constant time engraving n: %d waste: %d",
			qrc.VersionNumber, len(modules), waste)
	}
	for len(modules) < nmod {
		// Engrave the end point until the required number is filled.
		engrave(end)
	}
	plan := constantTimeQRPlan(modules)
	// The plan should be constant time by construction, but be paranoid.
	if !isConstantTimeQRPlan(qrc.VersionNumber, dim, plan) {
		panic("constant time plan is not constant")
	}
	return constantQRCmd{
		strokeWidth:  strokeWidth,
		scale:        scale,
		posMarkers:   posMarkers,
		alignMarkers: alignMarkers,
		plan:         plan,
	}, nil
}

func fillAlignmentMarker(qr bitmap, off image.Point) {
	fillMarker(qr, off, []image.Point{
		{X: 0, Y: 0},
		{X: 1, Y: 0},
		{X: 2, Y: 0},
		{X: 3, Y: 0},
		{X: 4, Y: 0},

		{X: 0, Y: 1},
		{X: 0, Y: 2},
		{X: 0, Y: 3},

		{X: 2, Y: 2},

		{X: 4, Y: 1},
		{X: 4, Y: 2},
		{X: 4, Y: 3},

		{X: 0, Y: 4},
		{X: 1, Y: 4},
		{X: 2, Y: 4},
		{X: 3, Y: 4},
		{X: 4, Y: 4},
	})
}

func fillPositionMarker(qr bitmap, off image.Point) {
	fillMarker(qr, off, []image.Point{
		{X: 0, Y: 0},
		{X: 1, Y: 0},
		{X: 2, Y: 0},
		{X: 3, Y: 0},
		{X: 4, Y: 0},
		{X: 5, Y: 0},
		{X: 6, Y: 0},

		{X: 0, Y: 1},
		{X: 0, Y: 2},
		{X: 0, Y: 3},
		{X: 0, Y: 4},
		{X: 0, Y: 5},

		{X: 6, Y: 1},
		{X: 6, Y: 2},
		{X: 6, Y: 3},
		{X: 6, Y: 4},
		{X: 6, Y: 5},

		{X: 2, Y: 2},
		{X: 3, Y: 2},
		{X: 4, Y: 2},
		{X: 2, Y: 3},
		{X: 3, Y: 3},
		{X: 4, Y: 3},
		{X: 2, Y: 4},
		{X: 3, Y: 4},
		{X: 4, Y: 4},

		{X: 0, Y: 6},
		{X: 1, Y: 6},
		{X: 2, Y: 6},
		{X: 3, Y: 6},
		{X: 4, Y: 6},
		{X: 5, Y: 6},
		{X: 6, Y: 6},
	})
}

func fillMarker(engraved bitmap, off image.Point, points []image.Point) {
	for _, p := range points {
		p = p.Add(off)
		engraved.Set(p)
	}
}

func findPath(modules []image.Point, visited, qr, engraved bitmap, to, from image.Point) ([]image.Point, bool) {
	if manhattanDist(from, to) <= qrMoves {
		return modules, true
	}
	var candidates []image.Point
	for y := -qrMoves; y <= qrMoves; y++ {
		for x := -qrMoves; x <= qrMoves; x++ {
			p := from.Add(image.Pt(x, y))
			if !qr.Get(p) || visited.Get(p) {
				continue
			}
			visited.Set(p)
			candidates = append(candidates, p)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		pi, pj := candidates[i], candidates[j]
		di, dj := manhattanDist(pi, to), manhattanDist(pj, to)
		if di == dj {
			// Equal distance; prefer the un-engraved path.
			return engraved.Get(pj)
		}
		return di < dj
	})
	for _, p := range candidates {
		path, ok := findPath(append(modules, p), visited, qr, engraved, to, p)
		if ok {
			return path, true
		}
	}
	return nil, false
}

type constantQRCmd struct {
	strokeWidth int
	scale       int

	posMarkers   []image.Point
	alignMarkers []image.Point
	plan         []image.Point
}

// constantTimeQRPlan returns the constant pattern movements for a QR code.
// A module is assumed engraved at every qrMoves movements, after the starting position
// placed first in the result.
func constantTimeQRPlan(modules []image.Point) []image.Point {
	plan := []image.Point{
		modules[0],
	}
	// move a constant number of times before reaching p.
	for _, m := range modules[1:] {
		for i := qrMoves - 1; i >= 0; i-- {
			needle := plan[len(plan)-1]
			d := m.Sub(needle)
			// Exactly one (manhattan) unit per move.
			if d.X < -1 {
				d.X = -1
			}
			if d.X > 1 {
				d.X = 1
			}
			if d.Y < -1 {
				d.Y = -1
			}
			if d.Y > 1 {
				d.Y = 1
			}
			if manhattanDist(needle, m) < i+1 {
				// Too close to target; add dummy move.
				switch {
				case d.X == 0:
					d.X = 1
				case d.Y == 0:
					d.Y = 1
				default:
					d.X = 0
				}
			}
			plan = append(plan, needle.Add(d))
		}
	}
	return plan
}

func isConstantTimeQRPlan(version, dim int, plan []image.Point) bool {
	// Plan length must be exactly qrMoves per expected module count,
	// not counting the starting point.
	nmod := constantTimeQRModules(version)
	want := 1 + qrMoves*(nmod-1)
	if len(plan) != want {
		return false
	}
	needle := plan[0]
	start, end := constantTimeStartEnd(dim)
	if needle != start {
		return false
	}
	for _, p := range plan[1:] {
		if manhattanDist(p, needle) != 1 {
			return false
		}
		needle = p
	}
	return needle == end
}

func (q constantQRCmd) engraveAlignMarker(p Program, off image.Point) {
	q.engraveBorder(p, 5, 5, off)
	q.engraveBlock(p, 1, 1, off.Add(image.Pt(2, 2)))
}

func (q constantQRCmd) engraveBorder(p Program, width, height int, off image.Point) {
	sw := q.strokeWidth
	radius := sw / 2
	corner := image.Pt(radius, radius).Add(off.Mul(q.scale * sw))
	line := func(pos image.Point) {
		p.Line(corner.Add(pos.Mul(sw)))
	}
	move := func(pos image.Point) {
		p.Move(corner.Add(pos.Mul(sw)))
	}

	q.engraveBlock(p, width, 1, off)
	switch q.scale {
	case 3:
		w := (width*q.scale - 1)
		h := ((height-1)*q.scale - 1)
		// Right side.
		line(image.Pt(w, 3+h))
		move(image.Pt(w-1, 3+h))
		line(image.Pt(w-1, 3))
		move(image.Pt(w-2, 3))
		line(image.Pt(w-2, 3+h))

		// Bottom.
		line(image.Pt(0, 3+h))
		move(image.Pt(0, 2+h))
		line(image.Pt(w-3, 2+h))
		move(image.Pt(w-3, 1+h))
		line(image.Pt(0, 1+h))

		// Left side.
		line(image.Pt(0, 3))
		move(image.Pt(1, 3))
		line(image.Pt(1, h))
		move(image.Pt(2, h))
		line(image.Pt(2, 3))
	case 4:
		// Left side.
		q.engraveBlock(p, 1, height-2, off.Add(image.Pt(0, 1)))
		// Bottom.
		q.engraveBlock(p, width, 1, off.Add(image.Pt(0, height-1)))
		// Right side.
		q.engraveBlock(p, 1, height-2, off.Add(image.Pt(width-1, 1)))
	}
}

func (q constantQRCmd) engravePositionMarker(p Program, off image.Point) {
	q.engraveBorder(p, 7, 7, off)
	q.engraveBlock(p, 3, 3, off.Add(image.Pt(2, 2)))
}

func (q constantQRCmd) engraveBlock(p Program, width, height int, off image.Point) {
	sw := q.strokeWidth
	radius := sw / 2
	corner := image.Pt(radius, radius).Add(off.Mul(q.scale * sw))
	w := (width*q.scale - 1) * sw
	x, y := 0, 0
	for i := 0; i < height*q.scale; i++ {
		p.Move(corner.Add(image.Pt(x, y)))
		x = w - x
		p.Line(corner.Add(image.Pt(x, y)))
		y += sw
	}
}

func (q constantQRCmd) Engrave(p Program) {
	for _, off := range q.posMarkers {
		q.engravePositionMarker(p, off)
	}
	for _, off := range q.alignMarkers {
		q.engraveAlignMarker(p, off)
	}
	sw := q.strokeWidth
	radius := sw / 2
	for i, m := range q.plan {
		center := image.Point{
			X: radius + (m.X*q.scale+1)*sw,
			Y: radius + (m.Y*q.scale+1)*sw,
		}
		p.Move(center)
		// Engrave a module every qrMoves moves, excluding the
		// start and end positions.
		if i == 0 || i == len(q.plan)-1 || i%qrMoves != 0 {
			continue
		}
		switch q.scale {
		case 3:
			p.Line(center.Add(image.Pt(sw, 0)))
			p.Line(center.Add(image.Pt(sw, sw)))
			p.Line(center.Add(image.Pt(-sw, sw)))
			p.Line(center.Add(image.Pt(-sw, -sw)))
			p.Line(center.Add(image.Pt(sw, -sw)))
		case 4:
			p.Line(center.Add(image.Pt(-sw, 0)))
			p.Line(center.Add(image.Pt(-sw, -sw)))
			p.Line(center.Add(image.Pt(2*sw, -sw)))
			p.Line(center.Add(image.Pt(2*sw, 2*sw)))
			p.Line(center.Add(image.Pt(-sw, 2*sw)))
			p.Line(center.Add(image.Pt(-sw, sw)))
			p.Line(center.Add(image.Pt(sw, sw)))
			p.Line(center.Add(image.Pt(sw, 0)))
		}
		p.Line(center)
	}
}

func manhattanDist(p1, p2 image.Point) int {
	d := p1.Sub(p2)
	if d.X < 0 {
		d.X = -d.X
	}
	if d.Y < 0 {
		d.Y = -d.Y
	}
	if d.X > d.Y {
		return d.X
	} else {
		return d.Y
	}
}

type bitmap struct {
	w    int
	bits []uint32
}

func NewBitmap(w, h int) bitmap {
	if w > 32 {
		panic("bitset too wide")
	}
	return bitmap{
		w:    w,
		bits: make([]uint32, h),
	}
}

func (b bitmap) Set(p image.Point) {
	if p.X < 0 || p.Y < 0 || p.X >= b.w || p.Y >= len(b.bits) {
		panic("out of range")
	}
	b.bits[p.Y] |= 1 << p.X
}

func (b bitmap) Get(p image.Point) bool {
	if p.X < 0 || p.Y < 0 || p.X >= b.w || p.Y >= len(b.bits) {
		return false
	}
	return b.bits[p.Y]&(1<<p.X) != 0
}

type Rect image.Rectangle

func (r Rect) Engrave(p Program) {
	p.Move(r.Min)
	p.Line(image.Pt(r.Max.X, r.Min.Y))
	p.Line(r.Max)
	p.Line(image.Pt(r.Min.X, r.Max.Y))
	p.Line(r.Min)
}

func String(face *font.Face, em int, txt string) *StringCmd {
	return &StringCmd{
		LineHeight: 1,
		face:       face,
		em:         em,
		txt:        txt,
	}
}

type StringCmd struct {
	LineHeight float32

	face *font.Face
	em   int
	txt  string
}

func (s *StringCmd) Engrave(p Program) {
	em := float32(s.em)
	ceil := func(v float32) int {
		return int(math.Ceil(float64(v)))
	}
	pos := image.Pt(0, ceil(s.face.Metrics.Ascent*em))
	addScale := func(p1 image.Point, p2 f32.Vec2) f32.Vec2 {
		return f32.Vec2{
			float32(p1.X) + p2[0]*em,
			float32(p1.Y) + p2[1]*em,
		}
	}
	for _, r := range s.txt {
		if r == '\n' {
			pos.X = 0
			pos.Y += ceil(s.face.Metrics.Height * em * s.LineHeight)
			continue
		}
		adv, segs, found := s.face.Decode(r)
		if !found {
			panic(fmt.Errorf("unsupported rune: %s", string(r)))
		}
		var p0 f32.Vec2
		for _, seg := range segs {
			switch seg.Op {
			case font.SegmentOpMoveTo:
				p1 := addScale(pos, seg.Args[0])
				p.Move(roundCoord(p1))
				p0 = p1
			case font.SegmentOpLineTo:
				p1 := addScale(pos, seg.Args[0])
				p.Line(roundCoord(p1))
				p0 = p1
			case font.SegmentOpQuadTo:
				p12 := addScale(pos, seg.Args[0])
				p3 := addScale(pos, seg.Args[1])
				// Expand to cubic.
				p1 := mix(p12, p0, 1.0/3.0)
				p2 := mix(p12, p3, 1.0/3.0)
				approxCubeBezier(p.Line, p0, p1, p2, p3)
				p0 = p3
			case font.SegmentOpCubeTo:
				p1 := addScale(pos, seg.Args[0])
				p2 := addScale(pos, seg.Args[1])
				p3 := addScale(pos, seg.Args[2])
				approxCubeBezier(p.Line, p0, p1, p2, p3)
				p0 = p3
			default:
				panic(errors.New("unsupported segment"))
			}
		}
		pos.X += int(adv * em)
	}
}

// approxCubeBezier uses de Casteljau subdivision to approximate a cubic Bézier
// curve with line segments.
//
// See "Piecewise Linear Approximation of Bézier Curves" by Kaspar Fischer, October 16, 2000,
// http://citeseerx.ist.psu.edu/viewdoc/download?doi=10.1.1.86.162&rep=rep1&type=pdf.
func approxCubeBezier(move func(to image.Point), p0, p1, p2, p3 f32.Vec2) {
	if isFlat(p0, p1, p2, p3) {
		move(roundCoord(p3))
	} else {
		l0, l1, l2, l3 := subdivideCubeBezier(0, .5, p0, p1, p2, p3)
		approxCubeBezier(move, l0, l1, l2, l3)
		r0, r1, r2, r3 := subdivideCubeBezier(.5, 1, p0, p1, p2, p3)
		approxCubeBezier(move, r0, r1, r2, r3)
	}
}

type Rasterizer struct {
	p       f32.Vec2
	started bool
	dasher  *rasterx.Dasher
	img     image.Image
	scale   float32
}

func (r *Rasterizer) Line(p image.Point) {
	pf := f32.Vec2{
		float32(p.X)*r.scale - float32(r.img.Bounds().Min.X),
		float32(p.Y)*r.scale - float32(r.img.Bounds().Min.Y),
	}
	if !r.started {
		r.dasher.Start(rasterx.ToFixedP(float64(r.p[0]), float64(r.p[1])))
		r.started = true
	}
	r.dasher.Line(rasterx.ToFixedP(float64(pf[0]), float64(pf[1])))
}

func (r *Rasterizer) Move(p image.Point) {
	pf := f32.Vec2{
		float32(p.X)*r.scale - float32(r.img.Bounds().Min.X),
		float32(p.Y)*r.scale - float32(r.img.Bounds().Min.Y),
	}
	if r.started {
		r.dasher.Stop(false)
		r.started = false
	}
	r.p = pf
}

func NewRasterizer(img draw.Image, dr image.Rectangle, scale, strokeWidth float32) *Rasterizer {
	width, height := dr.Dx(), dr.Dy()
	scanner := rasterx.NewScannerGV(width, height, img, img.Bounds())
	r := &Rasterizer{
		dasher: rasterx.NewDasher(width, height, scanner),
		img:    img,
		scale:  scale,
	}
	stroke := strokeWidth * 64
	r.dasher.SetStroke(fixed.Int26_6(stroke), 0, rasterx.RoundCap, rasterx.RoundCap, rasterx.RoundGap, rasterx.ArcClip, nil, 0)
	r.dasher.SetColor(color.Black)
	return r
}

func (r *Rasterizer) Rasterize() {
	if r.started {
		r.dasher.Stop(false)
	}
	r.dasher.Draw()
}

func subdivideCubeBezier(t0, t1 float32, p0, p1, p2, p3 f32.Vec2) (s0, s1, s2, s3 f32.Vec2) {
	u0 := 1 - t0
	u1 := 1 - t1
	s0[0] = u0*u0*u0*p0[0] + (t0*u0*u0+u0*t0*u0+u0*u0*t0)*p1[0] + (t0*t0*u0+u0*t0*t0+t0*u0*t0)*p2[0] + t0*t0*t0*p3[0]
	s0[1] = u0*u0*u0*p0[1] + (t0*u0*u0+u0*t0*u0+u0*u0*t0)*p1[1] + (t0*t0*u0+u0*t0*t0+t0*u0*t0)*p2[1] + t0*t0*t0*p3[1]
	s1[0] = u0*u0*u1*p0[0] + (t0*u0*u1+u0*t0*u1+u0*u0*t1)*p1[0] + (t0*t0*u1+u0*t0*t1+t0*u0*t1)*p2[0] + t0*t0*t1*p3[0]
	s1[1] = u0*u0*u1*p0[1] + (t0*u0*u1+u0*t0*u1+u0*u0*t1)*p1[1] + (t0*t0*u1+u0*t0*t1+t0*u0*t1)*p2[1] + t0*t0*t1*p3[1]
	s2[0] = u0*u1*u1*p0[0] + (t0*u1*u1+u0*t1*u1+u0*u1*t1)*p1[0] + (t0*t1*u1+u0*t1*t1+t0*u1*t1)*p2[0] + t0*t1*t1*p3[0]
	s2[1] = u0*u1*u1*p0[1] + (t0*u1*u1+u0*t1*u1+u0*u1*t1)*p1[1] + (t0*t1*u1+u0*t1*t1+t0*u1*t1)*p2[1] + t0*t1*t1*p3[1]
	s3[0] = u1*u1*u1*p0[0] + (t1*u1*u1+u1*t1*u1+u1*u1*t1)*p1[0] + (t1*t1*u1+u1*t1*t1+t1*u1*t1)*p2[0] + t1*t1*t1*p3[0]
	s3[1] = u1*u1*u1*p0[1] + (t1*u1*u1+u1*t1*u1+u1*u1*t1)*p1[1] + (t1*t1*u1+u1*t1*t1+t1*u1*t1)*p2[1] + t1*t1*t1*p3[1]
	return
}

func isFlat(p0, p1, p2, p3 f32.Vec2) bool {
	const tolerance = .2
	ux := 3.0*p1[0] - 2.0*p0[0] - p3[0]
	uy := 3.0*p1[1] - 2.0*p0[1] - p3[1]
	vx := 3.0*p2[0] - 2.0*p3[0] - p0[0]
	vy := 3.0*p2[1] - 2.0*p3[1] - p0[1]
	ux *= ux
	uy *= uy
	vx *= vx
	vy *= vy
	if ux < vx {
		ux = vx
	}
	if uy < vy {
		uy = vy
	}
	return ux+uy <= 16*tolerance*tolerance
}

func mix(p1, p2 f32.Vec2, a float32) f32.Vec2 {
	return affine.Add(affine.Scale(p1, 1.-a), affine.Scale(p2, a))
}
