// package gui implements the SeedHammer controller user interface.
package gui

import (
	"errors"
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

	"github.com/btcsuite/btcd/btcutil/v2/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg/v2"
	qr "github.com/seedhammer/kortschak-qr"
	"seedhammer.com/backup"
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
)

var ErrTooLarge = errors.New("backup: data does not fit plate")

// safetyMargin is the distance in mm that must be kept free of
// engraving.
const safetyMargin = 3

const (
	cornerRadius    = 5
	buttonPadX      = 6
	buttonPadY      = 1
	keyCornerRadius = 3
	keyLineWidth    = 1
	keyPadX         = 3
	keyPadY         = 4
)

type Context struct {
	Platform      Platform
	Styles        Styles
	Wakeup        time.Time
	Done          bool
	FrameCallback func(op.Op)
	B             op.Buffer

	Router EventRouter
}

func (c *Context) Frame(op op.Op) {
	if f := c.FrameCallback; f != nil {
		f(op)
	}
	c.B.Reset()
}

func NewContext(pl Platform) *Context {
	c := &Context{
		Platform: pl,
		Styles:   NewStyles(),
	}
	return c
}

func (c *Context) WakeupAt(t time.Time) {
	if c.Wakeup.IsZero() || t.Before(c.Wakeup) {
		c.Wakeup = t
	}
}

func (c *Context) Reset() {
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
	qaProgram
)

type richText struct {
	Content op.Op
	Y       int
}

func (r *richText) Add(b *op.Buffer, style text.Style, width int, col color.RGBA, str string) {
	r.Addf(b, style, width, col, "%s", str)
}

func (r *richText) Addf(b *op.Buffer, style text.Style, width int, col color.RGBA, format string, args ...any) {
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
		r.Content = op.Layer(
			r.Content,
			op.Compose(
				op.Color(b, col),
				op.Glyph(b, style.Face, g.Rune),
			).Offset(off),
		)
	}
	r.Y = offy + m.Descent.Ceil()
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

type ErrorScreen struct {
	Title string
	Body  string
	w     Warning
	ok    Clickable
}

func (s *ErrorScreen) Layout(ctx *Context, th *Colors, dims image.Point) (op.Op, bool) {
	s.ok.Button = Button3
	if s.ok.Clicked(ctx) {
		return op.Op{}, true
	}
	nav, _ := layoutNavigation(&ctx.B, th, dims, NavButton{Clickable: &s.ok, Style: StylePrimary, Icon: assets.IconCheckmark})
	content := s.w.Layout(ctx, th, dims, s.Title, s.Body)
	return op.Layer(nav, content), false
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

func (w *Warning) Layout(ctx *Context, th *Colors, dims image.Point, title, txt string) op.Op {
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

	btnOff := assets.NavBtnPrimary.Bounds().Dx() + btnMargin
	bodyClip := image.Rectangle{
		Min: image.Pt(boxMargin, leadingSize),
		Max: image.Pt(dims.X-btnOff, dims.Y-boxMargin),
	}
	body, bodysz := widget.Labelw(&ctx.B, ctx.Styles.body, bodyClip.Dx(), th.Text, txt)
	w.txtclip = bodyClip.Dy()
	maxScroll := bodysz.Y - (bodyClip.Dy() - 2*scrollFadeDist)
	if w.scroll > maxScroll {
		w.scroll = maxScroll
	}
	if w.scroll < 0 {
		w.scroll = 0
	}
	body = body.Offset(image.Pt(bodyClip.Min.X, bodyClip.Min.Y+scrollFadeDist-w.scroll))
	body = fadeClip(&ctx.B, body, image.Rectangle(bodyClip))

	titleOp, _ := layoutTitle(ctx, dims.X, th.Text, title)
	return op.Layer(
		body,
		titleOp,
		op.Color(&ctx.B, th.Background),
	)
}

func (s *ConfirmWarningScreen) Layout(ctx *Context, th *Colors, dims image.Point) (op.Op, ConfirmResult) {
	cancelBtn := s.cancelBtn.For(Button1)
	confirmBtn := s.confirmBtn.For(Button3, Center)
	if cancelBtn.Clicked(ctx) {
		return op.Op{}, ConfirmNo
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
		return op.Op{}, ConfirmYes
	}
	nav, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{
		{Clickable: cancelBtn, Style: StyleSecondary, Icon: assets.IconBack},
		{Clickable: confirmBtn, Style: StylePrimary, Icon: s.Icon, Progress: progress},
	}...)
	content := s.warning.Layout(ctx, th, dims, s.Title, s.Body)
	return op.Layer(nav, content), ConfirmNone
}

type ProgressImage struct {
	Progress float32
	Src      image.RGBA64Image
}

func (p *ProgressImage) Op(buf *op.Buffer) op.MaskOp {
	return op.ParamImageMask(buf, progressImageGen, []any{p.Src}, []uint32{math.Float32bits(p.Progress)})
}

func (p *ProgressImage) At(x, y int) color.Color {
	return p.RGBA64At(x, y)
}

func (p *ProgressImage) RGBA64At(x, y int) color.RGBA64 {
	b := p.Bounds()
	c := b.Max.Add(b.Min).Div(2)
	d := image.Pt(x, y).Sub(c)
	angle := float32(math.Atan2(float64(d.X), float64(d.Y)))
	angle = math.Pi - angle
	if angle > 2*math.Pi*p.Progress {
		return color.RGBA64{}
	}
	return p.Src.RGBA64At(x, y)
}

func (p *ProgressImage) ColorModel() color.Model {
	return p.Src.ColorModel()
}

func (p *ProgressImage) Bounds() image.Rectangle {
	return p.Src.Bounds()
}

