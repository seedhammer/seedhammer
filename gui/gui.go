// package gui implements the SeedHammer controller user interface.
package gui

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"log"
	"math"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	qr "github.com/seedhammer/kortschak-qr"
	"seedhammer.com/address"
	"seedhammer.com/backup"
	"seedhammer.com/bc/ur"
	"seedhammer.com/bc/urtypes"
	"seedhammer.com/bezier"
	"seedhammer.com/bip32"
	"seedhammer.com/bip380"
	"seedhammer.com/bip39"
	"seedhammer.com/bspline"
	"seedhammer.com/codex32"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
	"seedhammer.com/font/sh"
	"seedhammer.com/gui/assets"
	"seedhammer.com/gui/layout"
	"seedhammer.com/gui/op"
	"seedhammer.com/gui/saver"
	"seedhammer.com/gui/text"
	"seedhammer.com/gui/widget"
	"seedhammer.com/nonstandard"
	"seedhammer.com/seedqr"
	slip39words "seedhammer.com/slip39"
	"seedhammer.com/stepper"
)

var ErrTooLarge = errors.New("backup: data does not fit plate")

// safetyMargin is the distance in mm that must be kept free of
// engraving.
const safetyMargin = 3

type Context struct {
	Platform      Platform
	Styles        Styles
	Wakeup        time.Time
	Done          bool
	FrameCallback func()

	// Global UI state.
	Version        string
	LastDescriptor *bip380.Descriptor

	// scan is the last scanned object (seed, descriptor
	// etc.).
	scan scanResult

	Router EventRouter
}

func (c *Context) Frame() {
	if f := c.FrameCallback; f != nil {
		f()
	}
}

func NewContext(pl Platform) *Context {
	c := &Context{
		Platform: pl,
		Styles:   NewStyles(),
	}
	return c
}

func (c *Context) Scan() (scanResult, bool) {
	s := c.scan
	c.scan = scanResult{}
	return s, s.Object != nil || s.Status > scanIdle
}

func (c *Context) WakeupAt(t time.Time) {
	if c.Wakeup.IsZero() || t.Before(c.Wakeup) {
		c.Wakeup = t
	}
}

func (c *Context) Reset() {
	c.scan = scanResult{}
	c.Wakeup = time.Time{}
	// Immediately wake up to process remaining events.
	if c.Router.Reset() {
		c.Wakeup = time.Now()
	}
}

type InputTracker struct {
	Pressed [MaxButton]bool
	clicked [MaxButton]bool
	repeats [MaxButton]time.Time
}

func (t *InputTracker) Next(c *Context, filters ...Filter) (Event, bool) {
	now := time.Now()
	for _, btn := range []Button{Up, Down, Right, Left} {
		if !t.Pressed[btn] {
			t.repeats[btn] = time.Time{}
			continue
		}
		wakeup := t.repeats[btn]
		if wakeup.IsZero() {
			wakeup = now.Add(repeatStartDelay)
		}
		repeat := !now.Before(wakeup)
		if repeat {
			wakeup = now.Add(repeatDelay)
		}
		t.repeats[btn] = wakeup
		c.WakeupAt(wakeup)
		if repeat {
			return ButtonEvent{Button: btn, Pressed: true}.Event(), true
		}
	}

	e, ok := c.Router.Next(filters...)
	if !ok {
		return Event{}, false
	}
	if e, ok := e.AsButton(); ok {
		if int(e.Button) < len(t.clicked) {
			t.clicked[e.Button] = !e.Pressed && t.Pressed[e.Button]
			t.Pressed[e.Button] = e.Pressed
		}
	}
	return e, true
}

const widestWord = "TOMORROW"

type program int

const (
	backupWallet program = iota
)

type richText struct {
	Y int
}

func (r *richText) Add(ops op.Ctx, style text.Style, width int, col color.NRGBA, str string) {
	r.Addf(ops, style, width, col, "%s", str)
}

func (r *richText) Addf(ops op.Ctx, style text.Style, width int, col color.NRGBA, format string, args ...any) {
	m := style.Face.Metrics()
	offy := r.Y + m.Ascent.Ceil()
	lheight := style.LineHeight()
	l := &text.Layout{
		MaxWidth: width,
		Style:    style,
	}
	for {
		g, ok := l.Next(format, args...)
		if !ok {
			break
		}
		if g.Rune == '\n' {
			offy += lheight
			continue
		}
		off := image.Pt(g.Dot.Round(), offy)
		op.Offset(ops, off)
		op.GlyphOp(ops, style.Face, g.Rune)
		op.ColorOp(ops, col)
	}
	r.Y = offy + m.Descent.Ceil()
}

func ShowAddressesScreen(ctx *Context, ops op.Ctx, th *Colors, desc *bip380.Descriptor) {
	var s struct {
		addresses [2][]string
		page      int
		scroll    int
	}

	counter := 0
	for page := range len(s.addresses) {
		for len(s.addresses[page]) < 20 {
			var addr string
			var err error
			switch page {
			case 0:
				addr, err = address.Receive(desc, uint32(counter))
			case 1:
				addr, err = address.Change(desc, uint32(counter))
			}
			counter++
			if err != nil {
				// Very unlikely.
				continue
			}
			const addrLen = 12
			fmtAddr := fmt.Sprintf("%d: %s", len(s.addresses[page])+1, shortenAddress(addrLen, addr))
			s.addresses[page] = append(s.addresses[page], fmtAddr)
		}
	}

	const maxPage = len(s.addresses)
	inp := new(InputTracker)
	backBtn := &Clickable{Button: Button1}
	for !ctx.Done {
		scrollDelta := 0
		if backBtn.Clicked(ctx) {
			return
		}
		for {
			e, ok := inp.Next(ctx, ButtonFilter(Left), ButtonFilter(Right), ButtonFilter(Up), ButtonFilter(Down))
			if !ok {
				break
			}
			if e, ok := e.AsButton(); ok {
				switch e.Button {
				case Left:
					if e.Pressed {
						s.page = (s.page - 1 + maxPage) % maxPage
						s.scroll = 0
					}
				case Right:
					if e.Pressed {
						s.page = (s.page + 1) % maxPage
						s.scroll = 0
					}
				case Up:
					if e.Pressed {
						scrollDelta--
					}
				case Down:
					if e.Pressed {
						scrollDelta++
					}
				}
			}
		}
		op.ColorOp(ops, th.Background)
		dims := ctx.Platform.DisplaySize()

		// Title.
		r := layout.Rectangle{Max: dims}
		title := "Receive"
		if s.page == 1 {
			title = "Change"
		}
		layoutTitle(ctx, ops, dims.X, th.Text, title)

		op.ImageOp(ops.Begin(), assets.ArrowLeft, true)
		op.ColorOp(ops, th.Text)
		left := ops.End()

		op.ImageOp(ops.Begin(), assets.ArrowRight, true)
		op.ColorOp(ops, th.Text)
		right := ops.End()

		leftsz := assets.ArrowLeft.Bounds().Size()
		rightsz := assets.ArrowRight.Bounds().Size()

		content := r.Shrink(0, 12, 0, 12)
		body := content.Shrink(leadingSize, rightsz.X+12, 0, leftsz.X+12)
		inner := body.Shrink(scrollFadeDist, 0, scrollFadeDist, 0)

		op.Position(ops, left, content.W(leftsz))
		op.Position(ops, right, content.E(rightsz))

		var bodytxt richText
		ops.Begin()
		addrs := s.addresses[s.page]
		for _, addr := range addrs {
			ops := ops
			bodytxt.Add(ops, ctx.Styles.body, inner.Dx(), th.Text, addr)
		}
		addresses := ops.End()

		s.scroll += scrollDelta * body.Dy() / 2
		maxScroll := bodytxt.Y - inner.Dy()
		s.scroll = min(max(0, s.scroll), maxScroll)
		pos := inner.Min.Sub(image.Pt(0, s.scroll))
		op.Position(ops.Begin(), addresses, pos)
		fadeClip(ops, ops.End(), image.Rectangle(body))

		layoutNavigation(ops, th, dims, NavButton{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack})
		ctx.Frame()
	}
}

func shortenAddress(n int, addr string) string {
	if len(addr) <= n {
		return addr
	}
	return addr[:n/2] + "......" + addr[len(addr)-n/2:]
}

func descriptorKeyIdx(desc *bip380.Descriptor, m bip39.Mnemonic, pass string) (int, bool) {
	if len(desc.Keys) == 0 {
		return 0, false
	}
	network := desc.Keys[0].Network
	seed := bip39.MnemonicSeed(m, pass)
	mk, err := hdkeychain.NewMaster(seed, network)
	if err != nil {
		return 0, false
	}
	for i, k := range desc.Keys {
		xpub, err := bip32.Derive(mk, k.DerivationPath)
		if err != nil {
			// A derivation that generates an invalid key is by itself very unlikely,
			// but also means that the seed doesn't match this xpub.
			continue
		}
		if k.String() == xpub.String() {
			return i, true
		}
	}
	return 0, false
}

func deriveMasterKey(m bip39.Mnemonic, net *chaincfg.Params) (*hdkeychain.ExtendedKey, bool) {
	seed := bip39.MnemonicSeed(m, "")
	mk, err := hdkeychain.NewMaster(seed, net)
	// Err is only non-nil if the seed generates an invalid key, or we made a mistake.
	// According to [0] the odds of encountering a seed that generates
	// an invalid key by chance is 1 in 2^127.
	//
	// [0] https://bitcoin.stackexchange.com/questions/53180/bip-32-seed-resulting-in-an-invalid-private-key
	return mk, err == nil
}

// scaleRot is a specialized function for fast scaling and rotation of
// the camera frames for display.
func scaleRot(dst, src *image.Gray, rot180 bool) {
	db := dst.Bounds()
	sb := src.Bounds()
	if db.Empty() {
		return
	}
	scale := sb.Dx() / db.Dx()
	for y := 0; y < db.Dy(); y++ {
		sx := sb.Max.X - 1 - y*scale
		dy := db.Max.Y - y
		if rot180 {
			dy = y + db.Min.Y
		}
		for x := 0; x < db.Dx(); x++ {
			sy := x*scale + sb.Min.Y
			c := src.GrayAt(sx, sy)
			dx := db.Max.X - 1 - x
			if rot180 {
				dx = x + db.Min.X
			}
			dst.SetGray(dx, dy, c)
		}
	}
}

type QRDecoder struct {
	decoder   ur.Decoder
	nsdecoder nonstandard.Decoder
}

func (d *QRDecoder) Progress() int {
	progress := int(100 * d.decoder.Progress())
	if progress == 0 {
		progress = int(100 * d.nsdecoder.Progress())
	}
	return progress
}

