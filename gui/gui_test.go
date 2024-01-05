package gui

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/skip2/go-qrcode"
	"seedhammer.com/backup"
	"seedhammer.com/bc/urtypes"
	"seedhammer.com/bip32"
	"seedhammer.com/bip39"
	"seedhammer.com/driver/mjolnir"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
	"seedhammer.com/gui/op"
	"seedhammer.com/image/rgb565"
	"seedhammer.com/nonstandard"
)

func TestDescriptorScreenError(t *testing.T) {
	ctx := NewContext(newPlatform())
	dupDesc := urtypes.OutputDescriptor{
		Script:    urtypes.P2WSH,
		Threshold: 2,
		Keys:      make([]urtypes.KeyDescriptor, 2),
	}
	fillDescriptor(t, dupDesc, dupDesc.Script.DerivationPath(), 12, 0)
	dupDesc.Keys[1] = dupDesc.Keys[0]
	smallDesc := urtypes.OutputDescriptor{
		Script:    urtypes.P2WSH,
		Threshold: 2,
		Keys:      make([]urtypes.KeyDescriptor, 5),
	}
	fillDescriptor(t, smallDesc, smallDesc.Script.DerivationPath(), 12, 0)
	tests := []struct {
		name string
		desc urtypes.OutputDescriptor
	}{
		{"duplicate key", dupDesc},
		{"small threshold", smallDesc},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scr := &DescriptorScreen{
				Descriptor: test.desc,
			}
			ctxButton(ctx, Button3)
			scr.Layout(ctx, op.Ctx{}, image.Point{})
			if scr.warning == nil {
				t.Fatal("DescriptorScreen accepted invalid descriptor")
			}
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
			err := validateDescriptor(test.desc)
			if err == nil {
				t.Fatal("validateDescriptor accepted an unsupported descriptor")
			}
			if !errors.Is(err, test.err) {
				t.Fatalf("validateDescriptor returned %v, expected %v", err, test.err)
			}
		})
	}
}

