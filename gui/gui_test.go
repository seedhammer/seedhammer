package gui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/kortschak/qr"
	"seedhammer.com/backup"
	"seedhammer.com/bc/urtypes"
	"seedhammer.com/bip32"
	"seedhammer.com/bip39"
	"seedhammer.com/driver/mjolnir"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
	"seedhammer.com/gui/op"
	"seedhammer.com/nonstandard"
	"seedhammer.com/seedqr"
)

func TestDescriptorScreenError(t *testing.T) {
	dupDesc := urtypes.OutputDescriptor{
		Script:    urtypes.P2WSH,
		Type:      urtypes.SortedMulti,
		Threshold: 2,
		Keys:      make([]urtypes.KeyDescriptor, 2),
	}
	dupMnemonic := fillDescriptor(t, dupDesc, dupDesc.Script.DerivationPath(), 12, 0)
	dupDesc.Keys[1] = dupDesc.Keys[0]
	smallDesc := urtypes.OutputDescriptor{
		Script:    urtypes.P2WSH,
		Type:      urtypes.SortedMulti,
		Threshold: 2,
		Keys:      make([]urtypes.KeyDescriptor, 5),
	}
	smallMnemonic := fillDescriptor(t, smallDesc, smallDesc.Script.DerivationPath(), 12, 0)
	okDesc := urtypes.OutputDescriptor{
		Script:    urtypes.P2WSH,
		Type:      urtypes.SortedMulti,
		Threshold: 3,
		Keys:      make([]urtypes.KeyDescriptor, 5),
	}
	okMnemonic := fillDescriptor(t, okDesc, okDesc.Script.DerivationPath(), 12, 0)
	tests := []struct {
		name     string
		desc     urtypes.OutputDescriptor
		mnemonic bip39.Mnemonic
		ok       bool
	}{
		{"duplicate key", dupDesc, dupMnemonic, false},
		{"small threshold", smallDesc, smallMnemonic, false},
		{"ok descriptor", okDesc, okMnemonic, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scr := &DescriptorScreen{
				Descriptor: test.desc,
				Mnemonic:   test.mnemonic,
			}
			ctx := NewContext(newPlatform())
			// Ok descriptor, ok error message, back.
			ctxButton(ctx, Button3, Button3, Button1)
			quit := runUI(ctx, func() {
				if _, ok := scr.Confirm(ctx, op.Ctx{}, &descriptorTheme); ok != test.ok {
					t.Fatalf("DescriptorScreen.Confirm returned %v, expected %v", ok, test.ok)
				}
			})
			defer quit()
		})
	}
}

func TestValidateDescriptor(t *testing.T) {
	// Duplicate key.
	dup := urtypes.OutputDescriptor{
		Script:    urtypes.P2WSH,
		Threshold: 1,
		Type:      urtypes.SortedMulti,
		Keys:      make([]urtypes.KeyDescriptor, 2),
	}
	fillDescriptor(t, dup, dup.Script.DerivationPath(), 12, 0)
	dup.Keys[1] = dup.Keys[0]

	// Threshold too small.
	smallDesc := urtypes.OutputDescriptor{
		Script:    urtypes.P2WSH,
		Threshold: 2,
		Type:      urtypes.SortedMulti,
		Keys:      make([]urtypes.KeyDescriptor, 5),
	}
	fillDescriptor(t, smallDesc, smallDesc.Script.DerivationPath(), 12, 0)

	tests := []struct {
		name string
		desc urtypes.OutputDescriptor
		err  error
	}{
		{"duplicate key", dup, new(errDuplicateKey)},
		{"threshold too small", smallDesc, backup.ErrDescriptorTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDescriptor(mjolnir.Params, test.desc)
			if err == nil {
				t.Fatal("validateDescriptor accepted an unsupported descriptor")
			}
			if !errors.Is(err, test.err) {
				t.Fatalf("validateDescriptor returned %v, expected %v", err, test.err)
			}
		})
	}
}

func runUI(ctx *Context, f func()) func() {
	return runUILimit(ctx, 1000, f)
}

