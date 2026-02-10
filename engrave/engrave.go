// package engrave transforms shapes such as text and QR codes into
// line and move commands for use with an engraver.
package engrave

import (
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"
	"sort"
	"time"
	"unicode/utf8"

	qr "github.com/seedhammer/kortschak-qr"
	"seedhammer.com/bezier"
	"seedhammer.com/bspline"
	"seedhammer.com/font/vector"
)

// StepperConfig is a configuration for a [Stepper].
type StepperConfig struct {
	// Move speed in steps/second.
	Speed uint
	// EngraveSpeed in steps/second.
	EngravingSpeed uint
	// Acceleration (and deceleration) in steps/second².
	Acceleration uint
	// Jerk, the change in acceleration, in steps/second³.
	Jerk uint
	// Engraver ticks per second. A tick represents the duration
	// of a completed pio step.
	TicksPerSecond uint
}

// Params decribe the physical characteristics of an
// engraver.
type Params struct {
	// The StrokeWidth measured in machine units.
	StrokeWidth int
	// A Millimeter measured in machine units.
	Millimeter int
	StepperConfig
}

func (p Params) F(v float32) int {
	return int(math.Round(float64(v * float32(p.Millimeter))))
}

func (p Params) I(v int) int {
	return p.Millimeter * v
}

// Engraving is an iterator over the commands of an engraving.
type Engraving = iter.Seq[Command]

type Command struct {
	kind cmdKind
	args [3]uint
}

// splineKnot represents a control point in a uniform
// b-spline.
type splineKnot struct {
	Engrave      bool
	Knot         bezier.Point
	Multiplicity int
}

type cmdKind uint8

const (
	moveCmd cmdKind = iota
	lineCmd
	delayCmd
)

func (c Command) AsDelay() (denom, nom uint, ok bool) {
	switch c.kind {
	case delayCmd:
	default:
		return 0, 0, false
	}
	return uint(c.args[0]), uint(c.args[1]), true
}

func (c Command) AsKnot() (splineKnot, bool) {
	line := false
	switch c.kind {
	case moveCmd:
	case lineCmd:
		line = true
	default:
		return splineKnot{}, false
	}
	return splineKnot{
		Engrave: line,
		Knot: bezier.Point{
			X: int(c.args[0]),
			Y: int(c.args[1]),
		},
		Multiplicity: int(c.args[2]),
	}, true
}

type Transform struct {
	Yield func(Command) bool
	stack *transformStack
	slot  int
	id    int
}

func NewTransform(yield func(Command) bool) Transform {
	s := new(transformStack)
	s.yield = func(c Command) bool {
		if !s.done {
			switch c.kind {
			case moveCmd, lineCmd:
				p := bezier.Pt(int(c.args[0]), int(c.args[1]))
				coord := s.transform(p)
				c.args[0], c.args[1] = uint(coord.X), uint(coord.Y)
			}
			s.done = !yield(c)
		}
		return !s.done
	}
	s.stack = append(s.stack, transformSlot{t: offsetting(0, 0)})
	return Transform{stack: s, Yield: s.yield}
}

type transformStack struct {
	stack []transformSlot
	id    int
	yield func(Command) bool
	done  bool
}

type transformSlot struct {
	t  transform
	id int
}

func (s *transformStack) transform(p bezier.Point) bezier.Point {
	var t transform
	if n := len(s.stack); n > 0 {
		t = s.stack[n-1].t
	}
	return t.transform(p)
}

func (s *transformStack) push(id, slot int, t transform) Transform {
	if s.stack[slot].id != id {
		panic("transform was popped")
	}
	t0 := s.stack[slot].t
	e := transformSlot{
		id: s.id,
		t:  t0.Mul(t),
	}
	slot++
	s.stack = append(s.stack[:slot], e)
	s.id++
	return Transform{
		stack: s,
		slot:  slot,
		id:    e.id,
		Yield: s.yield,
	}
}

func (t Transform) Scale(sx, sy int) Transform {
	return t.push(scaling(sx, sy))
}

func (t Transform) Offset(x, y int) Transform {
	return t.push(offsetting(x, y))
}

func (t Transform) Rotate(radians float64) Transform {
	return t.push(rotating(radians))
}

func (t Transform) push(tr transform) Transform {
	return t.stack.push(t.id, t.slot, tr)
}

type transform [6]int

func (m transform) transform(p bezier.Point) bezier.Point {
	return bezier.Point{
		X: p.X*m[0] + p.Y*m[1] + m[2],
		Y: p.X*m[3] + p.Y*m[4] + m[5],
	}
}

