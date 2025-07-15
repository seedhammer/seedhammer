// package gui implements the SeedHammer controller user interface.
package gui

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"math"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/address"
	"seedhammer.com/backup"
	"seedhammer.com/bc/ur"
	"seedhammer.com/bc/urtypes"
	"seedhammer.com/bip32"
	"seedhammer.com/bip380"
	"seedhammer.com/bip39"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
	"seedhammer.com/gui/assets"
	"seedhammer.com/gui/layout"
	"seedhammer.com/gui/op"
	"seedhammer.com/gui/saver"
	"seedhammer.com/gui/text"
	"seedhammer.com/gui/widget"
	"seedhammer.com/nfc/poller"
	"seedhammer.com/nonstandard"
	"seedhammer.com/seedqr"
)

type Context struct {
	Platform Platform
	Styles   Styles
	Wakeup   time.Time
	Frame    func()

	// Global UI state.
	Version           string
	Calibrated        bool
	SuppressSDWarning bool
	RotateCamera      bool
	LastDescriptor    *bip380.Descriptor

	// scan is the last scanned object (seed, descriptor
	// etc.).
	scan scanResult

	events  []Event
	pointer struct {
		hits       []BoundedTag
		pressedTag op.Tag
		pressed    bool
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
	return s, s.Content != nil || s.Status > scanIdle
}

func (c *Context) WakeupAt(t time.Time) {
	if c.Wakeup.IsZero() || t.Before(c.Wakeup) {
		c.Wakeup = t
	}
}

func (c *Context) Reset() {
	c.scan = scanResult{}
	c.events = c.events[:0]
	c.Wakeup = time.Time{}
	c.pointer.hits = c.pointer.hits[:0]
}

func (c *Context) Events(o *op.Ops, evts ...Event) {
	c.events = append(c.events, evts...)
	pctx := &c.pointer
	var pressedBounds image.Rectangle
	if o != nil {
		b, ok := o.TagBounds(pctx.pressedTag)
		if !ok {
			pctx.pressedTag = nil
		}
		pressedBounds = b
	}
	for _, e := range evts {
		var pt BoundedTag
		e, ok := e.AsPointer()
		if !ok {
			continue
		}
		if pctx.pressed {
			pt = BoundedTag{
				Tag:    pctx.pressedTag,
				Bounds: pressedBounds,
			}
		} else {
			pt.Tag, pt.Bounds, _ = o.Hit(e.Pos)
			pctx.pressedTag = pt.Tag
		}
		pctx.pressed = e.Pressed
		if !pctx.pressed {
			pctx.pressedTag = nil
		}
		pctx.hits = append(pctx.hits, pt)
	}
}

func (c *Context) Next(filters ...Filter) (Event, bool) {
	for i, e := range c.events {
		for _, f := range filters {
			if e, ok := f.Matches(c.pointer.hits[i], e); ok {
				c.events = append(c.events[:i], c.events[i+1:]...)
				c.pointer.hits = append(c.pointer.hits[:i], c.pointer.hits[i+1:]...)
				return e, true
			}
		}
	}
	return Event{}, false
}

type InputTracker struct {
	Pressed [MaxButton]bool
	clicked [MaxButton]bool
	repeats [MaxButton]time.Time
}

func (t *InputTracker) Next(c *Context, filters ...Filter) (Event, bool) {
	now := c.Platform.Now()
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

	e, ok := c.Next(filters...)
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

const longestWord = "TOMORROW"

type program int

const (
	backupWallet program = iota
)

type richText struct {
	Y int
}

func (r *richText) Add(ops op.Ctx, style text.Style, width int, col color.NRGBA, format string, args ...any) {
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
	for {
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
		_, xpub, err := bip32.Derive(mk, k.DerivationPath)
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

type ScanScreen struct {
	Title string
	Lead  string
}

func (s *ScanScreen) Scan(ctx *Context, ops op.Ctx) (any, bool) {
	var (
		feed, feed2, gray *image.Gray
		cameraErr         error
		decoder           QRDecoder
	)
	backBtn := &Clickable{Button: Button1}
	flipBtn := &Clickable{Button: Button2}
	for {
		const cameraFrameScale = 3
		if backBtn.Clicked(ctx) {
			return nil, false
		}
		for flipBtn.Clicked(ctx) {
			ctx.RotateCamera = !ctx.RotateCamera
		}

		dims := ctx.Platform.DisplaySize()
		if feed == nil || dims != feed.Bounds().Size() {
			feed = image.NewGray(image.Rectangle{Max: dims})
			copy := *feed
			feed2 = &copy
			gray = new(image.Gray)
		}
		ctx.Platform.CameraFrame(dims.Mul(cameraFrameScale))
		for {
			f, ok := ctx.Next(FrameFilter())
			if !ok {
				break
			}
			if f, ok := f.AsFrame(); ok {
				cameraErr = f.Error
				if cameraErr == nil {
					ycbcr := f.Image.(*image.YCbCr)
					*gray = image.Gray{Pix: ycbcr.Y, Stride: ycbcr.YStride, Rect: ycbcr.Bounds()}

					// Swap image (but not backing store) to ensure the graphics backend treats
					// it as dirty.
					feed, feed2 = feed2, feed
					scaleRot(feed, gray, ctx.RotateCamera)
					results, _ := ctx.Platform.ScanQR(gray)
					for _, res := range results {
						if v, ok := decoder.parseQR(res); ok {
							return v, true
						}
					}
				}
			}
		}
		th := &cameraTheme
		r := layout.Rectangle{Max: dims}

		op.ImageOp(ops, feed, false)

		corners := assets.CameraCorners.Add(ops.Begin(), image.Rect(0, 0, 132, 132), false)
		op.Position(ops, ops.End(), r.Center(corners.Size()))

		underlay := assets.ButtonFocused
		background := func(ops op.Ctx, w op.CallOp, dst image.Rectangle, pos image.Point) {
			underlay.Add(ops.Begin(), dst, true)
			op.ColorOp(ops, color.NRGBA{A: theme.overlayMask})
			op.Position(ops, ops.End(), image.Point{})
			op.Position(ops, w, pos)
		}

		title := layoutTitle(ctx, ops.Begin(), dims.X, th.Text, s.Title)
		title.Min.Y += 4
		title.Max.Y -= 4
		background(ops, ops.End(), title, image.Point{})

		// Camera error, if any.
		if err := cameraErr; err != nil {
			sz := widget.Labelwf(ops.Begin(), ctx.Styles.body, dims.X-2*16, th.Text, err.Error())
			op.Position(ops, ops.End(), r.Center(sz))
		}

		width := dims.X - 2*8
		// Lead text.
		sz := widget.Labelwf(ops.Begin(), ctx.Styles.lead, width, th.Text, s.Lead)
		top, footer := r.CutBottom(sz.Y + 2*12)
		pos := footer.Center(sz)
		background(ops, ops.End(), image.Rectangle{Min: pos, Max: pos.Add(sz)}, pos)

		// Progress
		if progress := decoder.Progress(); progress > 0 {
			sz = widget.Labelwf(ops.Begin(), ctx.Styles.lead, width, th.Text, "%d%%", progress)
			_, percent := top.CutBottom(sz.Y)
			pos := percent.Center(sz)
			background(ops, ops.End(), image.Rectangle{Min: pos, Max: pos.Add(sz)}, pos)
		}

		nav := func(btn *Clickable, icn image.RGBA64Image) {
			nav := layoutNavigation(ops.Begin(), th, dims, NavButton{Clickable: btn, Style: StyleSecondary, Icon: icn})
			nav = image.Rectangle(layout.Rectangle(nav).Shrink(underlay.Padding()).Shrink(-2, -4, -2, -2))
			background(ops, ops.End(), nav, image.Point{})
		}
		nav(backBtn, assets.IconBack)
		nav(flipBtn, assets.IconFlip)
		ctx.Frame()
	}
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
	c.timeout = ctx.Platform.Now().Add(delay)
}

func (c *ConfirmDelay) Progress(ctx *Context) float32 {
	if c.timeout.IsZero() {
		return 0.
	}
	now := ctx.Platform.Now()
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
	titlesz := widget.Labelwf(ops.Begin(), ctx.Styles.warning, dims.X-btnOff*2, th.Text, title)
	titlew := ops.End()
	op.Position(ops, titlew, image.Pt((dims.X-titlesz.X)/2, r.Min.Y))

	bodyClip := image.Rectangle{
		Min: image.Pt(pstart+boxMargin, ptop+titlesz.Y),
		Max: image.Pt(dims.X-btnOff, dims.Y-pbottom-boxMargin),
	}
	bodysz := widget.Labelwf(ops.Begin(), ctx.Styles.body, bodyClip.Dx(), th.Text, txt)
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

type errDuplicateKey struct {
	Fingerprint uint32
}

func (e *errDuplicateKey) Error() string {
	return fmt.Sprintf("descriptor contains a duplicate share: %.8x", e.Fingerprint)
}

func (e *errDuplicateKey) Is(target error) bool {
	_, ok := target.(*errDuplicateKey)
	return ok
}

func NewErrorScreen(err error) *ErrorScreen {
	var errDup *errDuplicateKey
	switch {
	case errors.As(err, &errDup):
		return &ErrorScreen{
			Title: "Duplicated Share",
			Body:  fmt.Sprintf("The share %.8x is listed more than once in the wallet.", errDup.Fingerprint),
		}
	case errors.Is(err, backup.ErrDescriptorTooLarge):
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

func validateDescriptor(params engrave.Params, desc *bip380.Descriptor) error {
	keys := make(map[string]bool)
	for _, k := range desc.Keys {
		xpub := k.String()
		if keys[xpub] {
			return &errDuplicateKey{
				Fingerprint: k.MasterFingerprint,
			}
		}
		keys[xpub] = true
	}
	// Do a dummy engrave to see whether the backup fits any plate.
	descPlate := backup.Descriptor{
		Descriptor: desc,
		KeyIdx:     0,
		Font:       constant.Font,
		Size:       backup.LargePlate,
	}
	_, err := backup.EngraveDescriptor(params, descPlate)
	if err != nil {
		return err
	}
	// Verify that every permutation of desc.Threshold shares can recover the
	// descriptor. Note that this is impossible by construction and by exhaustive
	// tests, but it's good to be paranoid.
	if !backup.Recoverable(desc) {
		return errors.New("Descriptor is not recoverable. This is a bug in the program; please report it.")
	}
	return nil
}

type Plate struct {
	Size              backup.PlateSize
	MasterFingerprint uint32
	Sides             []engrave.Plan
}

func engraveSeed(sizes []backup.PlateSize, params engrave.Params, m bip39.Mnemonic) (Plate, error) {
	mfp, err := masterFingerprintFor(m, &chaincfg.MainNetParams)
	if err != nil {
		return Plate{}, err
	}
	var lastErr error
	for _, sz := range sizes {
		seedDesc := backup.Seed{
			KeyIdx:            0,
			Mnemonic:          m,
			Keys:              1,
			MasterFingerprint: mfp,
			Font:              constant.Font,
			Size:              sz,
		}
		seedSide, err := backup.EngraveSeed(params, seedDesc)
		if err != nil {
			lastErr = err
			continue
		}
		return Plate{
			Sides:             []engrave.Plan{seedSide},
			Size:              sz,
			MasterFingerprint: mfp,
		}, nil
	}
	return Plate{}, lastErr
}

func masterFingerprintFor(m bip39.Mnemonic, network *chaincfg.Params) (uint32, error) {
	mk, ok := deriveMasterKey(m, network)
	if !ok {
		return 0, errors.New("failed to derive mnemonic master key")
	}
	mfp, _, err := bip32.Derive(mk, bip32.Path{0})
	if err != nil {
		return 0, err
	}
	return mfp, nil
}

func engravePlate(sizes []backup.PlateSize, params engrave.Params, desc *bip380.Descriptor, keyIdx int, m bip39.Mnemonic) (Plate, error) {
	mfp, err := masterFingerprintFor(m, desc.Keys[keyIdx].Network)
	if err != nil {
		return Plate{}, err
	}
	var lastErr error
	for _, sz := range sizes {
		descPlate := backup.Descriptor{
			Descriptor: desc,
			KeyIdx:     keyIdx,
			Font:       constant.Font,
			Size:       sz,
		}
		descSide, err := backup.EngraveDescriptor(params, descPlate)
		if err != nil {
			lastErr = err
			continue
		}
		seedDesc := backup.Seed{
			Title:             desc.Title,
			KeyIdx:            keyIdx,
			Mnemonic:          m,
			Keys:              len(desc.Keys),
			MasterFingerprint: mfp,
			Font:              constant.Font,
			Size:              sz,
		}
		seedSide, err := backup.EngraveSeed(params, seedDesc)
		if err != nil {
			lastErr = err
			continue
		}
		return Plate{
			Size:              sz,
			MasterFingerprint: mfp,
			Sides:             []engrave.Plan{descSide, seedSide},
		}, nil
	}
	return Plate{}, lastErr
}

func plateImage(p backup.PlateSize) image.RGBA64Image {
	switch p {
	case backup.SquarePlate:
		return assets.Sh02
	case backup.LargePlate:
		return assets.Sh03
	default:
		panic("unsupported plate")
	}
}

func plateName(p backup.PlateSize) string {
	switch p {
	case backup.SquarePlate:
		return "SH02"
	case backup.LargePlate:
		return "SH03"
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
	Side  int
	Image image.RGBA64Image

	resolvedBody string
}

var (
	EngraveFirstSideA = []Instruction{
		{
			Body: "Make sure the fingerprint above represents the intended share.",
			Lead: "seedhammer.com/tip#1",
		},
		{
			Body: "Turn off the engraver and disconnect it from this device.",
			Lead: "seedhammer.com/tip#2",
		},
		{
			Body: "Manually move the hammerhead to the far upper left position.",
			Lead: "seedhammer.com/tip#3",
		},
		{
			Body:  "Place a {{.Name}} on the machine.",
			Image: assets.Sh02,
			Lead:  "seedhammer.com/tip#4",
		},
		{
			Body: "Tighten the nuts firmly.",
			Lead: "seedhammer.com/tip#4",
		},
		{
			Body: "Loosen the hammerhead finger screw. Adjust needle distance to ~1.5 mm above the plate.",
			Lead: "seedhammer.com/tip#5",
		},
		{
			Body: "Tighten the hammerhead finger screw and make sure the depth selector is set to \"Strong\".",
			Lead: "seedhammer.com/tip#6",
		},
		{
			Body: "Turn on the engraving machine and connect this device via the middle port.",
			Lead: "seedhammer.com/tip#7",
		},
		{
			Body: "Hold button to start the engraving process. The process is loud, use hearing protection.",
			Type: ConnectInstruction,
			Lead: "seedhammer.com/tip#8",
		},
		{
			Lead: "Engraving plate",
			Type: EngraveInstruction,
			Side: 0,
		},
	}

	EngraveSideA = []Instruction{
		{
			Body: "Make sure the fingerprint above represents the intended share.",
			Lead: "seedhammer.com/tip#1",
		},
		{
			Body:  "Place a {{.Name}} on the machine.",
			Image: assets.Sh02,
			Lead:  "seedhammer.com/tip#4",
		},
		{
			Body: "Tighten the nuts firmly.",
			Lead: "seedhammer.com/tip#4",
		},
		{
			Body: "Hold button to start the engraving process. The process is loud, use hearing protection.",
			Type: ConnectInstruction,
			Lead: "seedhammer.com/tip#8",
		},
		{
			Lead: "Engraving plate",
			Type: EngraveInstruction,
			Side: 0,
		},
	}

	EngraveSideB = []Instruction{
		{
			Body: "Unscrew the 4 nuts and flip the top metal plate horizontally.",
		},
		{
			Body: "Tighten the nuts firmly.",
		},
		{
			Body: "Hold button to start the engraving process. The process is loud, use hearing protection.",
			Type: ConnectInstruction,
		},
		{
			Lead: "Engraving plate",
			Type: EngraveInstruction,
			Side: 1,
		},
	}

	EngraveSideASimple = []Instruction{
		{
			Body: "Make sure the fingerprint above represents the intended share.",
		},
		{
			Body: "Place a {{.Name}} on the machine.",
		},
		{
			Body: "Hold button to start the engraving process. The process is loud, use hearing protection.",
			Type: ConnectInstruction,
			Lead: "seedhammer.com/tip#8",
		},
		{
			Lead: "Engraving plate",
			Type: EngraveInstruction,
			Side: 0,
		},
	}

	EngraveSideBSimple = []Instruction{
		{
			Body: "Flip the top metal plate horizontally.",
		},
		{
			Body: "Hold button to start the engraving process. The process is loud, use hearing protection.",
			Type: ConnectInstruction,
		},
		{
			Lead: "Engraving plate",
			Type: EngraveInstruction,
			Side: 1,
		},
	}

	EngraveSuccess = []Instruction{
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

func emptyMnemonic(nwords int) bip39.Mnemonic {
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

func inputWordsFlow(ctx *Context, ops op.Ctx, th *Colors, mnemonic bip39.Mnemonic, selected int) {
	kbd := NewKeyboard(ctx)
	backBtn := &Clickable{Button: Button1}
	okBtn := &Clickable{Button: Button2}
	layoutWord := func(ops op.Ctx, n int, word string) image.Point {
		style := ctx.Styles.word
		return widget.Labelf(ops, style, th.Background, "%2d: %s", n, word)
	}
	longest := layoutWord(op.Ctx{}, 24, longestWord)
	for {
		kbd.Update(ctx)
		if backBtn.Clicked(ctx) {
			return
		}
		for okBtn.Clicked(ctx) {
			w, complete := kbd.Complete()
			if !complete {
				continue
			}
			kbd.Clear()
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

		layoutWord(ops.Begin(), selected+1, kbd.WordLabel)
		word := ops.End()
		r := image.Rectangle{Max: longest}
		r.Min.Y -= 3
		assets.ButtonFocused.Add(ops.Begin(), r, true)
		op.ColorOp(ops, th.Text)
		word.Add(ops)
		top, _ := content.CutBottom(kbdsz.Y)
		op.Position(ops, ops.End(), top.Center(longest))

		layoutNavigation(ops, th, dims, []NavButton{{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack}}...)
		if _, complete := kbd.Complete(); complete {
			layoutNavigation(ops, th, dims, []NavButton{{Clickable: okBtn, Style: StylePrimary, Icon: assets.IconCheckmark}}...)
		}
		ctx.Frame()
	}
}

var kbdKeys = [...][]rune{
	[]rune("qwertyuiop"),
	[]rune("asdfghjkl"),
	[]rune("zxcvbnm⌫"),
}

const nkeys = 27

type Keyboard struct {
	Word      string
	WordLabel string

	nvalid    int
	keys      [len(kbdKeys)][]keyboardKey
	widest    image.Point
	backspace image.Point
	size      image.Point

	mask     uint32
	row, col int
	inp      InputTracker

	allKeys [nkeys]keyboardKey
}

type keyboardKey struct {
	pos image.Point
	clk Clickable
}

func NewKeyboard(ctx *Context) *Keyboard {
	k := new(Keyboard)
	k.widest = ctx.Styles.keyboard.Measure(math.MaxInt, "W")
	bsb := assets.KeyBackspace.Bounds()
	bsWidth := bsb.Min.X*2 + bsb.Dx()
	k.backspace = image.Pt(bsWidth, k.widest.Y)
	bgbnds := assets.Key.Bounds(image.Rectangle{Max: k.widest})
	const margin = 2
	bgsz := bgbnds.Size().Add(image.Pt(margin, margin))
	longest := 0
	for _, row := range kbdKeys {
		if n := len(row); n > longest {
			longest = n
		}
	}
	maxw := longest*bgsz.X - margin
	allKeys := k.allKeys[:]
	for i, row := range kbdKeys {
		n := len(row)
		if i == len(kbdKeys)-1 {
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
		Y: len(kbdKeys)*bgsz.Y - margin,
	}
	k.Clear()
	return k
}

func (k *Keyboard) Complete() (bip39.Word, bool) {
	word := k.Word
	w, ok := bip39.ClosestWord(word)
	if !ok {
		return -1, false
	}
	// The word is complete if it's in the word list or is the only option.
	return w, k.nvalid == 1 || word == bip39.LabelFor(w)
}

func (k *Keyboard) Clear() {
	k.Word = ""
	k.WordLabel = ""
	k.updateMask()
	k.row = len(kbdKeys) / 2
	k.col = len(kbdKeys[k.row]) / 2
	k.adjust(false)
}

func (k *Keyboard) updateMask() {
	k.mask = ^uint32(0)
	word := k.Word
	w, valid := bip39.ClosestWord(word)
	if !valid {
		return
	}
	k.nvalid = 0
	for ; w < bip39.NumWords; w++ {
		bip39w := bip39.LabelFor(w)
		if !strings.HasPrefix(bip39w, word) {
			break
		}
		k.nvalid++
		suffix := bip39w[len(word):]
		if len(suffix) > 0 {
			idx, valid := k.idxForRune(rune(suffix[0]))
			if !valid {
				panic("valid by construction")
			}
			k.mask &^= 1 << idx
		}
	}
	if k.nvalid == 1 {
		k.mask = ^uint32(0)
	}
}

func (k *Keyboard) idxForRune(r rune) (int, bool) {
	idx := int(r - 'a')
	if idx < 0 || idx >= 32 {
		return 0, false
	}
	return idx, true
}

func (k *Keyboard) Valid(r rune) bool {
	if r == '⌫' {
		return len(k.Word) > 0
	}
	idx, valid := k.idxForRune(r)
	return valid && k.mask&(1<<idx) == 0
}

func (k *Keyboard) Update(ctx *Context) {
	for i := range k.keys {
		for j := range k.keys[i] {
			key := &k.keys[i][j].clk
			for key.Clicked(ctx) {
				r := kbdKeys[i][j]
				k.row = i
				k.col = j
				k.rune(r)
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
						next = len(kbdKeys[k.row]) - 1
					}
					if !k.Valid(kbdKeys[k.row][next]) {
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
					if next == len(kbdKeys[k.row]) {
						next = 0
					}
					if !k.Valid(kbdKeys[k.row][next]) {
						continue
					}
					k.col = next
					k.adjust(true)
					break
				}
			case Up:
				n := len(kbdKeys)
				next := k.row
				for {
					next = (next - 1 + n) % n
					if k.adjustCol(next) {
						k.adjust(true)
						break
					}
				}
			case Down:
				n := len(kbdKeys)
				next := k.row
				for {
					next = (next + 1) % n
					if k.adjustCol(next) {
						k.adjust(true)
						break
					}
				}
			case Center, Button3:
				r := kbdKeys[k.row][k.col]
				k.rune(r)
			}
		}
		if e, ok := e.AsRune(); ok {
			k.rune(unicode.ToLower(e.Rune))
		}
	}
}

func (k *Keyboard) rune(r rune) {
	if !k.Valid(r) {
		return
	}
	if r == '⌫' {
		_, n := utf8.DecodeLastRuneInString(k.Word)
		k.Word = k.Word[:len(k.Word)-n]
	} else {
		k.Word = k.Word + string(r)
	}
	k.updateMask()
	k.adjust(r == '⌫')
	k.WordLabel = k.Word
	if completedWord, complete := k.Complete(); complete {
		k.WordLabel = bip39.LabelFor(completedWord)
	}
	k.WordLabel = strings.ToUpper(k.WordLabel)
}

// adjust resets the row and column to the nearest valid key, if any.
func (k *Keyboard) adjust(allowBackspace bool) {
	dist := int(1e6)
	current := k.keys[k.row][k.col].pos
	found := false
	for i, row := range kbdKeys {
		j := 0
		for _, key := range row {
			if !k.Valid(key) || key == '⌫' && !allowBackspace {
				j++
				continue
			}
			p := k.keys[i][j].pos
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
	for i, r := range kbdKeys[row] {
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
	for i, row := range kbdKeys {
		for j, key := range row {
			valid := k.Valid(key)
			bg := assets.Key
			bgsz := k.widest
			if key == '⌫' {
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
			if key == '⌫' {
				icn := assets.KeyBackspace
				sz = image.Pt(k.backspace.X, icn.Bounds().Dy())
				op.ImageOp(ops.Begin(), icn, true)
				op.ColorOp(ops, col)
			} else {
				sz = widget.Labelf(ops.Begin(), style, col, "%c", unicode.ToUpper(key))
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
	for {
		switch {
		case cancelBtn.Clicked(ctx):
			return 0, false
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
}

func (s *ChoiceScreen) Draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) {
	r := layout.Rectangle{Max: dims}
	op.ColorOp(ops, th.Background)

	layoutTitle(ctx, ops, dims.X, th.Text, s.Title)

	_, bottom := r.CutTop(leadingSize)
	sz := widget.Labelwf(ops.Begin(), ctx.Styles.lead, dims.X-2*8, th.Text, s.Lead)
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
		sz := widget.Labelf(ops.Begin(), style, col, c)
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
	for {
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
	for {
		for selectBtn.Clicked(ctx) {
			m.selectedFlow(ctx, ops)
			continue
		}
		if scan, ok := ctx.Scan(); ok {
			if time.Now().Before(m.scanTimeout) {
				m.scanStatus = max(m.scanStatus, scan.Status)
			} else {
				m.scanStatus = scan.Status
			}
			m.scanTimeout = time.Now().Add(scanStatusTimeout)
			switch scan := scan.Content.(type) {
			case debugPlan:
				if err := debugEngrave(ctx.Platform, nil); err != nil {
					log.Printf("debug engrave: %v", err)
					m.showError(ctx, ops, "Engraver Error", err.Error())
				}
				continue
			case bip39.Mnemonic:
				backupWalletFlow(ctx, ops, &descriptorTheme, scan)
				continue
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
	stsz := widget.Labelwf(ops.Begin(), ctx.Styles.subtitle, 300, th.Text, sttxt)
	op.Position(ops, ops.End(), r.S(stsz).Sub(image.Pt(0, 16)))

	versz := widget.Labelwf(ops.Begin(), ctx.Styles.debug, 100, th.Text, ctx.Version)
	op.Position(ops, ops.End(), r.SE(versz.Add(image.Pt(4, 0))))
	shsz := widget.Labelwf(ops.Begin(), ctx.Styles.debug, 100, th.Text, "SeedHammer")
	op.Position(ops, ops.End(), r.SW(shsz).Add(image.Pt(3, 0)))
}

func layoutTitle(ctx *Context, ops op.Ctx, width int, col color.NRGBA, title string, args ...any) image.Rectangle {
	const margin = 8
	sz := widget.Labelwf(ops.Begin(), ctx.Styles.title, width-2*16, col, title, args...)
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
	for i := 0; i < npages; i++ {
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
	ws := &ConfirmWarningScreen{
		Title: strings.ToTitle("Remove SD card"),
		Body:  "Remove SD card to continue.\n\nHold button to ignore this warning.",
		Icon:  assets.IconRight,
	}
	th := m.theme()
loop:
	for ctx.Platform.SDCardInserted() && !ctx.SuppressSDWarning {
		dims := ctx.Platform.DisplaySize()
		res := ws.Layout(ctx, ops.Begin(), th, dims)
		dialog := ops.End()
		switch res {
		case ConfirmYes:
			ctx.SuppressSDWarning = true
			break loop
		case ConfirmNo:
			return
		}
		m.draw(ctx, ops, dims)
		dialog.Add(ops)
		ctx.Frame()
	}
	switch m.page {
	case backupWallet:
		mnemonic, ok := newMnemonicFlow(ctx, ops, th)
		if !ok {
			break
		}
		backupWalletFlow(ctx, ops, th, mnemonic)
	}
}

func backupWalletFlow(ctx *Context, ops op.Ctx, th *Colors, mnemonic []bip39.Word) {
	ss := new(SeedScreen)
	for {
		if !ss.Confirm(ctx, ops, th, mnemonic) {
			return
		}
		desc, ok := inputDescriptorFlow(ctx, ops, th, mnemonic)
		if !ok {
			continue
		}
		if desc == nil {
			plate, err := engraveSeed(ctx.Platform.PlateSizes(), ctx.Platform.EngraverParams(), mnemonic)
			if err != nil {
				errScr := NewErrorScreen(err)
				for {
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
			continue
		}

		ds := &DescriptorScreen{
			Descriptor: desc,
			Mnemonic:   mnemonic,
		}
		for {
			keyIdx, ok := ds.Confirm(ctx, ops, th)
			if !ok {
				break
			}
			plate, err := engravePlate(ctx.Platform.PlateSizes(), ctx.Platform.EngraverParams(), desc, keyIdx, mnemonic)
			if err != nil {
				errScr := NewErrorScreen(err)
				for {
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
}

func newMnemonicFlow(ctx *Context, ops op.Ctx, th *Colors) (bip39.Mnemonic, bool) {
	cs := &ChoiceScreen{
		Title:   "Input Seed",
		Lead:    "Choose input method",
		Choices: []string{"KEYBOARD", "CAMERA"},
	}
	showErr := func(errScreen *ErrorScreen) {
		for {
			dims := ctx.Platform.DisplaySize()
			dismissed := errScreen.Layout(ctx, ops.Begin(), th, dims)
			d := ops.End()
			if dismissed {
				break
			}
			cs.Draw(ctx, ops, th, dims)
			d.Add(ops)
			ctx.Frame()
		}
	}
outer:
	for {
		var choice int
		if ctx.Platform.Features().Has(FeatureCamera) {
			c, ok := cs.Choose(ctx, ops, th)
			if !ok {
				return nil, false
			}
			choice = c
		}
		switch choice {
		case 0: // Keyboard.
			cs := &ChoiceScreen{
				Title:   "Input Seed",
				Lead:    "Choose number of words",
				Choices: []string{"12 WORDS", "24 WORDS"},
			}
			for {
				choice, ok := cs.Choose(ctx, ops, th)
				if !ok {
					if !ctx.Platform.Features().Has(FeatureCamera) {
						return nil, false
					}
					continue outer
				}
				mnemonic := emptyMnemonic([]int{12, 24}[choice])
				inputWordsFlow(ctx, ops, th, mnemonic, 0)
				if !isEmptyMnemonic(mnemonic) {
					return mnemonic, true
				}
			}
		case 1: // Camera.
			res, ok := (&ScanScreen{
				Title: "Scan",
				Lead:  "SeedQR or Mnemonic",
			}).Scan(ctx, ops)
			if !ok {
				continue
			}
			if b, ok := res.([]byte); ok {
				if sqr, ok := seedqr.Parse(b); ok {
					res = sqr
				} else if sqr, err := bip39.ParseMnemonic(strings.ToLower(string(b))); err == nil || errors.Is(err, bip39.ErrInvalidChecksum) {
					res = sqr
				}
			}
			seed, ok := res.(bip39.Mnemonic)
			if !ok {
				showErr(&ErrorScreen{
					Title: "Invalid Seed",
					Body:  "The scanned data does not represent a seed.",
				})
				continue
			}
			return seed, true
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
	for {
		for i := range s.words {
			c := &s.words[i]
			if c.Clicked(ctx) {
				s.selected = i
			}
		}
		for backBtn.Clicked(ctx) {
			if isEmptyMnemonic(mnemonic) {
				return false
			}
			confirm := &ConfirmWarningScreen{
				Title: strings.ToTitle("Discard Seed?"),
				Body:  "Going back will discard the seed.\n\nHold button to confirm.",
				Icon:  assets.IconDiscard,
			}
			for {
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
		for editBtn.Clicked(ctx) {
			inputWordsFlow(ctx, ops, th, mnemonic, s.selected)
			continue
		}
		for confirmBtn.Clicked(ctx) {
			if !isMnemonicComplete(mnemonic) {
				continue
			}
			showErr := func(scr *ErrorScreen) {
				for {
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
}

func isMnemonicComplete(m bip39.Mnemonic) bool {
	for _, w := range m {
		if w == -1 {
			return false
		}
	}
	return len(m) > 0
}

func (s *SeedScreen) Draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point, mnemonic bip39.Mnemonic) {
	if len(s.words) != len(mnemonic) {
		s.words = make([]Clickable, len(mnemonic))
	}
	op.ColorOp(ops, th.Background)
	layoutTitle(ctx, ops, dims.X, th.Text, "Confirm Seed")

	style := ctx.Styles.word
	longestPrefix := style.Measure(math.MaxInt, "24: ")
	layoutWord := func(ops op.Ctx, col color.NRGBA, n int, word string) image.Point {
		prefix := widget.Labelf(ops.Begin(), style, col, "%d: ", n)
		op.Position(ops, ops.End(), image.Pt(longestPrefix.X-prefix.X, 0))
		txt := widget.Labelf(ops.Begin(), style, col, word)
		op.Position(ops, ops.End(), image.Pt(longestPrefix.X, 0))
		return image.Pt(longestPrefix.X+txt.X, txt.Y)
	}

	y := 0
	longest := layoutWord(op.Ctx{}, color.NRGBA{}, 24, longestWord)
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

func inputDescriptorFlow(ctx *Context, ops op.Ctx, th *Colors, mnemonic bip39.Mnemonic) (*bip380.Descriptor, bool) {
	if !ctx.Platform.Features().Has(FeatureCamera) {
		// Skip.
		return nil, true
	}

	cs := &ChoiceScreen{
		Title:   "Descriptor",
		Lead:    "Choose input method",
		Choices: []string{"SCAN", "SKIP"},
	}
	if ctx.LastDescriptor != nil {
		if _, match := descriptorKeyIdx(ctx.LastDescriptor, mnemonic, ""); match {
			cs.Choices = append(cs.Choices, "RE-USE")
		}
	}
	showErr := func(errScreen *ErrorScreen) {
		for {
			dims := ctx.Platform.DisplaySize()
			dismissed := errScreen.Layout(ctx, ops.Begin(), th, dims)
			d := ops.End()
			if dismissed {
				break
			}
			cs.Draw(ctx, ops, th, dims)
			d.Add(ops)
			ctx.Frame()
		}
	}
	for {
		choice, ok := cs.Choose(ctx, ops, th)
		if !ok {
			return nil, false
		}
		switch choice {
		case 0: // Scan.
			res, ok := (&ScanScreen{
				Title: "Scan",
				Lead:  "Wallet Output Descriptor",
			}).Scan(ctx, ops)
			if !ok {
				continue
			}
			desc, ok := res.(*bip380.Descriptor)
			if !ok {
				if b, isbytes := res.([]byte); isbytes {
					d, err := nonstandard.OutputDescriptor(b)
					desc, ok = d, err == nil
				}
			}
			if !ok {
				showErr(&ErrorScreen{
					Title: "Invalid Descriptor",
					Body:  "The scanned data does not represent a wallet output descriptor or XPUB key.",
				})
				continue
			}
			if !address.Supported(desc) {
				showErr(&ErrorScreen{
					Title: "Invalid Descriptor",
					Body:  "The scanned descriptor is not supported.",
				})
				continue
			}
			if len(desc.Keys) == 1 && desc.Keys[0].MasterFingerprint == 0 {
				mfp, _ := masterFingerprintFor(mnemonic, &chaincfg.MainNetParams)
				desc.Keys[0].MasterFingerprint = mfp
			}
			desc.Title = backup.TitleString(constant.Font, desc.Title)
			ctx.LastDescriptor = desc
			return desc, true
		case 1: // Skip descriptor.
			return nil, true
		case 2: // Re-use.
			return ctx.LastDescriptor, true
		}
	}
}

type DescriptorScreen struct {
	Descriptor *bip380.Descriptor
	Mnemonic   bip39.Mnemonic
}

func (s *DescriptorScreen) Confirm(ctx *Context, ops op.Ctx, th *Colors) (int, bool) {
	showErr := func(errScreen *ErrorScreen) {
		for {
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
	infoBtn := &Clickable{Button: Button2}
	confirmBtn := &Clickable{Button: Button3}
	for {
		if backBtn.Clicked(ctx) {
			return 0, false
		}
		for infoBtn.Clicked(ctx) {
			ShowAddressesScreen(ctx, ops, th, s.Descriptor)
		}
		for confirmBtn.Clicked(ctx) {
			if err := validateDescriptor(ctx.Platform.EngraverParams(), s.Descriptor); err != nil {
				showErr(NewErrorScreen(err))
				continue
			}
			keyIdx, ok := descriptorKeyIdx(s.Descriptor, s.Mnemonic, "")
			if !ok {
				// Passphrase protected seeds don't match the descriptor, so
				// allow the user to ignore the mismatch. Don't allow this for
				// multisig descriptors where we can't know which key the seed
				// belongs to.
				if len(s.Descriptor.Keys) == 1 {
					confirm := &ConfirmWarningScreen{
						Title: strings.ToTitle("Unknown Wallet"),
						Body:  "The wallet does not match the seed.\n\nIf it is passphrase protected, long press to confirm.",
						Icon:  assets.IconCheckmark,
					}
				loop:
					for {
						dims := ctx.Platform.DisplaySize()
						res := confirm.Layout(ctx, ops.Begin(), th, dims)
						d := ops.End()
						switch res {
						case ConfirmYes:
							return 0, true
						case ConfirmNo:
							break loop
						}
						s.Draw(ctx, ops, th, dims)
						d.Add(ops)
						ctx.Frame()
					}
				} else {
					showErr(&ErrorScreen{
						Title: "Unknown Wallet",
						Body:  "The wallet does not match the seed or is passphrase protected.",
					})
				}
				continue
			}
			return keyIdx, true
		}

		dims := ctx.Platform.DisplaySize()
		s.Draw(ctx, ops, th, dims)
		layoutNavigation(ops, th, dims, []NavButton{
			{Clickable: backBtn, Style: StyleSecondary, Icon: assets.IconBack},
			{Clickable: infoBtn, Style: StyleSecondary, Icon: assets.IconInfo},
			{Clickable: confirmBtn, Style: StylePrimary, Icon: assets.IconCheckmark},
		}...)
		ctx.Frame()
	}
}

func (s *DescriptorScreen) Draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) {
	const infoSpacing = 8

	desc := s.Descriptor
	op.ColorOp(ops, th.Background)

	// Title.
	r := layout.Rectangle{Max: dims}
	layoutTitle(ctx, ops, dims.X, th.Text, "Confirm Wallet")

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
			bodytxt.Add(ops, bodyst, body.Dx(), th.Text, "Singlesig%s", testnet)
		default:
			bodytxt.Add(ops, bodyst, body.Dx(), th.Text, "%d-of-%d multisig%s", desc.Threshold, len(desc.Keys), testnet)
		}
		bodytxt.Y += infoSpacing
		bodytxt.Add(ops, subst, body.Dx(), th.Text, "Script")
		bodytxt.Add(ops, bodyst, body.Dx(), th.Text, desc.Script.String())
	}

	op.Position(ops, ops.End(), body.Min.Add(image.Pt(0, scrollFadeDist)))
}

func NewEngraveScreen(ctx *Context, plate Plate) *EngraveScreen {
	var ins []Instruction
	ext := ctx.Platform.Features().Has(FeatureExternalEngraver)
	switch {
	case ext && !ctx.Calibrated:
		ins = append(ins, EngraveFirstSideA...)
	case ext && ctx.Calibrated:
		ins = append(ins, EngraveSideA...)
	default:
		ins = append(ins, EngraveSideASimple...)
	}
	if len(plate.Sides) > 1 {
		if ext {
			ins = append(ins, EngraveSideB...)
		} else {
			ins = append(ins, EngraveSideBSimple...)
		}
	}
	ins = append(ins, EngraveSuccess...)
	s := &EngraveScreen{
		plate:        plate,
		instructions: ins,
	}
	for i, ins := range s.instructions {
		repl := strings.NewReplacer(
			"{{.Name}}", plateName(plate.Size),
		)
		s.instructions[i].resolvedBody = repl.Replace(ins.Body)
		// As a special case, the Sh02 image is a placeholder for the plate-specific image.
		if ins.Image == assets.Sh02 {
			s.instructions[i].Image = plateImage(plate.Size)
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
	engrave engraveState
}

type engraveState struct {
	dev          Engraver
	cancel       chan struct{}
	progress     chan float32
	errs         chan error
	lastProgress float32
}

func (s *EngraveScreen) showError(ctx *Context, ops op.Ctx, th *Colors, errScr *ErrorScreen) {
	for {
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

func (s *EngraveScreen) moveStep(ctx *Context, ops op.Ctx, th *Colors) bool {
	ins := s.instructions[s.step]
	if ins.Type == ConnectInstruction {
		if s.engrave.dev != nil {
			return false
		}
		s.engrave = engraveState{}
		dev, err := ctx.Platform.Engraver()
		if err != nil {
			log.Printf("gui: failed to connect to engraver: %v", err)
			s.showError(ctx, ops, th, &ErrorScreen{
				Title: "Connection Error",
				Body:  fmt.Sprintf("Ensure the engraver is turned on and verify that it is connected to the middle port of this device.\n\nError details: %v", err),
			})
			return false
		}
		s.engrave.dev = dev
	}
	s.step++
	if s.step == len(s.instructions) {
		return true
	}
	ins = s.instructions[s.step]
	if ins.Type == EngraveInstruction {
		plan := s.plate.Sides[ins.Side]
		if s.dryRun.enabled {
			plan = engrave.DryRun(plan)
		}
		totalDist := 0
		pen := image.Point{}
		for cmd := range plan {
			totalDist += engrave.ManhattanDist(pen, cmd.Coord)
			pen = cmd.Coord
		}
		cancel := make(chan struct{})
		errs := make(chan error, 1)
		progress := make(chan float32, 1)
		s.engrave.cancel = cancel
		s.engrave.errs = errs
		s.engrave.progress = progress
		dev := s.engrave.dev
		wakeup := ctx.Platform.Wakeup
		go func() {
			defer wakeup()
			defer dev.Close()
			pplan := func(yield func(cmd engrave.Command) bool) {
				dist := 0
				completed := 0
				pen := image.Point{}
				for cmd := range plan {
					if !yield(cmd) {
						return
					}
					completed++
					dist += engrave.ManhattanDist(pen, cmd.Coord)
					pen = cmd.Coord
					// Don't spam the progress channel.
					if completed%10 != 0 && dist < totalDist {
						continue
					}
					select {
					case <-progress:
					default:
					}
					p := float32(dist) / float32(totalDist)
					progress <- p
					wakeup()
				}
			}
			errs <- dev.Engrave(s.plate.Size, pplan, cancel)
		}()
	}
	return false
}

func (s *EngraveScreen) canPrev() bool {
	return s.step > 0 && s.instructions[s.step-1].Type == PrepareInstruction
}

func (s *EngraveScreen) Engrave(ctx *Context, ops op.Ctx, th *Colors) bool {
	defer func() {
		if s.engrave.cancel != nil {
			close(s.engrave.cancel)
		}
		s.engrave = engraveState{}
	}()
	inp := new(InputTracker)
	backBtn := &Clickable{Button: Button1}
	selectBtn := &Clickable{Button: Button3, AltButton: Center}
	for {
	loop:
		for {
			select {
			case p := <-s.engrave.progress:
				s.engrave.lastProgress = p
			case err := <-s.engrave.errs:
				s.engrave = engraveState{}
				if err != nil {
					log.Printf("gui: connection lost to engraver: %v", err)
					s.step--
					s.showError(ctx, ops, th, &ErrorScreen{
						Title: "Connection Error",
						Body:  fmt.Sprintf("Turn off the engraver and disconnect this device from it. Wait 10 seconds, then turn on the engraver and reconnect.\n\nError details: %v", err),
					})
					break
				}
				ctx.Calibrated = true
				s.step++
				if s.step == len(s.instructions) {
					return true
				}
			default:
				break loop
			}
		}

	outer:
		for {
			if !s.dryRun.timeout.IsZero() {
				now := ctx.Platform.Now()
				d := s.dryRun.timeout.Sub(now)
				if d <= 0 {
					s.dryRun.timeout = time.Time{}
					s.dryRun.enabled = !s.dryRun.enabled
				}
			}
			for backBtn.Clicked(ctx) {
				if s.canPrev() {
					s.step--
				} else {
					confirm := &ConfirmWarningScreen{
						Title: strings.ToTitle("Cancel?"),
						Body:  "This will cancel the engraving process.\n\nHold button to confirm.",
						Icon:  assets.IconDiscard,
					}
				loop2:
					for {
						dims := ctx.Platform.DisplaySize()
						res := confirm.Layout(ctx, ops.Begin(), th, dims)
						d := ops.End()
						switch res {
						case ConfirmNo:
							break loop2
						case ConfirmYes:
							return false
						}
						s.draw(ctx, ops, th, dims)
						d.Add(ops)
						ctx.Frame()
					}
				}
				continue
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
					for {
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
								continue outer
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
				if s.moveStep(ctx, ops, th) {
					return true
				}
			}
			e, ok := inp.Next(ctx, ButtonFilter(Button2))
			if !ok {
				break
			}
			if e, ok := e.AsButton(); ok {
				switch e.Button {
				case Button2:
					if e.Pressed {
						t := ctx.Platform.Now().Add(confirmDelay)
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
}

func (s *EngraveScreen) draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) {
	op.ColorOp(ops, th.Background)
	layoutTitle(ctx, ops, dims.X, th.Text, "Engrave Plate")

	r := layout.Rectangle{Max: dims}
	_, subt := r.CutTop(leadingSize)
	subtsz := widget.Labelf(ops.Begin(), ctx.Styles.body, th.Text, "%.8x", s.plate.MasterFingerprint)
	op.Position(ops, ops.End(), subt.N(subtsz).Sub(image.Pt(0, 4)))

	const margin = 8
	_, content := r.CutTop(leadingSize)
	ins := s.instructions[s.step]
	if ins.Type == EngraveInstruction {
		_, content = subt.CutTop(subtsz.Y)
		middle, _ := content.CutBottom(leadingSize)
		op.Offset(ops, middle.Center(assets.ProgressCircle.Bounds().Size()))
		(&ProgressImage{
			Progress: s.engrave.lastProgress,
			Src:      assets.ProgressCircle,
		}).Add(ops)
		op.ColorOp(ops, th.Text)
		sz := widget.Labelf(ops.Begin(), ctx.Styles.progress, th.Text, "%d%%", int(s.engrave.lastProgress*100))
		op.Position(ops, ops.End(), middle.Center(sz))
	}
	content = content.Shrink(0, margin, 0, margin)
	content, lead := content.CutBottom(leadingSize)
	bodysz := widget.Labelwf(ops.Begin(), ctx.Styles.lead, content.Dx(), th.Text, ins.resolvedBody)
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
	leadsz := widget.Labelwf(ops.Begin(), ctx.Styles.lead, dims.X-2*margin, th.Text, ins.Lead)
	op.Position(ops, ops.End(), lead.Center(leadsz))

	progressw := dims.X * (s.step + 1) / len(s.instructions)
	op.ClipOp(image.Rectangle{Max: image.Pt(progressw, 2)}).Add(ops)
	op.ColorOp(ops, th.Text)

	if s.dryRun.enabled {
		sz := widget.Labelf(ops.Begin(), ctx.Styles.debug, th.Text, "dry-run")
		op.Position(ops, ops.End(), r.SE(sz).Sub(image.Pt(4, 0)))
	}
}

func (s *EngraveScreen) drawNav(ops op.Ctx, th *Colors, dims image.Point, progress float32, backBtn, selectBtn *Clickable) {
	icnBack := assets.IconBack
	if s.canPrev() {
		icnBack = assets.IconLeft
	}
	layoutNavigation(ops, th, dims, NavButton{Clickable: backBtn, Style: StyleSecondary, Icon: icnBack})
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
	AppendEvents(deadline time.Time, evts []Event) []Event
	Wakeup()
	PlateSizes() []backup.PlateSize
	Engraver() (Engraver, error)
	NFCDevice() (poller.Device, func())
	EngraverParams() engrave.Params
	CameraFrame(size image.Point)
	SDCardInserted() bool
	Now() time.Time
	DisplaySize() image.Point
	// Dirty begins a refresh of the content
	// specified by r.
	Dirty(r image.Rectangle) error
	// NextChunk returns the next chunk of the refresh.
	NextChunk() (draw.RGBA64Image, bool)
	ScanQR(qr *image.Gray) ([][]byte, error)
	Debug() bool
	Features() Features
}

type Features int

const (
	FeatureExternalEngraver Features = 1 << iota
	FeatureCamera
)

func (f Features) Has(feat Features) bool {
	return f&feat != 0
}

type Engraver interface {
	Engrave(sz backup.PlateSize, plan engrave.Plan, quit <-chan struct{}) error
	Close()
}

const idleTimeout = 3 * time.Minute

func Run(pl Platform, version string) func(yield func() bool) {
	return func(yield func() bool) {
		ctx := NewContext(pl)
		ctx.Version = version
		a := struct {
			root op.Ops
			mask *image.Alpha
			ctx  *Context
			idle struct {
				start  time.Time
				active bool
				state  saver.State
			}
		}{
			ctx: ctx,
		}
		scans := make(chan scanResult, 1)
		if nfcdev, interrupt := pl.NFCDevice(); nfcdev != nil {
			defer interrupt()
			wakeup := pl.Wakeup
			go func() {
				for scan := range Scan(nfcdev) {
					select {
					case old := <-scans:
						// Merge the previous result.
						if scan.Content == nil {
							scan.Content = old.Content
						}
						scan.Status = max(scan.Status, old.Status)
					default:
					}
					scans <- scan
					wakeup()
				}
			}()
		}
		a.idle.start = pl.Now()

		it := func(yield func() bool) {
			stop := new(int)
			ctx.Frame = func() {
				if !yield() {
					panic(stop)
				}
			}
			defer func() {
				if err := recover(); err != stop {
					panic(err)
				}
			}()
			m := new(MainScreen)
			m.Flow(ctx, a.root.Context())
		}
		startTime := time.Now()
		var evts []Event
		stats := new(runtimeStats)
		for range it {
			dirty := a.root.Clip(image.Rectangle{Max: a.ctx.Platform.DisplaySize()})
			layoutTime := time.Since(startTime)
			if err := a.ctx.Platform.Dirty(dirty); err != nil {
				panic(err)
			}
			for {
				fb, ok := a.ctx.Platform.NextChunk()
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
			if a.ctx.Platform.Debug() {
				stats.Dump(drawTime, layoutTime, dirty)
			}
			for {
				if !yield() {
					return
				}
				wakeup := a.ctx.Wakeup
				evts = a.ctx.Platform.AppendEvents(wakeup, evts[:0])
				if len(evts) > 0 {
					a.idle.start = a.ctx.Platform.Now()
				}
				a.ctx.Reset()
				if !a.idle.active {
					a.ctx.Events(&a.root, evts...)
				}
				select {
				case scan := <-scans:
					a.ctx.scan = scan
					a.idle.start = a.ctx.Platform.Now()
				default:
				}
				idleWakeup := a.idle.start.Add(idleTimeout)
				now := a.ctx.Platform.Now()
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
					a.idle.state.Draw(a.ctx.Platform)
					// Throttle screen saver speed.
					const minFrameTime = 40 * time.Millisecond
					a.ctx.WakeupAt(now.Add(minFrameTime))
					continue
				}
				a.ctx.WakeupAt(idleWakeup)
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

func debugEngrave(p Platform, quit <-chan struct{}) error {
	const sz = backup.SquarePlate
	e, err := p.Engraver()
	if err != nil {
		return err
	}
	defer e.Close()
	plan := func(yield func(engrave.Command) bool) {
		mm := p.EngraverParams().Millimeter
		margin := 1 * mm
		mp := image.Pt(margin, margin)
		dims := sz.Dims().Mul(mm)
		yield(engrave.Move(mp))
		const (
			repeats  = 10
			segments = 16
		)
		center := dims.Div(2)
		radius := dims.X/2 - margin
		for {
			for range repeats {
				yield(engrave.Move(mp))
				yield(engrave.Move(image.Pt(dims.X-margin, margin)))
				yield(engrave.Move(dims.Sub(mp)))
				yield(engrave.Move(image.Pt(margin, dims.Y-margin)))
			}
			for range repeats {
				for i := range segments {
					angle := 2 * math.Pi * float64(i) / segments
					p := image.Point{
						X: center.X + int(float64(radius)*math.Cos(angle)),
						Y: center.Y + int(float64(radius)*math.Sin(angle)),
					}
					yield(engrave.Move(p))
				}
			}
		}
	}
	err = e.Engrave(sz, plan, quit)
	if err != nil {
		return err
	}
	return nil
}