func runUILimit(ctx *Context, limit int, f func()) func() {
	token := new(int)
	frameCh := make(chan struct{})
	closed := make(chan struct{})
	frames := 0
	ctx.Frame = func() {
		frames++
		if frames > limit {
			panic("UI is not making progress")
		}
		frameCh <- struct{}{}
		select {
		case <-frameCh:
		case <-closed:
		}
	}
	go func() {
		defer func() {
			if v := recover(); v != nil && v != token {
				panic(v)
			}
		}()
		defer close(closed)
		<-frameCh
		f()
	}()
	quit := func() {
		ctx.Frame = func() {
			panic(token)
		}
		close(frameCh)
		<-closed
		ctx.Frame = nil
	}
	return quit
}

func resetOps(ops *op.Ops, f func()) func() {
	return func() {
		ops.Reset()
		f()
	}
}

func opsContains(ops *op.Ops, str string) bool {
	clip := image.Rectangle{Max: image.Pt(testDisplayDim, testDisplayDim)}
	str = strings.ToLower(str)
	txt := strings.ToLower(ops.ExtractText(clip))
	clean := strings.ReplaceAll(strings.ToLower(str), " ", "")
	return strings.Index(txt, clean) != -1
}

func TestAllocs(t *testing.T) {
	res := testing.Benchmark(func(b *testing.B) {
		desc := urtypes.OutputDescriptor{
			Script:    urtypes.P2WSH,
			Type:      urtypes.SortedMulti,
			Threshold: 2,
			Keys:      make([]urtypes.KeyDescriptor, 5),
		}
		m := fillDescriptor(t, desc, desc.Script.DerivationPath(), 12, 0)
		ds := &DescriptorScreen{
			Descriptor: desc,
			Mnemonic:   m,
		}
		screens := []func(*Context, op.Ctx){
			func(ctx *Context, ops op.Ctx) {
				mainFlow(ctx, ops)
			},
			func(ctx *Context, ops op.Ctx) {
				ds.Confirm(ctx, ops, &descriptorTheme)
			},
		}
		frames := make([]func(), 0, len(screens))
		for _, s := range screens {
			ops := new(op.Ops)
			ctx := NewContext(newPlatform())
			quit := runUILimit(ctx, math.MaxInt, func() {
				s(ctx, ops.Context())
			})
			defer quit()
			frames = append(frames, resetOps(ops, ctx.Frame))
		}
		b.StartTimer()
		for range b.N {
			for _, f := range frames {
				f()
			}
		}
	})
	if a := res.AllocsPerOp(); a > 0 {
		t.Errorf("got %d allocs, expected %d", a, 0)
	}
}

func TestMainScreen(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)

	ops := new(op.Ops)
	quit := runUI(ctx, func() {
		mainFlow(ctx, ops.Context())
	})
	defer quit()
	frame := resetOps(ops, ctx.Frame)
	// Test sd card warning.
	ctxButton(ctx, Button3)
	frame()
	if !opsContains(ops, "Remove SD") {
		t.Fatal("MainScreen ignored SD card present")
	}
	ctx.EmptySDSlot = true
	frame()
	if opsContains(ops, "Remove SD") {
		t.Fatal("MainScreen ignored SD card ejected")
	}
	// Input method camera
	ctxButton(ctx, Down, Button3)
	// Scan xpub as descriptor.
	ctxQR(t, ctx, p, "xpub6F148LnjUhGrHfEN6Pa8VkwF8L6FJqYALxAkuHfacfVhMLVY4MRuUVMxr9pguAv67DHx1YFxqoKN8s4QfZtD9sR2xRCffTqi9E8FiFLAYk8")
	frame()
	if !opsContains(ops, "Invalid Seed") {
		t.Fatal("MainScreen accepted invalid data for a Seed")
	}
}

func TestNonParticipatingSeed(t *testing.T) {
	// Enter seed not part of the descriptor.
	mnemonic := make(bip39.Mnemonic, 12)
	for i := range mnemonic {
		mnemonic[i] = bip39.RandomWord()
	}
	mnemonic = mnemonic.FixChecksum()
	scr := &DescriptorScreen{
		Mnemonic:   mnemonic,
		Descriptor: twoOfThree.Descriptor,
	}
	ctx := NewContext(newPlatform())

	// Accept descriptor.
	ctxButton(ctx, Button3)

	ops := new(op.Ops)
	quit := runUI(ctx, func() {
		if _, ok := scr.Confirm(ctx, ops.Context(), &descriptorTheme); ok {
			t.Fatal("a non-participating seed was accepted")
		}
	})
	defer quit()
	ctx.Frame()
	if !opsContains(ops, "Unknown Wallet") {
		t.Fatal("a non-participating seed was accepted")
	}
}

