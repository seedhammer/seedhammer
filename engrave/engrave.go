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

	"github.com/kortschak/qr"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/f32"
	"golang.org/x/image/math/fixed"
	"seedhammer.com/font/vector"
)

type Options struct {
	MoveSpeed  float32
	PrintSpeed float32
	End        image.Point
}

// Plan is an iterator over the commands of an engraving.
type Plan func(yield func(Command))

type Command struct {
	Line  bool
	Coord image.Point
}

func Commands(plans ...Plan) Plan {
	return func(yield func(Command)) {
		for _, p := range plans {
			p(yield)
		}
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

func transformPlan(t transform, p Plan) Plan {
	return func(yield func(Command)) {
		p(func(c Command) {
			c.Coord = t.transform(c.Coord)
			yield(c)
		})
	}
}

func Offset(x, y int, cmd Plan) Plan {
	return transformPlan(offsetting(x, y), cmd)
}

func Rotate(radians float64, cmd Plan) Plan {
	return transformPlan(rotating(radians), cmd)
}

func Move(p image.Point) Command {
	return Command{
		Line:  false,
		Coord: p,
	}
}

func Line(p image.Point) Command {
	return Command{
		Line:  true,
		Coord: p,
	}
}

func DryRun(p Plan) Plan {
	return func(yield func(Command)) {
		p(func(cmd Command) {
			cmd.Line = false
			yield(cmd)
		})
	}
}

func QR(strokeWidth int, scale int, level qr.Level, content []byte) (Plan, error) {
	qr, err := qr.Encode(string(content), level)
	if err != nil {
		return nil, err
	}
	return func(yield func(Command)) {
		dim := qr.Size
		for y := 0; y < dim; y++ {
			for i := 0; i < scale; i++ {
				draw := false
				var firstx int
				line := y*scale + i
				// Swap direction every other line.
				rev := line%2 != 0
				radius := strokeWidth / 2
				if rev {
					radius = -radius
				}
				drawLine := func(endx int) {
					start := image.Pt(firstx*scale*strokeWidth+radius, line*strokeWidth)
					end := image.Pt(endx*scale*strokeWidth-radius, line*strokeWidth)
					yield(Move(start))
					yield(Line(end))
					draw = false
				}
				for x := -1; x <= dim; x++ {
					xl := x
					px := x
					if rev {
						xl = dim - 1 - x
						px = xl - 1
					}
					on := qr.Black(px, y)
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
	}, nil
}

// qrMoves is the exact number of qrMoves before engraving
// a constant time QR module.
const qrMoves = 4

// constantTimeQRModeuls returns the exact number of modules in a constant
// time QR code, given its version.
func constantTimeQRModules(dims int) int {
	// The numbers below are maximum numbers found through fuzzing.
	// Add a bit more to account for outliers not yet found.
	const extra = 5
	switch dims {
	case 21:
		return 163 + extra
	case 25:
		return 261 + extra
	case 29:
		return 385 + extra
	}
	// Not supported, return a low number to force error.
	return 0
}

func constantTimeStartEnd(dim int) (start, end image.Point) {
	return image.Pt(8+qrMoves, dim-1-qrMoves), image.Pt(dim-1-3, 3)
}

func bitmapForQR(qr *qr.Code) bitmap {
	dim := qr.Size
	bm := NewBitmap(dim, dim)
	for y := 0; y < dim; y++ {
		for x := 0; x < dim; x++ {
			if qr.Black(x, y) {
				bm.Set(image.Pt(x, y))
			}
		}
	}
	return bm
}

func bitmapForQRStatic(dim int) ([]image.Point, []image.Point, bitmap) {
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
	switch dim {
	case 21:
		// No marker.
	case 25:
		alignMarkers = append(alignMarkers, image.Pt(16, 16))
	case 29:
		alignMarkers = append(alignMarkers, image.Pt(20, 20))
	default:
		panic("unsupported qr code version")
	}
	for _, p := range alignMarkers {
		fillMarker(engraved, p, alignmentMarker)
	}
	return posMarkers, alignMarkers, engraved
}

// ConstantQR is like QR that engraves the QR code in a pattern independent of content,
// except for the QR code version (size).
func ConstantQR(strokeWidth, scale int, level qr.Level, content []byte) (Plan, error) {
	c, err := constantQR(strokeWidth, scale, level, content)
	if err != nil {
		return nil, err
	}
	return c.engrave, nil
}

func constantQR(strokeWidth, scale int, level qr.Level, content []byte) (*constantQRCmd, error) {
	qrc, err := qr.Encode(string(content), level)
	if err != nil {
		return nil, err
	}
	dim := qrc.Size
	qr := bitmapForQR(qrc)
	// No need to engrave static features of the QR code.
	posMarkers, alignMarkers, engraved := bitmapForQRStatic(dim)
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
			dist := ManhattanDist(pos, needle)
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
	nmod := constantTimeQRModules(dim)
	if len(modules) >= nmod {
		return nil, fmt.Errorf("too many dims %d QR modules for constant time engraving n: %d waste: %d",
			dim, len(modules), waste)
	}
	modules = padQRModules(nmod, content, modules)
	cmd := &constantQRCmd{
		start:       start,
		end:         end,
		strokeWidth: strokeWidth,
		scale:       scale,
		plan:        modules,
	}
	// Verify constant-ness without the static markers.
	if !isConstantQR(cmd, dim) {
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
	if ManhattanDist(from, to) <= qrMoves {
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
		di, dj := ManhattanDist(pi, to), ManhattanDist(pj, to)
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

func (q constantQRCmd) engraveAlignMarker(yield func(Command), off image.Point) {
	for _, m := range alignmentMarker {
		center := q.centerOf(m.Add(off))
		yield(Move(center))
		q.engraveModule(yield, center)
	}
}

func (q constantQRCmd) engravePositionMarker(yield func(Command), off image.Point) {
	for _, m := range positionMarker {
		center := q.centerOf(m.Add(off))
		yield(Move(center))
		q.engraveModule(yield, center)
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

func (q constantQRCmd) engrave(yield func(Command)) {
	for _, off := range q.posMarkers {
		q.engravePositionMarker(yield, off)
	}
	for _, off := range q.alignMarkers {
		q.engraveAlignMarker(yield, off)
	}
	sw := q.strokeWidth
	prev := q.centerOf(q.start)
	yield(Move(prev))
	moveDist := qrMoves * sw * q.scale
	for _, m := range q.plan {
		center := q.centerOf(m)
		constantMove(yield, center, prev, moveDist)
		prev = center
		q.engraveModule(yield, center)
		yield(Line(center))
	}
	end := q.centerOf(q.end)
	constantMove(yield, end, prev, moveDist)
}

func (q constantQRCmd) engraveModule(yield func(Command), center image.Point) {
	sw := q.strokeWidth
	switch q.scale {
	case 3:
		yield(Line(center.Add(image.Pt(sw, 0))))
		yield(Line(center.Add(image.Pt(sw, sw))))
		yield(Line(center.Add(image.Pt(-sw, sw))))
		yield(Line(center.Add(image.Pt(-sw, -sw))))
		yield(Line(center.Add(image.Pt(sw, -sw))))
	case 4:
		yield(Line(center.Add(image.Pt(-sw, 0))))
		yield(Line(center.Add(image.Pt(-sw, -sw))))
		yield(Line(center.Add(image.Pt(2*sw, -sw))))
		yield(Line(center.Add(image.Pt(2*sw, 2*sw))))
		yield(Line(center.Add(image.Pt(-sw, 2*sw))))
		yield(Line(center.Add(image.Pt(-sw, sw))))
		yield(Line(center.Add(image.Pt(sw, sw))))
		yield(Line(center.Add(image.Pt(sw, 0))))
	}
}

func ManhattanDist(p1, p2 image.Point) int {
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

func (r Rect) Engrave(yield func(Command)) {
	yield(Move(r.Min))
	yield(Line(image.Pt(r.Max.X, r.Min.Y)))
	yield(Line(r.Max))
	yield(Line(image.Pt(r.Min.X, r.Max.Y)))
	yield(Line(r.Min))
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

func engraveConstantRune(yield func(Command), face *vector.Face, em int, r rune) image.Point {
	m := face.Metrics()
	adv, segs, found := face.Decode(r)
	if !found {
		panic(fmt.Errorf("unsupported rune: %s", string(r)))
	}
	pos := image.Pt(0, int(m.Ascent)*em/int(m.Height))
	for {
		seg, ok := segs.Next()
		if !ok {
			break
		}
		p1 := image.Point{
			X: seg.Arg.X * em / int(m.Height),
			Y: seg.Arg.Y * em / int(m.Height),
		}
		switch seg.Op {
		case vector.SegmentOpMoveTo:
			yield(Move(pos.Add(p1)))
		case vector.SegmentOpLineTo:
			yield(Line(pos.Add(p1)))
		default:
			panic("constant rune has unsupported segment type")
		}
	}
	return image.Pt(adv*em/int(m.Height), em)
}

func NewConstantStringer(face *vector.Face, em int, shortest, longest int) *ConstantStringer {
	var runes []*collectProgram
	cs := &ConstantStringer{
		longest: longest,
	}
	// Collects path for every letter.
	for _, r := range alphabet {
		c := new(collectProgram)
		cs.dims = engraveConstantRune(c.Command, face, em, r)
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
			dist := ManhattanDist(needle, p)
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
		if d := ManhattanDist(center, start); d > cs.moveDist {
			cs.moveDist = d
		}
		if d := ManhattanDist(center, end); d > cs.moveDist {
			cs.moveDist = d
		}
	}
	return cs
}

func (c *ConstantStringer) String(txt string) Plan {
	cmd := func(yield func(Command)) {
		needle := c.wordStart
		yield(Move(needle))
		repeats := c.longest / len(txt)
		rest := c.longest - repeats*len(txt)
		for i, r := range txt {
			l := c.alphabet[r-'A']
			extra := 0
			if rest > 0 {
				rest--
				extra = 1
			}
			for j := 0; j < repeats+extra; j++ {
				off := image.Pt(i*c.dims.X, 0)
				// Move to center. Always equal distance.
				center := off.Add(image.Pt(c.dims.X/2, c.dims.Y/2))
				needle = center
				yield(Move(needle))
				start := l.path[0].Add(off)
				constantMove(yield, start, needle, c.moveDist)
				needle = start
				for _, pos := range l.path[1:] {
					needle = pos.Add(off)
					yield(Line(needle))
				}
				constantMove(yield, center, needle, c.moveDist)
				needle = center
				end := off.Add(image.Pt(c.dims.X, c.dims.Y/2))
				yield(Move(end))
				needle = end
			}
		}
		// constantMove by itself is correct but risks engraving out of bounds.
		// To keep movement inside the bounds of the word, move closer so
		// that the distance is less than half the line height.
		wantDist := c.finalDist
		dist := ManhattanDist(c.wordEnd, needle)
		if d := dist - c.dims.Y/2; d > 0 {
			dir := c.wordEnd.Sub(needle)
			mid := needle.Add(dir.Mul(d).Div(dist))
			wantDist -= ManhattanDist(mid, needle)
			needle = mid
			yield(Move(needle))
		}
		// Then let constantMove take care of the rest.
		constantMove(yield, c.wordEnd, needle, wantDist)
	}

	// Verify constant-ness.
	if !c.isConstant(cmd) {
		// Should be constant by construction.
		panic("command is not constant")
	}
	return cmd
}

func isConstantQR(cmd *constantQRCmd, dim int) bool {
	pt := new(pattern)
	cmd.engrave(pt.Command)
	start, end := constantTimeStartEnd(dim)
	start = cmd.centerOf(start)
	end = cmd.centerOf(end)
	// Constant start and end points.
	if pt.start != start || pt.end != end {
		return false
	}
	// Constant number of patterns: 2 per module, 1
	// for the end
	npatterns := 2*constantTimeQRModules(dim) + 1
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

func (c *ConstantStringer) isConstant(cmd Plan) bool {
	pt := new(pattern)
	cmd(pt.Command)
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

// constantMove moves to dst from src in exactly dist manhattan distance.
// It spends extra moves by moving along the square with dst in the center
// and src on its boundary.
// constantMove assumes the distance between dst and src is less than or
// equal to dist.
// constantMove panics if dst equals src and dist is 1.
func constantMove(yield func(Command), dst, src image.Point, dist int) {
	// extra is the distance to spend.
	extra := dist - ManhattanDist(dst, src)
	if dst == src {
		if extra == 1 {
			panic("dst and src coincides and dist allows no movement")
		}
		// If src and dst coincides the implied square reduces to a
		// point which cannot be used for spending moves.
		// Instead move half of extra away and continue from there.
		d := extra / 2
		src = src.Add(image.Pt(d, 0))
		yield(Move(src))
		extra -= d * 2
	}
	defer yield(Move(dst))
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
		yield(Move(src))
	}
}

type collectProgram struct {
	path []image.Point
	len  int
}

func (m *collectProgram) Command(c Command) {
	if c.Line {
		if len(m.path) == 0 {
			panic("no start point for constant rune")
		}
		needle := m.path[len(m.path)-1]
		d := ManhattanDist(needle, c.Coord)
		if d == 0 {
			return
		}
		m.len += d
	} else {
		if len(m.path) > 0 {
			panic("move during constant rune")
		}
	}
	m.path = append(m.path, c.Coord)
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

func (c *pattern) Command(cmd Command) {
	if len(c.pattern) == 0 {
		c.start = cmd.Coord
		c.end = cmd.Coord
		c.pattern = append(c.pattern, patternElem{line: cmd.Line})
		return
	}
	prev := c.end
	elem := &c.pattern[len(c.pattern)-1]
	dist := ManhattanDist(prev, cmd.Coord)
	if elem.line != cmd.Line {
		c.pattern = append(c.pattern, patternElem{line: cmd.Line, len: dist})
	} else {
		elem.len += dist
	}
	c.end = cmd.Coord
}

func String(face *vector.Face, em int, txt string) *StringCmd {
	return &StringCmd{
		LineHeight: 1,
		face:       face,
		em:         em,
		txt:        txt,
	}
}

type StringCmd struct {
	LineHeight int

	face *vector.Face
	em   int
	txt  string
}

func (s *StringCmd) Engrave(yield func(Command)) {
	s.engrave(yield)
}

func (s *StringCmd) Measure() image.Point {
	return s.engrave(nil)
}

func (s *StringCmd) engrave(yield func(Command)) image.Point {
	m := s.face.Metrics()
	pos := image.Pt(0, (int(m.Ascent)*s.em+int(m.Height)-1)/int(m.Height))
	addScale := func(p1, p2 image.Point) image.Point {
		return image.Point{
			X: p1.X + p2.X*s.em/int(m.Height),
			Y: p1.Y + p2.Y*s.em/int(m.Height),
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
		if yield != nil {
			for {
				seg, ok := segs.Next()
				if !ok {
					break
				}
				switch seg.Op {
				case vector.SegmentOpMoveTo:
					p1 := addScale(pos, seg.Arg)
					yield(Move(p1))
				case vector.SegmentOpLineTo:
					p1 := addScale(pos, seg.Arg)
					yield(Line(p1))
				default:
					panic(errors.New("unsupported segment"))
				}
			}
		}
		pos.X += adv * s.em / int(m.Height)
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

func (r *Rasterizer) Command(cmd Command) {
	pf := f32.Vec2{
		float32(cmd.Coord.X)*r.scale - float32(r.img.Bounds().Min.X),
		float32(cmd.Coord.Y)*r.scale - float32(r.img.Bounds().Min.Y),
	}
	if cmd.Line {
		if !r.started {
			r.dasher.Start(rasterx.ToFixedP(float64(r.p[0]), float64(r.p[1])))
			r.started = true
		}
		r.dasher.Line(rasterx.ToFixedP(float64(pf[0]), float64(pf[1])))
	} else {
		if r.started {
			r.dasher.Stop(false)
			r.started = false
		}
		r.p = pf
	}
}

func NewRasterizer(img draw.Image, dr image.Rectangle, scale float32, strokeWidth int) *Rasterizer {
	width, height := dr.Dx(), dr.Dy()
	scanner := rasterx.NewScannerGV(width, height, img, img.Bounds())
	r := &Rasterizer{
		dasher: rasterx.NewDasher(width, height, scanner),
		img:    img,
		scale:  scale,
	}
	r.dasher.SetStroke(fixed.I(strokeWidth), 0, rasterx.RoundCap, rasterx.RoundCap, rasterx.RoundGap, rasterx.ArcClip, nil, 0)
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

func (m *measureProgram) Command(cmd Command) {
	if cmd.Line {
		m.expand(cmd.Coord)
		m.expand(m.p)
	} else {
		m.p = cmd.Coord
	}
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

func Measure(c Plan) image.Rectangle {
	inf := image.Rectangle{Min: image.Pt(1e6, 1e6), Max: image.Pt(-1e6, -1e6)}
	measure := &measureProgram{
		bounds: inf,
	}
	c(measure.Command)
	b := measure.bounds
	if b == inf {
		b = image.Rectangle{}
	}
	return b
}
