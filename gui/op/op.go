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
	prevFrame frame
	fiter     frameIter
	fiterPrev frameIter
	fiterScan frameIter

	scratch struct {
		image     scratchImage
		intersect scratchImage
	}
}

type scratchImage struct {
	img genImage
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
	id int
	// gen is the ImageGenerator function as an interface.
	gen any
}

type Tag any

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
				if op.op.gen.id != glyphImage.id {
					continue
				}
				_, r := decodeGlyphImage(op.op.ImageArguments)
				b.WriteRune(r)
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

func (o *Ops) Clip(dst image.Rectangle) image.Rectangle {
	o.fiter.Reset(dst.Bounds())
	fiterPrev, fiterScan := &o.fiterPrev, &o.fiterScan
	fiterPrev.Reset(dst.Bounds())
	prevOp, _ := o.fiterPrev.Scan(o.prevFrame, opImage)
	clip := image.Rectangle{}
loop:
	for {
		fop, ok := o.fiter.Next(o.frame)
		if !ok {
			break
		}
		switch fop.Op {
		case opInput:
			o.inputs = append(o.inputs, fop.Input)
		case opImage:
			// Scan previous frame for matching operation.
			// Limit scan distance to dodge O(n²).
			const scanMax = 10
			scanned := 0
			// prevClip collects unmatched clip rectangles.
			prevClip := image.Rectangle{}
			fiterPrev.Clone(fiterScan)
			scanOp := prevOp
			for scanned < scanMax {
				if opsEqual(fop, scanOp) {
					// Match found; add interim unmatched clip areas and
					// advance the previous frame.
					clip = clip.Union(prevClip)
					fiterPrev, fiterScan = fiterScan, fiterPrev
					prevOp, _ = fiterPrev.Scan(o.prevFrame, opImage)
					continue loop
				}
				// Count the ops matched by opsEqual.
				scanned += len(scanOp.ImageStack)
				prevClip = prevClip.Union(scanOp.Clip)
				scanOp, ok = fiterScan.Scan(o.prevFrame, opImage)
				if !ok {
					break
				}
			}
			// No match found.
			clip = clip.Union(fop.Clip)
			if clip == dst {
				return clip
			}
		}
	}
	clip = clip.Union(prevOp.Clip)
	// Add remaining ops from the previous frame.
	for {
		fop, ok := fiterPrev.Next(o.prevFrame)
		if !ok {
			break
		}
		clip = clip.Union(fop.Clip)
	}
	return clip
}

func opsEqual(op1, op2 frameIterElem) bool {
	if len(op1.ImageStack) != len(op2.ImageStack) || op1.Clip != op2.Clip {
		return false
	}
	for i, op1 := range op1.ImageStack {
		if !opEqual(op1, op2.ImageStack[i]) {
			return false
		}
	}
	return true
}

func opEqual(op1, op2 frameOp) bool {
	if op1.pos != op2.pos {
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
				scratch := &o.scratch.intersect
				src := scratch.materialize(m.op)
				drawMask(maskfb, mclip, src, maskp, mfb, mfbPos, draw.Src)
			}
		}
		drawMask(dst, clip, o.materialize(src), clip.Min.Sub(pos), maskSrc, mfbPos, draw.Over)
		return
	}
	mask := masks[0]
	o.maskStack = append(o.maskStack, mask)
	o.drawMasks(dst, clip, src, pos, maskfb, masks[1:])
	o.maskStack = o.maskStack[:len(o.maskStack)-1]
}

func (o *Ops) materialize(op imageOp) image.Image {
	switch op.mask {
	case imageMask:
		return o.scratch.image.materialize(op)
	default:
		return o.scratch.intersect.materialize(op)
	}
}

