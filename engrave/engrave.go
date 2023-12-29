// package engrave transforms shapes such as text and QR codes into
// line and move commands for use with an engraver.
package engrave

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand"
	"sort"

	"github.com/skip2/go-qrcode"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/f32"
	"golang.org/x/image/math/fixed"
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
		return 163 + extra
	case 2:
		return 261 + extra
	case 3:
		return 385 + extra
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
		fillMarker(engraved, p, positionMarker)
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
		fillMarker(engraved, p, alignmentMarker)
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
	modules := []image.Point{}
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
		needle := start
		if len(modules) > 0 {
			needle = modules[len(modules)-1]
		}
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
			needle := start
			if len(modules) > 0 {
				needle = modules[len(modules)-1]
			}
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
	modules = padQRModules(nmod, content, modules)
	cmd := constantQRCmd{
		start:       start,
		end:         end,
		strokeWidth: strokeWidth,
		scale:       scale,
		plan:        modules,
	}
	// Verify constant-ness without the static markers.
	if !isConstantQR(cmd, dim, qrc.VersionNumber) {
		panic("constant QR engraving is not constant")
	}
	cmd.posMarkers = posMarkers
	cmd.alignMarkers = alignMarkers
	return cmd, nil
}

// padQRModules pads modules with extra engravings up to n modules.
func padQRModules(n int, content []byte, modules []image.Point) []image.Point {
	// Distribute the extra modules randomly as repeats of existing
	// modules.
	extra := n - len(modules)
	mac := hmac.New(sha256.New, []byte("seedhammer constant qr"))
	mac.Write(content)
	sum := mac.Sum(nil)
	seed := int64(binary.BigEndian.Uint64(sum))
	r := rand.New(rand.NewSource(seed))
	counts := make([]int, len(modules))
	for i := 0; i < extra; i++ {
		idx := r.Intn(len(counts))
		counts[idx]++
	}
	var paddedModules []image.Point
	for i, m := range modules {
		// Engrave once plus extra.
		c := 1 + counts[i]
		for j := 0; j < c; j++ {
			paddedModules = append(paddedModules, m)
		}
	}
	return paddedModules
}

var alignmentMarker = []image.Point{
	{X: 0, Y: 0},
	{X: 1, Y: 0},
	{X: 2, Y: 0},
	{X: 3, Y: 0},
	{X: 4, Y: 0},

	{X: 4, Y: 1},
	{X: 4, Y: 2},
	{X: 4, Y: 3},

	{X: 4, Y: 4},
	{X: 3, Y: 4},
	{X: 2, Y: 4},
	{X: 1, Y: 4},
	{X: 0, Y: 4},

	{X: 0, Y: 3},
	{X: 0, Y: 2},
	{X: 0, Y: 1},

	{X: 2, Y: 2},
}

var positionMarker = []image.Point{
	{X: 0, Y: 0},
	{X: 1, Y: 0},
	{X: 2, Y: 0},
	{X: 3, Y: 0},
	{X: 4, Y: 0},
	{X: 5, Y: 0},
	{X: 6, Y: 0},

	{X: 6, Y: 1},
	{X: 6, Y: 2},
	{X: 6, Y: 3},
	{X: 6, Y: 4},
	{X: 6, Y: 5},

	{X: 6, Y: 6},
	{X: 5, Y: 6},
	{X: 4, Y: 6},
	{X: 3, Y: 6},
	{X: 2, Y: 6},
	{X: 1, Y: 6},
	{X: 0, Y: 6},

	{X: 0, Y: 5},
	{X: 0, Y: 4},
	{X: 0, Y: 3},
	{X: 0, Y: 2},
	{X: 0, Y: 1},

	{X: 2, Y: 2},
	{X: 3, Y: 2},
	{X: 4, Y: 2},
	{X: 2, Y: 3},
	{X: 3, Y: 3},
	{X: 4, Y: 3},
	{X: 2, Y: 4},
	{X: 3, Y: 4},
	{X: 4, Y: 4},
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

	start, end   image.Point
	posMarkers   []image.Point
	alignMarkers []image.Point
	plan         []image.Point
}

func (q constantQRCmd) engraveAlignMarker(p Program, off image.Point) {
	for _, m := range alignmentMarker {
		center := q.centerOf(m.Add(off))
		p.Move(center)
		q.engraveModule(p, center)
	}
}

func (q constantQRCmd) engravePositionMarker(p Program, off image.Point) {
	for _, m := range positionMarker {
		center := q.centerOf(m.Add(off))
		p.Move(center)
		q.engraveModule(p, center)
	}
}