func dumpUI(t *testing.T, ops *op.Ops) {
	clip := image.Rectangle{Max: image.Pt(testDisplayDim, testDisplayDim)}
	ops.Clip(clip)
	fb := image.NewNRGBA(clip)
	maskfb := image.NewAlpha(clip)
	ops.Draw(fb, maskfb)
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, fb); err != nil {
		t.Error(err)
	}
	if err := os.WriteFile("ui.png", buf.Bytes(), 0o600); err != nil {
		t.Error(err)
	}
}

func newTestEngraveScreen(t *testing.T, ctx *Context) *EngraveScreen {
	desc := twoOfThree.Descriptor
	const keyIdx = 0
	plate, err := engravePlate(plateSizes, mjolnir.Params, desc, keyIdx, twoOfThree.Mnemonic)
	if err != nil {
		t.Fatal(err)
	}
	return NewEngraveScreen(
		ctx,
		plate,
	)
}

func TestEngraveScreenCancel(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	scr := newTestEngraveScreen(t, ctx)

	// Back.
	ctxButton(ctx, Button1)
	// Hold confirm.
	ctxPress(ctx, Button3)
	var cancelled bool
	quit := runUI(ctx, func() {
		cancelled = !scr.Engrave(ctx, op.Ctx{}, &engraveTheme)
	})
	defer quit()
	ctx.Frame()
	if cancelled {
		t.Error("exited screen without confirmation")
	}
	p.timeOffset += confirmDelay
	ctx.Frame()
	if !cancelled {
		t.Error("failed to exit screen")
	}
}

func TestEngraveError(t *testing.T) {
	nonstdPath := []uint32{
		hdkeychain.HardenedKeyStart + 86,
		hdkeychain.HardenedKeyStart + 0,
		hdkeychain.HardenedKeyStart + 0,
	}
	tests := []struct {
		name      string
		threshold int
		keys      int
		path      []uint32
		err       error
	}{
		{"threshold too small", 1, 5, nonstdPath, backup.ErrDescriptorTooLarge},
	}
	for i, test := range tests {
		name := fmt.Sprintf("%d-%d-of-%d", i, test.threshold, test.keys)
		t.Run(name, func(t *testing.T) {
			desc := urtypes.OutputDescriptor{
				Script:    urtypes.P2WSH,
				Threshold: test.threshold,
				Type:      urtypes.SortedMulti,
				Keys:      make([]urtypes.KeyDescriptor, test.keys),
			}
			mnemonic := fillDescriptor(t, desc, test.path, 12, 0)
			_, err := engravePlate(plateSizes, mjolnir.Params, desc, 0, mnemonic)
			if err == nil {
				t.Fatal("invalid descriptor succeeded")
			}
			if !errors.Is(err, test.err) {
				t.Fatalf("got error %v, expected %v", err, test.err)
			}
		})
	}
}

func TestEngraveScreenConnectionError(t *testing.T) {
	p := newPlatform()
	p.engrave.closed = make(chan []mjolnir.Cmd, 1)
	p.engrave.connErr = errors.New("failed to connect")
	ctx := NewContext(p)
	scr := newTestEngraveScreen(t, ctx)
	ops := new(op.Ops)
	quit := runUI(ctx, func() {
		scr.Engrave(ctx, ops.Context(), &engraveTheme)
	})
	defer quit()
	frame := resetOps(ops, ctx.Frame)
	// Press next until connect is reached.
	for scr.instructions[scr.step].Type != ConnectInstruction {
		ctxButton(ctx, Button3)
		ctx.Frame()
	}
	// Hold connect.
	ctxPress(ctx, Button3)
	frame()
	p.timeOffset += confirmDelay
	frame()
	if !opsContains(ops, p.engrave.connErr.Error()) {
		t.Fatal("engraver error did not propagate to screen")
	}
	// Dismiss error.
	ctxButton(ctx, Button3)
	// Successfully connect, but fail during engraving.
	p.engrave.connErr = nil
	p.engrave.ioErr = errors.New("error during engraving")
	delivered := make(chan struct{})
	p.engrave.ioErrDelivered = delivered
	// Hold connect.
	ctxPress(ctx, Button3)
	frame()
	p.timeOffset += confirmDelay
	frame()
	if opsContains(ops, "error") {
		t.Fatal("screen reported error for connection success")
	}
	<-delivered
	for {
		frame()
		if opsContains(ops, p.engrave.ioErr.Error()) {
			// t.Fatal("screen didn't report engraver error")
			break
		}
	}
	// Dismiss error and verify screen exits.
	ctxButton(ctx, Button3)
	frame()
	if opsContains(ops, "error") {
		t.Fatal("screen didn't exit after fatal engraver error")
	}
	// Verify device was closed.
	<-p.engrave.closed
}