func (s *scratchImage) materialize(op imageOp) image.Image {
	if op.src != nil {
		return op.src
	}
	s.img.imageOp = op
	return &s.img
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

func (it *frameIter) Clone(dst *frameIter) {
	dst.stack = append(dst.stack[:0], it.stack...)
	dst.maskStack = append(dst.maskStack[:0], it.maskStack...)
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

func (it *frameIter) Scan(f frame, t opType) (frameIterElem, bool) {
	for {
		fop, ok := it.Next(f)
		if !ok || fop.Op == t {
			return fop, ok
		}
	}
}

func (it *frameIter) Next(f frame) (frameIterElem, bool) {
outer:
	for {
		macros := 0
		istate := &it.stack[len(it.stack)-1]
		ops := f.args[istate.cur.op:]
		refs := f.refs[istate.cur.ref:]
		for len(ops) > 0 {
			opnargs := ops[0]
			op := opType(opnargs & 0xf)
			nrefs := (opnargs >> 8) & 0xf
			nargs := opnargs >> 16
			args := ops[1 : 1+nargs]
			istate.cur.op += int(1 + nargs)
			istate.cur.ref += int(nrefs)
			ops = ops[1+nargs:]
			switch op {
			case opBegin:
				macros++
				continue
			case opEnd:
				if macros == 0 {
					it.stack = it.stack[:len(it.stack)-1]
					istate = &it.stack[len(it.stack)-1]
					ops = f.args[istate.cur.op:]
					refs = f.refs[istate.cur.ref:]
					it.resetState()
				} else {
					macros--
				}
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
				iop := imageOp{
					mask: maskType(args[0]),
					ImageArguments: ImageArguments{
						Bounds: decodeRect(args[2:6]),
						Args:   args[6:],
						Refs:   rargs[2:],
					},
				}
				iop.gen.id = int(int32(args[1]))
				if src := rargs[0]; src != nil {
					iop.src = src.(image.Image)
				}
				if gen := rargs[1]; gen != nil {
					iop.gen.gen = gen.(ImageGenerator)
				}
				r := iop.Bounds.Add(istate.state.pos)
				istate.state.clip = istate.state.clip.Intersect(r)
				fop := frameOp{pos: istate.state.pos, op: iop}
				it.maskStack = append(it.maskStack, fop)
				if iop.mask != imageMask {
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
	col := colorFromArgs(args)
	r, g, b, a := uint16(col.R), uint16(col.G), uint16(col.B), uint16(col.A)
	return color.RGBA64{R: r | r<<8, G: g | g<<8, B: b | b<<8, A: a | a<<8}
})

var glyphImage = RegisterParameterizedImage(func(args ImageArguments, x, y int) color.RGBA64 {
	face, r := decodeGlyphImage(args)
	glyph, _, _ := face.Glyph(r)
	return glyph.RGBA64At(x, y)
})

func colorFromArgs(args ImageArguments) color.RGBA {
	nrgba := args.Args[0]
	r := nrgba >> 24
	g := (nrgba >> 16) & 0xff
	b := (nrgba >> 8) & 0xff
	a := nrgba & 0xff
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)}
}

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

	gen struct {
		id  int
		gen ImageGenerator
	}
	ImageArguments
}

func addImageOp(ops Ctx, src image.Image, img Image, mask maskType, bounds image.Rectangle, refs []any, args []uint32) {
	if ops.ops == nil {
		return
	}
	nargs := len(args) + 1 + 1 + 4
	nrefs := len(refs) + 1 + 1
	b := bounds
	ops.ops.frame.appendArgs(
		encodeCmdHeader(opImage, nargs, nrefs),
		uint32(mask),
		uint32(img.id),
		uint32(int32(b.Min.X)), uint32(int32(b.Min.Y)),
		uint32(int32(b.Max.X)), uint32(int32(b.Max.Y)),
	)
	ops.ops.frame.appendArgs(args...)
	ops.ops.frame.appendRefs(src, img.gen)
	ops.ops.frame.appendRefs(refs...)
}

type CallOp struct {
	start opCursor
}

func (c CallOp) Add(ops Ctx) {
	if c.start != (opCursor{}) {
		ops.add(opCall, uint32(int32(c.start.op)), uint32(int32(c.start.ref)))
	}
}
