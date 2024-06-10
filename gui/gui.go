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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/address"
	"seedhammer.com/backup"
	"seedhammer.com/bc/ur"
	"seedhammer.com/bc/urtypes"
	"seedhammer.com/bip32"
	"seedhammer.com/bip39"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
	"seedhammer.com/gui/assets"
	"seedhammer.com/gui/layout"
	"seedhammer.com/gui/op"
	"seedhammer.com/gui/saver"
	"seedhammer.com/gui/text"
	"seedhammer.com/gui/widget"
	"seedhammer.com/nonstandard"
	"seedhammer.com/seedqr"
)

const nbuttons = 8

type Context struct {
	Platform Platform
	Styles   Styles
	Wakeup   time.Time
	Frame    func()

	// Global UI state.
	Version        string
	Calibrated     bool
	EmptySDSlot    bool
	RotateCamera   bool
	LastDescriptor *urtypes.OutputDescriptor

	events []Event
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

const repeatStartDelay = 400 * time.Millisecond
const repeatDelay = 100 * time.Millisecond

func isRepeatButton(b Button) bool {
	switch b {
	case Up, Down, Right, Left:
		return true
	}
	return false
}

func (c *Context) Reset() {
	c.events = c.events[:0]
	c.Wakeup = time.Time{}
}

func (c *Context) Events(evts ...Event) {
	c.events = append(c.events, evts...)
}

func (c *Context) FrameEvent() (FrameEvent, bool) {
	for i, e := range c.events {
		if e, ok := e.AsFrame(); ok {
			c.events = append(c.events[:i], c.events[i+1:]...)
			return e, true
		}
	}
	return FrameEvent{}, false
}

func (c *Context) Next(btns ...Button) (ButtonEvent, bool) {
	for i, e := range c.events {
		e, ok := e.AsButton()
		if !ok {
			continue
		}
		for _, btn := range btns {
			if e.Button == btn {
				c.events = append(c.events[:i], c.events[i+1:]...)
				return e, true
			}
		}
	}
	return ButtonEvent{}, false
}

type InputTracker struct {
	Pressed [nbuttons]bool
	clicked [nbuttons]bool
	repeats [nbuttons]time.Time
}

func (t *InputTracker) Next(c *Context, btns ...Button) (ButtonEvent, bool) {
	now := c.Platform.Now()
	for _, b := range btns {
		if !isRepeatButton(b) {
			continue
		}
		if !t.Pressed[b] {
			t.repeats[b] = time.Time{}
			continue
		}
		wakeup := t.repeats[b]
		if wakeup.IsZero() {
			wakeup = now.Add(repeatStartDelay)
		}
		repeat := !now.Before(wakeup)
		if repeat {
			wakeup = now.Add(repeatDelay)
		}
		t.repeats[b] = wakeup
		c.WakeupAt(wakeup)
		if repeat {
			return ButtonEvent{Button: b, Pressed: true}, true
		}
	}

	e, ok := c.Next(btns...)
	if !ok {
		return ButtonEvent{}, false
	}
	if int(e.Button) < len(t.clicked) {
		t.clicked[e.Button] = !e.Pressed && t.Pressed[e.Button]
		t.Pressed[e.Button] = e.Pressed
	}
	return e, true
}

func (t *InputTracker) Clicked(b Button) bool {
	c := t.clicked[b]
	t.clicked[b] = false
	return c
}

const longestWord = "TOMORROW"

type program int

const (
	backupWallet program = iota
)

type linePos struct {
	W op.CallOp
	Y int
}

type richText struct {
	Lines []linePos
	Y     int
}

func (r *richText) Add(ops op.Ctx, style text.Style, width int, col color.NRGBA, txt string) {
	lines, _ := text.Style{
		Face:       style.Face,
		Alignment:  style.Alignment,
		LineHeight: style.LineHeight,
	}.Layout(width, txt)
	for _, line := range lines {
		doty := line.Dot.Y + r.Y
		(&op.TextOp{
			Src:  image.NewUniform(col),
			Face: style.Face,
			Dot:  image.Pt(line.Dot.X, doty),
			Txt:  line.Text,
		}).Add(ops.Begin())
		r.Lines = append(r.Lines, linePos{ops.End(), doty})
	}
	r.Y += lines[len(lines)-1].Dot.Y
}

type AddressesScreen struct {
	addresses [2][]string
	page      int
	scroll    int
}

func NewAddressesScreen(desc urtypes.OutputDescriptor) *AddressesScreen {
	s := new(AddressesScreen)
	for i := 0; i < 20; i++ {
		addr, err := address.Receive(desc, uint32(i))
		if err != nil {
			// Very unlikely.
			continue
		}
		const addrLen = 12
		s.addresses[0] = append(s.addresses[0], shortenAddress(addrLen, addr))
		change, err := address.Change(desc, uint32(i))
		if err != nil {
			continue
		}
		s.addresses[1] = append(s.addresses[1], shortenAddress(addrLen, change))
	}
	return s
}

func (s *AddressesScreen) Show(ctx *Context, ops op.Ctx, th *Colors) {
	const linesPerPage = 8
	const linesPerScroll = linesPerPage - 3

	const maxPage = len(s.addresses)
	inp := new(InputTracker)
	for {
		for {
			e, ok := inp.Next(ctx, Button1, Left, Right, Up, Down)
			if !ok {
				break
			}
			switch e.Button {
			case Button1:
				if inp.Clicked(e.Button) {
					return
				}
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
					s.scroll -= linesPerScroll
				}
			case Down:
				if e.Pressed {
					s.scroll += linesPerScroll
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

		op.MaskOp(ops.Begin(), assets.ArrowLeft)
		op.ColorOp(ops, th.Text)
		left := ops.End()

		op.MaskOp(ops.Begin(), assets.ArrowRight)
		op.ColorOp(ops, th.Text)
		right := ops.End()

		leftsz := assets.ArrowLeft.Bounds().Size()
		rightsz := assets.ArrowRight.Bounds().Size()

		content := r.Shrink(0, 12, 0, 12)
		body := content.Shrink(leadingSize, rightsz.X+12, 0, leftsz.X+12)
		inner := body.Shrink(scrollFadeDist, 0, scrollFadeDist, 0)

		bodyst := ctx.Styles.body
		var bodytxt richText
		addrs := s.addresses[s.page]
		for i, addr := range addrs {
			bodytxt.Add(ops, bodyst, body.Dx(), th.Text, fmt.Sprintf("%d: %s", i+1, addr))
		}

		op.Position(ops, left, content.W(leftsz))
		op.Position(ops, right, content.E(rightsz))
		maxScroll := len(bodytxt.Lines) - linesPerPage
		if s.scroll > maxScroll {
			s.scroll = maxScroll
		}
		if s.scroll < 0 {
			s.scroll = 0
		}
		off := bodytxt.Lines[s.scroll].Y - bodytxt.Lines[0].Y
		ops.Begin()
		for _, l := range bodytxt.Lines {
			op.Position(ops, l.W, inner.Min.Sub(image.Pt(0, off)))
		}
		fadeClip(ops, ops.End(), image.Rectangle(body))

		layoutNavigation(inp, ops, th, dims, []NavButton{{Button: Button1, Style: StyleSecondary, Icon: assets.IconBack}}...)
		ctx.Frame()
	}
}

func shortenAddress(n int, addr string) string {
	if len(addr) <= n {
		return addr
	}
	return addr[:n/2] + "......" + addr[len(addr)-n/2:]
}

func descriptorKeyIdx(desc urtypes.OutputDescriptor, m bip39.Mnemonic, pass string) (int, bool) {
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
		feed      *image.Gray
		cameraErr error
		decoder   QRDecoder
	)
	inp := new(InputTracker)
	for {
		const cameraFrameScale = 3
		for {
			e, ok := inp.Next(ctx, Button1, Button2)
			if !ok {
				break
			}
			if !inp.Clicked(e.Button) {
				continue
			}
			switch e.Button {
			case Button1:
				return nil, false
			case Button2:
				ctx.RotateCamera = !ctx.RotateCamera
			}
		}

		dims := ctx.Platform.DisplaySize()
		if feed == nil || dims != feed.Bounds().Size() {
			feed = image.NewGray(image.Rectangle{Max: dims})
		}
		ctx.Platform.CameraFrame(dims.Mul(cameraFrameScale))
		for {
			f, ok := ctx.FrameEvent()
			if !ok {
				break
			}
			cameraErr = f.Error
			if cameraErr == nil {
				ycbcr := f.Image.(*image.YCbCr)
				gray := &image.Gray{Pix: ycbcr.Y, Stride: ycbcr.YStride, Rect: ycbcr.Bounds()}

				scaleRot(feed, gray, ctx.RotateCamera)
				// Re-create image (but not backing store) to ensure redraw.
				copy := *feed
				feed = &copy
				results, _ := ctx.Platform.ScanQR(gray)
				for _, res := range results {
					if v, ok := decoder.parseQR(res); ok {
						return v, true
					}
				}
			}
		}
		th := &cameraTheme
		r := layout.Rectangle{Max: dims}

		op.ImageOp(ops, feed)

		corners := assets.CameraCorners.For(image.Rect(0, 0, 132, 132))
		op.ImageOp(ops.Begin(), corners)
		op.Position(ops, ops.End(), r.Center(corners.Bounds().Size()))

		underlay := assets.ButtonFocused
		background := func(ops op.Ctx, w op.CallOp, dst image.Rectangle, pos image.Point) {
			op.MaskOp(ops.Begin(), underlay.For(dst))
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
			sz := widget.LabelW(ops.Begin(), ctx.Styles.body, dims.X-2*16, th.Text, err.Error())
			op.Position(ops, ops.End(), r.Center(sz))
		}

		width := dims.X - 2*8
		// Lead text.
		sz := widget.LabelW(ops.Begin(), ctx.Styles.lead, width, th.Text, s.Lead)
		top, footer := r.CutBottom(sz.Y + 2*12)
		pos := footer.Center(sz)
		background(ops, ops.End(), image.Rectangle{Min: pos, Max: pos.Add(sz)}, pos)

		// Progress
		if progress := decoder.Progress(); progress > 0 {
			sz = widget.LabelW(ops.Begin(), ctx.Styles.lead, width, th.Text, fmt.Sprintf("%d%%", progress))
			_, percent := top.CutBottom(sz.Y)
			pos := percent.Center(sz)
			background(ops, ops.End(), image.Rectangle{Min: pos, Max: pos.Add(sz)}, pos)
		}

		nav := func(btn Button, icn image.RGBA64Image) {
			nav := layoutNavigation(inp, ops.Begin(), th, dims, []NavButton{{Button: btn, Style: StyleSecondary, Icon: icn}}...)
			nav = image.Rectangle(layout.Rectangle(nav).Shrink(underlay.Padding()).Shrink(-2, -4, -2, -2))
			background(ops, ops.End(), nav, image.Point{})
		}
		nav(Button1, assets.IconBack)
		nav(Button2, assets.IconFlip)
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
	inp   InputTracker
}

func (s *ErrorScreen) Layout(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) bool {
	for {
		e, ok := s.inp.Next(ctx, Button3)
		if !ok {
			break
		}
		switch e.Button {
		case Button3:
			if s.inp.Clicked(e.Button) {
				return true
			}
		}
	}
	s.w.Layout(ctx, ops, th, dims, s.Title, s.Body)
	layoutNavigation(&s.inp, ops, th, dims, []NavButton{{Button: Button3, Style: StylePrimary, Icon: assets.IconCheckmark}}...)
	return false
}

type ConfirmWarningScreen struct {
	Title string
	Body  string
	Icon  image.RGBA64Image

	warning Warning
	confirm ConfirmDelay
	inp     InputTracker
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
		e, ok := w.inp.Next(ctx, Up, Down)
		if !ok {
			break
		}
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
	box := wbbg.For(r)
	op.MaskOp(ops, box)
	op.ColorOp(ops, th.Background)
	op.MaskOp(ops, wbout.For(r))
	op.ColorOp(ops, th.Text)

	btnOff := assets.NavBtnPrimary.Bounds().Dx() + btnMargin
	titlesz := widget.LabelW(ops.Begin(), ctx.Styles.warning, dims.X-btnOff*2, th.Text, strings.ToTitle(title))
	titlew := ops.End()
	op.Position(ops, titlew, image.Pt((dims.X-titlesz.X)/2, r.Min.Y))

	bodyClip := image.Rectangle{
		Min: image.Pt(pstart+boxMargin, ptop+titlesz.Y),
		Max: image.Pt(dims.X-btnOff, dims.Y-pbottom-boxMargin),
	}
	bodysz := widget.LabelW(ops.Begin(), ctx.Styles.body, bodyClip.Dx(), th.Text, txt)
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
	var progress float32
	for {
		progress = s.confirm.Progress(ctx)
		if progress == 1 {
			return ConfirmYes
		}
		e, ok := s.inp.Next(ctx, Button3, Button1)
		if !ok {
			break
		}
		switch e.Button {
		case Button1:
			if s.inp.Clicked(e.Button) {
				return ConfirmNo
			}
		case Button3:
			if e.Pressed {
				s.confirm.Start(ctx, confirmDelay)
			} else {
				s.confirm = ConfirmDelay{}
			}
		}
	}
	s.warning.Layout(ctx, ops, th, dims, s.Title, s.Body)
	layoutNavigation(&s.inp, ops, th, dims, []NavButton{
		{Button: Button1, Style: StyleSecondary, Icon: assets.IconBack},
		{Button: Button3, Style: StylePrimary, Icon: s.Icon, Progress: progress},
	}...)
	return ConfirmNone
}

type ProgressImage struct {
	Progress float32
	Src      image.RGBA64Image
}

func (p ProgressImage) ColorModel() color.Model {
	return color.AlphaModel
}

func (p ProgressImage) Bounds() image.Rectangle {
	return p.Src.Bounds()
}

func (p ProgressImage) At(x, y int) color.Color {
	return p.RGBA64At(x, y)
}

func (p ProgressImage) RGBA64At(x, y int) color.RGBA64 {
	c := p.Bounds().Max.Add(p.Bounds().Min).Div(2)
	d := image.Pt(x, y).Sub(c)
	angle := float32(math.Atan2(float64(d.X), float64(d.Y)))
	angle = math.Pi - angle
	if angle > 2*math.Pi*p.Progress {
		return color.RGBA64{}
	}
	return p.Src.RGBA64At(x, y)
}

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

func validateDescriptor(params engrave.Params, desc urtypes.OutputDescriptor) error {
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
	mfp, _, err := bip32.Derive(mk, urtypes.Path{0})
	if err != nil {
		return 0, err
	}
	return mfp, nil
}

func engravePlate(sizes []backup.PlateSize, params engrave.Params, desc urtypes.OutputDescriptor, keyIdx int, m bip39.Mnemonic) (Plate, error) {
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
	case backup.SmallPlate:
		return assets.Sh01
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
	case backup.SmallPlate:
		return "SH01"
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
			Image: assets.Sh01,
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
			Image: assets.Sh01,
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
	op.MaskOp(ops, scrollMask(r))
	op.Position(ops, w, image.Pt(0, 0))
}

type scrollMask image.Rectangle

func (n scrollMask) At(x, y int) color.Color {
	return n.RGBA64At(x, y)
}

func (n scrollMask) RGBA64At(x, y int) color.RGBA64 {
	alpha := 0xffff
	b := n.Bounds()
	if d := y - b.Min.Y; d < scrollFadeDist {
		alpha = 0xffff * d / scrollFadeDist
	} else if d := b.Max.Y - y; d < scrollFadeDist {
		alpha = 0xffff * d / scrollFadeDist
	}
	a16 := uint16(alpha)
	return color.RGBA64{A: a16}
}

func (n scrollMask) Bounds() image.Rectangle {
	return image.Rectangle(n)
}

func (_ scrollMask) ColorModel() color.Model {
	return color.AlphaModel
}

func inputWordsFlow(ctx *Context, ops op.Ctx, th *Colors, mnemonic bip39.Mnemonic, selected int) {
	kbd := NewKeyboard(ctx)
	inp := new(InputTracker)
	for {
		for {
			kbd.Update(ctx)
			e, ok := inp.Next(ctx, Button1, Button2)
			if !ok {
				break
			}
			switch e.Button {
			case Button1:
				if inp.Clicked(e.Button) {
					return
				}
			case Button2:
				if !inp.Clicked(e.Button) {
					break
				}
				w, complete := kbd.Complete()
				if !complete {
					break
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
		}
		dims := ctx.Platform.DisplaySize()
		completedWord, complete := kbd.Complete()
		op.ColorOp(ops, th.Background)
		layoutTitle(ctx, ops, dims.X, th.Text, "Input Words")

		screen := layout.Rectangle{Max: dims}
		_, content := screen.CutTop(leadingSize)
		content, _ = content.CutBottom(8)

		kbdsz := kbd.Layout(ctx, ops.Begin(), th)
		op.Position(ops, ops.End(), content.S(kbdsz))

		layoutWord := func(ops op.Ctx, n int, word string) image.Point {
			style := ctx.Styles.word
			txt := fmt.Sprintf("%2d: %s", n, word)
			return widget.Label(ops, style, th.Background, txt)
		}

		longest := layoutWord(op.Ctx{}, 24, longestWord)
		hint := kbd.Word
		if complete {
			hint = strings.ToUpper(bip39.LabelFor(completedWord))
		}
		layoutWord(ops.Begin(), selected+1, hint)
		word := ops.End()
		r := image.Rectangle{Max: longest}
		r.Min.Y -= 3
		op.MaskOp(ops.Begin(), assets.ButtonFocused.For(r))
		op.ColorOp(ops, th.Text)
		word.Add(ops)
		top, _ := content.CutBottom(kbdsz.Y)
		op.Position(ops, ops.End(), top.Center(longest))

		layoutNavigation(inp, ops, th, dims, []NavButton{{Button: Button1, Style: StyleSecondary, Icon: assets.IconBack}}...)
		if complete {
			layoutNavigation(inp, ops, th, dims, []NavButton{{Button: Button2, Style: StylePrimary, Icon: assets.IconCheckmark}}...)
		}
		ctx.Frame()
	}
}

var kbdKeys = [...][]rune{
	[]rune("QWERTYUIOP"),
	[]rune("ASDFGHJKL"),
	[]rune("ZXCVBNM⌫"),
}

type Keyboard struct {
	Word string

	nvalid    int
	positions [len(kbdKeys)][]image.Point
	bginact   image.Image
	bgact     image.Image
	bsinact   image.Image
	bsact     image.Image
	widest    image.Point
	backspace image.Point
	size      image.Point

	mask     uint32
	row, col int
	inp      InputTracker
}

func NewKeyboard(ctx *Context) *Keyboard {
	k := new(Keyboard)
	_, k.widest = ctx.Styles.keyboard.Layout(math.MaxInt, "W")
	bsb := assets.KeyBackspace.Bounds()
	bsWidth := bsb.Min.X*2 + bsb.Dx()
	k.backspace = image.Pt(bsWidth, k.widest.Y)
	k.bginact = assets.Key.For(image.Rectangle{Max: k.widest})
	k.bgact = assets.KeyActive.For(image.Rectangle{Max: k.widest})
	k.bsinact = assets.Key.For(image.Rectangle{Max: k.backspace})
	k.bsact = assets.KeyActive.For(image.Rectangle{Max: k.backspace})
	bgbnds := k.bginact.Bounds()
	const margin = 2
	bgsz := bgbnds.Size().Add(image.Pt(margin, margin))
	longest := 0
	for _, row := range kbdKeys {
		if n := len(row); n > longest {
			longest = n
		}
	}
	maxw := longest*bgsz.X - margin
	for i, row := range kbdKeys {
		n := len(row)
		if i == len(kbdKeys)-1 {
			// Center row without the backspace key.
			n--
		}
		w := bgsz.X*n - margin
		off := image.Pt((maxw-w)/2, 0)
		for j := range row {
			pos := image.Pt(j*bgsz.X, i*bgsz.Y)
			pos = pos.Add(off)
			pos = pos.Sub(bgbnds.Min)
			k.positions[i] = append(k.positions[i], pos)
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
	word := strings.ToLower(k.Word)
	w, ok := bip39.ClosestWord(word)
	if !ok {
		return -1, false
	}
	// The word is complete if it's in the word list or is the only option.
	return w, k.nvalid == 1 || word == bip39.LabelFor(w)
}

func (k *Keyboard) Clear() {
	k.Word = ""
	k.updateMask()
	k.row = len(kbdKeys) / 2
	k.col = len(kbdKeys[k.row]) / 2
	k.adjust(false)
}

func (k *Keyboard) updateMask() {
	k.mask = ^uint32(0)
	word := strings.ToLower(k.Word)
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
			r := rune(strings.ToUpper(suffix)[0])
			idx, valid := k.idxForRune(r)
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
	idx := int(r - 'A')
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
	for {
		e, ok := k.inp.Next(ctx, Left, Right, Up, Down, CCW, CW, Center, Rune, Button3)
		if !ok {
			break
		}
		if !e.Pressed {
			continue
		}
		switch e.Button {
		case Left, CCW:
			next := k.col
			for {
				next--
				if next == -1 {
					if e.Button == CCW {
						nrows := len(kbdKeys)
						k.row = (k.row - 1 + nrows) % nrows
					}
					next = len(kbdKeys[k.row]) - 1
				}
				if !k.Valid(kbdKeys[k.row][next]) {
					continue
				}
				k.col = next
				k.adjust(true)
				break
			}
		case Right, CW:
			next := k.col
			for {
				next++
				if next == len(kbdKeys[k.row]) {
					if e.Button == CW {
						nrows := len(kbdKeys)
						k.row = (k.row + 1 + nrows) % nrows
					}
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
		case Rune:
			k.rune(e.Rune)
		case Center, Button3:
			r := kbdKeys[k.row][k.col]
			k.rune(r)
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
}

// adjust resets the row and column to the nearest valid key, if any.
func (k *Keyboard) adjust(allowBackspace bool) {
	dist := int(1e6)
	current := k.positions[k.row][k.col]
	found := false
	for i, row := range kbdKeys {
		j := 0
		for _, key := range row {
			if !k.Valid(key) || key == '⌫' && !allowBackspace {
				j++
				continue
			}
			p := k.positions[i][j]
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
		k.row = len(k.positions) - 1
		k.col = len(k.positions[k.row]) - 1
	}
}

// adjustCol sets the column to the one nearest the x position.
func (k *Keyboard) adjustCol(row int) bool {
	dist := int(1e6)
	found := false
	x := k.positions[k.row][k.col].X
	for i, r := range kbdKeys[row] {
		if !k.Valid(r) {
			continue
		}
		p := k.positions[row][i]
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
			bg := k.bginact
			bgsz := k.widest
			if key == '⌫' {
				bg = k.bsinact
			}
			bgcol := th.Text
			style := ctx.Styles.keyboard
			col := th.Text
			switch {
			case !valid:
				bgcol.A = theme.inactiveMask
				col = bgcol
			case i == k.row && j == k.col:
				bg = k.bgact
				if key == '⌫' {
					bg = k.bsact
				}
				col = th.Background
			}
			var sz image.Point
			if key == '⌫' {
				bgsz = k.backspace
				icn := assets.KeyBackspace
				sz = image.Pt(k.backspace.X, icn.Bounds().Dy())
				op.MaskOp(ops.Begin(), icn)
				op.ColorOp(ops, col)
			} else {
				sz = widget.Label(ops.Begin(), style, col, string(key))
			}
			key := ops.End()
			op.MaskOp(ops.Begin(), bg)
			op.ColorOp(ops, bgcol)
			op.Position(ops, key, bgsz.Sub(sz).Div(2))
			op.Position(ops, ops.End(), k.positions[i][j])
		}
	}
	return k.size
}

type ChoiceScreen struct {
	Title   string
	Lead    string
	Choices []string
	choice  int
}

func (s *ChoiceScreen) Choose(ctx *Context, ops op.Ctx, th *Colors) (int, bool) {
	inp := new(InputTracker)
	for {
		for {
			e, ok := inp.Next(ctx, Button1, Button3, Center, Up, Down, CCW, CW)
			if !ok {
				break
			}
			switch e.Button {
			case Button1:
				if inp.Clicked(e.Button) {
					return 0, false
				}
			case Button3, Center:
				if inp.Clicked(e.Button) {
					return s.choice, true
				}
			case Up, CCW:
				if e.Pressed {
					if s.choice > 0 {
						s.choice--
					}
				}
			case Down, CW:
				if e.Pressed {
					if s.choice < len(s.Choices)-1 {
						s.choice++
					}
				}
			}
		}

		dims := ctx.Platform.DisplaySize()
		s.Draw(ctx, ops, th, dims)

		layoutNavigation(inp, ops, th, dims, []NavButton{
			{Button: Button1, Style: StyleSecondary, Icon: assets.IconBack},
			{Button: Button3, Style: StylePrimary, Icon: assets.IconCheckmark},
		}...)
		ctx.Frame()
	}
}

func (s *ChoiceScreen) Draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) {
	r := layout.Rectangle{Max: dims}
	op.ColorOp(ops, th.Background)

	layoutTitle(ctx, ops, dims.X, th.Text, s.Title)

	_, bottom := r.CutTop(leadingSize)
	sz := widget.LabelW(ops.Begin(), ctx.Styles.lead, dims.X-2*8, th.Text, s.Lead)
	content, lead := bottom.CutBottom(leadingSize)
	op.Position(ops, ops.End(), lead.Center(sz))

	content = content.Shrink(16, 0, 16, 0)

	children := make([]struct {
		Size image.Point
		W    op.CallOp
	}, len(s.Choices))
	maxW := 0
	for i, c := range s.Choices {
		style := ctx.Styles.button
		col := th.Text
		if i == s.choice {
			col = th.Background
		}
		sz := widget.Label(ops.Begin(), style, col, c)
		ch := ops.End()
		children[i].Size = sz
		children[i].W = ch
		if sz.X > maxW {
			maxW = sz.X
		}
	}

	inner := ops.Begin()
	h := 0
	for i, c := range children {
		xoff := (maxW - c.Size.X) / 2
		pos := image.Pt(xoff, h)
		txt := c.W
		if i == s.choice {
			bg := image.Rectangle{Max: c.Size}
			bg.Min.X -= xoff
			bg.Max.X += xoff
			op.MaskOp(inner.Begin(), assets.ButtonFocused.For(bg))
			op.ColorOp(inner, th.Text)
			txt.Add(inner)
			txt = inner.End()
		}
		op.Position(inner, txt, pos)
		h += c.Size.Y
	}
	op.Position(ops, ops.End(), content.Center(image.Pt(maxW, h)))
}

func mainFlow(ctx *Context, ops op.Ctx) {
	var page program
	inp := new(InputTracker)
	for {
		dims := ctx.Platform.DisplaySize()
	events:
		for {
			e, ok := inp.Next(ctx, Button3, Center, Left, Right)
			if !ok {
				break
			}
			switch e.Button {
			case Button3, Center:
				if !inp.Clicked(e.Button) {
					break
				}
				ws := &ConfirmWarningScreen{
					Title: "Remove SD card",
					Body:  "Remove SD card to continue.\n\nHold button to ignore this warning.",
					Icon:  assets.IconRight,
				}
				th := mainScreenTheme(page)
			loop:
				for !ctx.EmptySDSlot {
					res := ws.Layout(ctx, ops.Begin(), th, dims)
					dialog := ops.End()
					switch res {
					case ConfirmYes:
						break loop
					case ConfirmNo:
						continue events
					}
					drawMainScreen(ctx, ops, dims, page)
					dialog.Add(ops)
					ctx.Frame()
				}
				ctx.EmptySDSlot = true
				switch page {
				case backupWallet:
					backupWalletFlow(ctx, ops, th)
				}
			case Left:
				if !e.Pressed {
					break
				}
				page--
				if page < 0 {
					page = backupWallet
				}
			case Right:
				if !e.Pressed {
					break
				}
				page++
				if page > backupWallet {
					page = 0
				}
			}
		}
		drawMainScreen(ctx, ops, dims, page)
		layoutNavigation(inp, ops, mainScreenTheme(page), dims, []NavButton{
			{Button: Button3, Style: StylePrimary, Icon: assets.IconCheckmark},
		}...)
		ctx.Frame()
	}
}

func mainScreenTheme(page program) *Colors {
	switch page {
	case backupWallet:
		return &descriptorTheme
	default:
		panic("invalid page")
	}
}

func drawMainScreen(ctx *Context, ops op.Ctx, dims image.Point, page program) {
	var th *Colors
	var title string
	th = mainScreenTheme(page)
	switch page {
	case backupWallet:
		title = "Backup Wallet"
	}
	op.ColorOp(ops, th.Background)

	layoutTitle(ctx, ops, dims.X, th.Text, title)

	r := layout.Rectangle{Max: dims}
	sz := layoutMainPage(ops.Begin(), th, dims.X, page)
	op.Position(ops, ops.End(), r.Center(sz))

	sz = layoutMainPager(ops.Begin(), th, page)
	_, footer := r.CutBottom(leadingSize)
	op.Position(ops, ops.End(), footer.Center(sz))

	versz := widget.LabelW(ops.Begin(), ctx.Styles.debug, 100, th.Text, ctx.Version)
	op.Position(ops, ops.End(), r.SE(versz.Add(image.Pt(4, 0))))
	shsz := widget.LabelW(ops.Begin(), ctx.Styles.debug, 100, th.Text, "SeedHammer")
	op.Position(ops, ops.End(), r.SW(shsz).Add(image.Pt(3, 0)))
}

func layoutTitle(ctx *Context, ops op.Ctx, width int, col color.NRGBA, title string) image.Rectangle {
	const margin = 8
	sz := widget.LabelW(ops.Begin(), ctx.Styles.title, width-2*16, col, title)
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
	Button   Button
	Style    ButtonStyle
	Icon     image.Image
	Progress float32
}

func layoutNavigation(inp *InputTracker, ops op.Ctx, th *Colors, dims image.Point, btns ...NavButton) image.Rectangle {
	navsz := assets.NavBtnPrimary.Bounds().Size()
	button := func(ops op.Ctx, b NavButton) {
		if b.Style == StyleNone {
			return
		}
		switch b.Style {
		case StyleSecondary:
			op.MaskOp(ops, assets.NavBtnPrimary)
			op.ColorOp(ops, th.Background)
			op.MaskOp(ops, assets.NavBtnSecondary)
			op.ColorOp(ops, th.Text)
		case StylePrimary:
			op.MaskOp(ops, assets.NavBtnPrimary)
			op.ColorOp(ops, th.Primary)
		}
		icn := b.Icon
		if b.Progress > 0 {
			icn = ProgressImage{
				Progress: b.Progress,
				Src:      assets.IconProgress,
			}
		}
		op.MaskOp(ops, icn)
		switch b.Style {
		case StyleSecondary:
			op.ColorOp(ops, th.Text)
		case StylePrimary:
			op.ColorOp(ops, th.Text)
		}
		if b.Progress == 0 && inp.Pressed[b.Button] {
			op.MaskOp(ops, assets.NavBtnPrimary)
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
		idx := int(b.Button - Button1)
		button(ops.Begin(), b)
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

func layoutMainPage(ops op.Ctx, th *Colors, width int, page program) image.Point {
	var h layout.Align

	op.MaskOp(ops.Begin(), assets.ArrowLeft)
	op.ColorOp(ops, th.Text)
	left := ops.End()
	leftsz := h.Add(assets.ArrowLeft.Bounds().Size())

	op.MaskOp(ops.Begin(), assets.ArrowRight)
	op.ColorOp(ops, th.Text)
	right := ops.End()
	rightsz := h.Add(assets.ArrowRight.Bounds().Size())

	contentsz := h.Add(layoutMainPlates(ops.Begin(), page))
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
		op.ImageOp(ops, img)
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
		op.MaskOp(ops, mask)
		op.ColorOp(ops, th.Text)
	}
	return image.Pt((sz.X+space)*npages-space, sz.Y)
}

func backupWalletFlow(ctx *Context, ops op.Ctx, th *Colors) {
	mnemonic, ok := newMnemonicFlow(ctx, ops, th)
	if !ok {
		return
	}
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
			Descriptor: *desc,
			Mnemonic:   mnemonic,
		}
		for {
			keyIdx, ok := ds.Confirm(ctx, ops, th)
			if !ok {
				break
			}
			plate, err := engravePlate(ctx.Platform.PlateSizes(), ctx.Platform.EngraverParams(), *desc, keyIdx, mnemonic)
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
		choice, ok := cs.Choose(ctx, ops, th)
		if !ok {
			return nil, false
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
}

func (s *SeedScreen) Confirm(ctx *Context, ops op.Ctx, th *Colors, mnemonic bip39.Mnemonic) bool {
	inp := new(InputTracker)
	for {
	events:
		for {
			e, ok := inp.Next(ctx, Button1, Button2, Center, Button3, Up, Down)
			if !ok {
				break
			}
			switch e.Button {
			case Button1:
				if !inp.Clicked(e.Button) {
					break
				}
				if isEmptyMnemonic(mnemonic) {
					return false
				}
				confirm := &ConfirmWarningScreen{
					Title: "Discard Seed?",
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
			case Button2, Center:
				if !inp.Clicked(e.Button) {
					break
				}
				inputWordsFlow(ctx, ops, th, mnemonic, s.selected)
				continue
			case Button3:
				if !inp.Clicked(e.Button) || !isMnemonicComplete(mnemonic) {
					break
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
					break
				}
				_, ok = deriveMasterKey(mnemonic, &chaincfg.MainNetParams)
				if !ok {
					showErr(&ErrorScreen{
						Title: "Invalid Seed",
						Body:  "The seed is invalid.",
					})
					break
				}
				return true
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

		dims := ctx.Platform.DisplaySize()
		s.Draw(ctx, ops, th, dims, mnemonic)

		layoutNavigation(inp, ops, th, dims, []NavButton{
			{Button: Button1, Style: StyleSecondary, Icon: assets.IconBack},
			{Button: Button2, Style: StyleSecondary, Icon: assets.IconEdit},
		}...)
		if isMnemonicComplete(mnemonic) {
			layoutNavigation(inp, ops, th, dims, []NavButton{
				{Button: Button3, Style: StylePrimary, Icon: assets.IconCheckmark},
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
	op.ColorOp(ops, th.Background)
	layoutTitle(ctx, ops, dims.X, th.Text, "Confirm Seed")

	style := ctx.Styles.word
	_, longestPrefix := style.Layout(math.MaxInt, "24: ")
	layoutWord := func(ops op.Ctx, col color.NRGBA, n int, word string) image.Point {
		prefix := widget.Label(ops.Begin(), style, col, fmt.Sprintf("%d: ", n))
		op.Position(ops, ops.End(), image.Pt(longestPrefix.X-prefix.X, 0))
		txt := widget.Label(ops.Begin(), style, col, word)
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
	off := content.Min.Add(image.Pt(0, -scroll*lineHeight))
	{
		ops := ops.Begin()
		for i, w := range mnemonic {
			ops.Begin()
			col := th.Text
			if i == s.selected {
				col = th.Background
				r := image.Rectangle{Max: longest}
				r.Min.Y -= 3
				op.MaskOp(ops, assets.ButtonFocused.For(r))
				op.ColorOp(ops, th.Text)
			}
			word := strings.ToUpper(bip39.LabelFor(w))
			layoutWord(ops, col, i+1, word)
			pos := image.Pt(0, y).Add(off)
			op.Position(ops, ops.End(), pos)
			y += lineHeight
		}
	}
	fadeClip(ops, ops.End(), image.Rectangle(list))
}

func inputDescriptorFlow(ctx *Context, ops op.Ctx, th *Colors, mnemonic bip39.Mnemonic) (*urtypes.OutputDescriptor, bool) {
	cs := &ChoiceScreen{
		Title:   "Descriptor",
		Lead:    "Choose input method",
		Choices: []string{"SCAN", "SKIP"},
	}
	if ctx.LastDescriptor != nil {
		if _, match := descriptorKeyIdx(*ctx.LastDescriptor, mnemonic, ""); match {
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
			desc, ok := res.(urtypes.OutputDescriptor)
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
			ctx.LastDescriptor = &desc
			return &desc, true
		case 1: // Skip descriptor.
			return nil, true
		case 2: // Re-use.
			return ctx.LastDescriptor, true
		}
	}
}

type DescriptorScreen struct {
	Descriptor urtypes.OutputDescriptor
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
	inp := new(InputTracker)
	for {
		for {
			e, ok := inp.Next(ctx, Button1, Button2, Button3)
			if !ok {
				break
			}
			switch e.Button {
			case Button1:
				if inp.Clicked(e.Button) {
					return 0, false
				}
			case Button2:
				if !inp.Clicked(e.Button) {
					break
				}
				NewAddressesScreen(s.Descriptor).Show(ctx, ops, th)
			case Button3:
				if !inp.Clicked(e.Button) {
					break
				}
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
							Title: "Unknown Wallet",
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
		}

		dims := ctx.Platform.DisplaySize()
		s.Draw(ctx, ops, th, dims)
		layoutNavigation(inp, ops, th, dims, []NavButton{
			{Button: Button1, Style: StyleSecondary, Icon: assets.IconBack},
			{Button: Button2, Style: StyleSecondary, Icon: assets.IconInfo},
			{Button: Button3, Style: StylePrimary, Icon: assets.IconCheckmark},
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

	type linePos struct {
		w op.CallOp
		y int
	}
	var bodytxt richText

	bodyst := ctx.Styles.body
	subst := ctx.Styles.subtitle
	if desc.Title != "" {
		bodytxt.Add(ops, subst, body.Dx(), th.Text, "Title")
		bodytxt.Add(ops, bodyst, body.Dx(), th.Text, desc.Title)
		bodytxt.Y += infoSpacing
	}
	bodytxt.Add(ops, subst, body.Dx(), th.Text, "Type")
	var typetxt string
	switch desc.Type {
	case urtypes.Singlesig:
		typetxt = "Singlesig"
	default:
		typetxt = fmt.Sprintf("%d-of-%d multisig", desc.Threshold, len(desc.Keys))
	}
	if len(desc.Keys) > 0 && desc.Keys[0].Network != &chaincfg.MainNetParams {
		typetxt += " (testnet)"
	}
	bodytxt.Add(ops, bodyst, body.Dx(), th.Text, typetxt)
	bodytxt.Y += infoSpacing
	bodytxt.Add(ops, subst, body.Dx(), th.Text, "Script")
	bodytxt.Add(ops, bodyst, body.Dx(), th.Text, desc.Script.String())

	ops.Begin()
	for _, l := range bodytxt.Lines {
		l.W.Add(ops)
	}
	op.Position(ops, ops.End(), body.Min.Add(image.Pt(0, scrollFadeDist)))
}

func NewEngraveScreen(ctx *Context, plate Plate) *EngraveScreen {
	var ins []Instruction
	if !ctx.Calibrated {
		ins = append(ins, EngraveFirstSideA...)
	} else {
		ins = append(ins, EngraveSideA...)
	}
	if len(plate.Sides) > 1 {
		ins = append(ins, EngraveSideB...)
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
		// As a special case, the Sh01 image is a placeholder for the plate-specific image.
		if ins.Image == assets.Sh01 {
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
		plan(func(cmd engrave.Command) {
			totalDist += engrave.ManhattanDist(pen, cmd.Coord)
			pen = cmd.Coord
		})
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
			pplan := func(yield func(cmd engrave.Command)) {
				dist := 0
				completed := 0
				pen := image.Point{}
				plan(func(cmd engrave.Command) {
					yield(cmd)
					completed++
					dist += engrave.ManhattanDist(pen, cmd.Coord)
					pen = cmd.Coord
					// Don't spam the progress channel.
					if completed%10 != 0 && dist < totalDist {
						return
					}
					select {
					case <-progress:
					default:
					}
					p := float32(dist) / float32(totalDist)
					progress <- p
					wakeup()
				})
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
			ins := s.instructions[s.step]
			if !s.dryRun.timeout.IsZero() {
				now := ctx.Platform.Now()
				d := s.dryRun.timeout.Sub(now)
				if d <= 0 {
					s.dryRun.timeout = time.Time{}
					s.dryRun.enabled = !s.dryRun.enabled
				}
			}
			e, ok := inp.Next(ctx, Button1, Button2, Button3)
			if !ok {
				break
			}
			switch e.Button {
			case Button1:
				if !inp.Clicked(e.Button) {
					break
				}
				if s.canPrev() {
					s.step--
				} else {
					confirm := &ConfirmWarningScreen{
						Title: "Cancel?",
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
			case Button2:
				if e.Pressed {
					t := ctx.Platform.Now().Add(confirmDelay)
					s.dryRun.timeout = t
					ctx.WakeupAt(t)
				} else {
					s.dryRun.timeout = time.Time{}
				}
			case Button3:
				switch ins.Type {
				case ConnectInstruction:
					if !e.Pressed {
						continue
					}
					confirm := new(ConfirmDelay)
					confirm.Start(ctx, confirmDelay)
					inp.Pressed[e.Button] = false
					for {
						p := confirm.Progress(ctx)
						if p == 1. {
							break
						}
						for {
							e, ok := inp.Next(ctx, Button3)
							if !ok {
								break
							}
							if e.Button == Button3 && !e.Pressed {
								continue outer
							}
						}
						dims := ctx.Platform.DisplaySize()
						s.draw(ctx, ops, th, dims)
						s.drawNav(inp, ops, th, dims, p)
						ctx.Frame()
					}
				case EngraveInstruction:
					continue
				default:
					if !inp.Clicked(e.Button) {
						continue
					}
				}
				if s.moveStep(ctx, ops, th) {
					return true
				}
			}
		}

		dims := ctx.Platform.DisplaySize()
		s.draw(ctx, ops, th, dims)
		s.drawNav(inp, ops, th, dims, 0)

		ctx.Frame()
	}
}

func (s *EngraveScreen) draw(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) {
	op.ColorOp(ops, th.Background)
	layoutTitle(ctx, ops, dims.X, th.Text, fmt.Sprintf("Engrave Plate"))

	r := layout.Rectangle{Max: dims}
	_, subt := r.CutTop(leadingSize)
	subtsz := widget.Label(ops.Begin(), ctx.Styles.body, th.Text, fmt.Sprintf("%.8x", s.plate.MasterFingerprint))
	op.Position(ops, ops.End(), subt.N(subtsz).Sub(image.Pt(0, 4)))

	const margin = 8
	_, content := r.CutTop(leadingSize)
	ins := s.instructions[s.step]
	if ins.Type == EngraveInstruction {
		progress := fmt.Sprintf("%d%%", int(s.engrave.lastProgress*100))
		_, content = subt.CutTop(subtsz.Y)
		middle, _ := content.CutBottom(leadingSize)
		op.Offset(ops, middle.Center(assets.ProgressCircle.Bounds().Size()))
		op.MaskOp(ops, ProgressImage{
			Progress: s.engrave.lastProgress,
			Src:      assets.ProgressCircle,
		})
		op.ColorOp(ops, th.Text)
		sz := widget.Label(ops.Begin(), ctx.Styles.progress, th.Text, progress)
		op.Position(ops, ops.End(), middle.Center(sz))
	}
	content = content.Shrink(0, margin, 0, margin)
	content, lead := content.CutBottom(leadingSize)
	bodysz := widget.LabelW(ops.Begin(), ctx.Styles.lead, content.Dx(), th.Text, ins.resolvedBody)
	if img := ins.Image; img != nil {
		sz := img.Bounds().Size()
		op.Offset(ops, image.Pt((bodysz.X-sz.X)/2, bodysz.Y))
		op.ImageOp(ops, img)
		if sz.X > bodysz.X {
			bodysz.X = sz.X
		}
		bodysz.Y += sz.Y
	}
	op.Position(ops, ops.End(), content.Center(bodysz))
	leadsz := widget.LabelW(ops.Begin(), ctx.Styles.lead, dims.X-2*margin, th.Text, ins.Lead)
	op.Position(ops, ops.End(), lead.Center(leadsz))

	progressw := dims.X * (s.step + 1) / len(s.instructions)
	op.ClipOp(image.Rectangle{Max: image.Pt(progressw, 2)}).Add(ops)
	op.ColorOp(ops, th.Text)

	if s.dryRun.enabled {
		sz := widget.Label(ops.Begin(), ctx.Styles.debug, th.Text, "dry-run")
		op.Position(ops, ops.End(), r.SE(sz).Sub(image.Pt(4, 0)))
	}
}

func (s *EngraveScreen) drawNav(inp *InputTracker, ops op.Ctx, th *Colors, dims image.Point, progress float32) {
	icnBack := assets.IconBack
	if s.canPrev() {
		icnBack = assets.IconLeft
	}
	layoutNavigation(inp, ops, th, dims, []NavButton{{Button: Button1, Style: StyleSecondary, Icon: icnBack}}...)
	ins := s.instructions[s.step]
	switch ins.Type {
	case EngraveInstruction:
	case ConnectInstruction:
		layoutNavigation(inp, ops, th, dims, []NavButton{{Button: Button3, Style: StylePrimary, Icon: assets.IconHammer, Progress: progress}}...)
	default:
		layoutNavigation(inp, ops, th, dims, []NavButton{{
			Button:   Button3,
			Style:    StylePrimary,
			Icon:     assets.IconRight,
			Progress: progress,
		}}...)
	}
}

type Platform interface {
	Events(deadline time.Time) []Event
	Wakeup()
	PlateSizes() []backup.PlateSize
	Engraver() (Engraver, error)
	EngraverParams() engrave.Params
	CameraFrame(size image.Point)
	Now() time.Time
	DisplaySize() image.Point
	// Dirty begins a refresh of the content
	// specified by r.
	Dirty(r image.Rectangle) error
	// NextChunk returns the next chunk of the refresh.
	NextChunk() (draw.RGBA64Image, bool)
	ScanQR(qr *image.Gray) ([][]byte, error)
	Debug() bool
}

type Engraver interface {
	Engrave(sz backup.PlateSize, plan engrave.Plan, quit <-chan struct{}) error
	Close()
}

type FrameEvent struct {
	Error error
	Image image.Image
}

type Event struct {
	typ  int
	data [4]uint32
	refs [2]any
}

const (
	buttonEvent = 1 + iota
	sdcardEvent
	frameEvent
)

type ButtonEvent struct {
	Button  Button
	Pressed bool
	// Rune is only valid if Button is Rune.
	Rune rune
}

type SDCardEvent struct {
	Inserted bool
}

type Button int

const (
	Up Button = iota
	Down
	Left
	Right
	Center
	Button1
	Button2
	Button3
	CCW
	CW
	// Synthetic keys only generated in debug mode.
	Rune // Enter rune.
)

func (b Button) String() string {
	switch b {
	case Up:
		return "up"
	case Down:
		return "down"
	case Left:
		return "left"
	case Right:
		return "right"
	case Center:
		return "center"
	case Button1:
		return "b1"
	case Button2:
		return "b2"
	case Button3:
		return "b3"
	case CCW:
		return "ccw"
	case CW:
		return "cw"
	case Rune:
		return "rune"
	default:
		panic("invalid button")
	}
}

type App struct {
	root op.Ops
	ctx  *Context
	idle struct {
		start  time.Time
		active bool
		state  saver.State
	}
}

func NewApp(pl Platform, version string) (*App, error) {
	ctx := NewContext(pl)
	ctx.Version = version
	a := &App{
		ctx: ctx,
	}
	a.idle.start = pl.Now()
	frameCh := make(chan struct{})
	ctx.Frame = func() {
		frameCh <- struct{}{}
		<-frameCh
	}
	go func() {
		<-frameCh
		mainFlow(ctx, a.root.Context())
	}()
	return a, nil
}

const idleTimeout = 3 * time.Minute

func (a *App) Frame() {
	wakeup := a.ctx.Wakeup
	a.ctx.Reset()
	now := a.ctx.Platform.Now()
	for _, e := range a.ctx.Platform.Events(wakeup) {
		a.idle.start = now
		if se, ok := e.AsSDCard(); ok {
			a.ctx.EmptySDSlot = !se.Inserted
		} else {
			a.ctx.Events(e)
		}
		wakeup = time.Time{}
	}
	a.ctx.WakeupAt(a.idle.start.Add(idleTimeout))
	idle := now.Sub(a.idle.start) >= idleTimeout
	if a.idle.active != idle {
		a.idle.active = idle
		if idle {
			a.idle.state = saver.State{}
		} else {
			// The screen saver has invalidated the cached
			// frame content.
			a.root = op.Ops{}
		}
	}
	if a.idle.active {
		a.idle.state.Draw(a.ctx.Platform)
		return
	}
	dims := a.ctx.Platform.DisplaySize()
	start := time.Now()
	a.root.Reset()
	a.ctx.Frame()
	dirty := a.root.Clip(image.Rectangle{Max: dims})
	layoutTime := time.Now()
	renderTime := time.Now()
	if err := a.ctx.Platform.Dirty(dirty); err != nil {
		panic(err)
	}
	for {
		fb, ok := a.ctx.Platform.NextChunk()
		if !ok {
			break
		}
		a.root.Draw(fb)
	}
	drawTime := time.Now()
	if a.ctx.Platform.Debug() {
		log.Printf("frame: %v layout: %v render: %v draw: %v %v",
			drawTime.Sub(start), layoutTime.Sub(start), renderTime.Sub(layoutTime), drawTime.Sub(renderTime), dirty)
	}
}

func rgb(c uint32) color.NRGBA {
	return argb(0xff000000 | c)
}

func argb(c uint32) color.NRGBA {
	return color.NRGBA{A: uint8(c >> 24), R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
}

func (f FrameEvent) Event() Event {
	e := Event{typ: frameEvent}
	e.refs[0] = f.Error
	e.refs[1] = f.Image
	return e
}

func (b ButtonEvent) Event() Event {
	pressed := uint32(0)
	if b.Pressed {
		pressed = 1
	}
	e := Event{typ: buttonEvent}
	e.data[0] = uint32(b.Button)
	e.data[1] = pressed
	e.data[2] = uint32(b.Rune)
	return e
}

func (s SDCardEvent) Event() Event {
	e := Event{typ: sdcardEvent}
	if s.Inserted {
		e.data[0] = 1
	}
	return e
}

func (e Event) AsFrame() (FrameEvent, bool) {
	if e.typ != frameEvent {
		return FrameEvent{}, false
	}
	f := FrameEvent{}
	if r := e.refs[0]; r != nil {
		f.Error = r.(error)
	}
	if r := e.refs[1]; r != nil {
		f.Image = r.(image.Image)
	}
	return f, true
}

func (e Event) AsButton() (ButtonEvent, bool) {
	if e.typ != buttonEvent {
		return ButtonEvent{}, false
	}
	return ButtonEvent{
		Button:  Button(e.data[0]),
		Pressed: e.data[1] != 0,
		Rune:    rune(e.data[2]),
	}, true
}

func (e Event) AsSDCard() (SDCardEvent, bool) {
	if e.typ != sdcardEvent {
		return SDCardEvent{}, false
	}
	return SDCardEvent{
		Inserted: e.data[0] != 0,
	}, true
}