func (q constantQRCmd) centerOf(p image.Point) image.Point {
	sw := q.strokeWidth
	radius := sw / 2
	return image.Point{
		X: radius + (p.X*q.scale+1)*sw,
		Y: radius + (p.Y*q.scale+1)*sw,
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
	prev := q.centerOf(q.start)
	p.Move(prev)
	moveDist := qrMoves * sw * q.scale
	for _, m := range q.plan {
		center := q.centerOf(m)
		constantMove(p, center, prev, moveDist)
		prev = center
		q.engraveModule(p, center)
		p.Line(center)
	}
	end := q.centerOf(q.end)
	constantMove(p, end, prev, moveDist)
}

func (q constantQRCmd) engraveModule(p Program, center image.Point) {
	sw := q.strokeWidth
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
}

func manhattanDist(p1, p2 image.Point) int {
	return manhattanLen(p1.Sub(p2))
}

func manhattanLen(v image.Point) int {
	if v.X < 0 {
		v.X = -v.X
	}
	if v.Y < 0 {
		v.Y = -v.Y
	}
	if v.X > v.Y {
		return v.X
	} else {
		return v.Y
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

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// ConstantStringer can engrave text in a timing insensitive way.
type ConstantStringer struct {
	moveDist    int
	finalDist   int
	engraveDist int
	longest     int
	wordStart   image.Point
	wordEnd     image.Point
	dims        image.Point
	alphabet    [len(alphabet)]constantRune
}

type constantRune struct {
	path []image.Point
}

func engraveConstantRune(p Program, face *font.Face, em int, r rune) image.Point {
	m := face.Metrics
	adv, segs, found := face.Decode(r)
	if !found {
		panic(fmt.Errorf("unsupported rune: %s", string(r)))
	}
	pos := image.Pt(0, m.Ascent*em/m.Height)
	for {
		seg, ok := segs.Next()
		if !ok {
			break
		}
		p1 := image.Point{
			X: seg.Arg.X * em / m.Height,
			Y: seg.Arg.Y * em / m.Height,
		}
		switch seg.Op {
		case font.SegmentOpMoveTo:
			p.Move(pos.Add(p1))
		case font.SegmentOpLineTo:
			p.Line(pos.Add(p1))
		default:
			panic("constant rune has unsupported segment type")
		}
	}
	return image.Pt(adv*em/m.Height, em)
}

func NewConstantStringer(face *font.Face, em int, shortest, longest int) *ConstantStringer {
	var runes []*collectProgram
	cs := &ConstantStringer{
		longest: longest,
	}
	// Collects path for every letter.
	for _, r := range alphabet {
		c := new(collectProgram)
		cs.dims = engraveConstantRune(c, face, em, r)
		if c.len > cs.engraveDist {
			cs.engraveDist = c.len
		}
		runes = append(runes, c)
	}
	// We rely on the advance being even so there is equal
	// distance from either edge to the center.
	if cs.dims.X%2 == 1 {
		cs.dims.X--
	}
	// Expand letters to match the longest letter.
	suffix := longest - shortest
	// end in the center of the rune between the shortest and
	// longest string. Minimizes the final movement.
	cs.finalDist = suffix * cs.dims.X / 2
	endx := shortest*cs.dims.X + cs.finalDist
	cs.wordStart = image.Pt(0, cs.dims.Y/2)
	cs.wordEnd = image.Pt(endx, cs.dims.Y/2)
	center := image.Pt(cs.dims.X/2, cs.dims.Y/2)
	for i, r := range alphabet {
		c := runes[i]
		path := c.path
		last := len(path) - 1
		n := c.len
		// Trace backwards, starting from the end.
		idx := last
		dir := -1
		for n != cs.engraveDist {
			idx += dir
			needle := path[len(path)-1]
			p := path[idx]
			dist := manhattanDist(needle, p)
			// Shorten path segment if required.
			if overflow := n + dist - cs.engraveDist; overflow > 0 {
				d := p.Sub(needle)
				abs := d
				if abs.X < 0 {
					abs.X = -abs.X
				}
				if abs.Y < 0 {
					abs.Y = -abs.Y
				}
				if abs.X >= abs.Y {
					// X determines manhattan distance, shorten it
					// by overflow.
					signx := d.X / abs.X
					d.X -= overflow * signx
					// Shorten Y proportionally.
					d.Y -= overflow * d.Y / abs.X
				} else {
					// Vice versa.
					signy := d.Y / abs.Y
					d.Y -= overflow * signy
					d.X -= overflow * d.X / abs.Y
				}
				p = needle.Add(d)
				dist -= overflow
			}
			n += dist
			path = append(path, p)
			if idx == 0 || idx == last {
				dir = -dir
			}
		}
		cs.alphabet[r-'A'] = constantRune{
			path: path,
		}
		start, end := path[0], path[len(path)-1]
		if d := manhattanDist(center, start); d > cs.moveDist {
			cs.moveDist = d
		}
		if d := manhattanDist(center, end); d > cs.moveDist {
			cs.moveDist = d
		}
	}
	return cs
}

func (c *ConstantStringer) String(txt string) Command {
	cmd := &constantStringCmd{
		cs:  c,
		txt: txt,
	}
	// Verify constant-ness.
	if !c.isConstant(cmd) {
		// Should be constant by construction.
		panic("command is not constant")
	}
	return cmd
}

func isConstantQR(cmd constantQRCmd, dim, ver int) bool {
	pt := new(pattern)
	cmd.Engrave(pt)
	start, end := constantTimeStartEnd(dim)
	start = cmd.centerOf(start)
	end = cmd.centerOf(end)
	// Constant start and end points.
	if pt.start != start || pt.end != end {
		return false
	}
	// Constant number of patterns: 2 per module, 1
	// for the end
	npatterns := 2*constantTimeQRModules(ver) + 1
	if len(pt.pattern) != npatterns {
		return false
	}
	sc := cmd.scale * cmd.strokeWidth
	moveLen := qrMoves * sc
	for _, p := range pt.pattern {
		wantLen := moveLen
		if p.line {
			wantLen = sc * cmd.scale
		}
		if p.len != wantLen {
			return false
		}
	}
	return true
}

func (c *ConstantStringer) isConstant(cmd Command) bool {
	pt := new(pattern)
	cmd.Engrave(pt)
	// Constant start and end points.
	if pt.start != c.wordStart || pt.end != c.wordEnd {
		return false
	}
	// Constant number of patterns.
	if len(pt.pattern) != 2*c.longest+1 {
		return false
	}
	// All pattern elements have constant sizes.
	line := false
	for i, p := range pt.pattern {
		if p.line != line {
			return false
		}
		var wantDist int
		switch {
		case i == 0:
			wantDist = c.moveDist + c.dims.X/2
		case i == len(pt.pattern)-1:
			wantDist = c.moveDist + c.dims.X/2 + c.finalDist
		case line:
			wantDist = c.engraveDist
		default:
			wantDist = 2*c.moveDist + c.dims.X
		}
		if wantDist != p.len {
			return false
		}
		line = !line
	}
	return true
}

type constantStringCmd struct {
	cs  *ConstantStringer
	txt string
}

func (s *constantStringCmd) Engrave(p Program) {
	needle := s.cs.wordStart
	p.Move(needle)
	repeats := s.cs.longest / len(s.txt)
	rest := s.cs.longest - repeats*len(s.txt)
	for i, r := range s.txt {
		l := s.cs.alphabet[r-'A']
		extra := 0
		if rest > 0 {
			rest--
			extra = 1
		}
		for j := 0; j < repeats+extra; j++ {
			off := image.Pt(i*s.cs.dims.X, 0)
			// Move to center. Always equal distance.
			center := off.Add(image.Pt(s.cs.dims.X/2, s.cs.dims.Y/2))
			needle = center
			p.Move(needle)
			start := l.path[0].Add(off)
			constantMove(p, start, needle, s.cs.moveDist)
			needle = start
			for _, pos := range l.path[1:] {
				needle = pos.Add(off)
				p.Line(needle)
			}
			constantMove(p, center, needle, s.cs.moveDist)
			needle = center
			end := off.Add(image.Pt(s.cs.dims.X, s.cs.dims.Y/2))
			p.Move(end)
			needle = end
		}
	}
	// constantMove by itself is correct but risks engraving out of bounds.
	// To keep movement inside the bounds of the word, move closer so
	// that the distance is less than half the line height.
	wantDist := s.cs.finalDist
	dist := manhattanDist(s.cs.wordEnd, needle)
	if d := dist - s.cs.dims.Y/2; d > 0 {
		dir := s.cs.wordEnd.Sub(needle)
		mid := needle.Add(dir.Mul(d).Div(dist))
		wantDist -= manhattanDist(mid, needle)
		needle = mid
		p.Move(needle)
	}
	// Then let constantMove take care of the rest.
	constantMove(p, s.cs.wordEnd, needle, wantDist)
}

// constantMove moves to dst from src in exactly dist manhattan distance.
// It spends extra moves by moving along the square with dst in the center
// and src on its boundary.
// constantMove assumes the distance between dst and src is less than or
// equal to dist.
// constantMove panics if dst equals src and dist is 1.
func constantMove(p Program, dst, src image.Point, dist int) {
	// extra is the distance to spend.
	extra := dist - manhattanDist(dst, src)
	if dst == src {
		if extra == 1 {
			panic("dst and src coincides and dist allows no movement")
		}
		// If src and dst coincides the implied square reduces to a
		// point which cannot be used for spending moves.
		// Instead move half of extra away and continue from there.
		d := extra / 2
		src = src.Add(image.Pt(d, 0))
		p.Move(src)
		extra -= d * 2
	}
	defer p.Move(dst)
	dp := src.Sub(dst)
	d := manhattanLen(dp)
	// axis is the direction from dst to src along the longest axis.
	axis := dp.Div(d)
	// Tie-break diagonals arbitrarily.
	if axis.X != 0 && axis.Y != 0 {
		axis.X = 0
	}
	for extra > 0 {
		dp := src.Sub(dst)
		axis = image.Pt(-axis.Y, axis.X)
		// cornerDist is the distance from src to the corner along
		// moveDir.
		cornerDist := d - dp.X*axis.X - dp.Y*axis.Y
		moveDist := cornerDist
		if moveDist > extra {
			moveDist = extra
		}
		extra -= moveDist
		src = src.Add(axis.Mul(moveDist))
		p.Move(src)
	}
}

type collectProgram struct {
	path []image.Point
	len  int
}

func (m *collectProgram) Line(p image.Point) {
	if len(m.path) == 0 {
		panic("no start point for constant rune")
	}
	needle := m.path[len(m.path)-1]
	d := manhattanDist(needle, p)
	if d == 0 {
		return
	}
	m.len += d
	m.path = append(m.path, p)
}

func (m *collectProgram) Move(p image.Point) {
	if len(m.path) > 0 {
		panic("move during constant rune")
	}
	m.path = append(m.path, p)
}

// pattern records the pattern of the engraving instructions
// sent to it.
type pattern struct {
	start, end image.Point
	pattern    []patternElem
}

type patternElem struct {
	line bool
	len  int
}

func (c *pattern) Line(p image.Point) {
	c.instruct(p, true)
}

func (c *pattern) Move(p image.Point) {
	c.instruct(p, false)
}

func (c *pattern) instruct(p image.Point, line bool) {
	if len(c.pattern) == 0 {
		c.start = p
		c.end = p
		c.pattern = append(c.pattern, patternElem{line: line})
		return
	}
	prev := c.end
	elem := &c.pattern[len(c.pattern)-1]
	dist := manhattanDist(prev, p)
	if elem.line != line {
		c.pattern = append(c.pattern, patternElem{line: line, len: dist})
	} else {
		elem.len += dist
	}
	c.end = p
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
	LineHeight int

	face *font.Face
	em   int
	txt  string
}

func (s *StringCmd) Engrave(p Program) {
	s.engrave(p)
}

func (s *StringCmd) Measure() image.Point {
	return s.engrave(nil)
}

func (s *StringCmd) engrave(p Program) image.Point {
	m := s.face.Metrics
	pos := image.Pt(0, (m.Ascent*s.em+m.Height-1)/m.Height)
	addScale := func(p1, p2 image.Point) image.Point {
		return image.Point{
			X: p1.X + p2.X*s.em/m.Height,
			Y: p1.Y + p2.Y*s.em/m.Height,
		}
	}
	height := s.em * s.LineHeight
	for _, r := range s.txt {
		if r == '\n' {
			pos.X = 0
			pos.Y += height
			continue
		}
		adv, segs, found := s.face.Decode(r)
		if !found {
			panic(fmt.Errorf("unsupported rune: %s", string(r)))
		}
		if p != nil {
			for {
				seg, ok := segs.Next()
				if !ok {
					break
				}
				switch seg.Op {
				case font.SegmentOpMoveTo:
					p1 := addScale(pos, seg.Arg)
					p.Move(p1)
				case font.SegmentOpLineTo:
					p1 := addScale(pos, seg.Arg)
					p.Line(p1)
				default:
					panic(errors.New("unsupported segment"))
				}
			}
		}
		pos.X += adv * s.em / m.Height
	}
	return image.Pt(pos.X, height)
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

type measureProgram struct {
	p      image.Point
	bounds image.Rectangle
}

func (m *measureProgram) Line(p image.Point) {
	m.expand(p)
	m.expand(m.p)
}

func (m *measureProgram) expand(p image.Point) {
	if p.X < m.bounds.Min.X {
		m.bounds.Min.X = p.X
	} else if p.X > m.bounds.Max.X {
		m.bounds.Max.X = p.X
	}
	if p.Y < m.bounds.Min.Y {
		m.bounds.Min.Y = p.Y
	} else if p.Y > m.bounds.Max.Y {
		m.bounds.Max.Y = p.Y
	}
}

func (m *measureProgram) Move(p image.Point) {
	m.p = p
}

func Measure(c Command) image.Rectangle {
	inf := image.Rectangle{Min: image.Pt(1e6, 1e6), Max: image.Pt(-1e6, -1e6)}
	measure := measureProgram{
		bounds: inf,
	}
	c.Engrave(&measure)
	b := measure.bounds
	if b == inf {
		b = image.Rectangle{}
	}
	return b
}