var progressImageGen = op.RegisterParameterizedImage(func() op.ParameterizedImage {
	img := new(ProgressImage)
	return func(args []uint32, refs []any) image.Image {
		img.Src = refs[0].(image.RGBA64Image)
		img.Progress = math.Float32frombits(args[0])
		return img
	}
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
	Duration uint
	Spline   bspline.Curve
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

func fadeClip(b *op.Buffer, o op.Op, r image.Rectangle) op.Op {
	// op.ParamImageOp(ops, scrollMask, true, r, nil, nil)
	return o.Offset(image.Pt(0, 0))
}

// var scrollMask = op.RegisterParameterizedImage(func(args op.ImageArguments, x, y int) color.RGBA64 {
// 	alpha := 0xffff
// 	if d := y - args.Bounds.Min.Y; d < scrollFadeDist {
// 		alpha = 0xffff * d / scrollFadeDist
// 	} else if d := args.Bounds.Max.Y - y; d < scrollFadeDist {
// 		alpha = 0xffff * d / scrollFadeDist
// 	}
// 	a16 := uint16(alpha)
// 	return color.RGBA64{A: a16}
// })

const wordKeys = "qwertyuiop\nasdfghjkl\nzxcvbnm"

func inputWordsFlow(ctx *Context, th *Colors, mnemonic bip39.Mnemonic, selected int) {
	kbd := NewKeyboard(ctx, wordKeys)
	wordLabel := ""
	backBtn := &Clickable{Button: Button1}
	okBtn := &Clickable{Button: Button3}
	layoutWord := func(buf *op.Buffer, n int, word string) (op.Op, image.Point) {
		style := ctx.Styles.word
		return widget.Labelf(buf, style, th.Background, "%2d: %s", n, word)
	}
	_, longest := layoutWord(nil, 24, widestWord)
	var nvalid int
	for !ctx.Done {
		for kbd.Update(ctx) {
			nvalid = updateValidBIP39Keys(kbd.Fragment, kbd.allKeys)
			wordLabel = kbd.Fragment
			if completedWord, complete := completeBIP39Word(wordLabel, nvalid); complete {
				wordLabel = bip39.LabelFor(completedWord)
			}
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

		screen := layout.Rectangle{Max: dims}
		_, content := screen.CutTop(leadingSize)
		content, _ = content.CutBottom(8)

		kbdOp, kbdsz := kbd.Layout(ctx, th)
		kbdOp = kbdOp.Offset(content.S(kbdsz))

		r := image.Rectangle{Max: longest}
		r.Min.Y -= 3 + buttonPadY
		r.Max.Y += buttonPadY
		r.Min.X -= buttonPadX
		r.Max.X += buttonPadX
		top, _ := content.CutBottom(kbdsz.Y)
		word, _ := layoutWord(&ctx.B, selected+1, wordLabel)
		txtBg := op.Layer(
			word,
			op.Compose(
				op.Color(&ctx.B, th.Text),
				op.RoundedRect2(&ctx.B, r, cornerRadius),
			),
		).Offset(top.Center(longest))

		nav, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack}}...)
		if _, complete := completeBIP39Word(kbd.Fragment, nvalid); complete {
			nav2, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{{Clickable: okBtn, Style: StylePrimary, Icon: assets.IconCheckmark}}...)
			nav = op.Layer(
				nav,
				nav2,
			)
		}
		title, _ := layoutTitle(ctx, dims.X, th.Text, "Input Words")
		ctx.Frame(op.Layer(
			kbdOp,
			txtBg,
			nav,
			title,
			op.Color(&ctx.B, th.Background),
		))
	}
}

func inputCodex32Flow(ctx *Context, th *Colors) (codex32.String, bool) {
	const alph = "1234567890\nqwertyup\nasdfghjk\nlzxcvnm"

	kbd := NewKeyboard(ctx, alph)
	backBtn := &Clickable{Button: Button1}
	okBtn := &Clickable{Button: Button3}
	var share codex32.String
	valid := false
	for !ctx.Done {
		for kbd.Update(ctx) {
			s, err := codex32.New(kbd.Fragment)
			share, valid = s, err == nil
		}
		if backBtn.Clicked(ctx) {
			break
		}
		if valid && okBtn.Clicked(ctx) {
			return share, true
		}
		dims := ctx.Platform.DisplaySize()

		screen := layout.Rectangle{Max: dims}
		_, content := screen.CutTop(leadingSize)
		content, _ = content.CutBottom(8)

		kbdOp, kbdsz := kbd.Layout(ctx, th)
		kbdOp = kbdOp.Offset(content.S(kbdsz))

		word, frgSize := widget.Labelw(&ctx.B, ctx.Styles.word, dims.X-50, th.Background, kbd.Fragment)
		frgSize.X = max(frgSize.X, 100)
		r := image.Rectangle{Max: frgSize}
		r.Min.Y -= 3
		r.Max.Y += buttonPadY
		r.Min.X -= buttonPadX
		r.Max.X += buttonPadX
		top, _ := content.CutBottom(kbdsz.Y)
		word = op.Layer(
			word,
			op.Compose(
				op.Color(&ctx.B, th.Text),
				op.RoundedRect2(&ctx.B, r, cornerRadius),
			),
		).Offset(top.Center(frgSize))

		nav, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack}}...)
		if valid {
			nav2, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{{Clickable: okBtn, Style: StylePrimary, Icon: assets.IconCheckmark}}...)
			nav = op.Layer(nav, nav2)
		}
		title, _ := layoutTitle(ctx, dims.X, th.Text, "Input Codex32 Share")
		ctx.Frame(op.Layer(
			kbdOp,
			word,
			nav,
			title,
			op.Color(&ctx.B, th.Background),
		))
	}
	return codex32.String{}, false
}