func (d *QRDecoder) parseNonStandard(qr []byte) (any, bool) {
	if err := d.nsdecoder.Add(string(qr)); err != nil {
		d.nsdecoder = nonstandard.Decoder{}
		return qr, true
	}
	enc := d.nsdecoder.Result()
	if enc == nil {
		return nil, false
	}
	return enc, true
}

func (d *QRDecoder) parseQR(qr []byte) (any, bool) {
	uqr := strings.ToUpper(string(qr))
	if !strings.HasPrefix(uqr, "UR:") {
		d.decoder = ur.Decoder{}
		return d.parseNonStandard(qr)
	}
	d.nsdecoder = nonstandard.Decoder{}
	if err := d.decoder.Add(uqr); err != nil {
		// Incompatible fragment. Reset decoder and try again.
		d.decoder = ur.Decoder{}
		d.decoder.Add(uqr)
	}
	typ, enc, err := d.decoder.Result()
	if err != nil {
		d.decoder = ur.Decoder{}
		return nil, false
	}
	if enc == nil {
		return nil, false
	}
	d.decoder = ur.Decoder{}
	v, err := urtypes.Parse(typ, enc)
	if err != nil {
		return nil, true
	}
	return v, true
}

type ErrorScreen struct {
	Title string
	Body  string
	w     Warning
	ok    Clickable
}

func (s *ErrorScreen) Layout(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) bool {
	s.ok.Button = Button3
	if s.ok.Clicked(ctx) {
		return true
	}
	s.w.Layout(ctx, ops, th, dims, s.Title, s.Body)
	layoutNavigation(ops, th, dims, NavButton{Clickable: &s.ok, Style: StylePrimary, Icon: assets.IconCheckmark})
	return false
}

type ConfirmWarningScreen struct {
	Title string
	Body  string
	Icon  image.RGBA64Image

	cancelBtn  Clickable
	confirmBtn Clickable
	pressed    bool
	warning    Warning
	confirm    ConfirmDelay
}

type Warning struct {
	scroll  int
	txtclip int
	inp     InputTracker
}

type ConfirmResult int

const (
	ConfirmNone ConfirmResult = iota
	ConfirmNo
	ConfirmYes
)

type ConfirmDelay struct {
	timeout time.Time
}

func (c *ConfirmDelay) Start(ctx *Context, delay time.Duration) {
	c.timeout = time.Now().Add(delay)
}

func (c *ConfirmDelay) Progress(ctx *Context) float32 {
	if c.timeout.IsZero() {
		return 0.
	}
	now := time.Now()
	d := c.timeout.Sub(now)
	if d <= 0 {
		return 1.
	}
	ctx.Platform.Wakeup()
	return 1. - float32(d.Seconds()/confirmDelay.Seconds())
}

const confirmDelay = 1 * time.Second

func (w *Warning) Layout(ctx *Context, ops op.Ctx, th *Colors, dims image.Point, title, txt string) image.Point {
	for {
		e, ok := w.inp.Next(ctx, ButtonFilter(Up), ButtonFilter(Down))
		if !ok {
			break
		}
		if e, ok := e.AsButton(); ok {
			switch e.Button {
			case Up:
				if e.Pressed {
					w.scroll -= w.txtclip / 2
				}
			case Down:
				if e.Pressed {
					w.scroll += w.txtclip / 2
				}
			}
		}
	}
	const btnMargin = 4
	const boxMargin = 6

	op.ColorOp(ops, color.NRGBA{A: theme.overlayMask})

	wbbg := assets.WarningBoxBg
	wbout := assets.WarningBoxBorder
	ptop, pend, pbottom, pstart := wbbg.Padding()
	r := image.Rectangle{
		Min: image.Pt(pstart+boxMargin, ptop+boxMargin),
		Max: image.Pt(dims.X-pend-boxMargin, dims.Y-pbottom-boxMargin),
	}
	box := wbbg.Add(ops, r, true)
	op.ColorOp(ops, th.Background)
	wbout.Add(ops, r, true)
	op.ColorOp(ops, th.Text)

	btnOff := assets.NavBtnPrimary.Bounds().Dx() + btnMargin
	titlesz := widget.Labelw(ops.Begin(), ctx.Styles.warning, dims.X-btnOff*2, th.Text, title)
	titlew := ops.End()
	op.Position(ops, titlew, image.Pt((dims.X-titlesz.X)/2, r.Min.Y))

	bodyClip := image.Rectangle{
		Min: image.Pt(pstart+boxMargin, ptop+titlesz.Y),
		Max: image.Pt(dims.X-btnOff, dims.Y-pbottom-boxMargin),
	}
	bodysz := widget.Labelw(ops.Begin(), ctx.Styles.body, bodyClip.Dx(), th.Text, txt)
	body := ops.End()
	innerCtx := ops.Begin()
	w.txtclip = bodyClip.Dy()
	maxScroll := bodysz.Y - (bodyClip.Dy() - 2*scrollFadeDist)
	if w.scroll > maxScroll {
		w.scroll = maxScroll
	}
	if w.scroll < 0 {
		w.scroll = 0
	}
	op.Position(innerCtx, body, image.Pt(bodyClip.Min.X, bodyClip.Min.Y+scrollFadeDist-w.scroll))
	fadeClip(ops, ops.End(), image.Rectangle(bodyClip))

	return box.Bounds().Size()
}

func (s *ConfirmWarningScreen) Layout(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) ConfirmResult {
	cancelBtn := s.cancelBtn.For(Button1)
	confirmBtn := s.confirmBtn.For(Button3, Center)
	if cancelBtn.Clicked(ctx) {
		return ConfirmNo
	}
	for {
		if _, ok := confirmBtn.Next(ctx); !ok {
			break
		}
		if confirmBtn.Pressed != s.pressed {
			s.pressed = confirmBtn.Pressed
			if s.pressed {
				s.confirm.Start(ctx, confirmDelay)
			} else {
				s.confirm = ConfirmDelay{}
			}
		}
	}
	progress := s.confirm.Progress(ctx)
	if progress == 1 {
		return ConfirmYes
	}
	s.warning.Layout(ctx, ops, th, dims, s.Title, s.Body)
	layoutNavigation(ops, th, dims, []NavButton{
		{Clickable: cancelBtn, Style: StyleSecondary, Icon: assets.IconBack},
		{Clickable: confirmBtn, Style: StylePrimary, Icon: s.Icon, Progress: progress},
	}...)
	return ConfirmNone
}

type ProgressImage struct {
	Progress float32
	Src      image.RGBA64Image
}

func (p *ProgressImage) Add(ctx op.Ctx) {
	op.ParamImageOp(ctx, ProgressImageGen, true, p.Src.Bounds(), []any{p.Src}, []uint32{math.Float32bits(p.Progress)})
}

var ProgressImageGen = op.RegisterParameterizedImage(func(args op.ImageArguments, x, y int) color.RGBA64 {
	src := args.Refs[0].(image.RGBA64Image)
	progress := math.Float32frombits(args.Args[0])
	b := src.Bounds()
	c := b.Max.Add(b.Min).Div(2)
	d := image.Pt(x, y).Sub(c)
	angle := float32(math.Atan2(float64(d.X), float64(d.Y)))
	angle = math.Pi - angle
	if angle > 2*math.Pi*progress {
		return color.RGBA64{}
	}
	return src.RGBA64At(x, y)
})

func NewErrorScreen(err error) *ErrorScreen {
	switch {
	case errors.Is(err, ErrTooLarge):
		return &ErrorScreen{
			Title: "Too Large",
			Body:  "The descriptor cannot fit any plate size.",
		}
	default:
		return &ErrorScreen{
			Title: "Error",
			Body:  err.Error(),
		}
	}
}

func validateDescriptor(params engrave.Params, desc *bip380.Descriptor) ([]string, []Plate, error) {
	enc := desc.Encode()
	qrc, err := qr.Encode(desc.EncodeCompact(), qr.L)
	if err != nil {
		return nil, nil, err
	}
	const qrScale = 3
	type textEngraving struct {
		Label     string
		Paragraph backup.Paragraph
	}

	engravings := []textEngraving{
		{
			"TEXT + QR",
			backup.Paragraph{Text: enc, QR: qrc, QRScale: qrScale},
		},
		{
			"TEXT ONLY",
			backup.Paragraph{Text: enc},
		},
		{
			"QR ONLY",
			backup.Paragraph{QR: qrc, QRScale: qrScale},
		},
	}
	var validLabels []string
	var validEngravings []Plate

	var lastErr error
	for _, e := range engravings {
		descPlate := backup.Text{
			Paragraphs: []backup.Paragraph{e.Paragraph},
			Font:       sh.Font,
		}
		plan := backup.EngraveText(params, descPlate)
		plate, err := toPlate(plan, params)
		if err != nil {
			lastErr = err
			continue
		}
		validLabels = append(validLabels, e.Label)
		validEngravings = append(validEngravings, plate)
	}
	if len(validEngravings) == 0 {
		return nil, nil, lastErr
	}
	return validLabels, validEngravings, nil
}

type Plate struct {
	Attrs  bspline.Attributes
	Spline bspline.Curve
}

func engraveSeed(params engrave.Params, m bip39.Mnemonic) (Plate, error) {
	mfp, err := masterFingerprintFor(m, &chaincfg.MainNetParams)
	if err != nil {
		return Plate{}, err
	}
	qrc, err := qr.Encode(string(seedqr.QR(m)), qr.M)
	if err != nil {
		return Plate{}, err
	}
	words := make([]string, len(m))
	for i, w := range m {
		words[i] = bip39.LabelFor(w)
	}
	seedDesc := backup.Seed{
		Mnemonic:          words,
		ShortestWord:      bip39.ShortestWord,
		LongestWord:       bip39.LongestWord,
		QR:                qrc,
		MasterFingerprint: mfp,
		Font:              constant.Font,
	}
	seedSide, err := backup.EngraveSeed(params, seedDesc)
	if err != nil {
		return Plate{}, err
	}
	return toPlate(seedSide, params)
}

func masterFingerprintFor(m bip39.Mnemonic, network *chaincfg.Params) (uint32, error) {
	mk, ok := deriveMasterKey(m, network)
	if !ok {
		return 0, errors.New("failed to derive mnemonic master key")
	}
	pkey, err := mk.ECPubKey()
	if err != nil {
		return 0, err
	}
	return bip32.Fingerprint(pkey), nil
}

func plateImage(p PlateSize) image.RGBA64Image {
	switch p {
	case SquarePlate:
		return assets.Sh02
	default:
		panic("unsupported plate")
	}
}

func plateName(p PlateSize) string {
	switch p {
	case SquarePlate:
		return "SH02"
	default:
		panic("unsupported plate")
	}
}

type InstructionType int