func TestMainScreen(t *testing.T) {
	scr := new(MainScreen)
	p := newPlatform()
	ctx := NewContext(p)

	frame := func() {
		scr.Layout(ctx, op.Ctx{}, image.Point{}, nil)
	}
	// Test sd card warning.
	ctxButton(ctx, Button3)
	frame()
	if scr.sdcard.warning == nil {
		t.Fatal("MainScreen ignored SD card present")
	}
	ctx.NoSDCard = true
	frame()
	if scr.sdcard.warning != nil {
		t.Fatal("MainScreen ignored SD card ejected")
	}
	// Input method camera
	ctxButton(ctx, Down, Button3)
	frame()
	// Scan xpub as descriptor.
	ctxQR(t, p, frame, "xpub6F148LnjUhGrHfEN6Pa8VkwF8L6FJqYALxAkuHfacfVhMLVY4MRuUVMxr9pguAv67DHx1YFxqoKN8s4QfZtD9sR2xRCffTqi9E8FiFLAYk8")
	if scr.seed.warning == nil {
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

	scr.Layout(ctx, op.Ctx{}, image.Point{})
	if scr.warning == nil || scr.warning.Title != "Unknown Wallet" {
		t.Fatal("a non-participating seed was accepted")
	}
}

func TestEngraveScreenCancel(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	scr, err := NewEngraveScreen(ctx, twoOfThree.Descriptor, 0, twoOfThree.Mnemonic)
	if err != nil {
		t.Fatal(err)
	}

	// Back.
	ctxButton(ctx, Button1)
	// Hold confirm.
	ctxPress(ctx, Button3)
	res := scr.Layout(ctx, op.Ctx{}, image.Point{})
	if res != ResultNone {
		t.Error("exited screen without confirmation")
	}
	p.timeOffset += confirmDelay
	res = scr.Layout(ctx, op.Ctx{}, image.Point{})
	if res != ResultCancelled {
		t.Error("failed to exit screen")
	}
}

func TestEngraveScreenError(t *testing.T) {
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
			ctx := NewContext(newPlatform())
			desc := urtypes.OutputDescriptor{
				Script:    urtypes.P2WSH,
				Threshold: test.threshold,
				Type:      urtypes.SortedMulti,
				Keys:      make([]urtypes.KeyDescriptor, test.keys),
			}
			mnemonic := fillDescriptor(t, desc, test.path, 12, 0)
			_, err := NewEngraveScreen(ctx, desc, 0, mnemonic)
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
	scr, err := NewEngraveScreen(ctx, twoOfThree.Descriptor, 0, twoOfThree.Mnemonic)
	if err != nil {
		t.Fatal(err)
	}
	// Press next until connect is reached.
	for scr.instructions[scr.step].Type != ConnectInstruction {
		ctxButton(ctx, Button3)
		scr.Layout(ctx, op.Ctx{}, image.Point{})
	}
	// Hold connect.
	ctxPress(ctx, Button3)
	if res := scr.Layout(ctx, op.Ctx{}, image.Point{}); res != ResultNone {
		t.Error("exited screen without confirmation")
	}
	p.timeOffset += confirmDelay
	scr.Layout(ctx, op.Ctx{}, image.Point{})
	if scr.engrave.warning == nil {
		t.Fatal("engraver error did not propagate to screen")
	}
	// Dismiss error.
	ctxButton(ctx, Button3)
	// Successfully connect, but fail during engraving.
	p.engrave.connErr = nil
	p.engrave.ioErr = errors.New("error during engraving")
	// Hold connect.
	ctxPress(ctx, Button3)
	if res := scr.Layout(ctx, op.Ctx{}, image.Point{}); res != ResultNone {
		t.Error("exited screen without confirmation")
	}
	p.timeOffset += confirmDelay
	scr.Layout(ctx, op.Ctx{}, image.Point{})
	if err := scr.engrave.warning; err != nil {
		t.Fatalf("screen reported error for connection success: %v", err)
	}
	for scr.engrave.warning == nil {
		scr.Layout(ctx, op.Ctx{}, image.Point{})
	}
	// Dismiss error and verify screen exits.
	ctxButton(ctx, Button3)
	scr.Layout(ctx, op.Ctx{}, image.Point{})
	if scr.engrave.warning != nil {
		t.Fatal("screen didn't exit after fatal engraver error")
	}
	// Verify device was closed.
	<-p.engrave.closed
}

func TestScanScreenError(t *testing.T) {
	p := newPlatform()
	// Fail on connect.
	p.camera.connErr = errors.New("failed to open camera")
	ctx := NewContext(p)
	scr := &ScanScreen{}
	for scr.camera.err == nil {
		scr.Layout(ctx, op.Ctx{}, image.Point{})
	}
	// Fail during streaming.
	p.camera.connErr = nil
	scr = &ScanScreen{}
	// Connect.
	scr.Layout(ctx, op.Ctx{}, image.Point{})
	go func() {
		<-p.camera.init
		p.camera.in <- testFrame{Err: errors.New("error during streaming")}
	}()
	for scr.camera.err == nil {
		scr.Layout(ctx, op.Ctx{}, image.Point{})
	}
}

func TestWordKeyboardScreen(t *testing.T) {
	ctx := NewContext(newPlatform())
	for i := bip39.Word(0); i < bip39.NumWords; i++ {
		scr := &WordKeyboardScreen{
			Mnemonic: make(bip39.Mnemonic, 1),
		}
		w := bip39.LabelFor(i)
		ctxString(ctx, strings.ToUpper(w))
		ctxButton(ctx, Button2)
		res := scr.Layout(ctx, op.Ctx{}, &singleTheme, image.Point{})
		if res == ResultNone {
			t.Errorf("keyboard did not accept %q", w)
		}
		if got := bip39.LabelFor(scr.Mnemonic[0]); got != w {
			t.Errorf("keyboard mapped %q to %q", w, got)
		}
	}
}

func ctxQR(t *testing.T, p *testPlatform, frame func(), qrs ...string) {
	t.Helper()
	for _, qr := range qrs {
		select {
		case <-p.camera.init:
		case <-time.After(5 * time.Second):
			t.Fatal("camera never turned on")
		}
		p.camera.in <- qrFrame(t, p, qr)
		delivered := make(chan struct{})
		go func() {
			<-p.camera.out
			close(delivered)
		}()
	loop:
		for {
			select {
			case <-delivered:
				break loop
			default:
				frame()
			}
		}
	}
}

func TestSeedScreenScan(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	scr := NewEmptySeedScreen("")
	frame := func() {
		scr.Layout(ctx, op.Ctx{}, &singleTheme, image.Point{})
	}
	// Select camera.
	ctxButton(ctx, Down, Button3)
	frame()
	ctxQR(t, p, frame, "011513251154012711900771041507421289190620080870026613431420201617920614089619290300152408010643")
	want, err := bip39.ParseMnemonic("attack pizza motion avocado network gather crop fresh patrol unusual wild holiday candy pony ranch winter theme error hybrid van cereal salon goddess expire")
	if err != nil {
		t.Fatal(err)
	}
	got := scr.Mnemonic
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scanned %v, want %v", got, want)
	}
}