func inputSLIP39Flow(ctx *Context, th *Colors, mnemonic slip39words.Mnemonic, selected int) bool {
	kbd := NewKeyboard(ctx, wordKeys)
	wordLabel := ""
	backBtn := &Clickable{Button: Button1}
	okBtn := &Clickable{Button: Button3}
	layoutWord := func(b *op.Buffer, n int, word string) (op.Op, image.Point) {
		style := ctx.Styles.word
		return widget.Labelf(b, style, th.Background, "%2d: %s", n, word)
	}
	const (
		widestWord = "WITHDRAW"
	)
	_, longest := layoutWord(nil, len(mnemonic), widestWord)
	var nvalid int
	for !ctx.Done {
		for kbd.Update(ctx) {
			nvalid = updateValidSLIP39Keys(kbd.Fragment, kbd.allKeys)
			wordLabel = kbd.Fragment
			if completedWord, complete := completeSLIP39Word(wordLabel, nvalid); complete {
				wordLabel = slip39words.LabelFor(completedWord)
			}
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
		screen := layout.Rectangle{Max: dims}
		_, content := screen.CutTop(leadingSize)
		content, _ = content.CutBottom(8)

		kbdOp, kbdsz := kbd.Layout(ctx, th)
		kbdOp = kbdOp.Offset(content.S(kbdsz))

		word, _ := layoutWord(&ctx.B, selected+1, wordLabel)
		r := image.Rectangle{Max: longest}
		r.Min.Y -= 3
		r.Max.Y += buttonPadY
		r.Min.X -= buttonPadX
		r.Max.X += buttonPadX
		top, _ := content.CutBottom(kbdsz.Y)
		word = op.Layer(
			word,
			op.Compose(
				op.Color(&ctx.B, th.Text),
				op.RoundedRect2(&ctx.B, r, cornerRadius),
			),
		).Offset(top.Center(longest))

		nav, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack}}...)
		if _, complete := completeSLIP39Word(kbd.Fragment, nvalid); complete {
			nav2, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{{Clickable: okBtn, Style: StylePrimary, Icon: assets.IconCheckmark}}...)
			nav = op.Layer(nav, nav2)
		}
		title, _ := layoutTitle(ctx, dims.X, th.Text, "Input Words")

		ctx.Frame(op.Layer(
			kbdOp,
			word,
			nav,
			title,
			op.Color(&ctx.B, th.Background),
		))
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
	k.backspace = image.Pt(max(bsWidth, k.widest.X), k.widest.Y)
	bgbnds := image.Rectangle{Max: k.widest}
	bgbnds.Min.X -= keyPadX
	bgbnds.Max.X += keyPadX
	bgbnds.Min.Y -= keyPadY
	bgbnds.Max.Y += keyPadY
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
			idx := unicode.ToLower(rune(suffix[0])) - 'a'
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
			idx := unicode.ToLower(rune(suffix[0])) - 'a'
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
		e, ok := k.inp.Next(ctx, ButtonFilter(Left), ButtonFilter(Right), ButtonFilter(Up), ButtonFilter(Down), ButtonFilter(Center), RuneFilter())
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
			case Center:
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
		k.Fragment = k.Fragment + string(unicode.ToUpper(r))
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

func (k *Keyboard) Layout(ctx *Context, th *Colors) (op.Op, image.Point) {
	var content op.Op
	for i, row := range k.keys {
		for j, key := range row {
			valid := k.Valid(key)
			bgsz := k.widest
			if key.r == '⌫' {
				bgsz = k.backspace
			}
			bgcol := th.Text
			style := ctx.Styles.keyboard
			col := th.Text
			active := false
			switch {
			case !valid:
				bgcol = mulAlpha(bgcol, theme.inactiveMask)
				col = bgcol
			case i == k.row && j == k.col:
				active = true
				col = th.Background
			}
			bgr := image.Rectangle{Max: bgsz}
			inpOp := op.Input(&ctx.B, &k.keys[i][j].clk).Clip(bgr)
			var keyOp op.Op
			var sz image.Point
			if key.r == '⌫' {
				icn := assets.KeyBackspace
				sz = image.Pt(k.backspace.X, icn.Bounds().Dy())
				keyOp = op.Compose(
					op.Color(&ctx.B, col),
					op.Mask(&ctx.B, icn),
				)
			} else {
				keyOp, sz = widget.Labelf(&ctx.B, style, col, "%c", unicode.ToUpper(key.r))
			}
			keyOp = keyOp.Offset(bgsz.Sub(sz).Div(2))
			bgr.Min.X -= keyPadX
			bgr.Max.X += keyPadX
			bgr.Min.Y -= keyPadY
			bgr.Max.Y += keyPadY
			bgOp := op.Color(&ctx.B, bgcol)
			var mask op.MaskOp
			if active {
				mask = op.RoundedRect2(&ctx.B, bgr, keyCornerRadius)
			} else {
				mask = op.RoundedOutline2(&ctx.B, bgr, keyCornerRadius, keyLineWidth)
			}
			btnOp := op.Layer(
				inpOp,
				keyOp,
				op.Compose(
					bgOp,
					mask,
				),
			).Offset(k.keys[i][j].pos)
			content = op.Layer(
				content, btnOp,
			)
		}
	}
	return content, k.size
}

func mulAlpha(col color.RGBA, a uint8) color.RGBA {
	col.R = uint8(uint(col.R) * uint(a) / 255)
	col.G = uint8(uint(col.G) * uint(a) / 255)
	col.B = uint8(uint(col.B) * uint(a) / 255)
	col.A = uint8(uint(col.A) * uint(a) / 255)
	return col
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
	W     op.Op
	click Clickable
}

func (s *ChoiceScreen) Choose(ctx *Context, th *Colors) (int, bool) {
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
		nav, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{
			{Clickable: cancelBtn, Style: StyleSecondary, Icon: assets.IconBack},
			{Clickable: chooseBtn, Style: StylePrimary, Icon: assets.IconCheckmark},
		}...)
		content := s.Draw(ctx, th, dims)
		ctx.Frame(op.Layer(nav, content))
	}
	return 0, false
}