const (
	PrepareInstruction InstructionType = iota
	ConnectInstruction
	EngraveInstruction
)

type Instruction struct {
	Body  string
	Lead  string
	Type  InstructionType
	Image image.RGBA64Image

	resolvedBody string
}

var (
	EngraveSideASimple = []Instruction{
		{
			Body: "Insert a blank plate and close the lock.",
		},
		{
			Body: "Hold button to start the engraving process. The process is loud, use hearing protection.",
			Type: ConnectInstruction,
		},
		{
			Lead: "Engraving plate",
			Type: EngraveInstruction,
		},
		{
			Body: "Engraving completed successfully.",
		},
	}
)

func isEmptyMnemonic(m bip39.Mnemonic) bool {
	for _, w := range m {
		if w != -1 {
			return false
		}
	}
	return true
}

func emptySLIP39Mnemonic(nwords int) slip39words.Mnemonic {
	m := make(slip39words.Mnemonic, nwords)
	for i := range m {
		m[i] = -1
	}
	return m
}

func emptyBIP39Mnemonic(nwords int) bip39.Mnemonic {
	m := make(bip39.Mnemonic, nwords)
	for i := range m {
		m[i] = -1
	}
	return m
}

const scrollFadeDist = 16

func fadeClip(ops op.Ctx, w op.CallOp, r image.Rectangle) {
	op.ParamImageOp(ops, scrollMask, true, r, nil, nil)
	op.Position(ops, w, image.Pt(0, 0))
}

var scrollMask = op.RegisterParameterizedImage(func(args op.ImageArguments, x, y int) color.RGBA64 {
	alpha := 0xffff
	if d := y - args.Bounds.Min.Y; d < scrollFadeDist {
		alpha = 0xffff * d / scrollFadeDist
	} else if d := args.Bounds.Max.Y - y; d < scrollFadeDist {
		alpha = 0xffff * d / scrollFadeDist
	}
	a16 := uint16(alpha)
	return color.RGBA64{A: a16}
})

const wordKeys = "qwertyuiop\nasdfghjkl\nzxcvbnm"

func inputWordsFlow(ctx *Context, ops op.Ctx, th *Colors, mnemonic bip39.Mnemonic, selected int) {
	kbd := NewKeyboard(ctx, wordKeys)
	wordLabel := ""
	backBtn := &Clickable{Button: Button1}
	okBtn := &Clickable{Button: Button2}
	layoutWord := func(ops op.Ctx, n int, word string) image.Point {
		style := ctx.Styles.word
		return widget.Labelf(ops, style, th.Background, "%2d: %s", n, word)
	}
	longest := layoutWord(op.Ctx{}, 24, widestWord)
	var nvalid int
	for !ctx.Done {
		for kbd.Update(ctx) {
			nvalid = updateValidBIP39Keys(kbd.Fragment, kbd.allKeys)
			wordLabel = kbd.Fragment
			if completedWord, complete := completeBIP39Word(wordLabel, nvalid); complete {
				wordLabel = bip39.LabelFor(completedWord)
			}
			wordLabel = strings.ToUpper(wordLabel)
		}
		if backBtn.Clicked(ctx) {
			return
		}
		for okBtn.Clicked(ctx) {
			w, complete := completeBIP39Word(kbd.Fragment, nvalid)
			if !complete {
				continue
			}
			kbd.Clear()
			wordLabel = ""
			nvalid = updateValidBIP39Keys("", kbd.allKeys)
			mnemonic[selected] = w
			for {
				selected++
				if selected == len(mnemonic) {
					return
				}
				if mnemonic[selected] == -1 {
					break
				}
			}
		}
		dims := ctx.Platform.DisplaySize()
		op.ColorOp(ops, th.Background)
		layoutTitle(ctx, ops, dims.X, th.Text, "Input Words")

		screen := layout.Rectangle{Max: dims}
		_, content := screen.CutTop(leadingSize)
		content, _ = content.CutBottom(8)

		kbdsz := kbd.Layout(ctx, ops.Begin(), th)
		op.Position(ops, ops.End(), content.S(kbdsz))

		layoutWord(ops.Begin(), selected+1, wordLabel)
		word := ops.End()
		r := image.Rectangle{Max: longest}
		r.Min.Y -= 3
		assets.ButtonFocused.Add(ops.Begin(), r, true)
		op.ColorOp(ops, th.Text)
		word.Add(ops)
		top, _ := content.CutBottom(kbdsz.Y)
		op.Position(ops, ops.End(), top.Center(longest))

		layoutNavigation(ops, th, dims, []NavButton{{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack}}...)
		if _, complete := completeBIP39Word(kbd.Fragment, nvalid); complete {
			layoutNavigation(ops, th, dims, []NavButton{{Clickable: okBtn, Style: StylePrimary, Icon: assets.IconCheckmark}}...)
		}
		ctx.Frame()
	}
}

func inputCodex32Flow(ctx *Context, ops op.Ctx, th *Colors) (codex32.String, bool) {
	const alph = "1234567890\nqwertyup\nasdfghjk\nlzxcvnm"

	kbd := NewKeyboard(ctx, alph)
	backBtn := &Clickable{Button: Button1}
	okBtn := &Clickable{Button: Button2}
	frag := ""
	var share codex32.String
	valid := false
	for !ctx.Done {
		for kbd.Update(ctx) {
			frag = strings.ToUpper(kbd.Fragment)
			s, err := codex32.New(frag)
			share, valid = s, err == nil
		}
		if backBtn.Clicked(ctx) {
			break
		}
		if valid && okBtn.Clicked(ctx) {
			return share, true
		}
		dims := ctx.Platform.DisplaySize()
		op.ColorOp(ops, th.Background)
		layoutTitle(ctx, ops, dims.X, th.Text, "Input Codex32 Share")

		screen := layout.Rectangle{Max: dims}
		_, content := screen.CutTop(leadingSize)
		content, _ = content.CutBottom(8)

		kbdsz := kbd.Layout(ctx, ops.Begin(), th)
		op.Position(ops, ops.End(), content.S(kbdsz))

		frgSize := widget.Labelw(ops.Begin(), ctx.Styles.word, dims.X-50, th.Background, frag)
		frgSize.X = max(frgSize.X, 100)
		word := ops.End()
		r := image.Rectangle{Max: frgSize}
		r.Min.Y -= 3
		assets.ButtonFocused.Add(ops.Begin(), r, true)
		op.ColorOp(ops, th.Text)
		word.Add(ops)
		top, _ := content.CutBottom(kbdsz.Y)
		op.Position(ops, ops.End(), top.Center(frgSize))

		layoutNavigation(ops, th, dims, []NavButton{{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack}}...)
		if valid {
			layoutNavigation(ops, th, dims, []NavButton{{Clickable: okBtn, Style: StylePrimary, Icon: assets.IconCheckmark}}...)
		}
		ctx.Frame()
	}
	return codex32.String{}, false
}

func inputSLIP39Flow(ctx *Context, ops op.Ctx, th *Colors, mnemonic slip39words.Mnemonic, selected int) bool {
	kbd := NewKeyboard(ctx, wordKeys)
	wordLabel := ""
	backBtn := &Clickable{Button: Button1}
	okBtn := &Clickable{Button: Button2}
	layoutWord := func(ops op.Ctx, n int, word string) image.Point {
		style := ctx.Styles.word
		return widget.Labelf(ops, style, th.Background, "%2d: %s", n, word)
	}
	const (
		widestWord = "WITHDRAW"
	)
	longest := layoutWord(op.Ctx{}, len(mnemonic), widestWord)
	var nvalid int
	for !ctx.Done {
		for kbd.Update(ctx) {
			nvalid = updateValidSLIP39Keys(kbd.Fragment, kbd.allKeys)
			wordLabel = kbd.Fragment
			if completedWord, complete := completeSLIP39Word(wordLabel, nvalid); complete {
				wordLabel = slip39words.LabelFor(completedWord)
			}
			wordLabel = strings.ToUpper(wordLabel)
		}
		if backBtn.Clicked(ctx) {
			break
		}
		for okBtn.Clicked(ctx) {
			w, complete := completeSLIP39Word(kbd.Fragment, nvalid)
			if !complete {
				continue
			}
			kbd.Clear()
			wordLabel = ""
			nvalid = updateValidSLIP39Keys("", kbd.allKeys)
			mnemonic[selected] = w
			for {
				selected++
				if selected == len(mnemonic) {
					return true
				}
				if mnemonic[selected] == -1 {
					break
				}
			}
		}
		dims := ctx.Platform.DisplaySize()
		op.ColorOp(ops, th.Background)
		layoutTitle(ctx, ops, dims.X, th.Text, "Input Words")

		screen := layout.Rectangle{Max: dims}
		_, content := screen.CutTop(leadingSize)
		content, _ = content.CutBottom(8)

		kbdsz := kbd.Layout(ctx, ops.Begin(), th)
		op.Position(ops, ops.End(), content.S(kbdsz))

		layoutWord(ops.Begin(), selected+1, wordLabel)
		word := ops.End()
		r := image.Rectangle{Max: longest}
		r.Min.Y -= 3
		assets.ButtonFocused.Add(ops.Begin(), r, true)
		op.ColorOp(ops, th.Text)
		word.Add(ops)
		top, _ := content.CutBottom(kbdsz.Y)
		op.Position(ops, ops.End(), top.Center(longest))

		layoutNavigation(ops, th, dims, []NavButton{{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack}}...)
		if _, complete := completeSLIP39Word(kbd.Fragment, nvalid); complete {
			layoutNavigation(ops, th, dims, []NavButton{{Clickable: okBtn, Style: StylePrimary, Icon: assets.IconCheckmark}}...)
		}
		ctx.Frame()
	}
	return false
}

type Keyboard struct {
	Fragment string

	keys      [][]keyboardKey
	widest    image.Point
	backspace image.Point
	size      image.Point

	row, col int
	inp      InputTracker

	allKeys []keyboardKey
}

type keyboardKey struct {
	r        rune
	disabled bool
	pos      image.Point
	clk      Clickable
}

