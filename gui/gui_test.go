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
	// Scan xpub as descriptor.
	ctxQR(t, ctx, p, "xpub6F148LnjUhGrHfEN6Pa8VkwF8L6FJqYALxAkuHfacfVhMLVY4MRuUVMxr9pguAv67DHx1YFxqoKN8s4QfZtD9sR2xRCffTqi9E8FiFLAYk8")
	frame()
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

func newTestEngraveScreen(t *testing.T, ctx *Context) *EngraveScreen {
	desc := twoOfThree.Descriptor
	const keyIdx = 0
	plate, err := engravePlate(desc, keyIdx, twoOfThree.Mnemonic)
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
			_, err := engravePlate(desc, 0, mnemonic)
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
	ctx := NewContext(p)
	scr := &ScanScreen{}
	ctx.events = append(ctx.events, testFrame{Err: errors.New("failed to open camera")})
	scr.Layout(ctx, op.Ctx{}, image.Point{})
	if scr.err == nil {
		t.Fatal("initial camera error not reported")
	}
	// Fail during streaming.
	scr = &ScanScreen{}
	// Connect.
	ctx.events = append(ctx.events, testFrame{Err: errors.New("error during streaming")})
	scr.Layout(ctx, op.Ctx{}, image.Point{})
	if scr.err == nil {
		t.Fatal("streaming camera error not reported")
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

func ctxQR(t *testing.T, ctx *Context, p *testPlatform, qrs ...string) {
	t.Helper()
	for _, qr := range qrs {
		ctx.events = append(ctx.events, qrFrame(t, p, qr))
	}
}

func TestSeedScreenScan(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	scr := NewEmptySeedScreen("")
	// Select camera.
	ctxButton(ctx, Down, Button3)
	ctxQR(t, ctx, p, "011513251154012711900771041507421289190620080870026613431420201617920614089619290300152408010643")
	scr.Layout(ctx, op.Ctx{}, &singleTheme, image.Point{})
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
	// Select camera.
	ctxButton(ctx, Down, Button3)
	ctxQR(t, ctx, p, "UR:CRYPTO-SEED/OYADGDIYWLAMAEJSZSWDWYTLTIFEENFTLNMNWKBDHNSSRO")
	scr.Layout(ctx, op.Ctx{}, &singleTheme, image.Point{})
	if scr.warning == nil {
		t.Error("SeedScreen accepted invalid seed")
	}
}

func NewSeedScreen(m bip39.Mnemonic) *SeedScreen {
	return &SeedScreen{
		Mnemonic: m,
	}
}

func TestSeedScreenInvalidSeed(t *testing.T) {
	p := newPlatform()
	ctx := NewContext(p)
	scr := &SeedScreen{}
	scr.Mnemonic = append(scr.Mnemonic, twoOfThree.Mnemonic...)
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

func TestSeed(t *testing.T) {
	const mnemonic = "doll clerk nice coast caught valid shallow taxi buyer economy lunch roof"

	r := newRunner(t)

	//Seed input method, keyboad input, select 12 words.
	r.Button(t, Button3, Button3, Button3)
	r.Frame(t)
	if r.app.scr.seed == nil {
		t.Fatal("not on seed screen")
	}

	m, err := bip39.ParseMnemonic(mnemonic)
	if err != nil {
		t.Fatal(err)
	}
	r.Mnemonic(t, m)

	// Accept seed, skip descriptor.
	r.Button(t, Button3, Down, Button3)

	// Accept descriptor, go to engrave.
	r.Button(t, Button3)
	r.Frame(t)
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
		Size:              backup.SmallPlate,
	}
	side, err := backup.EngraveSeed(mjolnir.Millimeter, mjolnir.StrokeWidth, seedDesc)
	if err != nil {
		t.Fatal(err)
	}
	testEngraving(t, r, r.app.scr.engrave, side)
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
		r.Frame(t)
		if r.app.scr.seed == nil {
			t.Fatal("not on seed screen")
		}

		m, err := bip39.ParseMnemonic(mnemonic)
		if err != nil {
			t.Fatal(err)
		}
		r.Mnemonic(t, m)

		// Accept seed, go to descriptor scan.
		r.Button(t, Button3, Button3)

		r.QR(t, func() { r.Frame(t) }, oneOfTwoDesc)

		// Accept descriptor, go to engrave.
		r.Button(t, Button3)
		r.Frame(t)
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
		descSide, err := backup.EngraveDescriptor(mjolnir.Millimeter, mjolnir.StrokeWidth, descPlate)
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
		seedSide, err := backup.EngraveSeed(mjolnir.Millimeter, mjolnir.StrokeWidth, seedDesc)
		if err != nil {
			t.Fatal(err)
		}
		plate := Plate{
			Size:  size,
			Sides: []engrave.Command{descSide, seedSide},
		}
		for _, side := range plate.Sides {
			testEngraving(t, r, r.app.scr.engrave, side)
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
		closed  chan []mjolnir.Cmd
		connErr error
		ioErr   error
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

func (*testPlatform) DisplaySize() image.Point {
	return image.Pt(1, 1)
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

func (p *testPlatform) Wakeup() {
}

func (p *testPlatform) Events() []Event {
	evts := p.events
	p.events = nil
	return evts
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

func (p *testPlatform) CameraFrame(dims image.Point) {
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

func (testFrame) ImplementsEvent() {}

type runner struct {
	p      *testPlatform
	app    *App
	frames int
}

func newPlatform() *testPlatform {
	return &testPlatform{}
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

func (r *runner) Frame(t *testing.T) {
	t.Helper()
	r.frames++
	if r.frames > 10000 {
		t.Fatal("test still incomplete after 10000 frames")
	}
	r.app.Frame()
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
	return testFrame{
		Img: frameImg,
	}
}

func (r *runner) QR(t *testing.T, frame func(), qrs ...string) {
	t.Helper()
	for _, qr := range qrs {
		r.p.events = append(r.p.events, qrFrame(t, r.p, qr))
	}
}

func (r *runner) Button(t *testing.T, bs ...Button) {
	t.Helper()
	for _, b := range bs {
		r.p.events = append(r.p.events, ButtonEvent{
			Button:  b,
			Pressed: true,
		}, ButtonEvent{
			Button:  b,
			Pressed: false,
		})
	}
}

func (r *runner) Press(t *testing.T, bs ...Button) {
	t.Helper()
	for _, b := range bs {
		r.p.events = append(r.p.events, ButtonEvent{
			Button:  b,
			Pressed: true,
		})
	}
}

func (r *runner) Mnemonic(t *testing.T, m bip39.Mnemonic) {
	for _, word := range m {
		for _, c := range strings.ToUpper(bip39.LabelFor(word)) {
			r.p.events = append(r.p.events, ButtonEvent{
				Button:  Rune,
				Rune:    c,
				Pressed: true,
			})
			r.Frame(t)
			if r.app.scr.seed.input.kbd.nvalid == 1 {
				r.Button(t, Button2)
				r.Frame(t)
				break
			}
		}
	}
	r.Frame(t)

	if sc := r.app.scr.seed; sc == nil || !sc.Mnemonic.Valid() {
		t.Fatalf("got invalid seed %v, wanted %v", sc.Mnemonic, m)

	}
	if got := r.app.scr.seed.Mnemonic; !reflect.DeepEqual(got, m) {
		t.Fatalf("got seed %v, wanted %v", got, m)
	}
}

func testEngraving(t *testing.T, r *runner, scr *EngraveScreen, side engrave.Command) {
	r.p.engrave.closed = make(chan []mjolnir.Cmd)
done:
	for {
		switch scr.instructions[scr.step].Type {
		case EngraveInstruction:
			break done
		case ConnectInstruction:
			// Hold connect.
			r.Press(t, Button3)
			r.p.timeOffset += confirmDelay
			r.Frame(t)
		default:
			r.Button(t, Button3)
			r.Frame(t)
		}
	}
received:
	for {
		select {
		case got := <-r.p.engrave.closed:
			// Verify the step is advanced after engrave completion.
			r.Frame(t)
			if scr.instructions[scr.step].Type == EngraveInstruction {
				t.Fatalf("instructions didn't progress part engraving screen")
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
