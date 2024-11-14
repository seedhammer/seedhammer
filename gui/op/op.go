package op

import (
	"image"
	"image/color"
	"strings"

	"golang.org/x/image/draw"
	"seedhammer.com/font/bitmap"
	"seedhammer.com/image/rgb565"
)

type Ops struct {
	maskStack []frameOp
	frame     frame
	prevFrame frame

	scratchMask genImage
	scratchImg  genImage
}

type Ctx struct {
	beginIdx opCursor
	ops      *Ops
}

type ImageGenerator func(args ImageArguments, x, y int) color.RGBA64

type ImageArguments struct {
	Refs   []any
	Args   []uint32
	Bounds image.Rectangle
}

type Image struct {
	id  int
	gen ImageGenerator
}

var globalID = 0

func RegisterParameterizedImage(gen ImageGenerator) Image {
	globalID++
	return Image{
		id:  globalID,
		gen: gen,
	}
}

type genImage struct {
	imageOp
}

type frame struct {
	ops     []frameOp
	drawOps []drawOp
	args    []uint32
	refs    []any
}

type opCursor struct {
	op  int
	ref int
}

type opType int

const (
	opBegin opType = iota
	opEnd
	opOffset
	opImage
	opClip
	opCall
)

type frameOp struct {
	pos  image.Point
	op   imageOp
	clip image.Rectangle
}

type drawOp struct {
	start, end int
}

func (o *Ctx) add(cmd opType, op ...uint32) {
	if o.ops == nil {
		return
	}
	o.ops.frame.args = append(o.ops.frame.args, (uint32(len(op))<<16)|uint32(cmd))
	o.ops.frame.args = append(o.ops.frame.args, op...)
}

func (o *Ctx) Begin() Ctx {
	if o.ops == nil {
		return Ctx{}
	}
	o.add(opBegin)
	o.beginIdx = opCursor{
		op:  len(o.ops.frame.args),
		ref: len(o.ops.frame.refs),
	}
	return Ctx{ops: o.ops}
}

func (o *Ctx) End() CallOp {
	if o.ops == nil {
		return CallOp{}
	}
	if o.beginIdx == (opCursor{}) {
		panic("End without a Begin")
	}
	o.add(opEnd)
	call := CallOp{start: o.beginIdx}
	o.beginIdx = opCursor{}
	return call
}

func (o *Ops) Context() Ctx {
	return Ctx{ops: o}
}

func (o *Ops) Reset() {
	o.frame, o.prevFrame = o.prevFrame, o.frame
	o.frame.Reset()
}

type drawState struct {
	pos  image.Point
	clip image.Rectangle
}

func (f *frame) Reset() {
	f.args = f.args[:0]
	clear(f.refs)
	f.refs = f.refs[:0]
	clear(f.ops)
	f.ops = f.ops[:0]
	f.drawOps = f.drawOps[:0]
}

