package op

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
	"golang.org/x/image/math/fixed"
	"seedhammer.com/font/bitmap"
	"seedhammer.com/image/alpha4"
	"seedhammer.com/image/ninepatch"
	"seedhammer.com/image/rgb565"
)

type Ops struct {
	ops      []any
	uniforms map[color.Color]*image.Uniform
	ninep    map[ninepatch.Image]*ninepatch.Image
	colors   map[color.NRGBA]*image.Uniform

	prevOps  map[frameOp]bool
	frameOps map[frameOp]bool
	frame    []frameOp

	scratch scratch
}

type Ctx struct {
	beginIdx int
	ops      *Ops
}

type frameOp struct {
	state drawState
	op    drawOp
}

func (o *Ctx) add(op any) {
	if o.ops == nil {
		return
	}
	o.ops.ops = append(o.ops.ops, op)
}

func (o *Ctx) Begin() Ctx {
	if o.ops == nil {
		return Ctx{}
	}
	o.add(beginOp{})
	o.beginIdx = len(o.ops.ops)
	return Ctx{ops: o.ops}
}

func (o *Ctx) End() CallOp {
	if o.ops == nil {
		return CallOp{}
	}
	if o.beginIdx == 0 {
		panic("End without a Begin")
	}
	o.add(endOp{})
	call := CallOp{startIdx: o.beginIdx}
	o.beginIdx = 0
	return call
}

func (o *Ops) Context() Ctx {
	return Ctx{ops: o}
}

func (o *Ops) Reset() {
	o.ops = o.ops[:0]
	if o.frameOps == nil {
		o.frameOps = make(map[frameOp]bool)
	}
	if o.prevOps == nil {
		o.prevOps = make(map[frameOp]bool)
	}
	o.frameOps, o.prevOps = o.prevOps, o.frameOps
	// Clear for GC.
	for i := range o.frameOps {
		delete(o.frameOps, i)
	}
	for i := range o.frame {
		o.frame[i] = frameOp{}
	}
	o.frame = o.frame[:0]
}

func (o *Ops) nrgba(c color.NRGBA) *image.Uniform {
	if o == nil {
		return image.NewUniform(c)
	}
	if o.colors == nil {
		o.colors = make(map[color.NRGBA]*image.Uniform)
	}
	if u, ok := o.colors[c]; ok {
		return u
	}
	u := image.NewUniform(c)
	o.colors[c] = u
	return u
}

func (o *Ops) intern(img image.Image) image.Image {
	if o == nil {
		return img
	}
	switch img := img.(type) {
	case *image.Uniform:
		if o.uniforms == nil {
			o.uniforms = make(map[color.Color]*image.Uniform)
		}
		if img, ok := o.uniforms[img.C]; ok {
			return img
		}
		o.uniforms[img.C] = img
	case *ninepatch.Image:
		if o.ninep == nil {
			o.ninep = make(map[ninepatch.Image]*ninepatch.Image)
		}
		if img, ok := o.ninep[*img]; ok {
			return img
		}
		o.ninep[*img] = img
	}
	return img
}

type drawState struct {
	pos   image.Point
	clip  image.Rectangle
	mask  image.Image
	maskp image.Point
}

type scratch struct {
	glyph alpha4.Image
}

func (o *Ops) ExtractText(dst image.Rectangle) []string {
	o.serialize(drawState{clip: dst}, 0)
	var text []string
	for _, op := range o.frame {
		if op, ok := op.op.(TextOp); ok {
			text = append(text, op.Txt)
		}
	}
	return text
}

func (o *Ops) Clip(dst image.Rectangle) image.Rectangle {
	o.serialize(drawState{clip: dst}, 0)
	var clip image.Rectangle
	for _, op := range o.frame {
		o.frameOps[op] = true
		if !o.prevOps[op] {
			clip = clip.Union(op.state.clip)
		} else {
			delete(o.prevOps, op)
		}
	}
	for op := range o.prevOps {
		clip = clip.Union(op.state.clip)
	}
	return clip
}

func (o *Ops) Draw(dst draw.Image) {
	b := dst.Bounds()
	for _, op := range o.frame {
		clip := b.Intersect(op.state.clip)
		if clip.Empty() {
			continue
		}
		pos := clip.Min.Sub(op.state.pos)
		maskp := clip.Min.Sub(op.state.maskp)
		op.op.draw(&o.scratch, dst, clip, op.state.mask, maskp, pos)
	}
}