func NewKeyboard(ctx *Context, alphabet string) *Keyboard {
	// Add backspace and end row.
	alphabet += "⌫\n"

	k := new(Keyboard)
	k.widest = ctx.Styles.keyboard.Measure(math.MaxInt, "W")
	bsb := assets.KeyBackspace.Bounds()
	bsWidth := bsb.Min.X*2 + bsb.Dx()
	k.backspace = image.Pt(bsWidth, k.widest.Y)
	bgbnds := assets.Key.Bounds(image.Rectangle{Max: k.widest})
	const margin = 2
	bgsz := bgbnds.Size().Add(image.Pt(margin, margin))
	longest := 0
	prevIdx := 0
	for _, r := range alphabet {
		if r == '\n' {
			row := k.allKeys[prevIdx:]
			prevIdx = len(k.allKeys)
			k.keys = append(k.keys, row)
			longest = max(longest, len(row))
			continue
		}
		k.allKeys = append(k.allKeys, keyboardKey{r: r})
	}
	maxw := longest*bgsz.X - margin
	allKeys := k.allKeys[:]
	for i, row := range k.keys {
		n := len(row)
		if i == len(k.keys)-1 {
			// Center row without the backspace key.
			n--
		}
		w := bgsz.X*n - margin
		off := image.Pt((maxw-w)/2, 0)
		k.keys[i] = allKeys[:len(row)]
		allKeys = allKeys[len(row):]
		for j := range row {
			pos := image.Pt(j*bgsz.X, i*bgsz.Y)
			pos = pos.Add(off)
			pos = pos.Sub(bgbnds.Min)
			k.keys[i][j].pos = pos
		}
	}
	k.size = image.Point{
		X: maxw,
		Y: len(k.keys)*bgsz.Y - margin,
	}
	k.Clear()
	return k
}

func (k *Keyboard) Clear() {
	k.Fragment = ""
	k.row = len(k.keys) / 2
	k.col = len(k.keys[k.row]) / 2
}

func completeSLIP39Word(frag string, nvalid int) (slip39words.Word, bool) {
	w, ok := slip39words.ClosestWord(frag)
	if !ok {
		return -1, false
	}
	// The word is complete if it's in the word list or is the only option.
	return w, nvalid == 1 || frag == slip39words.LabelFor(w)
}

func completeBIP39Word(frag string, nvalid int) (bip39.Word, bool) {
	w, ok := bip39.ClosestWord(frag)
	if !ok {
		return -1, false
	}
	// The word is complete if it's in the word list or is the only option.
	return w, nvalid == 1 || frag == bip39.LabelFor(w)
}

func updateValidBIP39Keys(frag string, keys []keyboardKey) int {
	mask := ^uint32(0)
	w, valid := bip39.ClosestWord(frag)
	if !valid {
		panic("invalid fragment")
	}
	nvalid := 0
	for ; w < bip39.NumWords; w++ {
		bip39w := bip39.LabelFor(w)
		if !strings.HasPrefix(bip39w, frag) {
			break
		}
		nvalid++
		suffix := bip39w[len(frag):]
		if len(suffix) > 0 {
			idx := suffix[0] - 'a'
			mask &^= 1 << idx
		}
	}
	if nvalid == 1 {
		mask = ^uint32(0)
	}
	updateValidKeys(mask, keys)
	return nvalid
}

func updateValidSLIP39Keys(frag string, keys []keyboardKey) int {
	mask := ^uint32(0)
	w, valid := slip39words.ClosestWord(frag)
	if !valid {
		panic("invalid fragment")
	}
	nvalid := 0
	for ; w < slip39words.NumWords; w++ {
		bip39w := slip39words.LabelFor(w)
		if !strings.HasPrefix(bip39w, frag) {
			break
		}
		nvalid++
		suffix := bip39w[len(frag):]
		if len(suffix) > 0 {
			idx := suffix[0] - 'a'
			mask &^= 1 << idx
		}
	}
	if nvalid == 1 {
		mask = ^uint32(0)
	}
	updateValidKeys(mask, keys)
	return nvalid
}

func updateValidKeys(mask uint32, keys []keyboardKey) {
	for i := range keys {
		key := &keys[i]
		idx := key.r - 'a'
		if idx < 0 || idx >= 32 {
			continue
		}
		key.disabled = mask&(1<<idx) != 0
	}
}

func (k *Keyboard) Valid(key keyboardKey) bool {
	if key.r == '⌫' {
		return len(k.Fragment) > 0
	}
	return !key.disabled
}

func (k *Keyboard) Update(ctx *Context) bool {
	k.adjust(k.keys[k.row][k.col].r == '⌫')
	for i, row := range k.keys {
		for j := range row {
			key := &row[j]
			if k.Valid(*key) && key.clk.Clicked(ctx) {
				k.row, k.col = i, j
				k.rune()
				return true
			}
		}
	}
	for {
		e, ok := k.inp.Next(ctx, ButtonFilter(Left), ButtonFilter(Right), ButtonFilter(Up), ButtonFilter(Down), ButtonFilter(Center), RuneFilter(), ButtonFilter(Button3))
		if !ok {
			break
		}
		if e, ok := e.AsButton(); ok {
			if !e.Pressed {
				continue
			}
			switch e.Button {
			case Left:
				next := k.col
				for {
					next--
					if next == -1 {
						next = len(k.keys[k.row]) - 1
					}
					if !k.Valid(k.keys[k.row][next]) {
						continue
					}
					k.col = next
					k.adjust(true)
					break
				}
			case Right:
				next := k.col
				for {
					next++
					if next == len(k.keys[k.row]) {
						next = 0
					}
					if !k.Valid(k.keys[k.row][next]) {
						continue
					}
					k.col = next
					k.adjust(true)
					break
				}
			case Up:
				n := len(k.keys)
				next := k.row
				for {
					next = (next - 1 + n) % n
					if k.adjustCol(next) {
						k.adjust(true)
						break
					}
				}
			case Down:
				n := len(k.keys)
				next := k.row
				for {
					next = (next + 1) % n
					if k.adjustCol(next) {
						k.adjust(true)
						break
					}
				}
			case Center, Button3:
				k.rune()
				return true
			}
		}
		if e, ok := e.AsRune(); ok {
			r := unicode.ToLower(e.Rune)
			for i, row := range k.keys {
				for j, key := range row {
					if key.r == r && k.Valid(key) {
						k.row, k.col = i, j
						k.rune()
						return true
					}
				}
			}
		}
	}
	return false
}

func (k *Keyboard) rune() {
	r := k.keys[k.row][k.col].r
	if r == '⌫' {
		_, n := utf8.DecodeLastRuneInString(k.Fragment)
		k.Fragment = k.Fragment[:len(k.Fragment)-n]
	} else {
		k.Fragment = k.Fragment + string(r)
	}
}

// adjust resets the row and column to the nearest valid key, if any.
func (k *Keyboard) adjust(allowBackspace bool) {
	dist := int(1e6)
	current := k.keys[k.row][k.col].pos
	found := false
	for i, row := range k.keys {
		j := 0
		for _, key := range row {
			if !k.Valid(key) || key.r == '⌫' && !allowBackspace {
				j++
				continue
			}
			p := key.pos
			d := p.Sub(current)
			d2 := d.X*d.X + d.Y*d.Y
			if d2 < dist {
				dist = d2
				k.row, k.col = i, j
				found = true
			}
			j++
		}
	}
	// Only if no other key was found, select backspace.
	if !found {
		k.row = len(k.keys) - 1
		k.col = len(k.keys[k.row]) - 1
	}
}

// adjustCol sets the column to the one nearest the x position.
func (k *Keyboard) adjustCol(row int) bool {
	dist := int(1e6)
	found := false
	x := k.keys[k.row][k.col].pos.X
	for i, r := range k.keys[row] {
		if !k.Valid(r) {
			continue
		}
		p := k.keys[row][i].pos
		found = true
		k.row = row
		d := p.X - x
		if d < 0 {
			d = -d
		}
		if d < dist {
			dist = d
			k.col = i
		}
	}
	return found
}

func (k *Keyboard) Layout(ctx *Context, ops op.Ctx, th *Colors) image.Point {
	for i, row := range k.keys {
		for j, key := range row {
			valid := k.Valid(key)
			bg := assets.Key
			bgsz := k.widest
			if key.r == '⌫' {
				bgsz = k.backspace
			}
			bgcol := th.Text
			style := ctx.Styles.keyboard
			col := th.Text
			switch {
			case !valid:
				bgcol.A = theme.inactiveMask
				col = bgcol
			case i == k.row && j == k.col:
				bg = assets.KeyActive
				col = th.Background
			}
			var sz image.Point
			if key.r == '⌫' {
				icn := assets.KeyBackspace
				sz = image.Pt(k.backspace.X, icn.Bounds().Dy())
				op.ImageOp(ops.Begin(), icn, true)
				op.ColorOp(ops, col)
			} else {
				sz = widget.Labelf(ops.Begin(), style, col, "%c", unicode.ToUpper(key.r))
			}
			key := ops.End()
			bgr := image.Rectangle{Max: bgsz}
			op.ClipOp(bg.Bounds(bgr)).Add(ops.Begin())
			op.InputOp(ops, &k.keys[i][j].clk)
			bg.Add(ops, bgr, true)
			op.ColorOp(ops, bgcol)
			op.Position(ops, key, bgsz.Sub(sz).Div(2))
			op.Position(ops, ops.End(), k.keys[i][j].pos)
		}
	}
	return k.size
}

type ChoiceScreen struct {
	Title    string
	Lead     string
	Choices  []string
	children []Choice
	choice   int
}

type Choice struct {
	Size  image.Point
	W     op.CallOp
	click Clickable
}

func (s *ChoiceScreen) Choose(ctx *Context, ops op.Ctx, th *Colors) (int, bool) {
	inp := new(InputTracker)
	cancelBtn := &Clickable{Button: Button1}
	chooseBtn := &Clickable{Button: Button3, AltButton: Center}
frames:
	for !ctx.Done {
		switch {
		case cancelBtn.Clicked(ctx):
			break frames
		case chooseBtn.Clicked(ctx):
			return s.choice, true
		}
		for i := range s.children {
			c := &s.children[i]
			if c.click.Clicked(ctx) {
				s.choice = i
			}
		}
		for {
			e, ok := inp.Next(ctx, ButtonFilter(Up), ButtonFilter(Down))
			if !ok {
				break
			}
			if e, ok := e.AsButton(); ok {
				switch e.Button {
				case Up:
					if e.Pressed {
						if s.choice > 0 {
							s.choice--
						}
					}
				case Down:
					if e.Pressed {
						if s.choice < len(s.Choices)-1 {
							s.choice++
						}
					}
				}
			}
		}

		dims := ctx.Platform.DisplaySize()
		s.Draw(ctx, ops, th, dims)

		layoutNavigation(ops, th, dims, []NavButton{
			{Clickable: cancelBtn, Style: StyleSecondary, Icon: assets.IconBack},
			{Clickable: chooseBtn, Style: StylePrimary, Icon: assets.IconCheckmark},
		}...)
		ctx.Frame()
	}
	return 0, false
}