func (o *Ops) ExtractText(dst image.Rectangle) string {
	o.serialize(drawState{clip: dst}, opCursor{})
	var b strings.Builder
	for _, fop := range o.frame.drawOps {
		for _, op := range o.frame.ops[fop.start:fop.end] {
			if op.op.gen.id != glyphImage.id {
				continue
			}
			_, r := decodeGlyphImage(op.op.ImageArguments)
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (o *Ops) Clip(dst image.Rectangle) image.Rectangle {
	o.serialize(drawState{clip: dst}, opCursor{})
	clip := image.Rectangle{}
	prevDrawOps := o.prevFrame.drawOps
loop:
	for _, op := range o.frame.drawOps {
		// Scan previous frame for matching operation.
		// Limit scan distance to dodge O(n²).
		const scanMax = 5
		firstOp := o.frame.ops[op.start]
		scanned := 0
		nops := op.end - op.start
		// prevClip collects unmatched clip rectangles.
		prevClip := image.Rectangle{}
		for i, prevOp := range prevDrawOps {
			prevFirstOp := o.prevFrame.ops[prevOp.start]
			prevNOps := prevOp.end - prevOp.start
			if nops == prevNOps && opEqual(firstOp, prevFirstOp) {
				// Match the remaining ops.
				ops := o.frame.ops[op.start+1 : op.end]
				prevOps := o.prevFrame.ops[prevOp.start+1 : prevOp.end]
				if opsEqual(ops, prevOps) {
					// Match found; add interim unmatched clip areas and
					// advance the previous frame.
					clip = clip.Union(prevClip)
					prevDrawOps = prevDrawOps[i+1:]
					continue loop
				}
				// Count the ops matched by opsEqual.
				scanned += len(ops)
			}
			prevClip = prevClip.Union(o.prevFrame.ops[prevOp.end-1].clip)
			scanned++
			if scanned >= scanMax {
				break
			}
		}
		// No match found.
		lastOp := o.frame.ops[op.end-1]
		oclip := lastOp.clip
		clip = clip.Union(oclip)
		if clip == dst {
			return clip
		}
	}
	// Add remaining ops from the previous frame.
	for _, prevOp := range prevDrawOps {
		oclip := o.prevFrame.ops[prevOp.end-1].clip
		clip = clip.Union(oclip)
	}
	return clip
}

func opsEqual(ops1, ops2 []frameOp) bool {
	if len(ops1) != len(ops2) {
		return false
	}
	for i, op1 := range ops1 {
		if !opEqual(op1, ops2[i]) {
			return false
		}
	}
	return true
}

func opEqual(op1, op2 frameOp) bool {
	if op1.pos != op2.pos {
		return false
	}
	if op1.clip != op2.clip {
		return false
	}
	iop1, iop2 := op1.op, op2.op
	if len(iop1.Args) != len(iop2.Args) {
		return false
	}
	if len(iop1.Refs) != len(iop2.Refs) {
		return false
	}
	for i, a := range iop1.Args {
		if a != iop2.Args[i] {
			return false
		}
	}
	for i, r := range iop1.Refs {
		if r != iop2.Refs[i] {
			return false
		}
	}
	if iop1.src != iop2.src {
		return false
	}
	if iop1.gen.id != iop2.gen.id {
		return false
	}
	return true
}

func (o *Ops) Draw(dst draw.Image, maskfb draw.Image) {
	b := dst.Bounds()
	for _, dop := range o.frame.drawOps {
		masks := o.frame.ops[dop.start : dop.end-1]
		op := o.frame.ops[dop.end-1]
		clip := b.Intersect(op.clip)
		if clip.Empty() {
			continue
		}
		o.maskStack = o.maskStack[:0]
		o.drawMasks(dst, clip, op.op, op.pos, maskfb, masks)
	}
}

func (o *Ops) drawMasks(dst draw.Image, clip image.Rectangle, src imageOp, pos image.Point, maskfb draw.Image, masks []frameOp) {
	if len(masks) == 0 {
		var maskSrc image.Image = maskfb
		mfbPos := maskfb.Bounds().Min
		switch len(o.maskStack) {
		case 0:
			maskSrc = nil
		case 1:
			m := o.maskStack[0]
			maskSrc = o.materialize(m.op)
			mfbPos = clip.Min.Sub(m.pos)
		default:
			mclip := image.Rectangle{Max: clip.Size()}
			for i, m := range o.maskStack {
				maskp := clip.Min.Sub(m.pos)
				mfb := maskfb
				if i == 0 {
					mfb = nil
				}
				src := m.op.src
				if src == nil {
					o.scratchMask.imageOp = m.op
					src = &o.scratchMask
				}
				drawMask(maskfb, mclip, src, maskp, mfb, mfbPos, draw.Src)
			}
		}
		drawMask(dst, clip, o.materialize(src), clip.Min.Sub(pos), maskSrc, mfbPos, draw.Over)
		return
	}
	mask := masks[0]
	o.maskStack = append(o.maskStack, mask)
	o.drawMasks(dst, clip.Intersect(mask.clip), src, pos, maskfb, masks[1:])
	o.maskStack = o.maskStack[:len(o.maskStack)-1]
}

func (o *Ops) materialize(op imageOp) image.Image {
	if op.src != nil {
		return op.src
	}
	switch op.mask {
	case imageMask:
		o.scratchImg.imageOp = op
		return &o.scratchImg
	default:
		o.scratchMask.imageOp = op
		return &o.scratchMask
	}
}

func (o *Ops) serialize(state drawState, from opCursor) {
	macros := 0
	depth := len(o.maskStack)
	origState := state
	ops := o.frame.args[from.op:]
	refs := o.frame.refs[from.ref:]
	for len(ops) > 0 {
		opnargs := ops[0]
		op := opType(opnargs & 0xf)
		nrefs := (opnargs >> 8) & 0xf
		nargs := opnargs >> 16
		args := ops[1 : 1+nargs]
		ops = ops[1+nargs:]
		switch op {
		case opBegin:
			macros++
			continue
		case opEnd:
			if macros == 0 {
				return
			}
			macros--
			continue
		}
		rargs := refs[:nrefs]
		refs = refs[nrefs:]
		if macros > 0 {
			continue
		}
		switch op {
		case opOffset:
			off := image.Point{X: int(int32(args[0])), Y: int(int32(args[1]))}
			state.pos = state.pos.Add(image.Point(off))
			continue
		case opClip:
			r := decodeRect(args)
			state.clip = state.clip.Intersect(r.Add(state.pos))
			continue
		case opCall:
			start := opCursor{
				op:  int(int32(args[0])),
				ref: int(int32(args[1])),
			}
			o.serialize(state, start)
		case opImage:
			op := imageOp{
				mask: maskType(args[0]),
				gen: Image{
					id: int(int32(args[1])),
				},
				ImageArguments: ImageArguments{
					Bounds: decodeRect(args[2:6]),
					Args:   args[6:],
					Refs:   rargs[2:],
				},
			}
			if src := rargs[0]; src != nil {
				op.src = src.(image.Image)
			}
			if gen := rargs[1]; gen != nil {
				op.gen.gen = gen.(ImageGenerator)
			}
			r := op.Bounds.Add(state.pos)
			clip := state.clip.Intersect(r)
			state.clip = clip
			fop := frameOp{pos: state.pos, op: op, clip: clip}
			if op.mask != imageMask {
				o.maskStack = append(o.maskStack, fop)
				continue
			}
			if state.clip.Empty() {
				break
			}
			start := len(o.frame.ops)
			o.frame.ops = append(o.frame.ops, o.maskStack...)
			o.frame.ops = append(o.frame.ops, fop)
			o.frame.drawOps = append(o.frame.drawOps, drawOp{
				start: start,
				end:   len(o.frame.ops),
			})
		}
		o.maskStack = o.maskStack[:depth]
		state = origState
	}
}

func decodeRect(args []uint32) image.Rectangle {
	return image.Rectangle{
		Min: image.Point{X: int(int32(args[0])), Y: int(int32(args[1]))},
		Max: image.Point{X: int(int32(args[2])), Y: int(int32(args[3]))},
	}
}

func Offset(ops Ctx, off image.Point) {
	ops.add(opOffset,
		uint32(int32(off.X)), uint32(int32(off.Y)),
	)
}

func Position(ops Ctx, c CallOp, off image.Point) {
	Offset(ops, off)
	c.Add(ops)
}

type ClipOp image.Rectangle

func (c ClipOp) Add(ops Ctx) {
	ops.add(opClip,
		uint32(int32(c.Min.X)), uint32(int32(c.Min.Y)),
		uint32(int32(c.Max.X)), uint32(int32(c.Max.Y)),
	)
}

var uniformImage = RegisterParameterizedImage(func(args ImageArguments, x, y int) color.RGBA64 {
	nrgba := args.Args[0]
	r := nrgba >> 24
	r |= r << 8
	g := (nrgba >> 16) & 0xff
	g |= g << 8
	b := (nrgba >> 8) & 0xff
	b |= b << 8
	a := nrgba & 0xff
	a |= a << 8
	return color.RGBA64{R: uint16(r), G: uint16(g), B: uint16(b), A: uint16(a)}
})

var glyphImage = RegisterParameterizedImage(func(args ImageArguments, x, y int) color.RGBA64 {
	face, r := decodeGlyphImage(args)
	glyph, _, _ := face.Glyph(r)
	return glyph.RGBA64At(x, y)
})

func decodeGlyphImage(args ImageArguments) (*bitmap.Face, rune) {
	return args.Refs[0].(*bitmap.Face), rune(args.Args[0])
}

func ColorOp(ops Ctx, col color.NRGBA) {
	a := uint32(col.A)
	r := uint32(col.R)
	r *= a
	r /= 0xff
	g := uint32(col.G)
	g *= a
	g /= 0xff
	b := uint32(col.B)
	b *= a
	b /= 0xff
	a |= a << 8
	nrgba := (r&0xff)<<24 | (g&0xff)<<16 | (b&0xff)<<8 | (a & 0xff)
	addImageOp(ops, nil, uniformImage, imageMask, image.Rect(-1e9, -1e9, 1e9, 1e9), nil, []uint32{nrgba})
}

func ImageOp(ops Ctx, img image.Image, mask bool) {
	m := imageMask
	if mask {
		m = intersectMask
	}
	addImageOp(ops, img, Image{}, m, img.Bounds(), nil, nil)
}

func GlyphOp(ops Ctx, face *bitmap.Face, r rune) {
	m, _, ok := face.Glyph(r)
	if !ok {
		ClipOp{}.Add(ops)
		return
	}
	addImageOp(
		ops, nil,
		glyphImage,
		intersectMask,
		m.Bounds(),
		[]any{face},
		[]uint32{uint32(r)},
	)
}

func ParamImageOp(ops Ctx, img Image, mask bool, bounds image.Rectangle, refs []any, args []uint32) {
	m := imageMask
	if mask {
		m = intersectMask
	}
	addImageOp(ops, nil, img, m, bounds, refs, args)
}

func (img *genImage) ColorModel() color.Model {
	return color.RGBA64Model
}

func (img *genImage) Bounds() image.Rectangle {
	return img.ImageArguments.Bounds
}

func (img *genImage) At(x, y int) color.Color {
	return img.RGBA64At(x, y)
}

func (img *genImage) RGBA64At(x, y int) color.RGBA64 {
	return img.gen.gen(img.ImageArguments, x, y)
}

type maskType int

const (
	imageMask maskType = iota
	intersectMask
)

type imageOp struct {
	mask maskType

	src image.Image

	gen Image
	ImageArguments
}

func addImageOp(ops Ctx, src image.Image, img Image, mask maskType, bounds image.Rectangle, refs []any, args []uint32) {
	if ops.ops == nil {
		return
	}
	nargs := len(args) + 1 + 1 + 4
	nrefs := len(refs) + 1 + 1
	cmdArgs := (uint32(nargs) << 16) | (uint32(nrefs))<<8 | uint32(opImage)
	b := bounds
	ops.ops.frame.args = append(ops.ops.frame.args,
		cmdArgs,
		uint32(mask),
		uint32(img.id),
		uint32(int32(b.Min.X)), uint32(int32(b.Min.Y)),
		uint32(int32(b.Max.X)), uint32(int32(b.Max.Y)),
	)
	ops.ops.frame.args = append(ops.ops.frame.args, args...)
	ops.ops.frame.refs = append(ops.ops.frame.refs, src, img.gen)
	ops.ops.frame.refs = append(ops.ops.frame.refs, refs...)
}

func drawMask(dst draw.Image, dr image.Rectangle, src image.Image, pos image.Point, mask image.Image, maskOff image.Point, op draw.Op) {
	// Optimize special cases.
	if rgb, ok := dst.(*rgb565.Image); ok {
		if mask == nil {
			rgb.Draw(dr, src, pos, op)
			return
		}
	}

	// General case.
	draw.DrawMask(
		dst, dr,
		src, pos,
		mask, maskOff,
		op,
	)
}

type CallOp struct {
	start opCursor
}

func (c CallOp) Add(ops Ctx) {
	if c.start != (opCursor{}) {
		ops.add(opCall, uint32(int32(c.start.op)), uint32(int32(c.start.ref)))
	}
}

type beginOp struct{}

type endOp struct{}