func TestScanScreenConnectError(t *testing.T) {
	p := newPlatform()
	// Fail on connect.
	ctx := NewContext(p)
	scr := &ScanScreen{}
	camErr := errors.New("failed to open camera")
	ctx.events = append(ctx.events, FrameEvent{Error: camErr}.Event())
	ops := new(op.Ops)
	quit := runUI(ctx, func() {
		scr.Scan(ctx, ops.Context())
	})
	defer quit()
	frame := resetOps(ops, ctx.Frame)
	frame()
	if !opsContains(ops, camErr.Error()) {
		t.Fatal("initial camera error not reported")
	}
}

func TestScanScreenStreamError(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	// Fail during streaming.
	scr := &ScanScreen{}
	// Connect.
	camErr := errors.New("error during streaming")
	ctx.events = append(ctx.events, FrameEvent{Error: camErr}.Event())
	ops := new(op.Ops)
	quit := runUI(ctx, func() {
		scr.Scan(ctx, ops.Context())
	})
	defer quit()
	ctx.Frame()
	if !opsContains(ops, camErr.Error()) {
		t.Fatal("streaming camera error not reported")
	}
}

func TestWordKeyboardScreen(t *testing.T) {
	ctx := NewContext(newPlatform())
	for i := bip39.Word(0); i < bip39.NumWords; i++ {
		w := bip39.LabelFor(i)
		ctxString(ctx, strings.ToUpper(w))
		ctxButton(ctx, Button2)
		m := make(bip39.Mnemonic, 1)
		inputWordsFlow(ctx, op.Ctx{}, &descriptorTheme, m, 0)
		if got := bip39.LabelFor(m[0]); got != w {
			t.Errorf("keyboard mapped %q to %q", w, got)
		}
	}
}

func ctxMnemonic(ctx *Context, m bip39.Mnemonic) {
	for _, word := range m {
		ctxString(ctx, strings.ToUpper(bip39.LabelFor(word)))
		ctxButton(ctx, Button2)
	}
}

func ctxQR(t *testing.T, ctx *Context, p *testPlatform, qrs ...string) {
	t.Helper()
	for _, qr := range qrs {
		ctx.events = append(ctx.events, qrFrame(t, p, qr).Event())
	}
}

func TestSeedScreenScan(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	// Select camera.
	ctxButton(ctx, Down, Button3)
	want, err := bip39.ParseMnemonic("attack pizza motion avocado network gather crop fresh patrol unusual wild holiday candy pony ranch winter theme error hybrid van cereal salon goddess expire")
	if err != nil {
		t.Fatal(err)
	}
	ctxQR(t, ctx, p, string(seedqr.QR(want)))
	got, ok := newMnemonicFlow(ctx, op.Ctx{}, &descriptorTheme)
	if !ok {
		t.Errorf("no mnemonic from scanned seed")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scanned %v, want %v", got, want)
	}
}

func TestSeedScreenScanInvalid(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	// Select camera.
	ctxButton(ctx, Down, Button3)
	ctxQR(t, ctx, p, "UR:CRYPTO-SEED/OYADGDIYWLAMAEJSZSWDWYTLTIFEENFTLNMNWKBDHNSSRO")
	ops := new(op.Ops)
	quit := runUI(ctx, func() {
		newMnemonicFlow(ctx, ops.Context(), &descriptorTheme)
	})
	defer quit()
	ctx.Frame()
	if !opsContains(ops, "invalid seed") {
		t.Error("invalid seed accepted")
	}
}