func (s *ChoiceScreen) Draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) {
	r := layout.Rectangle{Max: dims}
	op.ColorOp(ops, th.Background)

	layoutTitle(ctx, ops, dims.X, th.Text, s.Title)

	_, bottom := r.CutTop(leadingSize)
	sz := widget.Labelw(ops.Begin(), ctx.Styles.lead, dims.X-2*8, th.Text, s.Lead)
	content, lead := bottom.CutBottom(leadingSize)
	op.Position(ops, ops.End(), lead.Center(sz))

	content = content.Shrink(16, 0, 16, 0)

	if len(s.children) != len(s.Choices) {
		s.children = make([]Choice, len(s.Choices))
	}
	maxW := 0
	for i, c := range s.Choices {
		style := ctx.Styles.button
		col := th.Text
		if i == s.choice {
			col = th.Background
		}
		sz := widget.Label(ops.Begin(), style, col, c)
		ch := ops.End()
		s.children[i].Size = sz
		s.children[i].W = ch
		if sz.X > maxW {
			maxW = sz.X
		}
	}

	inner := ops.Begin()
	h := 0
	focusBg := assets.ButtonFocused
	for i := range s.children {
		c := &s.children[i]
		xoff := (maxW - c.Size.X) / 2
		pos := image.Pt(xoff, h)
		txt := c.W
		bg := image.Rectangle{Max: c.Size}
		bg.Min.X -= xoff
		bg.Max.X += xoff
		if i == s.choice {
			focusBg.Add(inner.Begin(), bg, true)
			op.ColorOp(inner, th.Text)
			txt.Add(inner)
			txt = inner.End()
		}
		op.Offset(inner, pos)
		op.ClipOp(focusBg.Bounds(bg)).Add(inner)
		op.InputOp(inner, &c.click)
		op.Position(inner, txt, pos)
		h += c.Size.Y
	}
	op.Position(ops, ops.End(), content.Center(image.Pt(maxW, h)))
}

type MainScreen struct {
	page        program
	scanTimeout time.Time
	scanStatus  scanStatus
}

func (m *MainScreen) showError(ctx *Context, ops op.Ctx, title, content string) {
	ws := &ErrorScreen{
		Title: title,
		Body:  content,
	}
	th := m.theme()
	for !ctx.Done {
		dims := ctx.Platform.DisplaySize()
		res := ws.Layout(ctx, ops.Begin(), th, dims)
		if res {
			break
		}
		dialog := ops.End()
		m.draw(ctx, ops, dims)
		dialog.Add(ops)
		ctx.Frame()
	}
}

const scanStatusTimeout = 1 * time.Second

func (m *MainScreen) Flow(ctx *Context, ops op.Ctx) {
	inp := new(InputTracker)
	selectBtn := &Clickable{Button: Button3, AltButton: Center}
	for !ctx.Done {
		if selectBtn.Clicked(ctx) {
			m.selectedFlow(ctx, ops)
		}
		if scan, ok := ctx.Scan(); ok {
			if time.Now().Before(m.scanTimeout) {
				m.scanStatus = max(m.scanStatus, scan.Status)
			} else {
				m.scanStatus = scan.Status
			}
			th := &descriptorTheme
			m.scanTimeout = time.Now().Add(scanStatusTimeout)
			if cnt := scan.Object; cnt != nil {
				switch cnt := cnt.(type) {
				case debugCommand:
					switch cmd := cnt.Command; cmd {
					case "FOREVERLAURA!":
						qaEngraveFlow(ctx, ops)
						continue
					case "lock-boot":
						m.scanStatus = scanIdle
						if err := ctx.Platform.LockBoot(); err != nil {
							log.Printf("lock-boot: %v", err)
							m.scanStatus = scanFailed
						}
						continue
					default:
						log.Printf("unknown debug command: %q", cmd)
					}
				}
				if !engraveObjectFlow(ctx, ops, th, cnt) {
					m.scanStatus = scanUnknownFormat
				}
			}
		}
		for {
			e, ok := inp.Next(ctx,
				ButtonFilter(Left),
				ButtonFilter(Right),
			)
			if !ok {
				break
			}
			if e, ok := e.AsButton(); ok {
				switch e.Button {
				case Left:
					if !e.Pressed {
						break
					}
					m.page--
					if m.page < 0 {
						m.page = backupWallet
					}
				case Right:
					if !e.Pressed {
						break
					}
					m.page++
					if m.page > backupWallet {
						m.page = 0
					}
				}
			}
		}
		dims := ctx.Platform.DisplaySize()
		m.draw(ctx, ops, dims)
		layoutNavigation(ops, m.theme(), dims,
			NavButton{Clickable: selectBtn, Style: StylePrimary, Icon: assets.IconCheckmark},
		)
		ctx.Frame()
	}
}

func (m *MainScreen) theme() *Colors {
	switch m.page {
	case backupWallet:
		return &descriptorTheme
	default:
		panic("invalid page")
	}
}

func (m *MainScreen) draw(ctx *Context, ops op.Ctx, dims image.Point) {
	var title string
	th := m.theme()
	switch m.page {
	case backupWallet:
		title = "Backup Wallet"
	}
	op.ColorOp(ops, th.Background)

	layoutTitle(ctx, ops, dims.X, th.Text, title)

	r := layout.Rectangle{Max: dims}
	sz := m.layout(ops.Begin(), dims.X)
	op.Position(ops, ops.End(), r.Center(sz))

	sz = layoutMainPager(ops.Begin(), th, m.page)
	_, middle := r.CutBottom(leadingSize)
	op.Position(ops, ops.End(), middle.Center(sz))
	sttxt := ""
	if time.Now().Before(m.scanTimeout) {
		ctx.WakeupAt(m.scanTimeout)
		switch m.scanStatus {
		case scanFailed:
			sttxt = "Scan error"
		case scanOverflow:
			sttxt = "Content too large"
		case scanStarted:
			sttxt = "Scanning..."
		case scanUnknownFormat:
			sttxt = "Unknown format"
		}
	}
	stsz := widget.Labelw(ops.Begin(), ctx.Styles.subtitle, 300, th.Text, sttxt)
	op.Position(ops, ops.End(), r.S(stsz).Sub(image.Pt(0, 16)))

	versz := widget.Labelw(ops.Begin(), ctx.Styles.debug, 200, th.Text, ctx.Version)
	op.Position(ops, ops.End(), r.SE(versz.Add(image.Pt(4, 0))))
	shsz := widget.Labelw(ops.Begin(), ctx.Styles.debug, 100, th.Text, "SeedHammer")
	op.Position(ops, ops.End(), r.SW(shsz).Add(image.Pt(3, 0)))
}

func layoutTitle(ctx *Context, ops op.Ctx, width int, col color.NRGBA, title string) image.Rectangle {
	return layoutTitlef(ctx, ops, width, col, "%s", title)
}

func layoutTitlef(ctx *Context, ops op.Ctx, width int, col color.NRGBA, format string, args ...any) image.Rectangle {
	const margin = 8
	sz := widget.Labelwf(ops.Begin(), ctx.Styles.title, width-2*16, col, format, args...)
	pos := image.Pt((width-sz.X)/2, margin)
	op.Position(ops, ops.End(), pos)
	return image.Rectangle{
		Min: pos,
		Max: pos.Add(sz),
	}
}

type ButtonStyle int

const (
	StyleNone ButtonStyle = iota
	StyleSecondary
	StylePrimary
)

type NavButton struct {
	Clickable *Clickable
	Style     ButtonStyle
	Icon      image.Image
	Progress  float32
}

func layoutNavigation(ops op.Ctx, th *Colors, dims image.Point, btns ...NavButton) image.Rectangle {
	navsz := assets.NavBtnPrimary.Bounds().Size()
	button := func(ops op.Ctx, b NavButton, t op.Tag, pressed bool) {
		if b.Style == StyleNone {
			return
		}
		op.ClipOp(assets.NavBtnPrimary.Bounds()).Add(ops)
		op.InputOp(ops, t)
		switch b.Style {
		case StyleSecondary:
			op.ImageOp(ops, assets.NavBtnPrimary, true)
			op.ColorOp(ops, th.Background)
			op.ImageOp(ops, assets.NavBtnSecondary, true)
			op.ColorOp(ops, th.Text)
		case StylePrimary:
			op.ImageOp(ops, assets.NavBtnPrimary, true)
			op.ColorOp(ops, th.Primary)
		}
		if b.Progress > 0 {
			(&ProgressImage{
				Progress: b.Progress,
				Src:      assets.IconProgress,
			}).Add(ops)
		} else {
			op.ImageOp(ops, b.Icon, true)
		}
		switch b.Style {
		case StyleSecondary:
			op.ColorOp(ops, th.Text)
		case StylePrimary:
			op.ColorOp(ops, th.Text)
		}
		if b.Progress == 0 && pressed {
			op.ImageOp(ops, assets.NavBtnPrimary, true)
			op.ColorOp(ops, color.NRGBA{A: theme.activeMask})
		}
	}
	btnsz := assets.NavBtnPrimary.Bounds().Size()
	ys := [3]int{
		leadingSize,
		(dims.Y - btnsz.Y) / 2,
		dims.Y - leadingSize - btnsz.Y,
	}
	var r image.Rectangle
	for _, b := range btns {
		clk := b.Clickable
		idx := int(clk.Button - Button1)
		pressed := clk.Pressed && clk.Entered
		button(ops.Begin(), b, clk, pressed)
		y := ys[idx]
		pos := image.Pt(dims.X-btnsz.X, y)
		op.Position(ops, ops.End(), pos)
		r = r.Union(image.Rectangle{
			Min: pos,
			Max: pos.Add(navsz),
		})
	}
	return r
}

func (m *MainScreen) layout(ops op.Ctx, width int) image.Point {
	th := m.theme()
	op.ImageOp(ops.Begin(), assets.ArrowLeft, true)
	op.ColorOp(ops, th.Text)
	left := ops.End()
	var h layout.Align
	leftsz := h.Add(assets.ArrowLeft.Bounds().Size())

	op.ImageOp(ops.Begin(), assets.ArrowRight, true)
	op.ColorOp(ops, th.Text)
	right := ops.End()
	rightsz := h.Add(assets.ArrowRight.Bounds().Size())

	contentsz := h.Add(layoutMainPlates(ops.Begin(), m.page))
	content := ops.End()

	const margin = 16

	op.Position(ops, content, image.Pt((width-contentsz.X)/2, 8+h.Y(contentsz)))
	const npage = int(backupWallet) + 1
	if npage > 1 {
		op.Position(ops, left, image.Pt(margin, h.Y(leftsz)))
		op.Position(ops, right, image.Pt(width-margin-rightsz.X, h.Y(rightsz)))
	}

	return image.Pt(width, h.Size.Y)
}

func layoutMainPlates(ops op.Ctx, page program) image.Point {
	switch page {
	case backupWallet:
		img := assets.Hammer
		op.ImageOp(ops, img, false)
		return img.Bounds().Size()
	}
	panic("invalid page")
}