func (s *ChoiceScreen) Draw(ctx *Context, th *Colors, dims image.Point) op.Op {
	r := layout.Rectangle{Max: dims}
	_, bottom := r.CutTop(leadingSize)
	leadOp, sz := widget.Labelw(&ctx.B, ctx.Styles.lead, dims.X-2*8, th.Text, s.Lead)
	content, lead := bottom.CutBottom(leadingSize)
	leadOp = leadOp.Offset(lead.Center(sz))

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
		o, sz := widget.Label(&ctx.B, style, col, c)
		s.children[i].Size = sz
		s.children[i].W = o
		if sz.X > maxW {
			maxW = sz.X
		}
	}

	h := 0
	var children op.Op
	for i := range s.children {
		c := &s.children[i]
		xoff := (maxW-c.Size.X)/2 + buttonPadX
		pos := image.Pt(xoff, h)
		txt := c.W
		bg := image.Rectangle{Max: c.Size}
		bg.Min.X -= xoff
		bg.Max.X += xoff
		bg.Min.Y -= buttonPadY
		bg.Max.Y += buttonPadY
		if i == s.choice {
			txt = op.Layer(
				txt,
				op.Compose(
					op.Color(&ctx.B, th.Text),
					op.RoundedRect2(&ctx.B, bg, cornerRadius),
				),
			)
		}
		children = op.Layer(
			children,
			txt.Offset(pos),
			op.Input(&ctx.B, &c.click).Clip(bg).Offset(pos),
		)
		h += c.Size.Y
	}
	title, _ := layoutTitle(ctx, dims.X, th.Text, s.Title)

	return op.Layer(
		leadOp,
		children.Offset(content.Center(image.Pt(maxW, h))),
		title,
		op.Color(&ctx.B, th.Background),
	)
}

func uiFlow(ctx *Context, version string) {
	th := &descriptorTheme
	s := &StartScreen{
		Version: version,
	}
	for {
		act, ok := s.Flow(ctx, th)
		if !ok {
			continue
		}
		obj := act.scan
		if obj == nil {
			switch act.prog {
			case qaProgram:
				qaEngraveFlow(ctx)
				continue
			case backupWallet:
				mnemonic, ok := newInputFlow(ctx, th)
				if !ok {
					continue
				}
				obj = mnemonic
			}
		}
		if !engraveObjectFlow(ctx, th, obj) {
			s.Status = scanUnknownFormat
		}
	}
}

type StartScreen struct {
	Version     string
	Status      scanStatus
	prog        program
	scanTimeout time.Time
}

type startScreenAction struct {
	prog program
	scan any
}

const scanStatusTimeout = 1 * time.Second

