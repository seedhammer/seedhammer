package op

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"seedhammer.com/ninepatch"
	"seedhammer.com/rgb16"
)

type Ops struct {
	ops      []any
	uniforms map[color.Color]*image.Uniform
	ninep    map[ninepatch.Image]*ninepatch.Image
	colors   map[color.NRGBA]*image.Uniform

	prevOps  map[frameOp]bool
	frameOps map[frameOp]bool
	frame    []frameOp
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

func (o *Ctx) addDrawOp(op drawOp) {
	o.add(op)
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

func (o *Ops) Reset() Ctx {
	o.ops = o.ops[:0]
	if o.uniforms == nil {
		o.uniforms = make(map[color.Color]*image.Uniform)
	}
	if o.ninep == nil {
		o.ninep = make(map[ninepatch.Image]*ninepatch.Image)
	}
	if o.colors == nil {
		o.colors = make(map[color.NRGBA]*image.Uniform)
	}
	if o.frameOps == nil {
		o.frameOps = make(map[frameOp]bool)
	}
	if o.prevOps == nil {
		o.prevOps = make(map[frameOp]bool)
	}
	return Ctx{ops: o}
}

func (o *Ops) nrgba(c color.NRGBA) *image.Uniform {
	if o == nil {
		return image.NewUniform(c)
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
		if img, ok := o.uniforms[img.C]; ok {
			return img
		}
		o.uniforms[img.C] = img
	case *ninepatch.Image:
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

func (o *Ops) Draw(dst draw.Image) image.Rectangle {
	o.frameOps, o.prevOps = o.prevOps, o.frameOps
	// Clear for GC.
	for i := range o.frameOps {
		delete(o.frameOps, i)
	}
	for i := range o.frame {
		o.frame[i] = frameOp{}
	}
	o.frame = o.frame[:0]
	o.serialize(drawState{clip: dst.Bounds()}, 0)
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
	for _, op := range o.frame {
		clip := clip.Intersect(op.state.clip)
		if clip.Empty() {
			continue
		}
		pos := clip.Min.Sub(op.state.pos)
		maskp := clip.Min.Sub(op.state.maskp)
		op.op.draw(dst, clip, op.state.mask, maskp, pos)
	}
	return clip
}

func (o *Ops) serialize(state drawState, from int) {
	macros := 0
	origState := state
	for i := from; i < len(o.ops); i++ {
		op := o.ops[i]
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
			r := op.bounds().Add(state.pos)
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
	ops.addDrawOp(imageOp{ops.ops.intern(img)})
}

type imageOp struct {
	src image.Image
}

func (im imageOp) bounds() image.Rectangle {
	return im.src.Bounds()
}

func (im imageOp) draw(dst draw.Image, dr image.Rectangle, mask image.Image, maskp, pos image.Point) {
	drawMask(dst, dr, im.src, pos, mask, maskp)
}

func drawMask(dst draw.Image, dr image.Rectangle, src image.Image, pos image.Point, mask image.Image, maskOff image.Point) {
	// Optimize special cases.
	if rgb, ok := dst.(*rgb16.Image); ok {
		if mask == nil {
			rgb.DrawOver(dr, src, pos)
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

func ScaledImageOp(ops Ctx, dst image.Rectangle, img image.Image) {
	ops.addDrawOp(scaledImageOp{dst, ops.ops.intern(img)})
}

type scaledImageOp struct {
	r   image.Rectangle
	src image.Image
}

func (im scaledImageOp) bounds() image.Rectangle {
	return im.r
}

func (im scaledImageOp) draw(dst draw.Image, dr image.Rectangle, mask image.Image, maskp, pos image.Point) {
	if mask != nil {
		panic("not supported")
	}
	draw.NearestNeighbor.Scale(
		dst, dr.Intersect(im.r.Add(pos)),
		im.src, im.src.Bounds().Sub(pos),
		draw.Over, nil,
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
	bounds() image.Rectangle
	draw(dst draw.Image, dr image.Rectangle, mask image.Image, maskp, pos image.Point)
}

type TextOp struct {
	Src           image.Image
	Face          font.Face
	Dot           fixed.Point26_6
	Txt           string
	LetterSpacing int
}

func (t TextOp) bounds() image.Rectangle {
	b := t.drawBounds(nil, image.Rectangle{}, nil, image.Point{}, image.Point{})
	return b.Intersect(t.Src.Bounds())
}

func (t TextOp) draw(dst draw.Image, dr image.Rectangle, mask image.Image, maskp, pos image.Point) {
	t.drawBounds(dst, dr, mask, maskp, pos)
}

func (t TextOp) drawBounds(dst draw.Image, dr image.Rectangle, mask image.Image, maskp, pos image.Point) image.Rectangle {
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
	dot := t.Dot
	var bounds image.Rectangle
	for _, c := range t.Txt {
		if prevC >= 0 {
			dot.X += t.Face.Kern(prevC, c)
		}
		gdr, mask, maskp, advance, ok := t.Face.Glyph(dot, c)
		if !ok {
			continue
		}
		advance += fixed.I(t.LetterSpacing)
		bounds = bounds.Union(gdr)
		if dst != nil {
			drawMask(dst, dr, src, tpos, mask, pos.Add(maskp).Sub(gdr.Min))
		}
		dot.X += advance
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
	ops.addDrawOp(t2)
}