func TestSeedScreenInvalidSeed(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	m := append(bip39.Mnemonic{}, twoOfThree.Mnemonic...)
	// Invalidate seed.
	m[0] = 0
	// Accept seed.
	ctxButton(ctx, Button3)
	scr := new(SeedScreen)
	var confirmed bool
	ops := new(op.Ops)
	var exited bool
	quit := runUI(ctx, func() {
		scr.Confirm(ctx, ops.Context(), &singleTheme, m)
		exited = true
	})
	defer quit()
	frame := resetOps(ops, ctx.Frame)
	frame()
	if confirmed || !opsContains(ops, "invalid seed") {
		t.Fatal("invalid seed accepted")
	}
	// Dismiss error.
	ctxButton(ctx, Button3)

	// Back.
	ctxButton(ctx, Button1)
	// Hold confirm.
	ctxPress(ctx, Button3)
	frame()
	if exited {
		t.Error("exited screen without confirmation")
	}
	p.timeOffset += confirmDelay
	frame()
	if !exited {
		t.Error("failed to exit screen")
	}
}

func TestXpubMasterFingerprintSinglesig(t *testing.T) {
	const mnemonic = "upset toe sheriff cotton vibrant shock torch waste congress innocent company review"
	const descriptor = "zpub6qiC7jMrWkhNEu7YamFTWx8YHQaDFynLYQCUmxjCWpBiLQ4Qp6c6PEwpZpkN27XmUtBjX7hVLyyBKa7zhgaB5B2qvdckaP21ADwx7oYgYD6"

	m, err := bip39.ParseMnemonic(mnemonic)
	if err != nil {
		t.Fatal(err)
	}

	p := newPlatform()
	ctx := NewContext(p)
	ops := new(op.Ops)
	ctxQR(t, ctx, p, descriptor)
	ctxButton(ctx, Button3)
	got, parsed := inputDescriptorFlow(ctx, ops.Context(), &descriptorTheme, m)

	if !parsed {
		t.Error("failed to parse descriptor")
	}

	want, err := nonstandard.OutputDescriptor([]byte(descriptor))
	if err != nil {
		t.Fatal(err)
	}
	mfp, err := masterFingerprintFor(m, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}
	want.Keys[0].MasterFingerprint = mfp
	if !reflect.DeepEqual(want, *got) {
		t.Error("descriptors don't match")
	}
}

func TestSeed(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)

	const mnemonic = "doll clerk nice coast caught valid shallow taxi buyer economy lunch roof"
	m, err := bip39.ParseMnemonic(mnemonic)
	if err != nil {
		t.Fatal(err)
	}
	mk, ok := deriveMasterKey(m, &chaincfg.MainNetParams)
	if !ok {
		t.Fatal("failed to derive master key")
	}
	mfp, _, err := bip32.Derive(mk, urtypes.Path{0})
	if err != nil {
		t.Fatal(err)
	}
	seedDesc := backup.Seed{
		KeyIdx:            0,
		Mnemonic:          m,
		Keys:              1,
		MasterFingerprint: mfp,
		Font:              constant.Font,
		Size:              backup.SquarePlate,
	}
	side, err := backup.EngraveSeed(p.EngraverParams(), seedDesc)
	if err != nil {
		t.Fatal(err)
	}
	plate := Plate{
		Sides:             []engrave.Plan{side},
		Size:              backup.SquarePlate,
		MasterFingerprint: mfp,
	}

	var completed bool
	scr := NewEngraveScreen(ctx, plate)
	quit := runUI(ctx, func() {
		completed = scr.Engrave(ctx, op.Ctx{}, &engraveTheme)
	})
	defer quit()

	testEngraving(t, p, ctx, scr, side)
	for !completed {
		ctxButton(ctx, Button3)
		ctx.Frame()
	}
}

