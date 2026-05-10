package op

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
	"seedhammer.com/font/bitmap"
	"seedhammer.com/image/rgb565"
)

type Ops struct {
	maskStack    []frameOp
	inputs       []inputOp
	skipInputOps bool
	frame        frame
	// text is non-nil for ExtractText to collect
	// all runes from a frame.
	text []rune
}

type Ctx struct {
	beginIdx opCursor
	ops      *Ops
}

type Image struct {
	scratch [2]ParameterizedImage
}

type Tag any

func RegisterParameterizedImage(factory func() ParameterizedImage) *Image {
	img := new(Image)
	for i := range img.scratch {
		img.scratch[i] = factory()
	}
	return img
}

type ParameterizedImage func(args []uint32, refs []any) image.Image

type frame struct {
	args []uint32
	refs []any
}

type inputOp struct {
	bounds image.Rectangle
	tag    Tag
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
	opMask
	opClip
	opCall
	opInput
)

type frameOp struct {
	pos image.Point
	op  imageOp
}

// These maximums ensure that no runtime resizing of
// the large argument and reference buffers happen.
const (
	maxArgs = 8192
	maxRefs = 2048
)

func (o *Ctx) add(cmd opType, op ...uint32) {
	if o.ops == nil {
		return
	}
	o.ops.frame.appendArgs(encodeCmdHeader(cmd, len(op), 0))
	o.ops.frame.appendArgs(op...)
}

func (f *frame) appendArgs(args ...uint32) {
	if cap(f.args) < maxArgs {
		f.args = make([]uint32, 0, maxArgs)
	}
	// Runtime resizing exacerbates memmory fragmentation in the
	// primitive TinyGo memory allocator. Don't allow it.
	if cap(f.args)-len(f.args) < len(args) {
		panic("no argument buffer space left")
	}
	f.args = append(f.args, args...)
}

func (f *frame) appendRefs(refs ...any) {
	if cap(f.refs) < maxRefs {
		f.refs = make([]any, 0, maxRefs)
	}
	// Runtime resizing exacerbates memory fragmentation in the
	// primitive TinyGo memory allocator. Don't allow it.
	if cap(f.refs)-len(f.refs) < len(refs) {
		panic("no refs buffer space left")
	}
	f.refs = append(f.refs, refs...)
}

func encodeCmdHeader(cmd opType, nargs, nrefs int) uint32 {
	return (uint32(nargs) << 16) | (uint32(nrefs))<<8 | uint32(cmd)
}

func (o *Ctx) Begin() Ctx {
	if o.ops == nil {
		return Ctx{}
	}
	// The end indices are filled in by Ctx.End.
	o.add(opBegin, 0, 0)
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
	// Fill in opBegin indices.
	o.ops.frame.args[o.beginIdx.op-2] = uint32(len(o.ops.frame.args))
	o.ops.frame.args[o.beginIdx.op-1] = uint32(len(o.ops.frame.refs))
	call := CallOp{start: o.beginIdx}
	o.beginIdx = opCursor{}
	return call
}

func (o *Ops) Context() Ctx {
	return Ctx{ops: o}
}

func (o *Ops) Reset() {
	o.frame.Reset()
	o.inputs = o.inputs[:0]
	o.skipInputOps = false
}

type drawState struct {
	pos  image.Point
	clip image.Rectangle
}

func (f *frame) Reset() {
	f.args = f.args[:0]
	clear(f.refs)
	f.refs = f.refs[:0]
}

func (o *Ops) ExtractText(dst image.Rectangle) string {
	// Instruct Draw to collect runes.
	o.text = []rune{}
	fb := rgb565.New(dst)
	maskfb := image.NewAlpha(dst)
	o.Draw(fb, maskfb)

	txt := string(o.text)
	o.text = nil
	return txt
}

func (o *Ops) TagBounds(t Tag) (image.Rectangle, bool) {
	for _, inp := range o.inputs {
		if t == inp.tag {
			return inp.bounds, true
		}
	}
	return image.Rectangle{}, false
}