func layoutMainPager(ops op.Ctx, th *Colors, page program) image.Point {
	const npages = int(backupWallet) + 1
	const space = 4
	if npages <= 1 {
		return image.Point{}
	}
	sz := assets.CircleFilled.Bounds().Size()
	for i := range npages {
		op.Offset(ops, image.Pt((sz.X+space)*i, 0))
		mask := assets.Circle
		if i == int(page) {
			mask = assets.CircleFilled
		}
		op.ImageOp(ops, mask, true)
		op.ColorOp(ops, th.Text)
	}
	return image.Pt((sz.X+space)*npages-space, sz.Y)
}

func (m *MainScreen) selectedFlow(ctx *Context, ops op.Ctx) {
	th := m.theme()
	switch m.page {
	case backupWallet:
		mnemonic, ok := newInputFlow(ctx, ops, th)
		if !ok {
			break
		}
		engraveObjectFlow(ctx, ops, th, mnemonic)
	}
}

func engraveObjectFlow(ctx *Context, ops op.Ctx, th *Colors, obj any) bool {
	switch scan := obj.(type) {
	case bip39.Mnemonic:
		backupWalletFlow(ctx, ops, th, scan)
		// TODO: re-enable SLIP39. See also nfcpoller.go.
	// case slip39.Share:
	// 	w, err := scan.Words()
	// 	// No space for secrets > 128 bits.
	// 	const maximumLength = 20
	// 	if err != nil || len(w) > maximumLength {
	// 		return false
	// 	}
	// 	title := fmt.Sprintf("%d #%d 1/%d", scan.Identifier, scan.MemberIndex+1, scan.MemberThreshold)
	// 	seedDesc := backup.Seed{
	// 		Mnemonic:     w,
	// 		ShortestWord: slip39words.ShortestWord,
	// 		LongestWord:  slip39words.LongestWord,
	// 		Title:        title,
	// 		Font:         constant.Font,
	// 	}
	// 	params := ctx.Platform.EngraverParams()
	// 	seedSide, err := backup.EngraveSeed(params, seedDesc)
	// 	if err != nil {
	// 		return false
	// 	}
	// 	plate, err := toPlate(seedSide, params)
	// 	if err != nil {
	// 		return false
	// 	}
	// 	for {
	// 		completed := NewEngraveScreen(ctx, plate).Engrave(ctx, ops, &engraveTheme)
	// 		if completed {
	// 			return true
	// 		}
	// 	}
	case codex32.String:
		id, _, _ := scan.Split()
		s := backup.SeedString{
			Title: id,
			Seed:  scan.String(),
			Font:  constant.Font,
		}
		backupSeedStringFlow(ctx, ops, th, s)
	case *bip380.Descriptor:
		descriptorFlow(ctx, ops, th, scan)
	default:
		return false
	}
	return true
}

func backupWalletFlow(ctx *Context, ops op.Ctx, th *Colors, mnemonic bip39.Mnemonic) {
	ss := new(SeedScreen)
	for {
		if !ss.Confirm(ctx, ops, th, mnemonic) {
			return
		}
		plate, err := engraveSeed(ctx.Platform.EngraverParams(), mnemonic)
		if err != nil {
			errScr := NewErrorScreen(err)
			for !ctx.Done {
				dims := ctx.Platform.DisplaySize()
				dismissed := errScr.Layout(ctx, ops.Begin(), th, dims)
				d := ops.End()
				if dismissed {
					break
				}
				ss.Draw(ctx, ops, th, dims, mnemonic)
				d.Add(ops)
				ctx.Frame()
			}
			continue
		}
		completed := NewEngraveScreen(ctx, plate).Engrave(ctx, ops, &engraveTheme)
		if completed {
			return
		}
	}
}

func backupSeedStringFlow(ctx *Context, ops op.Ctx, th *Colors, s backup.SeedString) {
	params := ctx.Platform.EngraverParams()
	p, err := backup.EngraveSeedString(params, s)
	if err != nil {
		return
	}
	plate, err := toPlate(p, params)
	if err != nil {
		return
	}
	for {
		completed := NewEngraveScreen(ctx, plate).Engrave(ctx, ops, &engraveTheme)
		if completed {
			return
		}
	}
}

func descriptorFlow(ctx *Context, ops op.Ctx, th *Colors, desc *bip380.Descriptor) {
	ds := &DescriptorScreen{
		Descriptor: desc,
	}
	for {
		plate, ok := ds.Confirm(ctx, ops, th)
		if !ok {
			break
		}
		completed := NewEngraveScreen(ctx, plate).Engrave(ctx, ops, &engraveTheme)
		if completed {
			return
		}
	}
}

func newInputFlow(ctx *Context, ops op.Ctx, th *Colors) (any, bool) {
	for {
		cs := &ChoiceScreen{
			Title:   "Input Seed",
			Lead:    "Choose number of words",
			Choices: []string{"12 WORDS", "24 WORDS" /* , "CODEX32", "SLIP-39" */},
		}
		for {
			choice, ok := cs.Choose(ctx, ops, th)
			if !ok {
				return nil, false
			}
			switch choice {
			case 0, 1:
				mnemonic := emptyBIP39Mnemonic([]int{12, 24}[choice])
				inputWordsFlow(ctx, ops, th, mnemonic, 0)
				if !isEmptyMnemonic(mnemonic) {
					return mnemonic, true
				}
			case 2:
				s, ok := inputCodex32Flow(ctx, ops, th)
				if ok {
					return s, true
				}
				// TODO: re-enable
				// case 3:
				// 	mnemonic := emptySLIP39Mnemonic(20)
				// 	if ok := inputSLIP39Flow(ctx, ops, th, mnemonic, 0); !ok {
				// 		break
				// 	}
				// 	share := new(strings.Builder)
				// 	for i, w := range mnemonic {
				// 		if i > 0 {
				// 			share.WriteByte(' ')
				// 		}
				// 		share.WriteString(slip39words.LabelFor(w))
				// 	}
				// 	s, err := slip39.ParseShare(share.String())
				// 	if err != nil {
				// 		break
				// 	}
				// 	return s, true
			}
		}
	}
}

type SeedScreen struct {
	selected int
	words    []Clickable
}

func (s *SeedScreen) Confirm(ctx *Context, ops op.Ctx, th *Colors, mnemonic bip39.Mnemonic) bool {
	inp := new(InputTracker)
	backBtn := &Clickable{Button: Button1}
	editBtn := &Clickable{Button: Button2, AltButton: Center}
	confirmBtn := &Clickable{Button: Button3}
events:
	for !ctx.Done {
		for i := range s.words {
			c := &s.words[i]
			if c.Clicked(ctx) {
				s.selected = i
			}
		}
		if backBtn.Clicked(ctx) {
			if isEmptyMnemonic(mnemonic) {
				break
			}
			confirm := &ConfirmWarningScreen{
				Title: strings.ToTitle("Discard Seed?"),
				Body:  "Going back will discard the seed.\n\nHold button to confirm.",
				Icon:  assets.IconDiscard,
			}
			for !ctx.Done {
				dims := ctx.Platform.DisplaySize()
				res := confirm.Layout(ctx, ops.Begin(), th, dims)
				d := ops.End()
				switch res {
				case ConfirmNo:
					continue events
				case ConfirmYes:
					return false
				}
				s.Draw(ctx, ops, th, dims, mnemonic)
				d.Add(ops)
				ctx.Frame()
			}
		}
		if editBtn.Clicked(ctx) {
			inputWordsFlow(ctx, ops, th, mnemonic, s.selected)
			continue
		}
		if confirmBtn.Clicked(ctx) {
			if !isMnemonicComplete(mnemonic) {
				continue
			}
			showErr := func(scr *ErrorScreen) {
				for !ctx.Done {
					dims := ctx.Platform.DisplaySize()
					dismissed := scr.Layout(ctx, ops.Begin(), th, dims)
					d := ops.End()
					if dismissed {
						break
					}
					s.Draw(ctx, ops, th, dims, mnemonic)
					d.Add(ops)
					ctx.Frame()
				}
			}
			if !mnemonic.Valid() {
				scr := &ErrorScreen{
					Title: "Invalid Seed",
				}
				var words []string
				for _, w := range mnemonic {
					words = append(words, bip39.LabelFor(w))
				}
				if nonstandard.ElectrumSeed(strings.Join(words, " ")) {
					scr.Body = "Electrum seeds are not supported."
				} else {
					scr.Body = "The seed phrase is invalid.\n\nCheck the words and try again."
				}
				showErr(scr)
				continue
			}
			if _, ok := deriveMasterKey(mnemonic, &chaincfg.MainNetParams); !ok {
				showErr(&ErrorScreen{
					Title: "Invalid Seed",
					Body:  "The seed is invalid.",
				})
				continue
			}
			return true
		}
		for {
			e, ok := inp.Next(ctx, ButtonFilter(Up), ButtonFilter(Down))
			if !ok {
				break
			}
			if e, ok := e.AsButton(); ok {
				switch e.Button {
				case Down:
					if e.Pressed && s.selected < len(mnemonic)-1 {
						s.selected++
					}
				case Up:
					if e.Pressed && s.selected > 0 {
						s.selected--
					}
				}
			}
		}

		dims := ctx.Platform.DisplaySize()
		s.Draw(ctx, ops, th, dims, mnemonic)

		layoutNavigation(ops, th, dims, []NavButton{
			{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack},
			{Clickable: editBtn, Style: StyleSecondary, Icon: assets.IconEdit},
		}...)
		if isMnemonicComplete(mnemonic) {
			layoutNavigation(ops, th, dims, []NavButton{
				{Clickable: confirmBtn, Style: StylePrimary, Icon: assets.IconCheckmark},
			}...)
		}
		ctx.Frame()
	}
	return false
}

func isMnemonicComplete(m bip39.Mnemonic) bool {
	if slices.Contains(m, -1) {
		return false
	}
	return len(m) > 0
}