func (o *Ops) serialize(state drawState, from int) {
	macros := 0
	origState := state
	for _, op := range o.ops[from:] {
		switch op.(type) {
		case beginOp:
			macros++
			continue
		case endOp:
			if macros == 0 {
				return
			}
			macros--
			continue
		}
		if macros > 0 {
			continue
		}
		switch op := op.(type) {
		case offsetOp:
			state.pos = state.pos.Add(image.Point(op))
			continue
		case ClipOp:
			r := image.Rectangle(op).Add(state.pos)
			state.clip = state.clip.Intersect(r)
			continue
		case maskOp:
			r := op.src.Bounds().Add(state.pos)
			state.clip = state.clip.Intersect(r)
			state.mask = op.src
			state.maskp = state.pos
			continue
		case CallOp:
			o.serialize(state, op.startIdx)
		case drawOp:
			r := op.bounds(&o.scratch).Add(state.pos)
			state.clip = state.clip.Intersect(r)
			if !state.clip.Empty() {
				o.frame = append(o.frame, frameOp{state, op})
			}
		}
		state = origState
	}
}

type offsetOp image.Point

func (o offsetOp) Add(ops Ctx) {
	ops.add(o)
}

func Offset(ops Ctx, off image.Point) {
	offsetOp(off).Add(ops)
}

func Position(ops Ctx, c CallOp, off image.Point) {
	Offset(ops, off)
	c.Add(ops)
}

type ClipOp image.Rectangle

func (c ClipOp) Add(ops Ctx) {
	ops.add(c)
}

func ColorOp(ops Ctx, col color.NRGBA) {
	ops.add(imageOp{ops.ops.nrgba(col)})
}

func MaskOp(ops Ctx, img image.Image) {
	ops.add(maskOp{ops.ops.intern(img)})
}

type maskOp struct {
	src image.Image
}

func ImageOp(ops Ctx, img image.Image) {
	ops.add(imageOp{ops.ops.intern(img)})
}

type imageOp struct {
	src image.Image
}

func (im imageOp) bounds(scr *scratch) image.Rectangle {
	return im.src.Bounds()
}

func (im imageOp) draw(_ *scratch, dst draw.Image, dr image.Rectangle, mask image.Image, maskp, pos image.Point) {
	drawMask(dst, dr, im.src, pos, mask, maskp)
}

func drawMask(dst draw.Image, dr image.Rectangle, src image.Image, pos image.Point, mask image.Image, maskOff image.Point) {
	// Optimize special cases.
	if rgb, ok := dst.(*rgb565.Image); ok {
		if mask == nil {
			rgb.Draw(dr, src, pos, draw.Over)
			return
		}
	}

	// General case.
	draw.DrawMask(
		dst, dr,
		src, pos,
		mask, maskOff,
		draw.Over,
	)
}

type CallOp struct {
	startIdx int
}

func (c CallOp) Add(ops Ctx) {
	if c.startIdx > 0 {
		ops.add(c)
	}
}

type beginOp struct{}

type endOp struct{}

type drawOp interface {
	bounds(scratch *scratch) image.Rectangle
	draw(scratch *scratch, dst draw.Image, dr image.Rectangle, mask image.Image, maskp, pos image.Point)
}

type TextOp struct {
	Src           image.Image
	Face          *bitmap.Face
	Dot           image.Point
	Txt           string
	LetterSpacing int
}

func (t TextOp) bounds(scr *scratch) image.Rectangle {
	b := t.drawBounds(scr, nil, image.Rectangle{}, nil, image.Point{}, image.Point{})
	return b.Intersect(t.Src.Bounds())
}

func (t TextOp) draw(scr *scratch, dst draw.Image, dr image.Rectangle, mask image.Image, maskp, pos image.Point) {
	t.drawBounds(scr, dst, dr, mask, maskp, pos)
}

func (t TextOp) drawBounds(scr *scratch, dst draw.Image, dr image.Rectangle, mask image.Image, maskp, pos image.Point) image.Rectangle {
	var orig draw.Image
	src := t.Src
	tpos := pos
	if dst != nil && mask != nil {
		orig = dst
		src = mask
		dst = image.NewAlpha(dr)
		tpos = maskp
	}
	prevC := rune(-1)
	dot := fixed.I(t.Dot.X)
	var bounds image.Rectangle
	for _, c := range t.Txt {
		if prevC >= 0 {
			dot += t.Face.Kern(prevC, c)
		}
		mask, advance, ok := t.Face.Glyph(c)
		if !ok {
			continue
		}
		off := image.Pt(dot.Round(), t.Dot.Y)
		gdr := mask.Bounds().Add(off)
		advance += fixed.I(t.LetterSpacing)
		bounds = bounds.Union(gdr)
		if dst != nil {
			scr.glyph = mask
			drawMask(dst, dr, src, tpos, &scr.glyph, pos.Sub(off))
		}
		dot += advance
		prevC = c
	}
	if orig != nil {
		drawMask(orig, dr, t.Src, pos, dst, dr.Min)
	}
	return bounds
}

func (t TextOp) Add(ops Ctx) {
	t2 := t
	t2.Src = ops.ops.intern(t2.Src)
	ops.add(t2)
}