func (m transform) Mul(m2 transform) transform {
	return transform{
		m[0]*m2[0] + m[1]*m2[3], m[0]*m2[1] + m[1]*m2[4], m[0]*m2[2] + m[1]*m2[5] + m[2],
		m[3]*m2[0] + m[4]*m2[3], m[3]*m2[1] + m[4]*m2[4], m[3]*m2[2] + m[4]*m2[5] + m[5],
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

func DelayMove(yield func(Command) bool, conf StepperConfig, t uint, from, to bezier.Point) bool {
	dur := timeMove(conf, ManhattanDist(from, to))
	return yield(Delay(dur, t)) &&
		yield(Move(to))
}

func Delay(denom, nom uint) Command {
	c := Command{
		kind: delayCmd,
	}
	c.args[0] = uint(denom)
	c.args[1] = uint(nom)
	return c
}

func ControlPoint(engrave bool, ctrl bezier.Point) Command {
	c := Command{kind: moveCmd}
	if engrave {
		c.kind = lineCmd
	}
	c.args[0], c.args[1], c.args[2] = uint(ctrl.X), uint(ctrl.Y), 1
	return c
}

func Move(p bezier.Point) Command {
	c := Command{
		kind: moveCmd,
	}
	c.args[0], c.args[1], c.args[2] = uint(p.X), uint(p.Y), 3
	return c
}

func Line(p bezier.Point) Command {
	c := Command{
		kind: lineCmd,
	}
	c.args[0], c.args[1], c.args[2] = uint(p.X), uint(p.Y), 3
	return c
}

func DryRun(s bspline.Curve) bspline.Curve {
	return func(yield func(bspline.Knot) bool) {
		for c := range s {
			c.Engrave = false
			if !yield(c) {
				return
			}
		}
	}
}

func QR(strokeWidth int, scale int, qr *qr.Code) Engraving {
	return func(yield func(Command) bool) {
		dim := qr.Size
		cont := true
		radius := strokeWidth / 2
		for y := range dim {
			for i := range scale {
				draw := false
				var firstx int
				line := y*scale + i
				// Swap direction every other line.
				rev := line%2 != 0
				off := radius
				if rev {
					off = -off
				}
				drawLine := func(endx int) {
					start := bezier.Pt(firstx*scale*strokeWidth+off, line*strokeWidth+radius)
					end := bezier.Pt(endx*scale*strokeWidth-off, line*strokeWidth+radius)
					cont = cont && yield(Move(start)) && yield(Line(end))
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

// qrMove represent a move up to [qrMovesPerModule] far.
type qrMove struct {
	m uint8
}

func (m qrMove) Point() bezier.Point {
	return bezier.Point{
		X: int(m.m&0b1111) - qrMovesPerModule,
		Y: int(m.m>>4) - qrMovesPerModule,
	}
}

// constantQRMove computes a list of moves from the origin to target.
func constantQRMove(target bezier.Point) qrMove {
	m := qrMove{
		m: (uint8(target.X+qrMovesPerModule) & 0b1111) | uint8(target.Y+qrMovesPerModule)<<4,
	}
	if m.Point() != target {
		panic("move too far")
	}
	return m
}

// constantTimeQRModules returns the exact number of modules in a constant
// time QR code, given its dimension.
func constantTimeQRModules(dims int) int {
	// The numbers below are maximum numbers found through fuzzing.
	// Add a bit more to account for outliers not yet found.
	const extra = 5
	switch dims {
	case 21:
		return 166 + extra
	case 25:
		return 261 + extra
	case 29:
		return 386 + extra
	case 33:
		return 542 + extra
	}
	// Not supported, return a low number to force error.
	return 0
}

func constantTimeStartEnd(dim int) (start, end bezier.Point) {
	return bezier.Pt(8+qrMovesPerModule, dim-1-qrMovesPerModule), bezier.Pt(dim-1-3, 3)
}

func bitmapForQR(qr *qr.Code) bitmap {
	dim := qr.Size
	bm := newBitmap(dim, dim)
	for y := range dim {
		for x := range dim {
			if qr.Black(x, y) {
				bm.Set(bezier.Pt(x, y))
			}
		}
	}
	return bm
}

func bitmapForQRStatic(dim int) ([]bezier.Point, []bezier.Point) {
	// First 3 position markers.
	posMarkers := []bezier.Point{
		{},
		{X: dim - 7},
		{Y: dim - 7},
	}
	var alignMarkers []bezier.Point
	switch dim {
	case 21:
		// No marker.
	case 25, 29, 33:
		// Single marker.
		alignMarkers = append(alignMarkers, bezier.Pt(dim-9, dim-9))
	default:
		panic("unsupported qr code version")
	}
	return posMarkers, alignMarkers
}

// ConstantQR is like QR that engraves the QR code in a pattern independent of content,
// except for the QR code version (size).
func ConstantQR(qrc *qr.Code) (*ConstantQRCmd, error) {
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
	pos := bezier.Pt(0, dim-1)
	// Iterating forward.
	dir := 1
	start, end := constantTimeStartEnd(dim)
	needle := start
	nmod := constantTimeQRModules(dim)
	modules := make([]qrMove, 0, nmod)
	waste := 0
	engrave := func(p bezier.Point) {
		m := constantQRMove(p.Sub(needle))
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
	move := func(p bezier.Point) error {
		clear(visited.bits)
		visited.Set(needle)
		path, ok := findPath(nil, visited, qr, engraved, p, needle)
		if !ok {
			return errors.New("QR modules spaced too far for constant time engraving")
		}
		for _, m := range path {
			engrave(needle.Add(m.Point()))
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
	if len(modules) > nmod {
		return nil, fmt.Errorf("too many dims %d QR modules for constant time engraving n: %d waste: %d",
			dim, len(modules), waste)
	}
	cmd := &ConstantQRCmd{
		Size: dim,
		plan: modules,
	}
	return cmd, nil
}

var alignmentMarker = []bezier.Point{
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

var positionMarker = []bezier.Point{
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

func fillMarker(engraved bitmap, off bezier.Point, points []bezier.Point) {
	for _, p := range points {
		p = p.Add(off)
		engraved.Set(p)
	}
}

func findPath(modules []qrMove, visited, qr, engraved bitmap, to, from bezier.Point) ([]qrMove, bool) {
	if ManhattanDist(from, to) <= qrMovesPerModule {
		return modules, true
	}
	// The maximum number of positions is the manhattan square reachable
	// from the starting point. Subtract 1 for the center which is always
	// marked visible.
	const nmoves = (2*qrMovesPerModule+1)*(2*qrMovesPerModule+1) - 1
	candidates := make([]qrMove, 0, nmoves)
	for y := -qrMovesPerModule; y <= qrMovesPerModule; y++ {
		for x := -qrMovesPerModule; x <= qrMovesPerModule; x++ {
			m := constantQRMove(bezier.Pt(x, y))
			p := from.Add(m.Point())
			if !qr.Get(p) || visited.Get(p) {
				continue
			}
			visited.Set(p)
			candidates = append(candidates, m)
		}
	}
	slices.SortFunc(candidates, func(mi, mj qrMove) int {
		pi := from.Add(mi.Point())
		pj := from.Add(mj.Point())
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
	for _, m := range candidates {
		p := from.Add(m.Point())
		path, ok := findPath(append(modules, m), visited, qr, engraved, to, p)
		if ok {
			return path, true
		}
	}
	return nil, false
}

// ConstantQRCmd represents the constant time plan for engraving a QR
// code.
type ConstantQRCmd struct {
	// The QR dimension.
	Size int
	// The list of moves.
	plan []qrMove
}

func centerOf(sw, scale int, p bezier.Point) bezier.Point {
	radius := sw / 2
	return p.Mul(scale).Add(bezier.Pt(1, 1)).Mul(sw).Add(bezier.Pt(radius, radius))
}

func (q ConstantQRCmd) Engrave(conf StepperConfig, strokeWidth, scale int) Engraving {
	return func(yield func(Command) bool) {
		cont := true
		posMarkers, alignMarkers := bitmapForQRStatic(q.Size)
		start, end := constantTimeStartEnd(q.Size)
		for _, off := range posMarkers {
			for _, m := range positionMarker {
				center := centerOf(strokeWidth, scale, m.Add(off))
				cont = cont && yield(Move(center)) &&
					engraveModule(yield, strokeWidth, scale, center)
			}
		}
		for _, off := range alignMarkers {
			for _, m := range alignmentMarker {
				center := centerOf(strokeWidth, scale, m.Add(off))
				cont = cont && yield(Move(center)) &&
					engraveModule(yield, strokeWidth, scale, center)
			}
		}
		needle := start
		cont = cont && yield(Move(centerOf(strokeWidth, scale, needle)))
		maxDur := timeMove(conf, qrMovesPerModule*strokeWidth*scale)
		nmod := constantTimeQRModules(q.Size)
		// len(q.plan) is generally less than nmod, the constant number of
		// modules to engrave. Accumulate fractions in units of 1/nmod where
		// each q.plan module contributes len(q.plan) fractions. Advance
		// the plan when the accumulated fraction is >= 1.
		accum := 0
		plan := q.plan
		advance := true
		for range nmod {
			var move bezier.Point
			if advance {
				move = plan[0].Point()
			}
			from := centerOf(strokeWidth, scale, needle)
			needle = needle.Add(move)
			to := centerOf(strokeWidth, scale, needle)
			cont = cont && DelayMove(yield, conf, maxDur, from, to) &&
				engraveModule(yield, strokeWidth, scale, to) &&
				yield(Line(to))
			accum += len(q.plan)
			advance = accum >= nmod
			if advance {
				accum -= nmod
				plan = plan[1:]
			}
		}
		// Move to end point.
		from := centerOf(strokeWidth, scale, needle)
		needle = end
		to := centerOf(strokeWidth, scale, needle)
		cont = cont && DelayMove(yield, conf, maxDur, from, to)
	}
}

func engraveModule(yield func(Command) bool, sw, scale int, center bezier.Point) bool {
	switch scale {
	case 3:
		return yield(Line(center.Add(bezier.Pt(sw, 0)))) &&
			yield(Line(center.Add(bezier.Pt(sw, sw)))) &&
			yield(Line(center.Add(bezier.Pt(-sw, sw)))) &&
			yield(Line(center.Add(bezier.Pt(-sw, -sw)))) &&
			yield(Line(center.Add(bezier.Pt(sw, -sw))))
	case 4:
		return yield(Line(center.Add(bezier.Pt(-sw, 0)))) &&
			yield(Line(center.Add(bezier.Pt(-sw, -sw)))) &&
			yield(Line(center.Add(bezier.Pt(2*sw, -sw)))) &&
			yield(Line(center.Add(bezier.Pt(2*sw, 2*sw)))) &&
			yield(Line(center.Add(bezier.Pt(-sw, 2*sw)))) &&
			yield(Line(center.Add(bezier.Pt(-sw, sw)))) &&
			yield(Line(center.Add(bezier.Pt(sw, sw)))) &&
			yield(Line(center.Add(bezier.Pt(sw, 0))))
	default:
		panic("unsupported module scale")
	}
}

func ManhattanDist(p1, p2 bezier.Point) int {
	return manhattanLen(p1.Sub(p2))
}

func manhattanLen(v bezier.Point) int {
	if v.X < 0 {
		v.X = -v.X
	}
	if v.Y < 0 {
		v.Y = -v.Y
	}
	return int(max(v.X, v.Y))
}

type bitmap struct {
	w    int
	bits []uint64
}

func newBitmap(w, h int) bitmap {
	if w > 64 {
		panic("bitset too wide")
	}
	return bitmap{
		w:    w,
		bits: make([]uint64, h),
	}
}

func (b bitmap) Set(p bezier.Point) {
	if p.X < 0 || p.Y < 0 || p.X >= b.w || int(p.Y) >= len(b.bits) {
		panic("out of range")
	}
	b.bits[p.Y] |= 1 << p.X
}

func (b bitmap) Get(p bezier.Point) bool {
	if p.X < 0 || p.Y < 0 || p.X >= b.w || int(p.Y) >= len(b.bits) {
		return false
	}
	return b.bits[p.Y]&(1<<p.X) != 0
}

type Rect bspline.Bounds

func (r Rect) Engrave(yield func(Command) bool) {
	_ = yield(Move(r.Min)) &&
		yield(Line(bezier.Point{X: r.Max.X, Y: r.Min.Y})) &&
		yield(Line(r.Max)) &&
		yield(Line(bezier.Point{X: r.Min.X, Y: r.Max.Y})) &&
		yield(Line(r.Min))
}

const constantAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// ConstantStringer can engrave text in a timing insensitive way.
type ConstantStringer struct {
	face *vector.Face
	// runeDuration is the duration of the longest rune.
	runeDuration uint
	// center is the starting position.
	center bezier.Point
	// startEndDist is the longest distance between a rune start and end
	// points.
	startEndDist int
	// advDist is the face advance in steps.
	advDist int
	// em is the font size.
	em       int
	alphabet []constantRune
	conf     StepperConfig
}

type constantRune struct {
	R    rune
	Info constantPlan
}

type constantPlan struct {
	Duration   uint
	Start, End bezier.Point
}

// An scurvePhase is a (duration, distance) tuple of a phase in an [scurve].
type scurvePhase struct {
	Duration uint
	Position uint
}

// computeSCurve computes the phases for traveling dist along a straight
// line. The waypoints respects machine limits and continuity up to and
// including acceleration.
// An S-curve is characterized by 7 phases:
//
//  1. Maximum jerk (j = jmax).
//  2. Zero jerk, constant acceleration (j = 0).
//  3. Minimum jerk, (j = -jmax).
//  4. Coasting at constant velocity (j=0).
//
// Phases 5, 6 and 7 are mirror images of phases 3, 2, 1, respectively.
func computeSCurve(smax, vmax, amax, jmax uint, tps uint) [7]scurvePhase {
	// Set the minimum number of ticks to avoid short segments. Short
	// segments lead to larger errors in kinetic value calculations.
	const minTicks = 50

	smaxf := float32(smax)
	vmaxf := float32(vmax)
	amaxf := float32(amax)
	jmaxf := float32(jmax)

	// The relation between position, velocity, acceleration and jerk is
	// given by
	//
	//  s(t) = s0 + v*t + a/2*t² + j/6*t³
	//
	// There are 3 phases with +jmax, 0, -jmax respectively. Denoting
	// the phase durations t1, t2, t3, the acceleration after phase 3 is
	//
	//  a_ph3 = jmax*t1-jmax*t3
	//
	// Since the acceleration must be 0 in the coasting phase,
	//
	//  a_ph3 = 0 => t3 = t1
	//
	// The duration of phase 1, t1, is limited by amax:
	//
	//  a_ph1 = jmax*t1, aph1 <= amax => t1 <= amax/jmax
	t1amax := amaxf / jmaxf

	// t1 is further limited by vmax. Assuming t2 is zero, the
	// velocity after phase 3 is
	//
	//  v_ph3 = jmax*t1² => t1 <= √(vmax/jmax)
	t1vmax := float32(math.Sqrt(float64(vmaxf / jmaxf)))

	// Finally, t1 is limited by half the distance, smax/2.
	// The displacement after phase 3 is given by
	//
	//  s_ph3 = jmax*t1³ => t1 <= ∛(1/2*smax/jmax)
	halfDist := 1. / 2 * smaxf
	t1smax := float32(math.Cbrt(float64(halfDist / jmaxf)))

	// t1f and t3 are now known, and respects machine limits.
	t1f := min(t1smax, t1vmax, t1amax)

	var t2vmax, t2smax float32
	if t1f != 0 {
		// The duration of phase 2, t2, is limited by vmax. The velocity after
		// phase 3 is given by
		//
		//  v_ph3 = jmax*t1² + jmax*t1*t2 => t2 <= (vmax - jmax*t1²)/(jmax*t1)
		t2vmax = (vmaxf - jmaxf*t1f*t1f) / (jmaxf * t1f)

		// t2 is also limited by smax/2:
		//
		//  s_ph3 = jmax*t1³ + 3/2*jmax*t1²*t2 + 1/2*jmax*t1*t2²
		//   => t2 <= (-3*jmax*t1² + √(jmax^2*t1^4 + 4*jmax*smax*t1))/(2*jmax*t1)
		//   => t2 <= -3/2*t1 + √(1/4*t1² + smax/(jmax*t1))
		if t1f != 0 {
			t2smax = -3./2*t1f + float32(math.Sqrt(float64(1./4*t1f*t1f+smaxf/(jmaxf*t1f))))
		}
	}

	// Clamp phase 2 duration to 0 to avoid round-off error.
	t2f := max(0, min(t2vmax, t2smax))

	// Knowing the jerk and duration of every phase,
	// compute the distance and velocities by integration.
	sph0 := physState{}
	sph1 := sph0.Simulate(t1f, +jmaxf)
	sph2 := sph1.Simulate(t2f, 0)
	sph3 := sph2.Simulate(t1f, -jmaxf)
	// Coasting phase 4 is the remaining distance.
	sph4 := max(0, smaxf-sph3.s*2)
	// Phase 4 velocity is vmax by construction (otherwise its distance is zero).
	t4f := sph4 / vmaxf
	type controlPoint struct {
		t float32
		s physState
	}
	tpsf := float32(tps)
	t1 := uint(t1f*tpsf + .5)
	t2 := uint(t2f*tpsf + .5)
	t4 := uint(t4f*tpsf + .5)
	ctrls := make([]controlPoint, 4)
	var nctrls int
	switch {
	case t4 > minTicks && t2 > minTicks:
		nctrls = copy(ctrls, []controlPoint{{t1f, sph0}, {t2f, sph1}, {t1f, sph2}, {t4f, sph3}})
	case t4 > minTicks:
		nctrls = copy(ctrls, []controlPoint{{t1f, sph0}, {t1f, sph2}, {t4f, sph3}})
	case t2 > minTicks:
		nctrls = copy(ctrls, []controlPoint{{t1f, sph0}, {t2f, sph1}, {t1f, sph2}, {t1f, sph3}})
	default:
		nctrls = copy(ctrls, []controlPoint{{t1f, sph0}, {t1f, sph2}, {t1f, sph3}})
	}
	ctrls = ctrls[:nctrls]
	spline := make([]uint, 4)[:nctrls-1]
	// Compute control points for phases 1-3.
	for i, s0 := range ctrls[:len(ctrls)-1] {
		s1 := ctrls[i+1]
		// Use polar coordinates to compute the knot control point
		// from the two middle control points of the Bézier segment
		// implied by the position and velocity of the two states
		// s0 and s1.
		p001 := s0.s.s + s0.s.v*s0.t/3
		p011 := s1.s.s - s1.s.v*s0.t/3
		p012 := (p011-p001)*s1.t/s0.t + p011
		spline[i] = uint(p012 + .5)
	}
	switch {
	case t4 > minTicks && t2 > minTicks:
		return [...]scurvePhase{
			{t1, spline[0]},
			{t2, spline[1]},
			{t1, spline[2]},
			{t4, smax - spline[2]},
			{t1, smax - spline[1]},
			{t2, smax - spline[0]},
			{t1, smax},
		}
	case t4 > minTicks:
		return [...]scurvePhase{
			{t1, spline[0]},
			{},
			{t1, spline[1]},
			{t4, smax - spline[1]},
			{t1, smax - spline[0]},
			{},
			{t1, smax},
		}
	case t2 > minTicks:
		return [...]scurvePhase{
			{t1, spline[0]},
			{t2, spline[1]},
			{t1, spline[2]},
			{},
			{t1, smax - spline[1]},
			{t2, smax - spline[0]},
			{t1, smax},
		}
	default:
		return [...]scurvePhase{
			{t1, spline[0]},
			{},
			{t1, spline[1]},
			{},
			{t1, smax - spline[0]},
			{},
			{t1, smax},
		}
	}
}

// physState models physical properties for simulating the
// movement of the engraving needle in one dimension.
type physState struct {
	// s is the position.
	s float32
	// v is the velocity.
	v float32
	// a is the acceleration.
	a float32
}

// Simulate advance time by t at jerk j.
func (p *physState) Simulate(t, j float32) physState {
	jt := j * t
	jtt := jt * t
	jttt := jtt * t
	at := p.a * t
	return physState{
		a: p.a + jt,
		v: p.v + at + jtt/2,
		s: p.s + p.v*t + at*t/2 + jttt/6,
	}
}

func PlanEngraving(conf StepperConfig, e Engraving) bspline.Curve {
	const maxSplineKnots = 100

	knotBuf := make([]bspline.Knot, 0, maxSplineKnots)
	return planEngraving(knotBuf, conf, e)
}

// planEngraving is like PlanEngraving but avoids garbage from the knot buffer on
// TinyGo.
func planEngraving(knotBuf []bspline.Knot, conf StepperConfig, e Engraving) bspline.Curve {
	return func(yield func(bspline.Knot) bool) {
		var ts timeScaler
		start := bspline.Knot{}
		spline := knotBuf[:0]
		// Initialize the spline with 2 clamping knots at (0, 0).
		spline = append(spline, start, start)
		for c := range e {
			if k, ok := c.AsKnot(); ok {
				for range k.Multiplicity {
					spline = append(spline, bspline.Knot{Engrave: k.Engrave, Ctrl: k.Knot})
					if len(spline) < 5 {
						continue
					}
					n := len(spline)
					k0, k1, k2 := spline[n-3].Ctrl, spline[n-2].Ctrl, spline[n-1].Ctrl
					if clamped := k0 == k1 && k1 == k2; !clamped {
						continue
					}

					engrave := spline[2].Engrave
					// Line segments have a closed form solution for
					// time minimal traversal.
					if len(spline) == 5 {
						s, e := spline[1].Ctrl, spline[3].Ctrl
						spline = appendLineBSpline(spline[:0], conf, engrave, s, e)
					} else {
						for i := range spline[2 : len(spline)-2] {
							spline[i+2].T = 1
						}
						maxv, maxa, maxj := bspline.ComputeKinematics(spline, 1)
						tscale := timeScale(conf, engrave, maxv, maxa, maxj)
						for i := range spline[2 : len(spline)-2] {
							spline[i+2].T = tscale
						}
					}
					dur := uint(0)
					for _, k := range spline[2:] {
						dur += k.T
					}
					if ts.Done() {
						ts.Reset(dur, dur)
					}
					for _, k := range spline[2:] {
						k.T = ts.Scale(k.T)
						if !yield(k) {
							return
						}
					}
					// Duplicate the last clamping knot twice to maintain
					// clamping start knots.
					spline = append(spline[:0], spline[len(spline)-2:]...)
				}
			} else if d, n, ok := c.AsDelay(); ok {
				if len(spline) > 3 {
					panic("delay during spline")
				}
				ts.Reset(n, d)
			}
		}
	}
}

// timeScaler precisely scales spline segment durations by
// a rational fraction.
type timeScaler struct {
	// nom/denom is the scaling fraction.
	nom, denom uint
	// frac2 accumulates twice the fractional ticks.
	frac2 uint
	// rem is the remaining ticks at nom/denom speed.
	rem uint
}

func (s *timeScaler) Reset(n, d uint) {
	if n < d {
		panic("invalid scale")
	}
	if s.rem > 0 {
		panic("scale already in effect")
	}
	s.rem = n
	s.nom, s.denom = n, d
	// Round to nearest tick by adding denom/2 ticks.
	s.frac2 = d
}

func (s *timeScaler) Scale(t uint) uint {
	var scaled uint
	if s.denom != 0 {
		frac2_64 := uint64(s.frac2) + uint64(2*s.nom)*uint64(t)
		d2 := uint64(2 * s.denom)
		d, r := frac2_64/d2, frac2_64%d2
		scaled = uint(d)
		s.frac2 = uint(r)
	} else {
		// Special case: a 0-length spline.
		scaled = s.rem
	}

	if scaled > s.rem {
		panic("unaligned delay")
	}
	s.rem -= scaled
	return scaled
}

func (s *timeScaler) Done() bool {
	return s.rem == 0
}

func appendLineBSpline(spline []bspline.Knot, conf StepperConfig, engrave bool, s, e bezier.Point) []bspline.Knot {
	tps := conf.TicksPerSecond
	vlim := conf.Speed
	if engrave {
		vlim = conf.EngravingSpeed
	}
	jlim := conf.Jerk
	alim := conf.Acceleration
	dist := uint(ManhattanDist(s, e))
	sc := computeSCurve(dist, vlim, alim, jlim, tps)
	knots := make([]bspline.Knot, len(sc))
	// Starting knot.
	nknots := 0
	for _, p := range sc {
		if p.Duration == 0 {
			continue
		}
		// Interpolate between endpoints.
		s64, e64 := bezier.P64(s), bezier.P64(e)
		ip := e64.Mul(int(p.Position)).Add(s64.Mul(int(dist - p.Position))).Div(int(dist)).Point()
		knots[nknots] = bspline.Knot{
			Ctrl:    ip,
			T:       p.Duration,
			Engrave: engrave,
		}
		nknots++
	}
	knots = knots[:nknots]
	start := bspline.Knot{Ctrl: s, Engrave: engrave}
	end := bspline.Knot{Ctrl: e, Engrave: engrave}
	spline = append(spline, start, start)
	spline = append(spline, knots...)
	if len(knots) == 0 {
		spline = append(spline, start)
	}
	spline = append(spline, end, end)
	return spline
}

// timeScale computes the minimum time in ticks to traverse c given
// limits.
func timeScale(c StepperConfig, engrave bool, v, a, j uint) uint {
	limv := c.Speed
	if engrave {
		limv = c.EngravingSpeed
	}
	lima, limj := c.Acceleration, c.Jerk
	// Compute the scale required by the velocity limit.
	// Velocity is propertional to the scale.
	tv := float32(v) / float32(limv)
	// Acceleration is proportional to the square of the scale.
	ta := float32(math.Sqrt(float64(float32(a) / float32(lima))))
	// Jerk by the cube.
	tj := float32(math.Cbrt(float64(float32(j) / float32(limj))))
	tps := float32(c.TicksPerSecond)
	scale := float32(math.Ceil(float64(max(0, tv, ta, tj) * tps)))
	return uint(scale)
}

// timeConstantPath computes the engraving time in ticks along
// with the start and end points.
func timeConstantPath(s bspline.Curve) constantPlan {
	engraving := false
	var inf constantPlan
	var seg bspline.Segment
	for k := range s {
		c, ticks, engrave := seg.Knot(k)
		switch {
		case !engraving && engrave:
			inf.Start = inf.End
			engraving = true
		case engraving && !engrave:
			panic("broken path")
		}
		if engrave {
			inf.Duration += ticks
		}
		inf.End = c.C3
	}
	return inf
}

func TimePlan(conf StepperConfig, p Engraving) time.Duration {
	ticks := uint(0)
	for k := range PlanEngraving(conf, p) {
		ticks += k.T
	}
	s := (ticks + conf.TicksPerSecond - 1) / conf.TicksPerSecond
	return time.Duration(s) * time.Second
}

func timeMove(conf StepperConfig, dist int) uint {
	sc := computeSCurve(uint(dist), conf.Speed, conf.Acceleration, conf.Jerk, conf.TicksPerSecond)
	t := uint(0)
	for _, s := range sc {
		t += s.Duration
	}
	return t
}

func NewConstantStringer(face *vector.Face, params Params, em int) *ConstantStringer {
	var bounds bspline.Bounds
	var adv int
	var maxDur uint
	m := face.Metrics()
	fh := m.Height
	conf := params.StepperConfig
	runes := make([]constantRune, 0, len(constantAlphabet))
	var lastr rune
	const maxSplineKnots = 100

	knotBuf := make([]bspline.Knot, 0, maxSplineKnots)
	// Compute engraving durations for the alphabet.
	for i, r := range constantAlphabet {
		if r < lastr {
			panic("unsorted alphabet")
		}
		lastr = r
		a, spline, found := face.Decode(r)
		if !found {
			panic(fmt.Errorf("unsupported rune: %s", string(r)))
		}
		if i > 0 && adv != a {
			panic("variable width font")
		}
		adv = a
		inf := timeConstantPath(planEngraving(knotBuf, conf, func(yield func(c Command) bool) {
			engraveSpline(yield, bezier.Point{}, em, fh, spline)
		}))
		bounds.Min.X = min(bounds.Min.X, inf.Start.X, inf.End.X)
		bounds.Min.Y = min(bounds.Min.Y, inf.Start.Y, inf.End.Y)
		bounds.Max.X = max(bounds.Max.X, inf.Start.X, inf.End.X)
		bounds.Max.Y = max(bounds.Max.Y, inf.Start.Y, inf.End.Y)
		runes = append(runes, constantRune{
			R:    r,
			Info: inf,
		})
		maxDur = max(inf.Duration, maxDur)
	}
	startEndDist := ManhattanDist(bounds.Min, bounds.Max)
	center := bounds.Max.Add(bounds.Min).Div(2)

	return &ConstantStringer{
		face:         face,
		runeDuration: maxDur,
		alphabet:     runes,
		em:           em,
		center:       center,
		startEndDist: startEndDist,
		conf:         params.StepperConfig,
		advDist:      adv * em / fh,
	}
}

func (c *ConstantStringer) String(yield func(Command) bool, txt string) bool {
	n := strlen(txt)
	return c.PaddedString(yield, txt, n, n)
}

func (c *ConstantStringer) PaddedString(yield func(Command) bool, txt string, shortest, longest int) bool {
	if n := strlen(txt); n < shortest || longest < n {
		panic("string length out of bounds")
	}
	return c.paddedString(yield, txt, shortest, longest)
}

func (c *ConstantStringer) paddedString(yield func(Command) bool, txt string, shortest, longest int) bool {
	f := c.face
	m := f.Metrics()
	fh := m.Height
	baseline := (m.Ascent*c.em + fh - 1) / fh
	dot := bezier.Pt(0, baseline)
	// Move to the data-independent start position.
	pen := dot.Add(c.center)
	cont := yield(Move(pen))
	// Compute worst case movement durations.
	padDur := timeMove(c.conf, ((longest-shortest)*c.advDist+1)/2+c.startEndDist)
	advDur := timeMove(c.conf, c.advDist+c.startEndDist)
	centerDur := timeMove(c.conf, (c.startEndDist+1)/2)
	totalDur := centerDur
	// accum accumulates the fraction each rune in txt
	// contributes towards engraving the total number of runes
	// (longest). This is to spread out the repeat runes.
	accum := 0
	ridx := 0
	for range longest {
		r, n := utf8.DecodeRuneInString(txt[ridx:])
		idx, found := sort.Find(len(c.alphabet), func(i int) int {
			return int(r - c.alphabet[i].R)
		})
		if !found {
			panic(fmt.Errorf("unsupported rune: %s", string(r)))
		}
		_, spline, found := f.Decode(r)
		if !found {
			// Unreachable by construction, since c.alphabet contains
			// only runes from f.
			panic("unreachable")
		}
		inf := c.alphabet[idx].Info
		// Skip starting move segment.
		if inf.Start != (bezier.Point{}) {
			for range 3 {
				if _, ok := spline.Next(); !ok {
					panic("unclamped spline")
				}
			}
		}
		start := dot.Add(inf.Start)
		cont = cont && DelayMove(yield, c.conf, totalDur, pen, start) &&
			yield(Delay(inf.Duration, c.runeDuration)) &&
			engraveSpline(yield, dot, c.em, fh, spline)
		pen = dot.Add(inf.End)
		totalDur = advDur
		accum += len(txt)
		if accum >= longest {
			accum -= longest
			ridx += n
			dot.X += c.advDist
		}
	}
	// Move to end, the midpoint between shortest and longest.
	mid2 := longest + shortest - 1
	dot = bezier.Pt(mid2*c.advDist/2, baseline)
	end := dot.Add(c.center)
	cont = cont && DelayMove(yield, c.conf, padDur, pen, end)
	return cont
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

func (s *StringCmd) Engrave(yield func(Command) bool) bool {
	_, ok := s.engrave(yield)
	return ok
}

func (s *StringCmd) Measure() (int, int) {
	b, _ := s.engrave(nil)
	return int(b.X), int(b.Y)
}

func (s *StringCmd) engrave(yield func(Command) bool) (bezier.Point, bool) {
	m := s.face.Metrics()
	mh := m.Height
	dot := bezier.Pt(0, (m.Ascent*s.em+mh-1)/mh)
	lheight := s.em * s.LineHeight
	cont := true
	for _, r := range s.txt {
		if r == '\n' {
			dot.X = 0
			dot.Y += lheight
			continue
		}
		adv, spline, found := s.face.Decode(r)
		if !found {
			panic(fmt.Errorf("unsupported rune: %s", string(r)))
		}
		if yield != nil {
			cont = cont && engraveSpline(yield, dot, s.em, mh, spline)
		}
		dot.X += adv * s.em / mh
	}
	return bezier.Point{X: dot.X, Y: lheight}, cont
}

func addScale(p1, p2 bezier.Point, em, height int) bezier.Point {
	return p2.Mul(em).Div(height).Add(p1)
}

func engraveSpline(yield func(Command) bool, pos bezier.Point, em, height int, spline vector.UniformBSpline) bool {
	for {
		k, ok := spline.Next()
		if !ok {
			break
		}
		c := addScale(pos, k.Ctrl, em, height)
		if !yield(ControlPoint(k.Line, c)) {
			return false
		}
	}
	return true
}

// Profile describes the engraving timing as well as the
// start and end points of a [Engraving].
type Profile struct {
	// Pattern is the times, in ticks, where the plan
	// switches from moves to engraves or back.
	Pattern []uint
	// Start and End points of the plan.
	Start, End bezier.Point
}

func ProfileSpline(s bspline.Curve) Profile {
	engraving := false
	firstPoint := true
	var prof Profile
	var t uint
	var seg bspline.Segment
	for k := range s {
		c, ticks, _ := seg.Knot(k)
		prof.End = c.C3
		if !k.Engrave && firstPoint {
			prof.Start = prof.End
			firstPoint = false
		}
		if k.Engrave != engraving {
			engraving = k.Engrave
			prof.Pattern = append(prof.Pattern, t)
		}
		t += ticks
	}
	if t > 0 {
		prof.Pattern = append(prof.Pattern, t)
	}
	return prof
}

func (p Profile) Equal(p2 Profile) bool {
	if p.Start != p2.Start || p.End != p2.End || len(p.Pattern) != len(p2.Pattern) {
		return false
	}
	for i, t := range p.Pattern {
		if p2.Pattern[i] != t {
			return false
		}
	}
	return true
}

func strlen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
