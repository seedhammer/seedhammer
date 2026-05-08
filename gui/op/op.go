package op

import (
	"image"
	"image/color"
	"strings"

	"golang.org/x/image/draw"
	"seedhammer.com/font/bitmap"
)

type Ops struct {
	maskStack []frameOp
	inputs    []inputOp
	frame     frame
	fiter     frameIter
}

type Ctx struct {
	beginIdx opCursor
	ops      *Ops
}

type ImageArguments struct {
	Refs []any
	Args []uint32
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

type ParameterizedImage func(ImageArguments) image.Image

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
	var b strings.Builder
	o.fiter.Reset(dst.Bounds())
	for {
		fop, ok := o.fiter.Next(o.frame)
		if !ok {
			break
		}
		switch fop.Op {
		case opImage:
			for _, op := range fop.ImageStack {
				switch img := op.op.materialize(0).(type) {
				case *glyph:
					b.WriteRune(img.r)
				}
			}
		}
	}
	return b.String()
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

func (o *Ops) Clip(dst image.Rectangle) {
	o.fiter.Reset(dst.Bounds())
	for {
		fop, ok := o.fiter.Next(o.frame)
		if !ok {
			break
		}
		switch fop.Op {
		case opInput:
			o.inputs = append(o.inputs, fop.Input)
		}
	}
}

func (o *Ops) Draw(dst draw.Image, maskfb draw.Image) {
	o.fiter.Reset(dst.Bounds())
	for {
		fop, ok := o.fiter.Next(o.frame)
		if !ok {
			break
		}
		switch fop.Op {
		case opImage:
			masks := fop.ImageStack[:len(fop.ImageStack)-1]
			op := fop.ImageStack[len(fop.ImageStack)-1]
			o.maskStack = o.maskStack[:0]
			o.drawMasks(dst, fop.Clip, op.op, op.pos, maskfb, masks)
		}
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
			maskSrc = m.op.materialize(1)
			mfbPos = clip.Min.Sub(m.pos)
		default:
			mclip := image.Rectangle{Max: clip.Size()}
			for i, m := range o.maskStack {
				maskp := clip.Min.Sub(m.pos)
				mfb := maskfb
				if i == 0 {
					mfb = nil
				}
				src := m.op.materialize(0)
				drawMask(maskfb, mclip, src, maskp, mfb, mfbPos, draw.Src)
			}
		}
		drawMask(dst, clip, src.materialize(0), clip.Min.Sub(pos), maskSrc, mfbPos, draw.Over)
		return
	}
	mask := masks[0]
	o.maskStack = append(o.maskStack, mask)
	o.drawMasks(dst, clip, src, pos, maskfb, masks[1:])
	o.maskStack = o.maskStack[:len(o.maskStack)-1]
}

func (op imageOp) materialize(slot int) image.Image {
	switch src := op.src.(type) {
	case *Image:
		return src.materialize(slot, op.ImageArguments)
	case image.Image:
		return src
	case nil:
		return nil
	default:
		panic("invalid source")
	}
}

func (img *Image) materialize(slot int, args ImageArguments) image.Image {
	return img.scratch[slot](args)
}

type frameIter struct {
	stack     []iterState
	maskStack []frameOp
}

type frameIterElem struct {
	Op   opType
	Clip image.Rectangle
	// For opInput.
	Input inputOp
	// For opImage.
	ImageStack []frameOp
}

type iterState struct {
	state drawState
	cur   opCursor
}

func (it *frameIter) Reset(dst image.Rectangle) {
	it.stack = it.stack[:0]
	it.maskStack = it.maskStack[:0]
	root := drawState{clip: dst}
	// Root state.
	it.push(root, opCursor{})
	// Current state.
	it.push(root, opCursor{})
}

func (it *frameIter) push(state drawState, cur opCursor) {
	it.stack = append(it.stack, iterState{
		state: state,
		cur:   cur,
	})
}

func (it *frameIter) resetState() {
	istate := &it.stack[len(it.stack)-1]
	istate.state = it.stack[len(it.stack)-2].state
	it.maskStack = it.maskStack[:0]
}