func (m *StartScreen) Flow(ctx *Context, th *Colors) (startScreenAction, bool) {
	scans := make(chan scanResult, 1)
	if r := ctx.Platform.NFCReader(); r != nil {
		closer := make(chan struct{})
		closed := make(chan struct{})
		defer func() {
			close(closer)
			r.Close()
			<-closed
		}()
		wakeup := ctx.Platform.Wakeup
		go func() {
			s := new(scanner)
			for {
				select {
				case <-closer:
					close(closed)
					return
				default:
				}
				obj, err := s.Scan(r)
				scan := scanResult{
					Object: obj,
				}
				switch {
				case errors.Is(err, errScanInProgress):
					scan.Status = scanStarted
				case errors.Is(err, errScanUnknownFormat):
					scan.Status = scanUnknownFormat
				case err == nil || err == io.EOF:
				default:
					scan.Status = scanFailed
					log.Printf("nfc scan: %v", err)
				}
				// Merge the previous result.
				select {
				case old := <-scans:
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
	inp := new(InputTracker)
	selectBtn := &Clickable{Button: Button3, AltButton: Center}
	for !ctx.Done {
		if selectBtn.Clicked(ctx) {
			return startScreenAction{prog: m.prog}, true
		}
		select {
		case scan := <-scans:
			if time.Now().Before(m.scanTimeout) {
				m.Status = max(m.Status, scan.Status)
			} else {
				m.Status = scan.Status
			}
			m.scanTimeout = time.Now().Add(scanStatusTimeout)
			if scan.Object == nil && scan.Status == scanIdle {
				break
			}
			if cnt := scan.Object; cnt != nil {
				switch cnt := cnt.(type) {
				case debugCommand:
					switch cmd := cnt.Command; cmd {
					case "FOREVERLAURA!":
						return startScreenAction{prog: qaProgram}, true
					case "lock-boot":
						m.Status = scanIdle
						if err := ctx.Platform.LockBoot(); err != nil {
							log.Printf("lock-boot: %v", err)
							m.Status = scanFailed
						}
						continue
					default:
						log.Printf("unknown debug command: %q", cmd)
					}
				}
				return startScreenAction{scan: cnt}, true
			}
		default:
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
					m.prog--
					if m.prog < 0 {
						m.prog = backupWallet
					}
				case Right:
					if !e.Pressed {
						break
					}
					m.prog++
					if m.prog > backupWallet {
						m.prog = 0
					}
				}
			}
		}
		dims := ctx.Platform.DisplaySize()
		nav, _ := layoutNavigation(&ctx.B, th, dims,
			NavButton{Clickable: selectBtn, Style: StylePrimary, Icon: assets.IconCheckmark},
		)
		content := m.draw(ctx, th, dims)
		ctx.Frame(op.Layer(nav, content))
	}
	return startScreenAction{}, false
}

func (m *StartScreen) draw(ctx *Context, th *Colors, dims image.Point) op.Op {
	var titleTxt string
	switch m.prog {
	case backupWallet:
		titleTxt = "Backup Wallet"
	}

	title, _ := layoutTitle(ctx, dims.X, th.Text, titleTxt)

	r := layout.Rectangle{Max: dims}
	content, sz := m.layout(&ctx.B, th, dims.X)
	content = content.Offset(r.Center(sz))

	inner, sz := layoutMainPager(&ctx.B, th, m.prog)
	_, middle := r.CutBottom(leadingSize)
	inner = inner.Offset(middle.Center(sz))
	sttxt := ""
	if time.Now().Before(m.scanTimeout) {
		ctx.WakeupAt(m.scanTimeout)
		switch m.Status {
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
	subt, sz := widget.Labelw(&ctx.B, ctx.Styles.subtitle, 300, th.Text, sttxt)
	subt = subt.Offset(r.S(sz).Sub(image.Pt(0, 16)))

	ver, sz := widget.Labelw(&ctx.B, ctx.Styles.debug, 200, th.Text, m.Version)
	ver = ver.Offset(r.SE(sz.Add(image.Pt(4, 0))))
	logo, sz := widget.Labelw(&ctx.B, ctx.Styles.debug, 100, th.Text, "SeedHammer")
	logo = logo.Offset(r.SW(sz).Add(image.Pt(3, 0)))
	return op.Layer(
		title,
		content,
		inner,
		subt,
		ver, logo,
		op.Color(&ctx.B, th.Background),
	)
}

func layoutTitle(ctx *Context, width int, col color.RGBA, title string) (op.Op, image.Rectangle) {
	return layoutTitlef(ctx, width, col, "%s", title)
}

func layoutTitlef(ctx *Context, width int, col color.RGBA, format string, args ...any) (op.Op, image.Rectangle) {
	const margin = 8
	lbl, sz := widget.Labelwf(&ctx.B, ctx.Styles.title, width-2*16, col, format, args...)
	pos := image.Pt((width-sz.X)/2, margin)
	return lbl.Offset(pos), image.Rectangle{
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

func layoutNavigation(buf *op.Buffer, th *Colors, dims image.Point, btns ...NavButton) (op.Op, image.Rectangle) {
	navsz := assets.NavBtnPrimary.Bounds().Size()
	button := func(buf *op.Buffer, b NavButton, t op.Tag, pressed bool) op.Op {
		if b.Style == StyleNone {
			return op.Op{}
		}
		content := op.Input(buf, t).Clip(assets.NavBtnPrimary.Bounds())
		if b.Progress == 0 && pressed {
			content = op.Layer(
				content,
				op.Compose(
					op.Color(buf, color.RGBA{A: theme.activeMask}),
					op.Mask(buf, assets.NavBtnPrimary),
				),
			)
		}
		const offset = 9
		var icn op.MaskOp
		if b.Progress > 0 {
			icn = (&ProgressImage{
				Progress: b.Progress,
				Src:      assets.IconProgress,
			}).Op(buf)
		} else {
			icn = op.Mask(buf, b.Icon)
		}
		content = op.Layer(
			content,
			op.Compose(
				op.Color(buf, th.Text),
				icn.Offset(image.Pt(offset, offset)),
			),
		)
		switch b.Style {
		case StyleSecondary:
			content = op.Layer(
				content,
				op.Compose(
					op.Color(buf, th.Text),
					op.Mask(buf, assets.NavBtnSecondary),
				),
				op.Compose(
					op.Color(buf, th.Background),
					op.Mask(buf, assets.NavBtnPrimary),
				),
			)
		case StylePrimary:
			content = op.Layer(
				content,
				op.Compose(
					op.Color(buf, th.Primary),
					op.Mask(buf, assets.NavBtnPrimary),
				),
			)
		}
		return content
	}
	btnsz := assets.NavBtnPrimary.Bounds().Size()
	ys := [3]int{
		leadingSize,
		(dims.Y - btnsz.Y) / 2,
		dims.Y - leadingSize - btnsz.Y,
	}
	var r image.Rectangle
	var content op.Op
	for _, b := range btns {
		clk := b.Clickable
		idx := int(clk.Button - Button1)
		pressed := clk.Pressed && clk.Entered
		bop := button(buf, b, clk, pressed)
		y := ys[idx]
		pos := image.Pt(dims.X-btnsz.X, y)
		content = op.Layer(content, bop.Offset(pos))
		r = r.Union(image.Rectangle{
			Min: pos,
			Max: pos.Add(navsz),
		})
	}
	return content, r
}

func (m *StartScreen) layout(buf *op.Buffer, th *Colors, width int) (op.Op, image.Point) {
	const margin = 16

	left := op.Compose(
		op.Color(buf, th.Text),
		op.Mask(buf, assets.ArrowLeft),
	)
	var h layout.Align
	leftsz := h.Add(assets.ArrowLeft.Bounds().Size())
	left = left.Offset(image.Pt(margin, h.Y(leftsz)))

	right := op.Compose(
		op.Color(buf, th.Text),
		op.Mask(buf, assets.ArrowRight),
	)
	rightsz := h.Add(assets.ArrowRight.Bounds().Size())
	right = right.Offset(image.Pt(width-margin-rightsz.X, h.Y(rightsz)))

	plates, sz := layoutMainPlates(buf, m.prog)
	contentsz := h.Add(sz)

	content := plates.Offset(image.Pt((width-contentsz.X)/2, 8+h.Y(contentsz)))
	const npage = int(backupWallet) + 1
	if npage > 1 {
		content = op.Layer(content, left, right)
	}

	return content, image.Pt(width, h.Size.Y)
}

func layoutMainPlates(buf *op.Buffer, page program) (op.Op, image.Point) {
	switch page {
	case backupWallet:
		img := assets.Hammer
		o := op.Image(buf, img)
		return o, img.Bounds().Size()
	}
	panic("invalid page")
}

func layoutMainPager(buf *op.Buffer, th *Colors, page program) (op.Op, image.Point) {
	const npages = int(backupWallet) + 1
	const space = 4
	if npages <= 1 {
		return op.Op{}, image.Point{}
	}
	sz := assets.CircleFilled.Bounds().Size()
	var content op.Op
	for i := range npages {
		mask := assets.Circle
		if i == int(page) {
			mask = assets.CircleFilled
		}
		content = op.Layer(content,
			op.Compose(
				op.Color(buf, th.Text),
				op.Mask(buf, mask),
			).Offset(image.Pt((sz.X+space)*i, 0)),
		)
	}
	return content, image.Pt((sz.X+space)*npages-space, sz.Y)
}

func engraveObjectFlow(ctx *Context, th *Colors, obj any) bool {
	switch scan := obj.(type) {
	case bip39.Mnemonic:
		backupWalletFlow(ctx, th, scan)
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
		backupSeedStringFlow(ctx, th, s)
	case *bip380.Descriptor:
		descriptorFlow(ctx, th, scan)
	default:
		return false
	}
	return true
}

func backupWalletFlow(ctx *Context, th *Colors, mnemonic bip39.Mnemonic) {
	ss := new(SeedScreen)
	for {
		if !ss.Confirm(ctx, th, mnemonic) {
			return
		}
		plate, err := engraveSeed(ctx.Platform.EngraverParams(), mnemonic)
		if err != nil {
			errScr := NewErrorScreen(err)
			for !ctx.Done {
				dims := ctx.Platform.DisplaySize()
				d, dismissed := errScr.Layout(ctx, th, dims)
				if dismissed {
					break
				}
				main := ss.Draw(ctx, th, dims, mnemonic)
				ctx.Frame(op.Layer(d, main))
			}
			continue
		}
		completed := NewEngraveScreen(ctx, plate).Engrave(ctx, &engraveTheme)
		if completed {
			return
		}
	}
}

func backupSeedStringFlow(ctx *Context, th *Colors, s backup.SeedString) {
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
		completed := NewEngraveScreen(ctx, plate).Engrave(ctx, &engraveTheme)
		if completed {
			return
		}
	}
}

func descriptorFlow(ctx *Context, th *Colors, desc *bip380.Descriptor) {
	ds := &DescriptorScreen{
		Descriptor: desc,
	}
	for {
		plate, ok := ds.Confirm(ctx, th)
		if !ok {
			break
		}
		completed := NewEngraveScreen(ctx, plate).Engrave(ctx, &engraveTheme)
		if completed {
			return
		}
	}
}

func newInputFlow(ctx *Context, th *Colors) (any, bool) {
	for {
		cs := &ChoiceScreen{
			Title:   "Input Seed",
			Lead:    "Choose number of words",
			Choices: []string{"12 WORDS", "24 WORDS" /* , "CODEX32", "SLIP-39" */},
		}
		for {
			choice, ok := cs.Choose(ctx, th)
			if !ok {
				return nil, false
			}
			switch choice {
			case 0, 1:
				mnemonic := emptyBIP39Mnemonic([]int{12, 24}[choice])
				inputWordsFlow(ctx, th, mnemonic, 0)
				if !isEmptyMnemonic(mnemonic) {
					return mnemonic, true
				}
			case 2:
				s, ok := inputCodex32Flow(ctx, th)
				if ok {
					return s, true
				}
				// TODO: re-enable
				// case 3:
				// 	mnemonic := emptySLIP39Mnemonic(20)
				// 	if ok := inputSLIP39Flow(ctx, th, mnemonic, 0); !ok {
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

func (s *SeedScreen) Confirm(ctx *Context, th *Colors, mnemonic bip39.Mnemonic) bool {
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
				d, res := confirm.Layout(ctx, th, dims)
				switch res {
				case ConfirmNo:
					continue events
				case ConfirmYes:
					return false
				}
				main := s.Draw(ctx, th, dims, mnemonic)
				ctx.Frame(op.Layer(d, main))
			}
		}
		if editBtn.Clicked(ctx) {
			inputWordsFlow(ctx, th, mnemonic, s.selected)
			continue
		}
		if confirmBtn.Clicked(ctx) {
			if !isMnemonicComplete(mnemonic) {
				continue
			}
			showErr := func(scr *ErrorScreen) {
				for !ctx.Done {
					dims := ctx.Platform.DisplaySize()
					d, dismissed := scr.Layout(ctx, th, dims)
					if dismissed {
						break
					}
					main := s.Draw(ctx, th, dims, mnemonic)
					ctx.Frame(op.Layer(d, main))
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
		nav, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{
			{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack},
			{Clickable: editBtn, Style: StyleSecondary, Icon: assets.IconEdit},
		}...)
		if isMnemonicComplete(mnemonic) {
			nav2, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{
				{Clickable: confirmBtn, Style: StylePrimary, Icon: assets.IconCheckmark},
			}...)
			nav = op.Layer(nav, nav2)
		}
		content := s.Draw(ctx, th, dims, mnemonic)

		ctx.Frame(op.Layer(
			nav,
			content,
		))
	}
	return false
}

func isMnemonicComplete(m bip39.Mnemonic) bool {
	if slices.Contains(m, -1) {
		return false
	}
	return len(m) > 0
}

func (s *SeedScreen) Draw(ctx *Context, th *Colors, dims image.Point, mnemonic bip39.Mnemonic) op.Op {
	if len(s.words) != len(mnemonic) {
		s.words = make([]Clickable, len(mnemonic))
	}

	style := ctx.Styles.word
	longestPrefix := style.Measure(math.MaxInt, "24: ")
	layoutWord := func(b *op.Buffer, col color.RGBA, n int, word string) (op.Op, image.Point) {
		numOp, prefix := widget.Labelf(b, style, col, "%d: ", n)
		numOp = numOp.Offset(image.Pt(longestPrefix.X-prefix.X, 0))
		txtOp, txt := widget.Label(b, style, col, word)
		txtOp = txtOp.Offset(image.Pt(longestPrefix.X, 0))
		return op.Layer(numOp, txtOp), image.Pt(longestPrefix.X+txt.X, txt.Y)
	}

	y := 0
	_, longest := layoutWord(nil, color.RGBA{}, 24, widestWord)
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
	var m op.Op
	for i, w := range mnemonic {
		col := th.Text
		if i == s.selected {
			col = th.Background
		}
		r := image.Rectangle{Max: longest}
		word := bip39.LabelFor(w)
		w, _ := layoutWord(&ctx.B, col, i+1, word)
		inp := op.Input(&ctx.B, &s.words[i]).Clip(r)
		wordOp := op.Layer(
			w,
			inp,
		)
		if i == s.selected {
			col = th.Background
			r.Min.Y -= 3
			r.Max.Y += buttonPadY
			r.Min.X -= buttonPadX
			r.Max.X += buttonPadX
			wordOp = op.Layer(
				wordOp,
				op.Compose(
					op.Color(&ctx.B, th.Text),
					op.RoundedRect2(&ctx.B, r, cornerRadius),
				),
			)
		}
		pos := image.Pt(0, y).Add(off)
		m = op.Layer(
			m,
			wordOp.Offset(pos),
		)
		y += lineHeight
		// TODO: hack to show words on two columns in
		// touch mode.
		if largeScreen && i == 11 {
			y = 0
			off.X += longest.X + 16
		}
	}
	m = fadeClip(&ctx.B, m, image.Rectangle(list))
	title, _ := layoutTitle(ctx, dims.X, th.Text, "Engrave Seed")
	return op.Layer(
		m,
		title,
		op.Color(&ctx.B, th.Background),
	)
}

type DescriptorScreen struct {
	Descriptor *bip380.Descriptor
}

func (s *DescriptorScreen) Confirm(ctx *Context, th *Colors) (Plate, bool) {
	showErr := func(errScreen *ErrorScreen) {
		for !ctx.Done {
			dims := ctx.Platform.DisplaySize()
			d, dismissed := errScreen.Layout(ctx, th, dims)
			if dismissed {
				break
			}
			main := s.Draw(ctx, th, dims)
			ctx.Frame(op.Layer(d, main))
		}
	}
	backBtn := &Clickable{Button: Button1}
	confirmBtn := &Clickable{Button: Button3}
	for !ctx.Done {
		if backBtn.Clicked(ctx) {
			break
		}
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
			choice, ok := cs.Choose(ctx, th)
			if ok {
				e := engravings[choice]
				return e, true
			}
		}

		dims := ctx.Platform.DisplaySize()
		nav, _ := layoutNavigation(&ctx.B, th, dims, []NavButton{
			{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack},
			{Clickable: confirmBtn, Style: StylePrimary, Icon: assets.IconCheckmark},
		}...)
		content := s.Draw(ctx, th, dims)
		ctx.Frame(op.Layer(nav, content))
	}
	return Plate{}, false
}

func (s *DescriptorScreen) Draw(ctx *Context, th *Colors, dims image.Point) op.Op {
	const infoSpacing = 8

	desc := s.Descriptor

	// Title.
	r := layout.Rectangle{Max: dims}

	btnw := assets.NavBtnPrimary.Bounds().Dx()
	body := r.Shrink(leadingSize, btnw, 0, btnw)

	var bodytxt richText

	bodyst := ctx.Styles.body
	subst := ctx.Styles.subtitle
	if desc.Title != "" {
		bodytxt.Add(&ctx.B, subst, body.Dx(), th.Text, "Title")
		bodytxt.Add(&ctx.B, bodyst, body.Dx(), th.Text, desc.Title)
		bodytxt.Y += infoSpacing
	}
	bodytxt.Add(&ctx.B, subst, body.Dx(), th.Text, "Type")
	testnet := any("") // TODO: TinyGo allocates without explicit interface conversion.
	if len(desc.Keys) > 0 && desc.Keys[0].Network != &chaincfg.MainNetParams {
		testnet = " (testnet)"
	}
	switch desc.Type {
	case bip380.Singlesig:
		bodytxt.Addf(&ctx.B, bodyst, body.Dx(), th.Text, "Singlesig%s", testnet)
	default:
		bodytxt.Addf(&ctx.B, bodyst, body.Dx(), th.Text, "%d-of-%d multisig%s", desc.Threshold, len(desc.Keys), testnet)
	}
	bodytxt.Y += infoSpacing
	bodytxt.Add(&ctx.B, subst, body.Dx(), th.Text, "Script")
	bodytxt.Add(&ctx.B, bodyst, body.Dx(), th.Text, desc.Script.String())

	bodyOp := bodytxt.Content.Offset(body.Min.Add(image.Pt(0, scrollFadeDist)))

	title, _ := layoutTitle(ctx, dims.X, th.Text, "Engrave Descriptor")
	return op.Layer(
		bodyOp,
		title,
		op.Color(&ctx.B, th.Background),
	)
}

func NewEngraveScreen(ctx *Context, plate Plate) *EngraveScreen {
	return &EngraveScreen{
		duration: plate.Duration,
		job:      newEngraverJob(ctx.Platform, plate.Spline, 0),
	}
}

type EngraveScreen struct {
	duration uint
	job      *engraveJob
}

func (s *EngraveScreen) Engrave(ctx *Context, th *Colors) bool {
	defer s.job.Stop()
	inp := new(InputTracker)
	backBtn := &Clickable{Button: Button1}
	selectBtn := &Clickable{Button: Button3, AltButton: Center}
frames:
	for !ctx.Done {
		for backBtn.Clicked(ctx) {
			st := s.job.Status()
			if st.State != engraveRunning {
				break frames
			}
			s.job.Stop()
		}
		switch s.job.Status().State {
		case engraveDone:
			if selectBtn.Clicked(ctx) {
				return true
			}
		default:
			if _, ok := selectBtn.Next(ctx); !ok {
				break
			}
			if !selectBtn.Pressed {
				break
			}
			confirm := new(ConfirmDelay)
			confirm.Start(ctx, confirmDelay)
			inp.Pressed[selectBtn.Button] = false
			selectBtn.Pressed = false
			for !ctx.Done {
				p := confirm.Progress(ctx)
				if p == 1. {
					s.job.Start()
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
				nav := s.drawNav(&ctx.B, th, dims, p, backBtn, selectBtn)
				content := s.draw(ctx, th, dims)
				ctx.Frame(op.Layer(nav, content))
			}
		}

		if s.job.Status().State == engraveRunning {
			// Update progress twice a second.
			ctx.WakeupAt(time.Now().Add(time.Second / 2))
		}

		dims := ctx.Platform.DisplaySize()
		nav := s.drawNav(&ctx.B, th, dims, 0, backBtn, selectBtn)
		content := s.draw(ctx, th, dims)

		ctx.Frame(op.Layer(nav, content))
	}
	return false
}

func (s *EngraveScreen) draw(ctx *Context, th *Colors, dims image.Point) op.Op {
	r := layout.Rectangle{Max: dims}

	const margin = 8
	_, content := r.CutTop(leadingSize)

	st := s.job.Status()
	var contentOp op.Op
	switch st.State {
	default:
		content := content.Shrink(0, margin, 0, margin)
		content, _ = content.CutBottom(leadingSize)
		var bodysz image.Point
		var bodyOp op.Op
		switch st.State {
		case engraveIdle:
			const body = "Insert a blank plate and close the lock.\n\nHold button to start the engraving process. The process is loud, use hearing protection."
			bodyOp, bodysz = widget.Labelw(&ctx.B, ctx.Styles.lead, content.Dx(), th.Text, body)
		case engraveDone:
			const body = "Engraving completed successfully."
			bodyOp, bodysz = widget.Labelw(&ctx.B, ctx.Styles.lead, content.Dx(), th.Text, body)
		case engraveStopped:
			const body = "Engraving paused.\nHold button to resume."
			bodyOp, bodysz = widget.Labelw(&ctx.B, ctx.Styles.lead, content.Dx(), th.Text, body)
		case engraveStopping:
			const body = "Engraving stopping..."
			bodyOp, bodysz = widget.Labelw(&ctx.B, ctx.Styles.lead, content.Dx(), th.Text, body)
		case engraveFailed:
			bodyOp, bodysz = widget.Labelwf(&ctx.B, ctx.Styles.lead, content.Dx(), th.Text,
				"Engraving failed.\nHold button to retry.\n\nError: %s", st.Error)
		}
		contentOp = bodyOp.Offset(content.Center(bodysz))
	case engraveRunning:
		middle, lead := content.CutBottom(leadingSize)
		// Remaining seconds, rounded up.
		rem := s.duration - st.Completed
		tps := ctx.Platform.EngraverParams().TicksPerSecond
		remSec := (rem + tps - 1) / tps
		min, sec := remSec/60, remSec%60
		remOp, sz := widget.Labelf(&ctx.B, ctx.Styles.progress, th.Text, "%d:%.2d", min, sec)
		remOp = remOp.Offset(middle.Center(sz))
		const leadTxt = "Engraving plate"
		leadOp, leadsz := widget.Labelw(&ctx.B, ctx.Styles.lead, dims.X-2*margin, th.Text, leadTxt)
		leadOp = leadOp.Offset(lead.Center(leadsz))
		contentOp = op.Layer(remOp, leadOp)
	}
	title, _ := layoutTitle(ctx, dims.X, th.Text, "Engrave Plate")
	return op.Layer(
		contentOp,
		title,
		op.Color(&ctx.B, th.Background),
	)
}

func (s *EngraveScreen) drawNav(b *op.Buffer, th *Colors, dims image.Point, progress float32, backBtn, selectBtn *Clickable) op.Op {
	st := s.job.Status()
	var nav op.Op
	switch st.State {
	case engraveRunning:
		nav, _ = layoutNavigation(b, th, dims, NavButton{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconLeft})
	case engraveDone:
		nav, _ = layoutNavigation(b, th, dims, NavButton{
			Clickable: selectBtn,
			Style:     StylePrimary,
			Icon:      assets.IconRight,
		})
	case engraveStopping:
		nav, _ = layoutNavigation(b, th, dims, NavButton{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack})
	default:
		nav, _ = layoutNavigation(b, th, dims,
			NavButton{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack},
			NavButton{Clickable: selectBtn, Style: StylePrimary, Icon: assets.IconHammer, Progress: progress},
		)
	}
	return nav
}

type Platform interface {
	LockBoot() error
	AppendEvents(deadline time.Time, evts []Event) []Event
	Wakeup()
	Engraver(stall bool) (Engraver, error)
	NFCReader() io.ReadCloser
	EngraverParams() engrave.Params
	DisplaySize() image.Point
	// Dirty begins a refresh of the content
	// specified by r.
	Dirty(r image.Rectangle) error
	// NextChunk returns the next chunk of the refresh.
	NextChunk() (draw.RGBA64Image, bool)
	Features() Features
	HardwareVersion() string
}

type Features int

const (
	FeatureSecureBoot Features = 1 << iota
)

func (f Features) Has(feat Features) bool {
	return f&feat != 0
}

type EngraverStats struct {
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
		a := struct {
			mask *image.Alpha
			idle struct {
				start  time.Time
				active bool
				state  saver.State
			}
		}{}
		a.idle.start = time.Now()

		it := func(yield func(op.Op) bool) {
			ctx.FrameCallback = func(op op.Op) {
				ctx.Done = ctx.Done || !yield(op)
			}
			version := "Firmware: " + version + "\nHardware: " + pl.HardwareVersion()
			if !pl.Features().Has(FeatureSecureBoot) {
				version += " (UNLOCKED)"
			}
			uiFlow(ctx, version)
		}
		startTime := time.Now()
		var evts []Event
		stats := new(runtimeStats)
		d := new(op.Drawer)
		for content := range it {
			d.Reset()
			dirty := image.Rectangle{Max: pl.DisplaySize()}
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
				d.Draw(fb, a.mask, content)
			}
			drawTime := time.Since(startTime)
			if debug {
				stats.Dump(drawTime, layoutTime)
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
					ctx.Router.Events(d, evts...)
				}
				idleWakeup := a.idle.start.Add(idleTimeout)
				idle := now.Sub(idleWakeup) >= 0
				if a.idle.active != idle {
					a.idle.active = idle
					if idle {
						a.idle.state = saver.State{}
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
			startTime = time.Now()
		}
	}
}

type runtimeStats struct {
	mallocs uint64
	buf     [200]byte
}

func (r *runtimeStats) Dump(drawTime, layoutTime time.Duration) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	dm := mem.Mallocs - r.mallocs
	r.mallocs = mem.Mallocs
	format := "frame: %dms layout: %dms draw: %dms mem %d allocs %d total %d\n"
	// Cast values to int to avoid a TinyGo allocation for larger integers.
	args := []any{int(drawTime.Milliseconds()), int(layoutTime.Milliseconds()), int((drawTime - layoutTime).Milliseconds()),
		uint(mem.HeapInuse), uint(dm), uint(mem.Mallocs - mem.Frees)}
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

func rgb(c uint32) color.RGBA {
	return color.RGBA{A: 0xff, R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
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
		Duration: attrs.Duration,
		Spline:   spline,
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
