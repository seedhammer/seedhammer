// package engrave transforms shapes such as text and QR codes into
// line and move commands for use with an engraver.
package engrave

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"iter"
	"math"
	"slices"
	"unicode/utf8"

	"github.com/seedhammer/kortschak-qr"
	"seedhammer.com/bresenham"
	"seedhammer.com/font/vector"
)

// Params decribe the physical characteristics of an
// engraver.
type Params struct {
	// The StrokeWidth measured in machine units.
	StrokeWidth int
	// A Millimeter measured in machine units.
	Millimeter int
}

func (p Params) F(v float32) int {
	return int(math.Round(float64(v * float32(p.Millimeter))))
}

func (p Params) I(v int) int {
	return p.Millimeter * v
}

// Plan is an iterator over the commands of an engraving.
type Plan = iter.Seq[Command]

type Command struct {
	Line  bool
	Coord image.Point
}

func Commands(plans ...Plan) Plan {
	return func(yield func(Command) bool) {
		for _, p := range plans {
			for c := range p {
				if !yield(c) {
					return
				}
			}
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

func scaling(sx, sy int) transform {
	return transform{
		sx, 0, 0,
		0, sy, 0,
	}
}

func transformPlan(t transform, p Plan) Plan {
	return func(yield func(Command) bool) {
		for c := range p {
			c.Coord = t.transform(c.Coord)
			if !yield(c) {
				return
			}
		}
	}
}

func Scale(sx, sy int, cmd Plan) Plan {
	return transformPlan(scaling(sx, sy), cmd)
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
	return func(yield func(Command) bool) {
		for c := range p {
			c.Line = false
			if !yield(c) {
				return
			}
		}
	}
}

func QR(strokeWidth int, scale int, qr *qr.Code) Plan {
	return func(yield func(Command) bool) {
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
					if !yield(Move(start)) || !yield(Line(end)) {
						return
					}
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
	}
}

// qrMovesPerModule is the exact number of qrMovesPerModule before engraving
// a constant time QR module.
const qrMovesPerModule = 4

// direction represents a direction in a compass direction.
type direction uint8

// qrMoves represent [qrMovesPerModule] moves, each move packed into
// 3 bits.
type qrMoves uint16

// Compass directions for QR moves.
const (
	dirN direction = iota
	dirNE
	dirE
	dirSE
	dirS
	dirSW
	dirW
	dirNW
)

func (d direction) Reverse() direction {
	switch d {
	case dirN:
		return dirS
	case dirNE:
		return dirSW
	case dirE:
		return dirW
	case dirSE:
		return dirNW
	case dirS:
		return dirN
	case dirSW:
		return dirNE
	case dirW:
		return dirE
	default:
		return dirSE
	}
}

func (m direction) Direction() image.Point {
	switch m {
	case dirN:
		return image.Pt(0, 1)
	case dirNE:
		return image.Pt(1, 1)
	case dirE:
		return image.Pt(1, 0)
	case dirSE:
		return image.Pt(1, -1)
	case dirS:
		return image.Pt(0, -1)
	case dirSW:
		return image.Pt(-1, -1)
	case dirW:
		return image.Pt(-1, 0)
	case dirNW:
		return image.Pt(-1, 1)
	default:
		panic("invalid move")
	}
}

func (q qrMoves) Set(i int, m direction) qrMoves {
	if i < 0 || i >= qrMovesPerModule {
		panic("index out of bounds")
	}
	return q | qrMoves(m)<<(i*3)
}

func (q qrMoves) Get(i int) image.Point {
	if i < 0 || i >= qrMovesPerModule {
		panic("index out of bounds")
	}
	return direction((q >> (i * 3)) & 0b111).Direction()
}

func moveFromDirection(dir image.Point) direction {
	switch dir {
	case image.Pt(0, 1):
		return dirN
	case image.Pt(1, 1):
		return dirNE
	case image.Pt(1, 0):
		return dirE
	case image.Pt(1, -1):
		return dirSE
	case image.Pt(0, -1):
		return dirS
	case image.Pt(-1, -1):
		return dirSW
	case image.Pt(-1, 0):
		return dirW
	case image.Pt(-1, 1):
		return dirNW
	default:
		panic("invalid direction")
	}
}

// constantQRMoves computes a list of moves from the origin to target.
func constantQRMoves(target image.Point) qrMoves {
	moves := make([]direction, 0, qrMovesPerModule)
	moves = appendConstantMove(moves, qrMovesPerModule, target)
	var qrm qrMoves
	for i, m := range moves {
		qrm = qrm.Set(i, m)
	}
	return qrm
}

// appendConstantMove computes a list of moves from the origin to target.
func appendConstantMove(moves []direction, n int, target image.Point) []direction {
	m := 0
	// Burn extra moves until the manhattan distance is
	// equal to the remaining moves.
	for manhattanLen(target) < (n - m) {
		// Make a distance-neutral move for any non-zero
		// target. Zero targets will increase the distance
		// by 1.
		var dir image.Point
		abs := image.Point{
			X: max(target.X, -target.X),
			Y: max(target.Y, -target.Y),
		}
		if abs.X >= abs.Y {
			if target.Y >= 0 {
				dir.Y = 1
			} else {
				dir.Y = -1
			}
		} else {
			if target.X >= 0 {
				dir.X = 1
			} else {
				dir.X = -1
			}
		}
		target = target.Sub(dir)
		moves = append(moves, moveFromDirection(dir))
		m++
	}
	// Spend remaining moves towards target.
	for m < n {
		var dir image.Point
		switch {
		case target.X > 0:
			dir.X = 1
		case target.X < 0:
			dir.X = -1
		}
		switch {
		case target.Y > 0:
			dir.Y = 1
		case target.Y < 0:
			dir.Y = -1
		}
		target = target.Sub(dir)
		moves = append(moves, moveFromDirection(dir))
		m++
	}
	if target != (image.Point{}) {
		panic("move out of range")
	}
	return moves
}

// constantTimeQRModules returns the exact number of modules in a constant
// time QR code, given its version.
func constantTimeQRModules(dims int) int {
	// The numbers below are maximum numbers found through fuzzing.
	// Add a bit more to account for outliers not yet found.
	const extra = 5
	switch dims {
	case 21:
		return 164 + extra
	case 25:
		return 262 + extra
	case 29:
		return 386 + extra
	}
	// Not supported, return a low number to force error.
	return 0
}

func constantTimeStartEnd(dim int) (start, end image.Point) {
	return image.Pt(8+qrMovesPerModule, dim-1-qrMovesPerModule), image.Pt(dim-1-3, 3)
}

func bitmapForQR(qr *qr.Code) bitmap {
	dim := qr.Size
	bm := newBitmap(dim, dim)
	for y := 0; y < dim; y++ {
		for x := 0; x < dim; x++ {
			if qr.Black(x, y) {
				bm.Set(image.Pt(x, y))
			}
		}
	}
	return bm
}

func bitmapForQRStatic(dim int) ([]image.Point, []image.Point) {
	// First 3 position markers.
	posMarkers := []image.Point{
		{0, 0},
		{dim - 7, 0},
		{0, dim - 7},
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
	return posMarkers, alignMarkers
}

// ConstantQR is like QR that engraves the QR code in a pattern independent of content,
// except for the QR code version (size).
func ConstantQR(strokeWidth, scale int, qrc *qr.Code) (Plan, error) {
	c, err := constantQR(qrc)
	if err != nil {
		return nil, err
	}
	return c.engrave(strokeWidth, scale), nil
}

func constantQR(qrc *qr.Code) (*constantQRCmd, error) {
	dim := qrc.Size
	qr := bitmapForQR(qrc)
	engraved := newBitmap(dim, dim)
	posMarkers, alignMarkers := bitmapForQRStatic(dim)
	// No need to engrave static features of the QR code.
	for _, p := range posMarkers {
		fillMarker(engraved, p, positionMarker)
	}
	for _, p := range alignMarkers {
		fillMarker(engraved, p, alignmentMarker)
	}
	// Start in the lower-left corner.
	pos := image.Pt(0, dim-1)
	// Iterating forward.
	dir := 1
	start, end := constantTimeStartEnd(dim)
	needle := start
	nmod := constantTimeQRModules(dim)
	modules := make([]qrMoves, 0, nmod)
	waste := 0
	engrave := func(p image.Point) {
		m := constantQRMoves(p.Sub(needle))
		modules = append(modules, m)
		needle = p
		if engraved.Get(p) {
			waste++
		} else {
			engraved.Set(p)
		}
	}
	visited := newBitmap(dim, dim)
	// Find path to a module close enough to p.
	move := func(p image.Point) error {
		clear(visited.bits)
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
			dist := ManhattanDist(pos, needle)
			if dist > qrMovesPerModule {
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
	// Pad up to, but not including, the end point.
	modules = padQRModules(nmod-1, modules)
	// Add end point.
	modules = append(modules, constantQRMoves(end.Sub(needle)))
	if len(modules) != nmod {
		return nil, fmt.Errorf("too many dims %d QR modules for constant time engraving n: %d waste: %d",
			dim, len(modules), waste)
	}
	cmd := &constantQRCmd{
		dim:  dim,
		plan: modules,
	}
	assertConstantQR(cmd)
	return cmd, nil
}

// padQRModules pads modules with extra engravings up to n modules.
func padQRModules(n int, modules []qrMoves) []qrMoves {
	// Distribute the extra modules evenly.
	zeroMove := constantQRMoves(image.Point{})
	extra := n - len(modules)
	// Extend slice, possibly in place.
	result := append(modules, make([]qrMoves, extra)...)
	// Use a line tracer to determine when to insert dummy moves.
	// The x axis denote modules, the y axis denote extras.
	var l bresenham.Line
	_, _, steps := l.Reset(image.Pt(len(modules), extra))
	// Iterate backwards in case result shares memory with modules.
	ri := len(result) - 1
	mi := len(modules) - 1
	for range steps {
		dx, dy := l.Step()
		if dx != 0 {
			result[ri] = modules[mi]
			mi--
			ri--
		}
		if dy != 0 {
			result[ri] = zeroMove
			ri--
		}
	}
	return result
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
	if ManhattanDist(from, to) <= qrMovesPerModule {
		return modules, true
	}
	candidates := make([]image.Point, 0, qrMovesPerModule*qrMovesPerModule)
	for y := -qrMovesPerModule; y <= qrMovesPerModule; y++ {
		for x := -qrMovesPerModule; x <= qrMovesPerModule; x++ {
			p := from.Add(image.Pt(x, y))
			if !qr.Get(p) || visited.Get(p) {
				continue
			}
			visited.Set(p)
			candidates = append(candidates, p)
		}
	}
	slices.SortFunc(candidates, func(pi, pj image.Point) int {
		di, dj := ManhattanDist(pi, to), ManhattanDist(pj, to)
		if di == dj {
			// Equal distance; prefer the un-engraved path.
			if engraved.Get(pj) {
				return -1
			} else {
				return 1
			}
		}
		return di - dj
	})
	for _, p := range candidates {
		path, ok := findPath(append(modules, p), visited, qr, engraved, to, p)
		if ok {
			return path, true
		}
	}
	return nil, false
}

// constantQRCmd represents the constant time plan for engraving a QR
// code.
type constantQRCmd struct {
	// The QR dimension.
	dim int
	// The list of moves.
	plan []qrMoves
}

func centerOf(sw, scale int, p image.Point) image.Point {
	radius := sw / 2
	return image.Point{
		X: radius + (p.X*scale+1)*sw,
		Y: radius + (p.Y*scale+1)*sw,
	}
}

func (q constantQRCmd) engrave(strokeWidth, scale int) Plan {
	return func(yield func(Command) bool) {
		cont := true
		posMarkers, alignMarkers := bitmapForQRStatic(q.dim)
		start, _ := constantTimeStartEnd(q.dim)
		for _, off := range posMarkers {
			for _, m := range positionMarker {
				center := centerOf(strokeWidth, scale, m.Add(off))
				cont = cont && yield(Move(center))
				cont = cont && engraveModule(yield, strokeWidth, scale, center)
			}
		}
		for _, off := range alignMarkers {
			for _, m := range alignmentMarker {
				center := centerOf(strokeWidth, scale, m.Add(off))
				cont = cont && yield(Move(center))
				cont = cont && engraveModule(yield, strokeWidth, scale, center)
			}
		}
		needle := start
		cont = cont && yield(Move(centerOf(strokeWidth, scale, needle)))
		for i, m := range q.plan {
			for i := range qrMovesPerModule {
				dir := m.Get(i)
				needle = needle.Add(dir)
				cont = cont && yield(Move(centerOf(strokeWidth, scale, needle)))
			}
			// Don't engrave the end point.
			if i < len(q.plan)-1 {
				center := centerOf(strokeWidth, scale, needle)
				cont = cont && engraveModule(yield, strokeWidth, scale, center)
				cont = cont && yield(Line(center))
			}
		}
	}
}

func engraveModule(yield cmdYielder, sw, scale int, center image.Point) bool {
	switch scale {
	case 3:
		return yield(Line(center.Add(image.Pt(sw, 0)))) &&
			yield(Line(center.Add(image.Pt(sw, sw)))) &&
			yield(Line(center.Add(image.Pt(-sw, sw)))) &&
			yield(Line(center.Add(image.Pt(-sw, -sw)))) &&
			yield(Line(center.Add(image.Pt(sw, -sw))))
	case 4:
		return yield(Line(center.Add(image.Pt(-sw, 0)))) &&
			yield(Line(center.Add(image.Pt(-sw, -sw)))) &&
			yield(Line(center.Add(image.Pt(2*sw, -sw)))) &&
			yield(Line(center.Add(image.Pt(2*sw, 2*sw)))) &&
			yield(Line(center.Add(image.Pt(-sw, 2*sw)))) &&
			yield(Line(center.Add(image.Pt(-sw, sw)))) &&
			yield(Line(center.Add(image.Pt(sw, sw)))) &&
			yield(Line(center.Add(image.Pt(sw, 0))))
	default:
		panic("unsupported module scale")
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
	return max(v.X, v.Y)
}

func manhattanLenFrac(f1, f2 fraction) fraction {
	f1.Nom = abs(f1.Nom)
	f2.Nom = abs(f2.Nom)
	if f1.GreaterEq(f2) {
		return f1
	} else {
		return f2
	}
}

type bitmap struct {
	w    int
	bits []uint32
}

func newBitmap(w, h int) bitmap {
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

func (r Rect) Engrave(yield func(Command) bool) {
	_ = yield(Move(r.Min)) &&
		yield(Line(image.Pt(r.Max.X, r.Min.Y))) &&
		yield(Line(r.Max)) &&
		yield(Line(image.Pt(r.Min.X, r.Max.Y))) &&
		yield(Line(r.Min))
}

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// ConstantStringer can engrave text in a timing insensitive way.
type ConstantStringer struct {
	origin     image.Point
	advance    int
	ascent     int
	height     int
	lineMoves  []unitMove
	startMoves []direction
	// endMoves contains fractional end moves.
	endMoves []direction
	// endMoveDenom is the denominator for end move fractions.
	endMoveDenom uint
}

// unitMove represent a move of unit Manhattan distance.
type unitMove struct {
	// Dir is one of the 4 the major axis directions (N, S, W, E).
	Dir direction
	// Minor the fractional move of the Minor axis.
	Minor fraction
}

func (m unitMove) Reverse() unitMove {
	return unitMove{
		Dir:   m.Dir.Reverse(),
		Minor: m.Minor.Mul(-1),
	}
}

func (m unitMove) Direction() (fraction, fraction) {
	switch m.Dir {
	case dirN:
		return m.Minor, intFrac(1)
	case dirE:
		return intFrac(1), m.Minor
	case dirS:
		return m.Minor, intFrac(-1)
	default:
		return intFrac(-1), m.Minor
	}
}

type fraction struct {
	Nom   int
	Denom uint
}

var zeroFrac = fraction{Denom: 1}

func intFrac(v int) fraction {
	return fraction{
		Nom:   v,
		Denom: 1,
	}
}

func (f fraction) GreaterEq(f2 fraction) bool {
	f1nom := f.Nom * int(f2.Denom)
	f2nom := f2.Nom * int(f.Denom)
	return f1nom >= f2nom
}

func (f fraction) Mul(s int) fraction {
	return fraction{
		Nom:   f.Nom * s,
		Denom: f.Denom,
	}.Reduce()
}

func (f fraction) Div(s int) fraction {
	if s < 0 {
		f.Nom = -f.Nom
		s = -s
	}
	f.Denom *= uint(s)
	return f
}

func (f fraction) Add(f2 fraction) fraction {
	return fraction{
		Nom:   f.Nom*int(f2.Denom) + f2.Nom*int(f.Denom),
		Denom: f.Denom * f2.Denom,
	}.Reduce()
}

func (f fraction) Split() (int, fraction) {
	return f.Nom / int(f.Denom), fraction{
		Nom:   f.Nom % int(f.Denom),
		Denom: f.Denom,
	}
}

func (f fraction) Reduce() fraction {
	nom := f.Nom
	if nom < 0 {
		nom = -nom
	}
	d := gcd(uint(nom), f.Denom)
	return fraction{
		Nom:   f.Nom / int(d),
		Denom: f.Denom / d,
	}
}

func gcd(a, b uint) uint {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func NewConstantStringer(face *vector.Face) *ConstantStringer {
	// Compute alphabet bounds, engraving length etc.
	// bstart is the bounds of all start points.
	bstart := image.Rectangle{
		Min: image.Pt(math.MaxInt, math.MaxInt),
		Max: image.Pt(math.MinInt, math.MinInt),
	}
	var advance int
	maxLineTotal := 0
	for _, r := range alphabet {
		a, segs, found := face.Decode(r)
		if !found {
			panic(fmt.Errorf("unsupported rune: %s", string(r)))
		}
		advance = a
		seenLine := false
		var needle image.Point
		lineTotal := 0
		for {
			seg, ok := segs.Next()
			if !ok {
				break
			}
			p := seg.Arg
			switch seg.Op {
			case vector.SegmentOpMoveTo:
				if seenLine {
					panic("constant glyph path is not an unbroken path")
				}
				bstart.Min.X = min(bstart.Min.X, p.X)
				bstart.Min.Y = min(bstart.Min.Y, p.Y)
				bstart.Max.X = max(bstart.Max.X, p.X)
				bstart.Max.Y = max(bstart.Max.Y, p.Y)
			case vector.SegmentOpLineTo:
				if !seenLine {
					seenLine = true
				}
				l := manhattanLen(p.Sub(needle))
				lineTotal += l
			default:
				panic("constant glyph has unsupported segment type")
			}
			needle = p
		}
		maxLineTotal = max(maxLineTotal, lineTotal)
	}

	// Build line paths and start moves.
	nmoves := len(alphabet) * maxLineTotal
	origin := bstart.Max.Add(bstart.Min).Div(2)
	startDist := max(ManhattanDist(origin, bstart.Max), ManhattanDist(origin, bstart.Min))
	m := face.Metrics()
	s := &ConstantStringer{
		ascent:       int(m.Ascent),
		height:       int(m.Height),
		advance:      advance,
		origin:       origin,
		lineMoves:    make([]unitMove, 0, nmoves),
		startMoves:   make([]direction, 0, len(alphabet)*startDist),
		endMoveDenom: 1,
	}
	// The maximum, fractional distance from end point to origin.
	var endDist fraction
	for _, r := range alphabet {
		_, segs, _ := face.Decode(r)
		var needle image.Point
		n := 0
		startIdx := len(s.lineMoves)
		for {
			seg, ok := segs.Next()
			if !ok {
				break
			}
			p := seg.Arg
			switch seg.Op {
			case vector.SegmentOpMoveTo:
				s.startMoves = appendConstantMove(s.startMoves, startDist, p.Sub(s.origin))
			case vector.SegmentOpLineTo:
				d := p.Sub(needle)
				l := manhattanLen(d)
				s.lineMoves = appendConstantUnitMove(s.lineMoves, d)
				n += l
			default:
				panic("constant glyph has unsupported segment type")
			}
			needle = p
		}
		// Pad engraving by retracing the engraved path.
		idx := len(s.lineMoves) - 1
		// Reverse direction.
		dir := -1
		// Accumulate the fractional position.
		xfrac := intFrac(needle.X)
		yfrac := intFrac(needle.Y)
		for n < maxLineTotal {
			m := s.lineMoves[idx]
			rev := m
			if dir == -1 {
				rev = rev.Reverse()
			}
			s.lineMoves = append(s.lineMoves, rev)
			dx, dy := rev.Direction()
			xfrac = xfrac.Add(dx)
			yfrac = yfrac.Add(dy)
			// Flip direction if the beginning is reached.
			if idx == startIdx {
				dir = 1
			}
			idx += dir
			n++
		}
		div := gcd(s.endMoveDenom, xfrac.Denom)
		s.endMoveDenom = s.endMoveDenom * xfrac.Denom / div
		div = gcd(s.endMoveDenom, yfrac.Denom)
		s.endMoveDenom = s.endMoveDenom * yfrac.Denom / div
		if dist := manhattanLenFrac(xfrac, yfrac); dist.GreaterEq(endDist) {
			endDist = dist
		}
	}

	// Build (fractional) end moves.
	lineMoves := endDist.Mul(int(s.endMoveDenom)).Nom
	s.endMoves = make([]direction, 0, len(alphabet)*lineMoves)
	i, j := 0, 0
	for range alphabet {
		var needle image.Point
		// Move to line beginning.
		for range startDist {
			d := s.startMoves[j].Direction()
			j++
			needle = needle.Add(d)
		}
		// Trace line until end.
		xfrac := intFrac(needle.X)
		yfrac := intFrac(needle.Y)
		for range maxLineTotal {
			dx, dy := s.lineMoves[i].Direction()
			i++
			xfrac = xfrac.Add(dx)
			yfrac = yfrac.Add(dy)
		}
		xfrac = xfrac.Mul(int(s.endMoveDenom))
		yfrac = yfrac.Mul(int(s.endMoveDenom))
		ix, xrem := xfrac.Split()
		iy, yrem := yfrac.Split()
		if xrem.Nom != 0 || yrem.Nom != 0 {
			panic("non-integer line move")
		}
		s.endMoves = appendConstantMove(s.endMoves, lineMoves, image.Pt(-ix, -iy))
	}
	return s
}

func abs(v int) int {
	if v >= 0 {
		return v
	}
	return -v
}

func appendConstantUnitMove(moves []unitMove, target image.Point) []unitMove {
	absTarget := image.Point{
		X: abs(target.X),
		Y: abs(target.Y),
	}
	var m unitMove
	if absTarget.X > absTarget.Y {
		m.Minor = fraction{Nom: target.Y, Denom: uint(absTarget.X)}
		switch {
		case target.X < 0:
			m.Dir = dirW
		case target.X > 0:
			m.Dir = dirE
		}
	} else {
		m.Minor = fraction{Nom: target.X, Denom: uint(absTarget.Y)}
		switch {
		case target.Y < 0:
			m.Dir = dirS
		case target.Y > 0:
			m.Dir = dirN
		}
	}
	dist := max(absTarget.X, absTarget.Y)
	for range dist {
		moves = append(moves, m)
	}
	return moves
}

type constantGlyph struct {
	reverse bool
	index   int
}

func (c *ConstantStringer) String(txt string, em int, longest int) Plan {
	var l bresenham.Line
	glyphs := make([]constantGlyph, 0, longest)
	// Use line stepping to distribute padding letters.
	_, _, steps := l.Reset(image.Pt(len(txt), longest-len(txt)))
	var last int
	for range steps {
		dx, dy := l.Step()
		if dx != 0 {
			r, n := utf8.DecodeRuneInString(txt)
			txt = txt[n:]
			last = int(r) - 'A'
			glyphs = append(glyphs, constantGlyph{index: last})
		}
		if dy != 0 {
			glyphs = append(glyphs, constantGlyph{reverse: true, index: last})
		}
	}
	return func(yield func(Command) bool) {
		scale := em / c.height
		px, py := intFrac(0), intFrac(0)
		moveFrac := func(x, y fraction) image.Point {
			px = px.Add(x)
			py = py.Add(y)
			nx, _ := px.Mul(scale).Split()
			ny, _ := py.Mul(scale).Split()
			return image.Pt(nx, ny)
		}
		move := func(d image.Point) image.Point {
			return moveFrac(intFrac(d.X), intFrac(d.Y))
		}
		orig := c.origin.Add(image.Pt(0, int(c.ascent)))
		if !yield(Move(move(orig))) {
			return
		}
		advHalf := fraction{Nom: c.advance, Denom: 2}
		startDist := len(c.startMoves) / len(alphabet)
		endDist := len(c.endMoves) / len(alphabet)
		lineDist := len(c.lineMoves) / len(alphabet)
		cont := true
		for i, g := range glyphs {
			// Advance.
			if i > 0 {
				cont = cont && yield(Move(moveFrac(advHalf, intFrac(0))))
				dx := advHalf
				if g.reverse {
					dx = dx.Mul(-1)
				}
				cont = cont && yield(Move(moveFrac(dx, intFrac(0))))
			}
			// Move from origin to beginning of path.
			startMoves := c.startMoves[g.index*startDist : (g.index+1)*startDist]
			for _, m := range startMoves {
				cont = cont && yield(Move(move(m.Direction())))
			}
			// Engrave glyph.
			lineMoves := c.lineMoves[g.index*lineDist : (g.index+1)*lineDist]
			for _, m := range lineMoves {
				dx, dy := m.Direction()
				cont = cont && yield(Line(moveFrac(dx, dy)))
			}
			// Move to origin.
			endMoves := c.endMoves[g.index*endDist : (g.index+1)*endDist]
			for _, m := range endMoves {
				dir := m.Direction()
				dx := intFrac(dir.X).Div(int(c.endMoveDenom))
				dy := intFrac(dir.Y).Div(int(c.endMoveDenom))
				cont = cont && yield(Move(moveFrac(dx, dy)))
			}
		}
	}
}

// assertConstantQR verifies that the engraving time of cmd
// depends only on its dimension.
func assertConstantQR(cmd *constantQRCmd) {
	// Verify the engraving length.
	nmod := constantTimeQRModules(cmd.dim)
	if len(cmd.plan) != nmod {
		panic("constant QR engraving is not constant")
	}
	// Verify the end point.
	start, end := constantTimeStartEnd(cmd.dim)
	needle := start
	for _, m := range cmd.plan {
		for j := range qrMovesPerModule {
			needle = needle.Add(m.Get(j))
		}
	}
	if needle != end {
		panic("constant QR engraving is not constant")
	}
}

type cmdYielder func(Command) bool

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

func (s *StringCmd) Engrave() Plan {
	return func(yield func(Command) bool) {
		s.engrave(yield)
	}
}

func (s *StringCmd) Measure() image.Point {
	return s.engrave(nil)
}

func (s *StringCmd) engrave(yield func(Command) bool) image.Point {
	m := s.face.Metrics()
	pos := image.Pt(0, (int(m.Ascent)*s.em+int(m.Height)-1)/int(m.Height))
	addScale := func(p1, p2 image.Point) image.Point {
		return image.Point{
			X: p1.X + p2.X*s.em/int(m.Height),
			Y: p1.Y + p2.Y*s.em/int(m.Height),
		}
	}
	height := s.em * s.LineHeight
	cont := true
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
					cont = cont && yield(Move(p1))
				case vector.SegmentOpLineTo:
					p1 := addScale(pos, seg.Arg)
					cont = cont && yield(Line(p1))
				default:
					panic(errors.New("unsupported segment"))
				}
			}
		}
		pos.X += adv * s.em / int(m.Height)
	}
	return image.Pt(pos.X, height)
}

func Rasterize(img draw.Image, p Plan) {
	var pen image.Point
	for c := range p {
		var l bresenham.Line
		v := c.Coord.Sub(pen)
		xd, yd, steps := l.Reset(v)
		xdir := int(xd)*2 - 1
		ydir := int(yd)*2 - 1
		if c.Line {
			img.Set(pen.X, pen.Y, color.Black)
		}
		for range steps {
			dx, dy := l.Step()
			pen.X -= int(dx) * xdir
			pen.Y -= int(dy) * ydir
			if c.Line {
				img.Set(pen.X, pen.Y, color.Black)
			}
		}
	}
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

func Measure(plan Plan) image.Rectangle {
	inf := image.Rectangle{Min: image.Pt(1e6, 1e6), Max: image.Pt(-1e6, -1e6)}
	measure := &measureProgram{
		bounds: inf,
	}
	for c := range plan {
		measure.Command(c)
	}
	b := measure.bounds
	if b == inf {
		b = image.Rectangle{}
	}
	return b
}