func (it *frameIter) Next(f frame) (frameIterElem, bool) {
outer:
	for {
		istate := &it.stack[len(it.stack)-1]
		ops := f.args[istate.cur.op:]
		refs := f.refs[istate.cur.ref:]
		for len(ops) > 0 {
			opnargs := ops[0]
			op := opType(opnargs & 0xf)
			nrefs := (opnargs >> 8) & 0xf
			nargs := opnargs >> 16
			args := ops[1 : 1+nargs]
			switch op {
			case opBegin:
				// Skip interleaved macro.
				istate.cur = opCursor{
					op:  int(int32(args[0])),
					ref: int(int32(args[1])),
				}
				continue outer
			case opEnd:
				it.stack = it.stack[:len(it.stack)-1]
				it.resetState()
				continue outer
			}
			istate.cur.op += int(1 + nargs)
			istate.cur.ref += int(nrefs)
			ops = ops[1+nargs:]
			rargs := refs[:nrefs]
			refs = refs[nrefs:]
			switch op {
			case opOffset:
				off := image.Point{X: int(int32(args[0])), Y: int(int32(args[1]))}
				istate.state.pos = istate.state.pos.Add(image.Point(off))
			case opClip:
				r := decodeRect(args)
				istate.state.clip = istate.state.clip.Intersect(r.Add(istate.state.pos))
			case opCall:
				state := istate.state
				it.push(state, opCursor{
					op:  int(int32(args[0])),
					ref: int(int32(args[1])),
				})
				continue outer
			case opInput:
				fop := frameIterElem{
					Op:   op,
					Clip: istate.state.clip,
					Input: inputOp{
						tag:    rargs[0],
						bounds: istate.state.clip,
					},
				}
				it.resetState()
				return fop, true
			case opImage:
				mask := maskType(args[0])
				iop := imageOp{
					src: rargs[0],
					ImageArguments: ImageArguments{
						Args: args[1:],
						Refs: rargs[1:],
					},
				}
				r := iop.materialize(0).Bounds().Add(istate.state.pos)
				istate.state.clip = istate.state.clip.Intersect(r)
				fop := frameOp{pos: istate.state.pos, op: iop}
				it.maskStack = append(it.maskStack, fop)
				if mask != imageMask {
					break
				}
				elem := frameIterElem{
					Op:         op,
					Clip:       istate.state.clip,
					ImageStack: it.maskStack,
				}
				it.resetState()
				if elem.Clip.Empty() {
					break
				}
				return elem, true
			}
		}
		return frameIterElem{}, false
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
	addImageOp(ops, uniformImage, imageMask, nil, []uint32{nrgba})
}

func InputOp(ops Ctx, tag Tag) {
	if ops.ops == nil {
		return
	}
	ops.ops.frame.appendArgs(encodeCmdHeader(opInput, 0, 1))
	ops.ops.frame.appendRefs(tag)
}

func ImageOp(ops Ctx, img image.Image, mask bool) {
	m := imageMask
	if mask {
		m = intersectMask
	}
	addImageOp(ops, img, m, nil, nil)
}

func GlyphOp(ops Ctx, face *bitmap.Face, r rune) {
	_, _, ok := face.Glyph(r)
	if !ok {
		ClipOp{}.Add(ops)
		return
	}
	addImageOp(
		ops,
		glyphImage,
		intersectMask,
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
	m := imageMask
	if mask {
		m = intersectMask
	}
	addImageOp(ops, img, m, refs, args)
}

type maskType int

const (
	imageMask maskType = iota
	intersectMask
)

type imageOp struct {
	src any
	ImageArguments
}

func addImageOp(ops Ctx, src any, mask maskType, refs []any, args []uint32) {
	if ops.ops == nil {
		return
	}
	nargs := len(args) + 1
	nrefs := len(refs) + 1
	ops.ops.frame.appendArgs(
		encodeCmdHeader(opImage, nargs, nrefs),
		uint32(mask),
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