func (s *SeedScreen) Draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point, mnemonic bip39.Mnemonic) {
	if len(s.words) != len(mnemonic) {
		s.words = make([]Clickable, len(mnemonic))
	}
	op.ColorOp(ops, th.Background)
	layoutTitle(ctx, ops, dims.X, th.Text, "Engrave Seed")

	style := ctx.Styles.word
	longestPrefix := style.Measure(math.MaxInt, "24: ")
	layoutWord := func(ops op.Ctx, col color.NRGBA, n int, word string) image.Point {
		prefix := widget.Labelf(ops.Begin(), style, col, "%d: ", n)
		op.Position(ops, ops.End(), image.Pt(longestPrefix.X-prefix.X, 0))
		txt := widget.Label(ops.Begin(), style, col, word)
		op.Position(ops, ops.End(), image.Pt(longestPrefix.X, 0))
		return image.Pt(longestPrefix.X+txt.X, txt.Y)
	}

	y := 0
	longest := layoutWord(op.Ctx{}, color.NRGBA{}, 24, widestWord)
	r := layout.Rectangle{Max: dims}
	navw := assets.NavBtnPrimary.Bounds().Dx()
	list := r.Shrink(leadingSize, 0, 0, 0)
	content := list.Shrink(scrollFadeDist, navw, scrollFadeDist, navw)
	lineHeight := longest.Y + 2
	linesPerPage := content.Dy() / lineHeight
	scroll := s.selected - linesPerPage/2
	maxScroll := len(mnemonic) - linesPerPage
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	largeScreen := dims.X >= 480
	if largeScreen {
		scroll = 0
	}
	off := content.Min.Add(image.Pt(0, -scroll*lineHeight))
	{
		ops := ops.Begin()
		for i, w := range mnemonic {
			ops.Begin()
			col := th.Text
			r := image.Rectangle{Max: longest}
			if i == s.selected {
				col = th.Background
				r.Min.Y -= 3
				assets.ButtonFocused.Add(ops, r, true)
				op.ColorOp(ops, th.Text)
			}
			word := strings.ToUpper(bip39.LabelFor(w))
			layoutWord(ops, col, i+1, word)
			op.ClipOp(r).Add(ops)
			op.InputOp(ops, &s.words[i])
			pos := image.Pt(0, y).Add(off)
			op.Position(ops, ops.End(), pos)
			y += lineHeight
			// TODO: hack to show words on two columns in
			// touch mode.
			if largeScreen && i == 11 {
				y = 0
				off.X += longest.X + 16
			}
		}
	}
	fadeClip(ops, ops.End(), image.Rectangle(list))
}

type DescriptorScreen struct {
	Descriptor *bip380.Descriptor
}

func (s *DescriptorScreen) Confirm(ctx *Context, ops op.Ctx, th *Colors) (Plate, bool) {
	showErr := func(errScreen *ErrorScreen) {
		for !ctx.Done {
			dims := ctx.Platform.DisplaySize()
			dismissed := errScreen.Layout(ctx, ops.Begin(), th, dims)
			d := ops.End()
			if dismissed {
				break
			}
			s.Draw(ctx, ops, th, dims)
			d.Add(ops)
			ctx.Frame()
		}
	}
	backBtn := &Clickable{Button: Button1}
	// TODO: re-enable addresses screen.
	// infoBtn := &Clickable{Button: Button2}
	confirmBtn := &Clickable{Button: Button3}
	for !ctx.Done {
		if backBtn.Clicked(ctx) {
			break
		}
		// for infoBtn.Clicked(ctx) {
		// 	ShowAddressesScreen(ctx, ops, th, s.Descriptor)
		// }
		if confirmBtn.Clicked(ctx) {
			labels, engravings, err := validateDescriptor(ctx.Platform.EngraverParams(), s.Descriptor)
			if err != nil {
				showErr(NewErrorScreen(err))
				continue
			}
			cs := &ChoiceScreen{
				Title:   "Engrave",
				Lead:    "Choose engraving",
				Choices: labels,
			}
			choice, ok := cs.Choose(ctx, ops, th)
			if ok {
				e := engravings[choice]
				return e, true
			}
		}

		dims := ctx.Platform.DisplaySize()
		s.Draw(ctx, ops, th, dims)
		layoutNavigation(ops, th, dims, []NavButton{
			{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack},
			// {Clickable: infoBtn, Style: StyleSecondary, Icon: assets.IconInfo},
			{Clickable: confirmBtn, Style: StylePrimary, Icon: assets.IconCheckmark},
		}...)
		ctx.Frame()
	}
	return Plate{}, false
}

func (s *DescriptorScreen) Draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) {
	const infoSpacing = 8

	desc := s.Descriptor
	op.ColorOp(ops, th.Background)

	// Title.
	r := layout.Rectangle{Max: dims}
	layoutTitle(ctx, ops, dims.X, th.Text, "Engrave Descriptor")

	btnw := assets.NavBtnPrimary.Bounds().Dx()
	body := r.Shrink(leadingSize, btnw, 0, btnw)

	{
		ops := ops.Begin()
		var bodytxt richText

		bodyst := ctx.Styles.body
		subst := ctx.Styles.subtitle
		if desc.Title != "" {
			bodytxt.Add(ops, subst, body.Dx(), th.Text, "Title")
			bodytxt.Add(ops, bodyst, body.Dx(), th.Text, desc.Title)
			bodytxt.Y += infoSpacing
		}
		bodytxt.Add(ops, subst, body.Dx(), th.Text, "Type")
		testnet := any("") // TODO: TinyGo allocates without explicit interface conversion.
		if len(desc.Keys) > 0 && desc.Keys[0].Network != &chaincfg.MainNetParams {
			testnet = " (testnet)"
		}
		switch desc.Type {
		case bip380.Singlesig:
			bodytxt.Addf(ops, bodyst, body.Dx(), th.Text, "Singlesig%s", testnet)
		default:
			bodytxt.Addf(ops, bodyst, body.Dx(), th.Text, "%d-of-%d multisig%s", desc.Threshold, len(desc.Keys), testnet)
		}
		bodytxt.Y += infoSpacing
		bodytxt.Add(ops, subst, body.Dx(), th.Text, "Script")
		bodytxt.Add(ops, bodyst, body.Dx(), th.Text, desc.Script.String())
	}

	op.Position(ops, ops.End(), body.Min.Add(image.Pt(0, scrollFadeDist)))
}

func NewEngraveScreen(ctx *Context, plate Plate) *EngraveScreen {
	ins := append([]Instruction{}, EngraveSideASimple...)
	s := &EngraveScreen{
		plate:        plate,
		instructions: ins,
	}
	for i, ins := range s.instructions {
		repl := strings.NewReplacer(
			"{{.Name}}", plateName(SquarePlate),
		)
		s.instructions[i].resolvedBody = repl.Replace(ins.Body)
		// As a special case, the Sh02 image is a placeholder for the plate-specific image.
		if ins.Image == assets.Sh02 {
			s.instructions[i].Image = plateImage(SquarePlate)
		}
	}
	return s
}

type EngraveScreen struct {
	instructions []Instruction
	plate        Plate

	step   int
	dryRun struct {
		timeout time.Time
		enabled bool
	}
	engrave struct {
		job      *engraveJob
		duration uint
		err      error
	}
}

func (s *EngraveScreen) showError(ctx *Context, ops op.Ctx, th *Colors, errScr *ErrorScreen) {
	for !ctx.Done {
		dims := ctx.Platform.DisplaySize()
		dismissed := errScr.Layout(ctx, ops.Begin(), th, dims)
		d := ops.End()
		if dismissed {
			break
		}
		s.draw(ctx, ops, th, dims)
		d.Add(ops)
		ctx.Frame()
	}
}

func (s *EngraveScreen) moveStep(p Platform) bool {
	ins := s.instructions[s.step]
	s.step++
	if s.step == len(s.instructions) {
		return true
	}
	ins = s.instructions[s.step]
	if ins.Type == EngraveInstruction {
		spline := s.plate.Spline
		if s.dryRun.enabled {
			spline = engrave.DryRun(spline)
		}
		s.engrave.err = nil
		ticks := s.plate.Attrs.Duration
		s.engrave.duration = ticks
		s.engrave.job = newEngraverJob(p, spline)
	}
	return false
}

var errEngravingCancelled = errors.New("engraving cancelled")

func (s *EngraveScreen) cancelEngraving() {
	d := s.engrave.job
	if d == nil {
		return
	}
	d.Cancel()
	s.engrave.job = nil
	s.engrave.err = errEngravingCancelled
}

func (s *EngraveScreen) Engrave(ctx *Context, ops op.Ctx, th *Colors) bool {
	defer s.cancelEngraving()
	inp := new(InputTracker)
	backBtn := &Clickable{Button: Button1}
	selectBtn := &Clickable{Button: Button3, AltButton: Center}
frames:
	for !ctx.Done {
		if d := s.engrave.job; d != nil {
			// Update progress twice a second.
			ctx.WakeupAt(time.Now().Add(time.Second / 2))
			if done, err := d.Status(); done {
				s.engrave.job = nil
				s.engrave.err = err
				if err == nil {
					s.step++
					if s.step == len(s.instructions) {
						return true
					}
				} else {
					s.step--
				}
			}
		}

		if !s.dryRun.timeout.IsZero() {
			now := time.Now()
			d := s.dryRun.timeout.Sub(now)
			if d <= 0 {
				s.dryRun.timeout = time.Time{}
				s.dryRun.enabled = !s.dryRun.enabled
			}
		}
		for s.step < len(s.instructions)-1 && backBtn.Clicked(ctx) {
			if s.step == 0 {
				break frames
			}
			s.cancelEngraving()
			s.step--
		}
		for {
			e, ok := selectBtn.Next(ctx)
			if !ok {
				break
			}
			switch s.instructions[s.step].Type {
			case ConnectInstruction:
				if !selectBtn.Pressed {
					continue
				}
				confirm := new(ConfirmDelay)
				confirm.Start(ctx, confirmDelay)
				inp.Pressed[selectBtn.Button] = false
				selectBtn.Pressed = false
				for !ctx.Done {
					p := confirm.Progress(ctx)
					if p == 1. {
						break
					}
					for {
						_, ok := selectBtn.Next(ctx)
						if !ok {
							break
						}
						if !selectBtn.Pressed {
							continue frames
						}
					}
					dims := ctx.Platform.DisplaySize()
					s.draw(ctx, ops, th, dims)
					s.drawNav(ops, th, dims, p, backBtn, selectBtn)
					ctx.Frame()
				}
			case EngraveInstruction:
				continue
			default:
				if !e.Clicked {
					continue
				}
			}
			if s.moveStep(ctx.Platform) {
				return true
			}
		}
		for {
			e, ok := inp.Next(ctx, ButtonFilter(Button2))
			if !ok {
				break
			}
			if e, ok := e.AsButton(); ok {
				switch e.Button {
				case Button2:
					if e.Pressed {
						t := time.Now().Add(confirmDelay)
						s.dryRun.timeout = t
						ctx.WakeupAt(t)
					} else {
						s.dryRun.timeout = time.Time{}
					}
				}
			}
		}

		dims := ctx.Platform.DisplaySize()
		s.draw(ctx, ops, th, dims)
		s.drawNav(ops, th, dims, 0, backBtn, selectBtn)

		ctx.Frame()
	}
	return false
}