func TestSeedScreenScanInvalid(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	scr := NewEmptySeedScreen("")
	frame := func() {
		scr.Layout(ctx, op.Ctx{}, &singleTheme, image.Point{})
	}
	// Select camera.
	ctxButton(ctx, Down, Button3)
	frame()
	ctxQR(t, p, frame, "UR:CRYPTO-SEED/OYADGDIYWLAMAEJSZSWDWYTLTIFEENFTLNMNWKBDHNSSRO")
	if scr.warning == nil {
		t.Error("SeedScreen accepted invalid seed")
	}
}

func TestSeedScreenInvalidSeed(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	scr := NewSeedScreen(make(bip39.Mnemonic, len(twoOfThree.Mnemonic)))
	copy(scr.Mnemonic, twoOfThree.Mnemonic)
	// Invalidate seed.
	scr.Mnemonic[0] = 0
	// Accept seed.
	ctxButton(ctx, Button3)
	_, res := scr.Layout(ctx, op.Ctx{}, &singleTheme, image.Point{})
	if res != ResultNone || scr.warning == nil {
		t.Fatal("invalid seed accepted")
	}
	// Dismiss error.
	ctxButton(ctx, Button3)

	// Back.
	ctxButton(ctx, Button1)
	// Hold confirm.
	ctxPress(ctx, Button3)
	_, res = scr.Layout(ctx, op.Ctx{}, &singleTheme, image.Point{})
	if res != ResultNone {
		t.Error("exited screen without confirmation")
	}
	p.timeOffset += confirmDelay
	_, res = scr.Layout(ctx, op.Ctx{}, &singleTheme, image.Point{})
	if res == ResultNone {
		t.Error("failed to exit screen")
	}
}

func TestMulti(t *testing.T) {
	const oneOfTwoDesc = "wsh(sortedmulti(1,[94631f99/48h/0h/0h/2h]xpub6ENfRaMWq2UoFy5FrLRMwiEkdgFdMgjEoikR34RBGzhsx8JzAkn7fyQeR5odirEwERvmxhSEv7rsmV7nuzjSKKKJHBP2aQZVu3R2d5ERgcw,[4bbaa801/48h/0h/0h/2h]xpub6E8mpiqJiVKuJZqxtu5SbHQnwUWWPQpZEy9CVtvfU1gxXZnbb9DG2AvZyMHvyVRtUPAEmu6BuRCy4LK2rKMeNr7jQKXsCyFfr1osgFCMYpc))"
	mnemonics := []string{
		"doll clerk nice coast caught valid shallow taxi buyer economy lunch roof",
		"road lend lyrics shift rabbit amazing fetch impulse provide reopen sphere network",
	}

	for i, mnemonic := range mnemonics {
		r := newRunner(t)

		//Seed input method, keyboad input, select 12 words.
		r.Button(t, Button3, Button3, Button3)

		m, err := bip39.ParseMnemonic(mnemonic)
		if err != nil {
			t.Fatal(err)
		}
		r.Frame(t)
		r.Frame(t)

		for _, word := range m {
			r.String(t, strings.ToUpper(bip39.LabelFor(word)))
			r.Button(t, Button2)
		}
		r.Frame(t)
		r.Frame(t)

		if sc := r.app.scr.seed; sc == nil || !sc.Mnemonic.Valid() {
			t.Fatalf("got invalid seed %v, wanted %v", sc.Mnemonic, m)

		}
		if got := r.app.scr.seed.Mnemonic; !reflect.DeepEqual(got, m) {
			t.Fatalf("got seed %v, wanted %v", got, m)
		}

		// Accept seed, go to descriptor scan.
		r.Button(t, Button3, Button3)

		r.QR(t, oneOfTwoDesc)
		for r.app.scr.desc == nil {
			r.Frame(t)
		}

		// Accept descriptor, go to engrave.
		r.Button(t, Button3)
		oneOfTwo, err := nonstandard.OutputDescriptor([]byte(oneOfTwoDesc))
		if err != nil {
			t.Fatal(err)
		}
		for r.app.scr.engrave == nil {
			r.Frame(t)
		}
		testEngraving(t, r, r.app.scr.engrave, oneOfTwo, m, i)
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
	input struct {
		in   chan<- ButtonEvent
		init chan struct{}
	}

	camera struct {
		in      chan<- Frame
		out     <-chan Frame
		init    chan struct{}
		connErr error
	}

	engrave struct {
		closed  chan []mjolnir.Cmd
		connErr error
		ioErr   error
	}

	timeOffset time.Duration
	sdcard     chan bool
	qrImages   map[*uint8][]byte
}

type testLCD struct{}

func (t *testPlatform) ScanQR(img *image.Gray) ([][]byte, error) {
	if content, ok := t.qrImages[&img.Pix[0]]; ok {
		return [][]byte{content}, nil
	}
	return nil, errors.New("no QR code")
}

func (t *testPlatform) Display() (LCD, error) {
	return testLCD{}, nil
}

func (testLCD) Framebuffer() draw.RGBA64Image {
	return rgb565.New(image.Rect(0, 0, 1, 1))
}

func (testLCD) Dirty(sr image.Rectangle) error {
	return nil
}

func (t *testPlatform) SDCard() <-chan bool {
	if t.sdcard == nil {
		t.sdcard = make(chan bool, 1)
		t.sdcard <- false
	}
	return t.sdcard
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
			},
		)
	}
}

