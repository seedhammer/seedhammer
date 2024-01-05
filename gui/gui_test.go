package gui

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"io"
	"iter"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/bip32"
	"seedhammer.com/bip380"
	"seedhammer.com/bip39"
	"seedhammer.com/bspline"
	"seedhammer.com/engrave"
	"seedhammer.com/gui/op"
	"seedhammer.com/image/rgb565"
)

func BenchmarkRedraw(b *testing.B) {
	ops := new(op.Ops)
	ctx := NewContext(newPlatform())
	ctx.FrameCallback = func() {
		ctx.Done = true
	}
	m := new(MainScreen)
	m.Flow(ctx, ops.Context())
	clip := image.Rectangle{Max: ctx.Platform.DisplaySize()}
	ops.Clip(clip)
	fb := rgb565.New(clip)
	maskfb := image.NewAlpha(clip)
	for b.Loop() {
		ops.Draw(fb, maskfb)
	}
}

func BenchmarkAllocs(b *testing.B) {
	desc := &bip380.Descriptor{
		Script:    bip380.P2WSH,
		Type:      bip380.SortedMulti,
		Threshold: 2,
		Keys:      make([]bip380.Key, 5),
	}
	fillDescriptor(b, desc, desc.Script.DerivationPath(), 12, 0)
	ds := &DescriptorScreen{
		Descriptor: desc,
	}
	m := new(MainScreen)
	screens := []func(*Context, op.Ctx){
		m.Flow,
		func(ctx *Context, ops op.Ctx) {
			ds.Confirm(ctx, ops, &descriptorTheme)
		},
	}
	var frames []func()
	for _, s := range screens {
		it := func(yield func(struct{}) bool) {
			ops := new(op.Ops)
			ctx := NewContext(newPlatform())
			ctx.FrameCallback = func() {
				ctx.Done = !yield(struct{}{})
				ctx.Reset()
				ops.Reset()
			}
			s(ctx, ops.Context())
		}
		next, quit := iter.Pull(it)
		defer quit()
		frames = append(frames, func() { next() })
	}
	for b.Loop() {
		for _, f := range frames {
			f()
		}
	}
}

func TestAllocs(t *testing.T) {
	res := testing.Benchmark(BenchmarkAllocs)
	if a := res.AllocsPerOp(); a > 0 {
		t.Errorf("got %d allocs, expected %d", a, 0)
	}
}

func dumpUI(t testing.TB, ops *op.Ops, path string) {
	t.Helper()
	clip := image.Rectangle{Max: image.Pt(testDisplayDim, testDisplayDim)}
	fb := image.NewNRGBA(clip)
	maskfb := image.NewAlpha(clip)
	ops.Draw(fb, maskfb)
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, fb); err != nil {
		t.Error(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Error(err)
	}
}

func newTestEngraveScreen(t *testing.T, ctx *Context) *EngraveScreen {
	desc := &bip380.Descriptor{
		Script:    bip380.P2WSH,
		Threshold: 2,
		Type:      bip380.SortedMulti,
		Keys: []bip380.Key{
			{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0x5a0804e3,
				DerivationPath:    bip32.Path{0x80000030, 0x80000000, 0x80000000, 0x80000002},
				KeyData:           []byte{0x3, 0xa9, 0x39, 0x4a, 0x2f, 0x1a, 0x4f, 0x99, 0x61, 0x3a, 0x71, 0x69, 0x56, 0xc8, 0x54, 0xf, 0x6d, 0xba, 0x6f, 0x18, 0x93, 0x1c, 0x26, 0x39, 0x10, 0x72, 0x21, 0xb2, 0x67, 0xd7, 0x40, 0xaf, 0x23},
				ChainCode:         []byte{0xdb, 0xe8, 0xc, 0xbb, 0x4e, 0xe, 0x41, 0x8b, 0x6, 0xf4, 0x70, 0xd2, 0xaf, 0xe7, 0xa8, 0xc1, 0x7b, 0xe7, 0x1, 0xab, 0x20, 0x6c, 0x59, 0xa6, 0x5e, 0x65, 0xa8, 0x24, 0x1, 0x6a, 0x6c, 0x70},
				ParentFingerprint: 0xc7bce7a8,
			},
			{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0xdd4fadee,
				DerivationPath:    bip32.Path{0x80000030, 0x80000000, 0x80000000, 0x80000002},
				KeyData:           []byte{0x2, 0x21, 0x96, 0xad, 0xc2, 0x5f, 0xde, 0x16, 0x9f, 0xe9, 0x2e, 0x70, 0x76, 0x90, 0x59, 0x10, 0x22, 0x75, 0xd2, 0xb4, 0xc, 0xc9, 0x87, 0x76, 0xea, 0xab, 0x92, 0xb8, 0x2a, 0x86, 0x13, 0x5e, 0x92},
				ChainCode:         []byte{0x43, 0x8e, 0xff, 0x7b, 0x3b, 0x36, 0xb6, 0xd1, 0x1a, 0x60, 0xa2, 0x2c, 0xcb, 0x93, 0x6, 0xee, 0xa3, 0x5, 0xb0, 0x43, 0x9f, 0x1e, 0xa0, 0x9d, 0x59, 0x28, 0x1, 0x5d, 0xe3, 0x73, 0x81, 0x16},
				ParentFingerprint: 0x22969377,
			},
			{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0x9bacd5c0,
				DerivationPath:    bip32.Path{0x80000030, 0x80000000, 0x80000000, 0x80000002},
				KeyData:           []byte{0x2, 0xfb, 0x72, 0x50, 0x7f, 0xc2, 0xd, 0xdb, 0xa9, 0x29, 0x91, 0xb1, 0x7c, 0x4b, 0xb4, 0x66, 0x13, 0xa, 0xd9, 0x3a, 0x88, 0x6e, 0x73, 0x17, 0x50, 0x33, 0xbb, 0x43, 0xe3, 0xbc, 0x78, 0x5a, 0x6d},
				ChainCode:         []byte{0x95, 0xb3, 0x49, 0x13, 0x93, 0x7f, 0xa5, 0xf1, 0xc6, 0x20, 0x5b, 0x52, 0x5b, 0xb5, 0x7d, 0xe1, 0x51, 0x76, 0x25, 0xe0, 0x45, 0x86, 0xb5, 0x95, 0xbe, 0x68, 0xe7, 0x13, 0x62, 0xd3, 0xed, 0xc5},
				ParentFingerprint: 0x97ec38f9,
			},
		},
	}

	_, engravings, err := validateDescriptor(ctx.Platform.EngraverParams(), desc)
	if err != nil {
		t.Fatal(err)
	}
	return NewEngraveScreen(
		ctx,
		engravings[0],
	)
}

func TestEngraveScreenCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newPlatform()
		ctx := NewContext(p)
		ops := new(op.Ops)
		frame, quit := runUI(ctx, ops, func() {
			scr := newTestEngraveScreen(t, ctx)
			if ok := scr.Engrave(ctx, ops.Context(), &engraveTheme); ok {
				t.Error("EngraveScreen: succeeded unexpectedly")
			}
		})
		defer quit()

		// Start engraving.
		click(&ctx.Router, Button3, Button3, Button3)
		// Hold confirm.
		press(&ctx.Router, Button3)
		if _, ok := frame(); !ok {
			t.Fatal("EngraveScreen: exited unexpectedly")
		}
		time.Sleep(confirmDelay)
		if _, ok := frame(); !ok {
			t.Fatal("EngraveScreen: exited unexpectedly")
		}

		// Back and press confirm.
		click(&ctx.Router, Button1)
		press(&ctx.Router, Button3)
		if _, ok := frame(); !ok {
			t.Fatal("EngraveScreen: cancelled without confirmation")
		}
		// Hold confirm.
		time.Sleep(confirmDelay)
		if _, ok := frame(); ok {
			t.Fatal("engrave screen did not cancel")
		}
		select {
		case <-p.engrave.quit:
		default:
			t.Fatal("EngraveScreen: did not close quit channel")
		}
	})
}

func TestEngraveScreenError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newPlatform()
		ctx := NewContext(p)
		ops := new(op.Ops)
		scr := newTestEngraveScreen(t, ctx)
		frame, quit := runUI(ctx, ops, func() {
			scr.Engrave(ctx, ops.Context(), &engraveTheme)
		})
		defer quit()

		// Fail during engraving.
		ioErr := errors.New("error during engraving")
		p.engrave.ioErr = ioErr
		// Press next until connect is reached.
		click(&ctx.Router, Button3, Button3, Button3)
		// Hold connect.
		press(&ctx.Router, Button3)
		frame()
		time.Sleep(confirmDelay)
		frame()
		<-p.engrave.done
		content, ok := frame()
		if !ok || !uiContains(content, ioErr.Error()) {
			t.Fatalf("EngraveScreen: no error reported, expected %v", ioErr)
		}
		// Dismiss error and verify screen exits.
		click(&ctx.Router, Button3)
		content, ok = frame()
		if ok && uiContains(content, "error") {
			t.Fatal("EngraveScreen: didn't dismiss error")
		}
	})
}

func TestEngraveScreen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newPlatform()
		ctx := NewContext(p)
		ops := new(op.Ops)
		scr := newTestEngraveScreen(t, ctx)
		success := false
		frame, quit := runUI(ctx, ops, func() {
			success = scr.Engrave(ctx, ops.Context(), &engraveTheme)
		})
		defer quit()

		// Press next until connect is reached.
		click(&ctx.Router, Button3, Button3, Button3)
		// Hold connect.
		press(&ctx.Router, Button3)
		frame()
		time.Sleep(confirmDelay)
	loop:
		for {
			frame()
			select {
			case <-p.engrave.done:
				break loop
			case <-p.wakeups:
			}
		}
		click(&ctx.Router, Button3)
		synctest.Wait()
		if _, ok := frame(); ok || !success {
			t.Fatal("EngraveScreen: didn't complete successfully")
		}
	})
}