func TestMulti(t *testing.T) {
	const oneOfTwoDesc = "wsh(sortedmulti(1,[94631f99/48h/0h/0h/2h]xpub6ENfRaMWq2UoFy5FrLRMwiEkdgFdMgjEoikR34RBGzhsx8JzAkn7fyQeR5odirEwERvmxhSEv7rsmV7nuzjSKKKJHBP2aQZVu3R2d5ERgcw,[4bbaa801/48h/0h/0h/2h]xpub6E8mpiqJiVKuJZqxtu5SbHQnwUWWPQpZEy9CVtvfU1gxXZnbb9DG2AvZyMHvyVRtUPAEmu6BuRCy4LK2rKMeNr7jQKXsCyFfr1osgFCMYpc))"
	mnemonics := []string{
		"doll clerk nice coast caught valid shallow taxi buyer economy lunch roof",
		"road lend lyrics shift rabbit amazing fetch impulse provide reopen sphere network",
	}

	for i, mnemonic := range mnemonics {
		p := newPlatform()
		ctx := NewContext(p)

		m, err := bip39.ParseMnemonic(mnemonic)
		if err != nil {
			t.Fatal(err)
		}

		oneOfTwo, err := nonstandard.OutputDescriptor([]byte(oneOfTwoDesc))
		if err != nil {
			t.Fatal(err)
		}
		const size = backup.LargePlate
		descPlate := backup.Descriptor{
			Descriptor: oneOfTwo,
			KeyIdx:     i,
			Font:       constant.Font,
			Size:       size,
		}
		descSide, err := backup.EngraveDescriptor(p.EngraverParams(), descPlate)
		if err != nil {
			t.Fatal(err)
		}
		seedDesc := backup.Seed{
			Title:             oneOfTwo.Title,
			KeyIdx:            i,
			Mnemonic:          m,
			Keys:              len(oneOfTwo.Keys),
			MasterFingerprint: oneOfTwo.Keys[i].MasterFingerprint,
			Font:              constant.Font,
			Size:              size,
		}
		seedSide, err := backup.EngraveSeed(p.EngraverParams(), seedDesc)
		if err != nil {
			t.Fatal(err)
		}
		plate := Plate{
			Size:  size,
			Sides: []engrave.Plan{descSide, seedSide},
		}
		var completed bool
		scr := NewEngraveScreen(ctx, plate)
		quit := runUI(ctx, func() {
			completed = scr.Engrave(ctx, op.Ctx{}, &engraveTheme)
		})
		defer quit()
		for _, side := range plate.Sides {
			testEngraving(t, p, ctx, scr, side)
		}
		for !completed {
			ctxButton(ctx, Button3)
			ctx.Frame()
		}
	}
}

