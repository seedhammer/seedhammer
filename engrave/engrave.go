// package engrave transforms shapes such as text and QR codes into
// line and move commands for use with an engraver.
package engrave

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"

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
// Units are in millimeters.
type Program interface {
	Move(p f32.Vec2)
	Line(p f32.Vec2)
}

type transformedProgram struct {
	prog  Program
	trans f32.Aff3
}

func (t *transformedProgram) Move(p f32.Vec2) {
	t.prog.Move(affine.Transform(t.trans, p))

}

func (t *transformedProgram) Line(p f32.Vec2) {
	t.prog.Line(affine.Transform(t.trans, p))
}

func TransformedProgram(prog Program, transform f32.Aff3) Program {
	return &transformedProgram{
		prog:  prog,
		trans: transform,
	}
}

type transformCmd struct {
	t   f32.Aff3
	cmd Command
}

func Scale(x, y float32, cmd Command) Command {
	return transformCmd{
		t:   affine.Scaling(f32.Vec2{x, y}),
		cmd: cmd,
	}
}

func Offset(x, y float32, cmd Command) Command {
	return transformCmd{
		t:   affine.Offsetting(f32.Vec2{x, y}),
		cmd: cmd,
	}
}

func Rotate(radians float32, cmd Command) Command {
	return transformCmd{
		t:   affine.Rotating(radians),
		cmd: cmd,
	}
}

func (t transformCmd) Engrave(p Program) {
	p = TransformedProgram(p, t.t)
	t.cmd.Engrave(p)
}

func QR(strokeWidth float32, scale int, level qrcode.RecoveryLevel, content []byte) Command {
	return qrCmd{
		strokeWidth: strokeWidth,
		scale:       scale,
		content:     content,
		level:       level,
	}
}

type qrCmd struct {
	strokeWidth float32
	scale       int
	content     []byte
	level       qrcode.RecoveryLevel
}

func (q qrCmd) Engrave(p Program) {
	qr, err := qrcode.New(string(q.content), q.level)
	if err != nil {
		panic(err)
	}
	qr.DisableBorder = true
	bitmap := qr.Bitmap()
	for y := 0; y < len(bitmap); y++ {
		row := bitmap[y]
		for i := 0; i < q.scale; i++ {
			draw := false
			var firstx int
			line := y*q.scale + i
			// Swap direction every other line.
			rev := line%2 != 0
			radius := float32(.5)
			if rev {
				radius = -radius
			}
			drawLine := func(endx int) {
				start := affine.Scale(f32.Vec2{float32(firstx*q.scale) + radius, float32(line)}, q.strokeWidth)
				end := affine.Scale(f32.Vec2{float32(endx*q.scale) - radius, float32(line)}, q.strokeWidth)
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

func String(face *font.Face, mmPrEm float32, msg string) *StringCmd {
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
	mmPrEm float32
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
				p.Move(p1)
				p0 = p1
			case font.SegmentOpLineTo:
				p1 := affine.Add(pos, affine.Scale(seg.Args[0], ppem))
				p.Line(p1)
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
func approxCubeBezier(move func(to f32.Vec2), p0, p1, p2, p3 f32.Vec2) {
	if isFlat(p0, p1, p2, p3) {
		move(p3)
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
}

func (r *Rasterizer) Line(p f32.Vec2) {
	p[0] -= float32(r.img.Bounds().Min.X)
	p[1] -= float32(r.img.Bounds().Min.Y)
	if !r.started {
		r.dasher.Start(rasterx.ToFixedP(float64(r.p[0]), float64(r.p[1])))
		r.started = true
	}
	r.dasher.Line(rasterx.ToFixedP(float64(p[0]), float64(p[1])))
}

func (r *Rasterizer) Move(p f32.Vec2) {
	p[0] -= float32(r.img.Bounds().Min.X)
	p[1] -= float32(r.img.Bounds().Min.Y)
	if r.started {
		r.dasher.Stop(false)
		r.started = false
	}
	r.p = p
}

func NewRasterizer(img draw.Image, dr image.Rectangle, strokeWidth float32) *Rasterizer {
	width, height := dr.Dx(), dr.Dy()
	scanner := rasterx.NewScannerGV(width, height, img, img.Bounds())
	r := &Rasterizer{
		dasher: rasterx.NewDasher(width, height, scanner),
		img:    img,
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
	const tolerance = 1e-3
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