func ctxPress(ctx *Context, bs ...Button) {
	for _, b := range bs {
		ctx.Events(
			ButtonEvent{
				Button:  b,
				Pressed: true,
			},
		)
	}
}

func ctxButton(ctx *Context, bs ...Button) {
	for _, b := range bs {
		ctx.Events(
			ButtonEvent{
				Button:  b,
				Pressed: true,
			},
			ButtonEvent{
				Button:  b,
				Pressed: false,
			},
		)
	}
}

func (p *testPlatform) Input(ch chan<- ButtonEvent) error {
	p.input.in = ch
	close(p.input.init)
	return nil
}

type wrappedEngraver struct {
	dev    *mjolnir.Simulator
	closed chan<- []mjolnir.Cmd
	ioErr  error
}

func (w *wrappedEngraver) Read(p []byte) (int, error) {
	n, err := w.dev.Read(p)
	if err == nil {
		err = w.ioErr
	}
	return n, err
}

func (w *wrappedEngraver) Write(p []byte) (int, error) {
	n, err := w.dev.Write(p)
	if err == nil {
		err = w.ioErr
	}
	return n, err
}

func (w *wrappedEngraver) Close() error {
	if w.closed != nil {
		w.closed <- w.dev.Cmds
	}
	return w.dev.Close()
}

func (p *testPlatform) Engraver() (io.ReadWriteCloser, error) {
	if err := p.engrave.connErr; err != nil {
		return nil, err
	}
	sim := mjolnir.NewSimulator()
	return &wrappedEngraver{sim, p.engrave.closed, p.engrave.ioErr}, nil
}

func (p *testPlatform) Camera(dims image.Point, frames chan Frame, out <-chan Frame) func() {
	if err := p.camera.connErr; err != nil {
		go func() {
			frames <- testFrame{Err: err}
		}()
		return func() {}
	}
	p.camera.in = frames
	p.camera.out = out
	close(p.camera.init)
	return func() {}
}

type testFrame struct {
	Err error
	Img image.Image
}

func (t testFrame) Image() image.Image {
	return t.Img
}

func (t testFrame) Error() error {
	return t.Err
}

type runner struct {
	p      *testPlatform
	app    *App
	frames int
}

func newPlatform() *testPlatform {
	p := &testPlatform{}
	p.input.init = make(chan struct{})
	p.camera.init = make(chan struct{})
	return p
}