func (o *Ops) Hit(p image.Point) (Tag, image.Rectangle, bool) {
	for _, inp := range o.inputs {
		if p.In(inp.bounds) {
			return inp.tag, inp.bounds, true
		}
	}
	return nil, image.Rectangle{}, false
}

func (o *Ops) Draw(dst draw.Image, maskfb draw.Image) {
	o.maskStack = o.maskStack[:0]
	o.draw(dst, maskfb, drawState{clip: image.Rect(-1e9, -1e9, 1e9, 1e9)}, opCursor{})
	// o.inputs has been populated, skip the work for subsequent frames.
	o.skipInputOps = true
}

func (o *Ops) draw(dst draw.Image, maskfb draw.Image, state drawState, cur opCursor) {
	origMaskStackLen := len(o.maskStack)
	orig := state
	ops := o.frame.args
	refs := o.frame.refs
	for len(ops) > cur.op {
		opnargs := ops[cur.op]
		op := opType(opnargs & 0xf)
		nrefs := int((opnargs >> 8) & 0xf)
		nargs := int(opnargs >> 16)
		args := ops[cur.op+1 : cur.op+1+nargs]
		switch op {
		case opBegin:
			cur = opCursor{op: int(args[0]), ref: int(args[1])}
			continue
		case opEnd:
			return
		}
		rargs := refs[cur.ref : cur.ref+nrefs]
		cur.op += 1 + nargs
		cur.ref += nrefs
		switch op {
		case opOffset:
			off := image.Point{X: int(int32(args[0])), Y: int(int32(args[1]))}
			state.pos = state.pos.Add(image.Point(off))
		case opClip:
			r := image.Rectangle{
				Min: image.Point{X: int(int32(args[0])), Y: int(int32(args[1]))},
				Max: image.Point{X: int(int32(args[2])), Y: int(int32(args[3]))},
			}.Add(state.pos)
			state.clip = state.clip.Intersect(r)
		case opCall:
			cur := opCursor{op: int(args[0]), ref: int(args[1])}
			o.draw(dst, maskfb, state, cur)
			state = orig
			o.maskStack = o.maskStack[:origMaskStackLen]
		case opInput:
			if !o.skipInputOps {
				iop := inputOp{
					tag:    rargs[0],
					bounds: state.clip,
				}
				o.inputs = append(o.inputs, iop)
			}
			state = orig
			o.maskStack = o.maskStack[:origMaskStackLen]
		case opImage, opMask:
			iop := imageOp{src: rargs[0], args: args, refs: rargs[1:]}
			r := iop.materialize(0).Bounds().Add(state.pos)
			state.clip = state.clip.Intersect(r)
			fop := frameOp{pos: state.pos, op: iop}
			if op != opImage {
				o.maskStack = append(o.maskStack, fop)
				break
			}
			clip := state.clip.Intersect(dst.Bounds())
			if !clip.Empty() {
				var maskSrc image.Image
				var maskPos image.Point
				maskStack := o.maskStack
				if o.text != nil {
					for _, m := range maskStack {
						switch img := m.op.materialize(0).(type) {
						case *glyph:
							o.text = append(o.text, img.r)
						}
					}
				}
				switch len(maskStack) {
				case 0:
				case 1:
					mask := maskStack[len(maskStack)-1]
					maskSrc = mask.op.materialize(1)
					maskPos = mask.pos
				default:
					// Blend the masks into maskfb.
					maskSrc = maskfb
					// First operation initializes maskfb content.
					op := draw.Src
					for len(maskStack) > 0 {
						if len(maskStack) > 1 {
							// Draw 2 masks at a time.
							m1, m2 := maskStack[0], maskStack[1]
							maskStack = maskStack[2:]
							drawMask(maskfb, clip,
								m2.op.materialize(0), clip.Min.Sub(m2.pos),
								m1.op.materialize(1), clip.Min.Sub(m1.pos), op)
						} else {
							m := maskStack[0]
							maskStack = maskStack[1:]
							drawMask(maskfb, clip,
								m.op.materialize(0), clip.Min.Sub(m.pos),
								nil, image.Point{}, op)
						}
						// Subsequent operations blend into maskfb.
						op = draw.Over
					}
				}
				src := fop.op.materialize(0)
				drawMask(dst, clip, src, clip.Min.Sub(fop.pos), maskSrc, clip.Min.Sub(maskPos), draw.Over)
			}
			state = orig
			o.maskStack = o.maskStack[:origMaskStackLen]
		}
	}
}