func (s *EngraveScreen) draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) {
	op.ColorOp(ops, th.Background)
	layoutTitle(ctx, ops, dims.X, th.Text, "Engrave Plate")

	r := layout.Rectangle{Max: dims}

	const margin = 8
	_, content := r.CutTop(leadingSize)
	ins := s.instructions[s.step]
	switch ins.Type {
	case EngraveInstruction:
		middle, _ := content.CutBottom(leadingSize)
		// Remaining seconds, rounded up.
		p := s.engrave.job.Progress()
		rem := s.engrave.duration - p.Ticks
		tps := ctx.Platform.EngraverParams().TicksPerSecond
		remSec := (rem + tps - 1) / tps
		min, sec := remSec/60, remSec%60
		sz := widget.Labelf(ops.Begin(), ctx.Styles.progress, th.Text, "%d:%.2d", min, sec)
		op.Position(ops, ops.End(), middle.Center(sz))
	default:
		content = content.Shrink(0, margin, 0, margin)
		content, lead := content.CutBottom(leadingSize)
		var bodysz image.Point
		if err := s.engrave.err; err != nil && ins.Type == ConnectInstruction {
			if errors.Is(err, errEngravingCancelled) {
				bodysz = widget.Labelw(ops.Begin(), ctx.Styles.lead, content.Dx(), th.Text,
					"Engraving paused.\nHold button to resume.")
			} else {
				bodysz = widget.Labelwf(ops.Begin(), ctx.Styles.lead, content.Dx(), th.Text,
					"Engraving failed.\nHold button to retry.\n\nError: %s", err.Error())
			}
		} else {
			bodysz = widget.Labelw(ops.Begin(), ctx.Styles.lead, content.Dx(), th.Text, ins.resolvedBody)
		}
		if img := ins.Image; img != nil {
			sz := img.Bounds().Size()
			op.Offset(ops, image.Pt((bodysz.X-sz.X)/2, bodysz.Y))
			op.ImageOp(ops, img, false)
			if sz.X > bodysz.X {
				bodysz.X = sz.X
			}
			bodysz.Y += sz.Y
		}
		op.Position(ops, ops.End(), content.Center(bodysz))
		leadsz := widget.Labelw(ops.Begin(), ctx.Styles.lead, dims.X-2*margin, th.Text, ins.Lead)
		op.Position(ops, ops.End(), lead.Center(leadsz))
	}

	progressw := dims.X * (s.step + 1) / len(s.instructions)
	op.ClipOp(image.Rectangle{Max: image.Pt(progressw, 2)}).Add(ops)
	op.ColorOp(ops, th.Text)

	if s.dryRun.enabled {
		sz := widget.Labelf(ops.Begin(), ctx.Styles.debug, th.Text, "dry-run")
		op.Position(ops, ops.End(), r.SE(sz).Sub(image.Pt(4, 0)))
	}
}

func (s *EngraveScreen) drawNav(ops op.Ctx, th *Colors, dims image.Point, progress float32, backBtn, selectBtn *Clickable) {
	switch {
	case s.step == 0:
		layoutNavigation(ops, th, dims, NavButton{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack})
	case s.step < len(s.instructions)-1:
		layoutNavigation(ops, th, dims, NavButton{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconLeft})
	}
	ins := s.instructions[s.step]
	switch ins.Type {
	case EngraveInstruction:
	case ConnectInstruction:
		layoutNavigation(ops, th, dims, NavButton{Clickable: selectBtn, Style: StylePrimary, Icon: assets.IconHammer, Progress: progress})
	default:
		layoutNavigation(ops, th, dims, NavButton{
			Clickable: selectBtn,
			Style:     StylePrimary,
			Icon:      assets.IconRight,
			Progress:  progress,
		})
	}
}

type Platform interface {
	LockBoot() error
	AppendEvents(deadline time.Time, evts []Event) []Event
	Wakeup()
	Engrave(stall bool, spline bspline.Curve, quit <-chan struct{}, progress chan stepper.Progress) error
	EngraverStatus() EngraverStatus
	NFCReader() io.Reader
	EngraverParams() engrave.Params
	DisplaySize() image.Point
	// Dirty begins a refresh of the content
	// specified by r.
	Dirty(r image.Rectangle) error
	// NextChunk returns the next chunk of the refresh.
	NextChunk() (draw.RGBA64Image, bool)
	Features() Features
}

type Features int

const (
	FeatureSecureBoot Features = 1 << iota
)

func (f Features) Has(feat Features) bool {
	return f&feat != 0
}

type EngraverStatus struct {
	StallSpeed       int
	XSpeed, YSpeed   int
	XLoad, YLoad     int
	XStalls, YStalls int
	Error            error
}

const idleTimeout = 3 * time.Minute

func Run(pl Platform, version string) func(yield func() bool) {
	return func(yield func() bool) {
		ctx := NewContext(pl)
		ctx.Version = version
		secure := pl.Features().Has(FeatureSecureBoot)
		if !secure {
			ctx.Version += " FIRMWARE UNLOCKED"
		}
		a := struct {
			root op.Ops
			mask *image.Alpha
			idle struct {
				start  time.Time
				active bool
				state  saver.State
			}
		}{}
		scans := make(chan scanResult, 1)
		if r := pl.NFCReader(); r != nil {
			wakeup := pl.Wakeup
			go func() {
				s := new(scanner)
				for {
					obj, err := s.Scan(r)
					scan := scanResult{
						Object: obj,
					}
					switch {
					case errors.Is(err, errScanInProgress):
						scan.Status = scanStarted
					case errors.Is(err, errScanUnknownFormat):
						scan.Status = scanUnknownFormat
					case err == nil:
					default:
						scan.Status = scanFailed
						log.Printf("nfc scan: %v", err)
					}
					select {
					case old := <-scans:
						// Merge the previous result.
						if scan.Object == nil {
							scan.Object = old.Object
						}
						scan.Status = max(scan.Status, old.Status)
					default:
					}
					scans <- scan
					wakeup()
					if scan.Status == scanFailed {
						// Wait a bit before attempting to scan again.
						time.Sleep(1 * time.Second)
					}
				}
			}()
		}
		a.idle.start = time.Now()

		it := func(yield func() bool) {
			ctx.FrameCallback = func() {
				ctx.Done = ctx.Done || !yield()
			}
			m := new(MainScreen)
			m.Flow(ctx, a.root.Context())
		}
		startTime := time.Now()
		var evts []Event
		stats := new(runtimeStats)
		for range it {
			dirty := a.root.Clip(image.Rectangle{Max: pl.DisplaySize()})
			layoutTime := time.Since(startTime)
			if err := pl.Dirty(dirty); err != nil {
				panic(err)
			}
			for {
				fb, ok := pl.NextChunk()
				if !ok {
					break
				}
				fbdims := fb.Bounds().Size()
				npix := fbdims.X * fbdims.Y
				if a.mask == nil || len(a.mask.Pix) < npix {
					a.mask = image.NewAlpha(image.Rectangle{Max: fbdims})
				}
				a.mask.Rect = image.Rectangle{Max: fbdims}
				a.root.Draw(fb, a.mask)
			}
			drawTime := time.Since(startTime)
			if debug {
				stats.Dump(drawTime, layoutTime, dirty)
			}
			for {
				if ctx.Done || !yield() {
					return
				}
				wakeup := ctx.Wakeup
				evts = pl.AppendEvents(wakeup, evts[:0])
				now := time.Now()
				if len(evts) > 0 {
					a.idle.start = now
				}
				ctx.Reset()
				if !a.idle.active {
					ctx.Router.Events(&a.root, evts...)
				}
				select {
				case scan := <-scans:
					ctx.scan = scan
					a.idle.start = time.Now()
				default:
				}
				idleWakeup := a.idle.start.Add(idleTimeout)
				idle := now.Sub(idleWakeup) >= 0
				if a.idle.active != idle {
					a.idle.active = idle
					if idle {
						a.idle.state = saver.State{}
					} else {
						// The screen saver has invalidated the cached
						// frame content.
						a.root.Reset()
					}
				}
				if a.idle.active {
					a.idle.state.Draw(pl)
					// Throttle screen saver speed.
					const minFrameTime = 40 * time.Millisecond
					ctx.WakeupAt(now.Add(minFrameTime))
					continue
				}
				ctx.WakeupAt(idleWakeup)
				break
			}
			a.root.Reset()
			startTime = time.Now()
		}
	}
}

type runtimeStats struct {
	mallocs uint64
	buf     [200]byte
}

func (r *runtimeStats) Dump(drawTime, layoutTime time.Duration, dirty image.Rectangle) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	dm := mem.Mallocs - r.mallocs
	r.mallocs = mem.Mallocs
	format := "frame: %dms layout: %dms draw: %dms (%d,%d)-(%d,%d) mem %d allocs %d total %d\n"
	// Cast values to int to avoid a TinyGo allocation for larger integers.
	args := []any{int(drawTime.Milliseconds()), int(layoutTime.Milliseconds()), int((drawTime - layoutTime).Milliseconds()),
		dirty.Min.X, dirty.Min.Y, dirty.Max.X, dirty.Max.Y, uint(mem.HeapInuse), uint(dm), uint(mem.Mallocs - mem.Frees)}
	f := new(text.Formatter)
	buf := r.buf[:0]
	for {
		r, ok := f.Next(format, args...)
		if !ok {
			break
		}
		buf = utf8.AppendRune(buf, r)
	}
	log.Writer().Write(buf)
}

func rgb(c uint32) color.NRGBA {
	return argb(0xff000000 | c)
}

func argb(c uint32) color.NRGBA {
	return color.NRGBA{A: uint8(c >> 24), R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
}

func toPlate(plan engrave.Engraving, params engrave.Params) (Plate, error) {
	size := SquarePlate
	sz := size.Dims(params.Millimeter)
	spline := engrave.PlanEngraving(params.StepperConfig, plan)
	attrs := bspline.Measure(spline)
	safetyMargin := bezier.Pt(safetyMargin*params.Millimeter, safetyMargin*params.Millimeter)
	if !attrs.Bounds.In(bspline.Bounds{Min: safetyMargin, Max: sz.Sub(safetyMargin)}) {
		return Plate{}, ErrTooLarge
	}
	return Plate{
		Attrs:  attrs,
		Spline: spline,
	}, nil
}

type PlateSize int

const (
	SquarePlate PlateSize = iota
)

func (p PlateSize) Dims(mm int) bezier.Point {
	switch p {
	case SquarePlate:
		return bezier.Pt(85*mm, 85*mm)
	}
	panic("unreachable")
}

type scanResult struct {
	Object any
	Status scanStatus
}

type scanStatus int

const (
	scanIdle scanStatus = iota
	scanStarted
	scanOverflow
	scanUnknownFormat
	scanFailed
)