func newRunner(t *testing.T) *runner {
	r := &runner{
		p: newPlatform(),
	}
	a, err := NewApp(r.p, "")
	if err != nil {
		t.Fatal(err)
	}
	r.app = a
	r.app.ctx.NoSDCard = true
	return r
}

func (r *runner) String(t *testing.T, str string) {
	t.Helper()
	wait(t, r, r.p.input.init)
	for _, c := range str {
		evt := ButtonEvent{
			Button:  Rune,
			Rune:    c,
			Pressed: true,
		}
		deliver(t, r, r.p.input.in, evt)
	}
}

func (r *runner) Frame(t *testing.T) {
	t.Helper()
	r.frames++
	if r.frames > 10000 {
		t.Fatal("test still incomplete after 10000 frames")
	}
	r.app.Frame()
}

func deliver[T any](t *testing.T, r *runner, in chan<- T, v T) {
	t.Helper()
delivery:
	for {
		select {
		case in <- v:
			break delivery
		default:
			r.Frame(t)
		}
	}
}

func wait[T any](t *testing.T, r *runner, out <-chan T) {
	for {
		select {
		case <-out:
			return
		default:
			r.Frame(t)
		}
	}
}

func qrFrame(t *testing.T, p *testPlatform, content string) Frame {
	qr, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		t.Fatal(err)
	}
	qrImg := qr.Image(512)
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
	return testFrame{
		Img: frameImg,
	}
}

func (r *runner) QR(t *testing.T, qrs ...string) {
	t.Helper()
	wait(t, r, r.p.camera.init)
	for _, qr := range qrs {
		frame := qrFrame(t, r.p, qr)
		deliver(t, r, r.p.camera.in, frame)
		delivered := make(chan struct{})
		go func() {
			<-r.p.camera.out
			close(delivered)
		}()
		wait(t, r, delivered)
	}
}

func (r *runner) Button(t *testing.T, bs ...Button) {
	t.Helper()
	wait(t, r, r.p.input.init)
	for _, b := range bs {
		deliver(t, r, r.p.input.in, ButtonEvent{
			Button:  b,
			Pressed: true,
		})
		deliver(t, r, r.p.input.in, ButtonEvent{
			Button:  b,
			Pressed: false,
		})
	}
}

func (r *runner) Press(t *testing.T, bs ...Button) {
	t.Helper()
	wait(t, r, r.p.input.init)
	for _, b := range bs {
		deliver(t, r, r.p.input.in, ButtonEvent{
			Button:  b,
			Pressed: true,
		})
	}
}

func testEngraving(t *testing.T, r *runner, scr *EngraveScreen, desc urtypes.OutputDescriptor, mnemonic bip39.Mnemonic, keyIdx int) {
	plateDesc := backup.PlateDesc{
		Descriptor: desc,
		Mnemonic:   mnemonic,
		KeyIdx:     keyIdx,
		Font:       constant.Font,
	}
	plate, err := backup.Engrave(mjolnir.Millimeter, mjolnir.StrokeWidth, plateDesc)
	if err != nil {
		t.Fatal(err)
	}
	r.p.engrave.closed = make(chan []mjolnir.Cmd, len(plate.Sides))
	for _, side := range plate.Sides {
	done:
		for {
			switch scr.instructions[scr.step].Type {
			case EngraveInstruction:
				break done
			case ConnectInstruction:
				// Hold connect.
				r.Press(t, Button3)
				r.p.timeOffset += confirmDelay
			default:
				r.Button(t, Button3)
			}
		}
	received:
		for {
			select {
			case got := <-r.p.engrave.closed:
				// Verify the step is advanced after engrave completion.
				for scr.instructions[scr.step].Type == EngraveInstruction {
					r.Frame(t)
				}
				want := simEngrave(t, side)
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("engraver commands mismatch for side %v", side)
				}
				break received
			default:
				r.Frame(t)
			}
		}
	}
}

func simEngrave(t *testing.T, plate engrave.Command) []mjolnir.Cmd {
	sim := mjolnir.NewSimulator()
	defer sim.Close()
	prog := &mjolnir.Program{}
	plate.Engrave(prog)
	prog.Prepare()
	errs := make(chan error, 1)
	go func() {
		errs <- mjolnir.Engrave(sim, prog, nil, nil)
	}()
	plate.Engrave(prog)
	if err := <-errs; err != nil {
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