func TestWordKeyboardScreen(t *testing.T) {
	ctx := NewContext(newPlatform())
	for i := bip39.Word(0); i < bip39.NumWords; i++ {
		w := bip39.LabelFor(i)
		runes(&ctx.Router, strings.ToUpper(w))
		click(&ctx.Router, Button2)
		m := make(bip39.Mnemonic, 1)
		inputWordsFlow(ctx, op.Ctx{}, &descriptorTheme, m, 0)
		if got := bip39.LabelFor(m[0]); got != w {
			t.Errorf("keyboard mapped %q to %q", w, got)
		}
	}
}

func fillDescriptor(t testing.TB, desc *bip380.Descriptor, path bip32.Path, seedlen int, keyIdx int) bip39.Mnemonic {
	var mnemonic bip39.Mnemonic
	for i := range desc.Keys {
		m := make(bip39.Mnemonic, seedlen)
		for j := range m {
			m[j] = bip39.Word(i*seedlen + j)
		}
		m = m.FixChecksum()
		seed := bip39.MnemonicSeed(m, "")
		network := &chaincfg.MainNetParams
		mk, err := hdkeychain.NewMaster(seed, network)
		if err != nil {
			t.Fatal(err)
		}
		pkey, err := mk.ECPubKey()
		if err != nil {
			t.Fatal(err)
		}
		mfp := bip32.Fingerprint(pkey)
		xpub, err := bip32.Derive(mk, path)
		if err != nil {
			t.Fatal(err)
		}
		pub, err := xpub.ECPubKey()
		if err != nil {
			t.Fatal(err)
		}
		desc.Keys[i] = bip380.Key{
			Network:           network,
			MasterFingerprint: mfp,
			DerivationPath:    path,
			KeyData:           pub.SerializeCompressed(),
			ChainCode:         xpub.ChainCode(),
			ParentFingerprint: xpub.ParentFingerprint(),
		}
		if i == keyIdx {
			mnemonic = m
		}
	}
	return mnemonic
}

type testPlatform struct {
	events  []Event
	wakeups chan struct{}
	engrave struct {
		ioErr error
		quit  <-chan struct{}
		done  chan struct{}
		jobs  chan bspline.Curve
	}
}

const (
	mm             = 6400
	strokeWidth    = 0.3 * mm
	topSpeed       = 30 * mm
	engravingSpeed = 8 * mm
	acceleration   = 250 * mm
	jerk           = 2600 * mm

	testDisplayDim = 240
)

var (
	engraverConf = engrave.StepperConfig{
		TicksPerSecond: topSpeed,
		Speed:          topSpeed,
		EngravingSpeed: engravingSpeed,
		Acceleration:   acceleration,
		Jerk:           jerk,
	}
	engraverParams = engrave.Params{
		StrokeWidth:   strokeWidth,
		Millimeter:    mm,
		StepperConfig: engraverConf,
	}
)

func (*testPlatform) DisplaySize() image.Point {
	return image.Pt(testDisplayDim, testDisplayDim)
}

func (*testPlatform) Dirty(r image.Rectangle) error {
	return nil
}

func (*testPlatform) NextChunk() (draw.RGBA64Image, bool) {
	return nil, false
}

func (p *testPlatform) Wakeup() {
	select {
	case <-p.wakeups:
	default:
	}
	p.wakeups <- struct{}{}
}

func (p *testPlatform) AppendEvents(deadline time.Time, evts []Event) []Event {
	evts = append(evts, p.events...)
	p.events = nil
	return evts
}

func (p *testPlatform) Features() Features {
	return 0
}

func (p *testPlatform) LockBoot() error {
	panic("not implemented")
}

func (p *testPlatform) EngraverParams() engrave.Params {
	return engraverParams
}

func (p *testPlatform) NFCReader() io.Reader {
	return nil
}

func (p *testPlatform) Engrave(stall bool, spline bspline.Curve, status chan<- EngraverStatus, quit <-chan struct{}) error {
	defer close(p.engrave.done)
	p.engrave.quit = quit
	select {
	case p.engrave.jobs <- spline:
	default:
	}
	if err := p.engrave.ioErr; err != nil {
		return err
	}
	for range spline {
	}
	return nil
}

func newPlatform() *testPlatform {
	t := &testPlatform{
		wakeups: make(chan struct{}, 1),
	}
	t.engrave.done = make(chan struct{})
	t.engrave.jobs = make(chan bspline.Curve, 1)
	return t
}

func mnemonicFor(phrase string) bip39.Mnemonic {
	m, err := bip39.ParseMnemonic(phrase)
	if err != nil {
		panic(err)
	}
	return m
}

func runUI(ctx *Context, ops *op.Ops, ui func()) (frame func() (string, bool), close func()) {
	return iter.Pull(func(yield func(content string) bool) {
		ctx.FrameCallback = func() {
			r := image.Rectangle{Max: ctx.Platform.DisplaySize()}
			content := ops.ExtractText(r)
			ctx.Reset()
			ops.Reset()
			ctx.Done = ctx.Done || !yield(content)
		}
		ui()
	})
}

func uiContains(content, str string) bool {
	str = strings.ToLower(str)
	txt := strings.ToLower(content)
	clean := strings.ReplaceAll(strings.ToLower(str), " ", "")
	return strings.Index(txt, clean) != -1
}
