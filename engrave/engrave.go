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

func QR(strokeWidth int, scale int, level qrcode.RecoveryLevel, content []byte) Command {
	qr, err := qrcode.New(string(content), level)
	if err != nil {
		panic(err)
	}
	qr.DisableBorder = true
	return qrCmd{
		strokeWidth: strokeWidth,
		scale:       scale,
		qr:          qr.Bitmap(),
	}
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

type Rect image.Rectangle

func (r Rect) Engrave(p Program) {
	p.Move(r.Min)
	p.Line(image.Pt(r.Max.X, r.Min.Y))
	p.Line(r.Max)
	p.Line(image.Pt(r.Min.X, r.Max.Y))
	p.Line(r.Min)
}

func String(face *font.Face, mmPrEm int, msg string) *StringCmd {
	return &StringCmd{
		LineHeight: 1,
		face:       face,
		mmPrEm:     mmPrEm,
		msg:        msg,
	}
}

type StringCmd struct {
	LineHeight float32

	face   *font.Face
	mmPrEm int
	msg    string
}

func (s *StringCmd) Engrave(p Program) {
	ppem := float32(s.mmPrEm)
	pos := f32.Vec2{0, s.face.Metrics.Ascent * ppem}
	for _, r := range s.msg {
		if r == '\n' {
			pos[1] += s.face.Metrics.Height * ppem * s.LineHeight
			pos[0] = 0
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
				p1 := affine.Add(pos, affine.Scale(seg.Args[0], ppem))
				p.Move(roundCoord(p1))
				p0 = p1
			case font.SegmentOpLineTo:
				p1 := affine.Add(pos, affine.Scale(seg.Args[0], ppem))
				p.Line(roundCoord(p1))
				p0 = p1
			case font.SegmentOpQuadTo:
				p12 := affine.Add(pos, affine.Scale(seg.Args[0], ppem))
				p3 := affine.Add(pos, affine.Scale(seg.Args[1], ppem))
				// Expand to cubic.
				p1 := mix(p12, p0, 1.0/3.0)
				p2 := mix(p12, p3, 1.0/3.0)
				approxCubeBezier(p.Line, p0, p1, p2, p3)
				p0 = p3
			case font.SegmentOpCubeTo:
				p1 := affine.Add(pos, affine.Scale(seg.Args[0], ppem))
				p2 := affine.Add(pos, affine.Scale(seg.Args[1], ppem))
				p3 := affine.Add(pos, affine.Scale(seg.Args[2], ppem))
				approxCubeBezier(p.Line, p0, p1, p2, p3)
				p0 = p3
			default:
				panic(errors.New("unsupported segment"))
			}
		}
		pos[0] += adv * ppem
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