func fillDescriptor(t *testing.T, desc urtypes.OutputDescriptor, path urtypes.Path, seedlen int, keyIdx int) bip39.Mnemonic {
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
		mfp, xpub, err := bip32.Derive(mk, path)
		if err != nil {
			t.Fatal(err)
		}
		pub, err := xpub.ECPubKey()
		if err != nil {
			t.Fatal(err)
		}
		desc.Keys[i] = urtypes.KeyDescriptor{
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
	events []Event

	engrave struct {
		closed         chan []mjolnir.Cmd
		connErr        error
		ioErr          error
		ioErrDelivered chan<- struct{}
	}

	timeOffset time.Duration
	qrImages   map[*uint8][]byte
}

func (t *testPlatform) ScanQR(img *image.Gray) ([][]byte, error) {
	if content, ok := t.qrImages[&img.Pix[0]]; ok {
		return [][]byte{content}, nil
	}
	return nil, errors.New("no QR code")
}

const testDisplayDim = 240

func (*testPlatform) DisplaySize() image.Point {
	return image.Pt(testDisplayDim, testDisplayDim)
}

func (*testPlatform) Dirty(r image.Rectangle) error {
	return nil
}

func (*testPlatform) NextChunk() (draw.RGBA64Image, bool) {
	return nil, false
}

func (t *testPlatform) Now() time.Time {
	return time.Now().Add(t.timeOffset)
}

func (*testPlatform) Debug() bool {
	return false
}

func ctxString(ctx *Context, str string) {
	for _, r := range str {
		ctx.Events(
			ButtonEvent{
				Button:  Rune,
				Rune:    r,
				Pressed: true,
			}.Event(),
		)
	}
}

func ctxPress(ctx *Context, bs ...Button) {
	for _, b := range bs {
		ctx.Events(
			ButtonEvent{
				Button:  b,
				Pressed: true,
			}.Event(),
		)
	}
}

func ctxButton(ctx *Context, bs ...Button) {
	for _, b := range bs {
		ctx.Events(
			ButtonEvent{
				Button:  b,
				Pressed: true,
			}.Event(),
			ButtonEvent{
				Button:  b,
				Pressed: false,
			}.Event(),
		)
	}
}

func (p *testPlatform) Wakeup() {
}

func (p *testPlatform) Events(deadline time.Time) []Event {
	evts := p.events
	p.events = nil
	return evts
}

type wrappedEngraver struct {
	dev            *mjolnir.Simulator
	closed         chan<- []mjolnir.Cmd
	ioErr          error
	ioErrDelivered chan<- struct{}
}

func (w *wrappedEngraver) Read(p []byte) (int, error) {
	n, err := w.dev.Read(p)
	if err == nil && w.ioErr != nil {
		err = w.ioErr
		w.ioErr = nil
		close(w.ioErrDelivered)
	}
	return n, err
}

func (w *wrappedEngraver) Write(p []byte) (int, error) {
	n, err := w.dev.Write(p)
	if err == nil && w.ioErr != nil {
		err = w.ioErr
		w.ioErr = nil
		close(w.ioErrDelivered)
	}
	return n, err
}

func (w *wrappedEngraver) Close() error {
	if w.closed != nil {
		w.closed <- w.dev.Cmds
	}
	return w.dev.Close()
}

func (p *testPlatform) EngraverParams() engrave.Params {
	return mjolnir.Params
}

var plateSizes = []backup.PlateSize{backup.SquarePlate, backup.LargePlate}

func (p *testPlatform) PlateSizes() []backup.PlateSize {
	return plateSizes
}

func (p *testPlatform) Engraver() (Engraver, error) {
	if err := p.engrave.connErr; err != nil {
		return nil, err
	}
	sim := mjolnir.NewSimulator()
	return &engraver{
		dev: &wrappedEngraver{sim, p.engrave.closed, p.engrave.ioErr, p.engrave.ioErrDelivered},
	}, nil
}

type engraver struct {
	dev io.ReadWriteCloser
}

func (e *engraver) Engrave(sz backup.PlateSize, plan engrave.Plan, quit <-chan struct{}) error {
	return mjolnir.Engrave(e.dev, mjolnir.Options{}, plan, quit)
}

func (e *engraver) Close() {
	e.dev.Close()
}

func (p *testPlatform) CameraFrame(dims image.Point) {
}

func newPlatform() *testPlatform {
	return &testPlatform{}
}

func qrFrame(t *testing.T, p *testPlatform, content string) FrameEvent {
	qr, err := qr.Encode(content, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	qrImg := qr.Image()
	b := qrImg.Bounds()
	frameImg := image.NewYCbCr(b, image.YCbCrSubsampleRatio420)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			off := frameImg.YOffset(x, y)
			r, _, _, _ := qrImg.At(x, y).RGBA()
			frameImg.Y[off] = uint8(r >> 8)
		}
	}
	if p.qrImages == nil {
		p.qrImages = make(map[*byte][]byte)
	}
	p.qrImages[&frameImg.Y[0]] = []byte(content)
	return FrameEvent{
		Image: frameImg,
	}
}

func testEngraving(t *testing.T, p *testPlatform, ctx *Context, scr *EngraveScreen, side engrave.Plan) {
	p.engrave.closed = make(chan []mjolnir.Cmd)
done:
	for {
		switch scr.instructions[scr.step].Type {
		case EngraveInstruction:
			break done
		case ConnectInstruction:
			// Hold connect.
			ctxPress(ctx, Button3)
			ctx.Frame()
			p.timeOffset += confirmDelay
			ctx.Frame()
		default:
			ctxButton(ctx, Button3)
			ctx.Frame()
		}
	}
	got := <-p.engrave.closed
	// Verify the step is advanced after engrave completion.
	for scr.instructions[scr.step].Type == EngraveInstruction {
		ctx.Frame()
	}
	want := simEngrave(t, side)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("engraver commands mismatch for side %v", side)
	}
}

func simEngrave(t *testing.T, plate engrave.Plan) []mjolnir.Cmd {
	sim := mjolnir.NewSimulator()
	defer sim.Close()
	if err := mjolnir.Engrave(sim, mjolnir.Options{}, plate, nil); err != nil {
		t.Fatal(err)
	}
	return sim.Cmds
}