func (op imageOp) materialize(slot int) image.Image {
	switch src := op.src.(type) {
	case *Image:
		return src.materialize(slot, op.args, op.refs)
	case image.Image:
		return src
	case nil:
		return nil
	default:
		panic("invalid source")
	}
}

func (img *Image) materialize(slot int, args []uint32, refs []any) image.Image {
	return img.scratch[slot](args, refs)
}

func Offset(ops Ctx, off image.Point) {
	ops.add(opOffset,
		uint32(off.X), uint32(off.Y),
	)
}

func Position(ops Ctx, c CallOp, off image.Point) {
	Offset(ops, off)
	c.Add(ops)
}

type ClipOp image.Rectangle

func (c ClipOp) Add(ops Ctx) {
	ops.add(opClip,
		uint32(c.Min.X), uint32(c.Min.Y),
		uint32(c.Max.X), uint32(c.Max.Y),
	)
}

func ColorOp(ops Ctx, col color.RGBA) {
	nrgba := uint32(col.R)<<24 | uint32(col.G)<<16 | uint32(col.B)<<8 | uint32(col.A)
	addImageOp(ops, opImage, uniformImage, nil, []uint32{nrgba})
}

func AlphaOp(ops Ctx, alpha byte) {
	nrgba := uint32(alpha)
	addImageOp(ops, opMask, uniformImage, nil, []uint32{nrgba})
}

func InputOp(ops Ctx, tag Tag) {
	if ops.ops == nil {
		return
	}
	ops.ops.frame.appendArgs(encodeCmdHeader(opInput, 0, 1))
	ops.ops.frame.appendRefs(tag)
}

func ImageOp(ops Ctx, img image.Image, mask bool) {
	op := opImage
	if mask {
		op = opMask
	}
	addImageOp(ops, op, img, nil, nil)
}

func GlyphOp(ops Ctx, face *bitmap.Face, r rune) {
	_, _, ok := face.Glyph(r)
	if !ok {
		ClipOp{}.Add(ops)
		return
	}
	addImageOp(
		ops,
		opMask,
		glyphImage,
		[]any{face},
		[]uint32{uint32(r)},
	)
}

func RoundedOutline(ops Ctx, bounds image.Rectangle, cornerRadius, lineWidth int) {
	r := cornerRadius * px
	lw := (lineWidth - 1) * px
	Offset(ops, bounds.Min)
	sz := bounds.Size()
	ParamImageOp(ops, roundedOutlineImage, true, nil, []uint32{
		uint32(sz.X),
		uint32(sz.Y),
		uint32(r),
		uint32(lw),
	})
}

func RoundedRect(ops Ctx, bounds image.Rectangle, cornerRadius int) {
	r := cornerRadius * px
	Offset(ops, bounds.Min)
	sz := bounds.Size()
	ParamImageOp(ops, roundedRectImage, true, nil, []uint32{
		uint32(sz.X),
		uint32(sz.Y),
		uint32(r),
	})
}

func ParamImageOp(ops Ctx, img *Image, mask bool, refs []any, args []uint32) {
	op := opImage
	if mask {
		op = opMask
	}
	addImageOp(ops, op, img, refs, args)
}

type imageOp struct {
	src  any
	refs []any
	args []uint32
}

func addImageOp(ops Ctx, op opType, src any, refs []any, args []uint32) {
	if ops.ops == nil {
		return
	}
	nargs := len(args)
	nrefs := len(refs) + 1
	ops.ops.frame.appendArgs(
		encodeCmdHeader(op, nargs, nrefs),
	)
	ops.ops.frame.appendArgs(args...)
	ops.ops.frame.appendRefs(src)
	ops.ops.frame.appendRefs(refs...)
}

type CallOp struct {
	start opCursor
}

func (c CallOp) Add(ops Ctx) {
	if c.start != (opCursor{}) {
		ops.add(opCall, uint32(c.start.op), uint32(c.start.ref))
	}
}