func mnemonicFor(phrase string) bip39.Mnemonic {
	m, err := bip39.ParseMnemonic(phrase)
	if err != nil {
		panic(err)
	}
	return m
}

var twoOfThree = struct {
	Descriptor urtypes.OutputDescriptor
	Mnemonic   bip39.Mnemonic
}{
	Mnemonic: mnemonicFor("flip begin artist fringe online release swift genre wool general transfer arm"),
	Descriptor: urtypes.OutputDescriptor{
		Script:    urtypes.P2WSH,
		Threshold: 2,
		Type:      urtypes.SortedMulti,
		Keys: []urtypes.KeyDescriptor{
			{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0x5a0804e3,
				DerivationPath:    urtypes.Path{0x80000030, 0x80000000, 0x80000000, 0x80000002},
				KeyData:           []byte{0x3, 0xa9, 0x39, 0x4a, 0x2f, 0x1a, 0x4f, 0x99, 0x61, 0x3a, 0x71, 0x69, 0x56, 0xc8, 0x54, 0xf, 0x6d, 0xba, 0x6f, 0x18, 0x93, 0x1c, 0x26, 0x39, 0x10, 0x72, 0x21, 0xb2, 0x67, 0xd7, 0x40, 0xaf, 0x23},
				ChainCode:         []byte{0xdb, 0xe8, 0xc, 0xbb, 0x4e, 0xe, 0x41, 0x8b, 0x6, 0xf4, 0x70, 0xd2, 0xaf, 0xe7, 0xa8, 0xc1, 0x7b, 0xe7, 0x1, 0xab, 0x20, 0x6c, 0x59, 0xa6, 0x5e, 0x65, 0xa8, 0x24, 0x1, 0x6a, 0x6c, 0x70},
				ParentFingerprint: 0xc7bce7a8,
			},
			{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0xdd4fadee,
				DerivationPath:    urtypes.Path{0x80000030, 0x80000000, 0x80000000, 0x80000002},
				KeyData:           []byte{0x2, 0x21, 0x96, 0xad, 0xc2, 0x5f, 0xde, 0x16, 0x9f, 0xe9, 0x2e, 0x70, 0x76, 0x90, 0x59, 0x10, 0x22, 0x75, 0xd2, 0xb4, 0xc, 0xc9, 0x87, 0x76, 0xea, 0xab, 0x92, 0xb8, 0x2a, 0x86, 0x13, 0x5e, 0x92},
				ChainCode:         []byte{0x43, 0x8e, 0xff, 0x7b, 0x3b, 0x36, 0xb6, 0xd1, 0x1a, 0x60, 0xa2, 0x2c, 0xcb, 0x93, 0x6, 0xee, 0xa3, 0x5, 0xb0, 0x43, 0x9f, 0x1e, 0xa0, 0x9d, 0x59, 0x28, 0x1, 0x5d, 0xe3, 0x73, 0x81, 0x16},
				ParentFingerprint: 0x22969377,
			},
			{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0x9bacd5c0,
				DerivationPath:    urtypes.Path{0x80000030, 0x80000000, 0x80000000, 0x80000002},
				KeyData:           []byte{0x2, 0xfb, 0x72, 0x50, 0x7f, 0xc2, 0xd, 0xdb, 0xa9, 0x29, 0x91, 0xb1, 0x7c, 0x4b, 0xb4, 0x66, 0x13, 0xa, 0xd9, 0x3a, 0x88, 0x6e, 0x73, 0x17, 0x50, 0x33, 0xbb, 0x43, 0xe3, 0xbc, 0x78, 0x5a, 0x6d},
				ChainCode:         []byte{0x95, 0xb3, 0x49, 0x13, 0x93, 0x7f, 0xa5, 0xf1, 0xc6, 0x20, 0x5b, 0x52, 0x5b, 0xb5, 0x7d, 0xe1, 0x51, 0x76, 0x25, 0xe0, 0x45, 0x86, 0xb5, 0x95, 0xbe, 0x68, 0xe7, 0x13, 0x62, 0xd3, 0xed, 0xc5},
				ParentFingerprint: 0x97ec38f9,
			},
		},
	},
}
