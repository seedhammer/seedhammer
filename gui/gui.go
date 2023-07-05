// package gui implements the SeedHammer controller user interface.
package gui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log"
	"math"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"seedhammer.com/address"
	"seedhammer.com/backup"
	"seedhammer.com/bc/ur"
	"seedhammer.com/bc/urtypes"
	"seedhammer.com/bip32"
	"seedhammer.com/bip39"
	"seedhammer.com/camera"
	"seedhammer.com/font/constant"
	"seedhammer.com/gui/assets"
	"seedhammer.com/gui/layout"
	"seedhammer.com/gui/op"
	"seedhammer.com/gui/saver"
	"seedhammer.com/gui/text"
	"seedhammer.com/gui/widget"
	"seedhammer.com/input"
	"seedhammer.com/mjolnir"
	"seedhammer.com/nonstandard"
	"seedhammer.com/seedqr"
	"seedhammer.com/zbar"
)

const nbuttons = 8

type Context struct {
	Buttons      [nbuttons]bool
	Repeats      [nbuttons]time.Time
	Platform     Platform
	Styles       Styles
	Version      string
	Calibrated   bool
	NoSDCard     bool
	RotateCamera bool

	Wakeup chan struct{}
	events []Event
}

type Event struct {
	input.Event
	Click bool
}

func NewContext(pl Platform) *Context {
	c := &Context{
		Platform: pl,
		Wakeup:   make(chan struct{}, 1),
		Styles:   NewStyles(),
	}
	// Wake up initially.
	c.Wakeup <- struct{}{}
	return c
}

func (c *Context) WakeupAfter(d time.Duration) {
	go func() {
		time.Sleep(d)
		select {
		case c.Wakeup <- struct{}{}:
		default:
		}
	}()
}

func WakeupChan[T any](ctx *Context, in <-chan T) <-chan T {
	if in == nil {
		return in
	}
	out := make(chan T, cap(in))
	go func() {
		defer close(out)
		for v := range in {
		delivery:
			for {
				select {
				case out <- v:
					break delivery
				case ctx.Wakeup <- struct{}{}:
				}
			}
			select {
			case ctx.Wakeup <- struct{}{}:
			default:
			}
		}
	}()
	return out
}

const repeatStartDelay = 400 * time.Millisecond
const repeatDelay = 100 * time.Millisecond

func isRepeatButton(b input.Button) bool {
	switch b {
	case input.Up, input.Down, input.Right, input.Left:
		return true
	}
	return false
}

func (c *Context) Repeat() {
	now := c.Platform.Now()
	for btn, pressed := range c.Buttons {
		b := input.Button(btn)
		if !pressed || !isRepeatButton(b) {
			continue
		}
		if now.Before(c.Repeats[btn]) {
			continue
		}
		c.events = append(c.events, Event{Event: input.Event{Button: b, Pressed: true}})
		c.Repeats[b] = c.Platform.Now().Add(repeatDelay)
		c.WakeupAfter(repeatDelay)
	}
}

func (c *Context) Events(evts ...input.Event) {
	for _, e := range evts {
		e2 := Event{Event: e}
		if int(e.Button) < len(c.Buttons) {
			e2.Click = !e.Pressed && c.Buttons[e.Button]
			c.Buttons[e.Button] = e.Pressed
			if e.Pressed && isRepeatButton(e.Button) {
				c.Repeats[e.Button] = c.Platform.Now().Add(repeatStartDelay)
				c.WakeupAfter(repeatStartDelay)
			}
		}
		c.events = append(c.events, e2)
	}
}

func (c *Context) Next() (Event, bool) {
	if len(c.events) == 0 {
		return Event{}, false
	}
	evt := c.events[0]
	c.events = c.events[1:]
	return evt, true
}

const longestWord = "TOMORROW"

type program int

const (
	backupWallet program = iota
)

type AddressesScreen struct {
	addresses [2][]string
	page      int
	scroll    int
}

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
			Dot:  fixed.P(line.Dot.X, doty),
			Txt:  line.Text,
		}).Add(ops.Begin())
		r.Lines = append(r.Lines, linePos{ops.End(), doty})
	}
	r.Y += lines[len(lines)-1].Dot.Y
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

func shortenAddress(n int, addr string) string {
	if len(addr) <= n {
		return addr
	}
	return addr[:n/2] + "......" + addr[len(addr)-n/2:]
}

func (s *AddressesScreen) Layout(ctx *Context, ops op.Ctx, dims image.Point) bool {
	const linesPerPage = 8
	const linesPerScroll = linesPerPage - 3

	const maxPage = len(s.addresses)
	for {
		e, ok := ctx.Next()
		if !ok {
			break
		}
		switch e.Button {
		case input.Button1:
			if e.Click {
				return true
			}
		case input.Left:
			if e.Pressed {
				s.page = (s.page - 1 + maxPage) % maxPage
				s.scroll = 0
			}
		case input.Right:
			if e.Pressed {
				s.page = (s.page + 1) % maxPage
				s.scroll = 0
			}
		case input.Up:
			if e.Pressed {
				s.scroll -= linesPerScroll
			}
		case input.Down:
			if e.Pressed {
				s.scroll += linesPerScroll
			}
		}
	}

	th := &descriptorTheme
	op.ColorOp(ops, th.Background)

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
	clipScroll(ops, ops.End(), image.Rectangle(body))

	layoutNavigation(ctx, ops, th, dims,
		NavButton{Button: input.Button1, Style: StyleSecondary, Icon: assets.IconBack},
	)
	return false
}

type DescriptorScreen struct {
	Descriptor urtypes.OutputDescriptor
	mnemonic   bip39.Mnemonic

	addresses *AddressesScreen
	seed      *SeedScreen
	warning   *ErrorScreen
	engrave   *EngraveScreen
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

const infoSpacing = 8

func (s *DescriptorScreen) inputSeed(ctx *Context) {
	s.seed = NewEmptySeedScreen(ctx, "Input Share")
}

func (s *DescriptorScreen) Layout(ctx *Context, ops op.Ctx, dims image.Point) bool {
	th := &descriptorTheme
	for {
		switch {
		case s.addresses != nil:
			done := s.addresses.Layout(ctx, ops.Begin(), dims)
			dialog := ops.End()
			if !done {
				dialog.Add(ops)
				return false
			}
			s.addresses = nil
			continue
		case s.seed != nil:
			m, done := s.seed.Layout(ctx, ops.Begin(), th, dims)
			dialog := ops.End()
			if !done {
				dialog.Add(ops)
				return false
			}
			s.seed = nil
			if m == nil {
				break
			}
			s.mnemonic = m
			eng, err := NewEngraveScreen(ctx, s.Descriptor, s.mnemonic)
			if err != nil {
				s.warning = NewErrorScreen(err)
				continue
			}
			s.engrave = eng
			continue
		case s.engrave != nil:
			res := s.engrave.Layout(ctx, ops.Begin(), dims)
			dialog := ops.End()
			switch res {
			case ResultNone:
				dialog.Add(ops)
				return false
			case ResultCancelled:
				s.seed = NewSeedScreen(ctx, s.mnemonic)
			case ResultComplete:
				s.inputSeed(ctx)
			}
			s.engrave = nil
			continue
		case s.warning != nil:
			dismissed := s.warning.Layout(ctx, ops.Begin(), th, dims)
			warning := ops.End()
			if dismissed {
				s.warning = nil
				continue
			}
			defer warning.Add(ops)
		}
		e, ok := ctx.Next()
		if !ok {
			break
		}
		switch e.Button {
		case input.Button1:
			if e.Click {
				return true
			}
		case input.Button2:
			if !e.Click {
				break
			}
			s.addresses = NewAddressesScreen(s.Descriptor)
		case input.Button3:
			if !e.Click {
				break
			}
			if err := validateDescriptor(s.Descriptor); err != nil {
				s.warning = NewErrorScreen(err)
				continue
			}
			s.inputSeed(ctx)
		}
	}

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

	if s.warning == nil {
		layoutNavigation(ctx, ops, th, dims,
			NavButton{Button: input.Button1, Style: StyleSecondary, Icon: assets.IconBack},
			NavButton{Button: input.Button2, Style: StyleSecondary, Icon: assets.IconInfo},
			NavButton{Button: input.Button3, Style: StylePrimary, Icon: assets.IconCheckmark},
		)
	}
	return false
}

func derivationPath(path urtypes.Path) string {
	var b strings.Builder
	b.WriteString("m")
	for _, p := range path {
		b.WriteString("/")
		if p >= hdkeychain.HardenedKeyStart {
			fmt.Fprintf(&b, "%d'", p-hdkeychain.HardenedKeyStart)
		} else {
			fmt.Fprintf(&b, "%d", p)
		}
	}
	return b.String()
}

type ScanScreen struct {
	Title     string
	Lead      string
	decoder   ur.Decoder
	nsdecoder nonstandard.Decoder
	feed      *image.Gray
	camera    struct {
		out  chan<- camera.Frame
		in   <-chan camera.Frame
		quit chan struct{}
		err  error
	}
}

func (s *ScanScreen) close() {
	if s.camera.quit != nil {
		s.camera.quit <- struct{}{}
		<-s.camera.quit
	}
}

func (s *ScanScreen) Layout(ctx *Context, ops op.Ctx, dims image.Point) (any, Result) {
	const cameraFrameScale = 3
	if s.camera.quit == nil && s.camera.err == nil {
		frames := make(chan camera.Frame, 1)
		out := make(chan camera.Frame)
		quit := make(chan struct{})
		go func() {
			defer close(quit)
			defer close(frames)
			closer, err := ctx.Platform.Camera(dims.Mul(cameraFrameScale), frames, out)
			if err != nil {
				log.Println(err)
				select {
				case frames <- camera.Frame{Err: err}:
					<-quit
				case <-quit:
				}
				return
			}
			defer closer()
			<-quit
		}()
		s.camera.quit = quit
		s.camera.in = WakeupChan(ctx, frames)
		s.camera.out = out
	}
	for {
		e, ok := ctx.Next()
		if !ok {
			break
		}
		if !e.Click {
			continue
		}
		switch e.Button {
		case input.Button1:
			s.close()
			return nil, ResultCancelled
		case input.Button2:
			ctx.RotateCamera = !ctx.RotateCamera
		}
	}

	if s.feed == nil || dims != s.feed.Bounds().Size() {
		s.feed = image.NewGray(image.Rectangle{Max: dims})
	}
	select {
	case frame := <-s.camera.in:
		if frame.Err != nil {
			s.camera.quit <- struct{}{}
			<-s.camera.quit
			s.camera.err = frame.Err
			s.camera.quit = nil
			s.camera.in = nil
			s.camera.out = nil
			break
		}
		ycbcr := frame.Image.(*image.YCbCr)
		gray := &image.Gray{Pix: ycbcr.Y, Stride: ycbcr.YStride, Rect: ycbcr.Bounds()}

		scaleRot(s.feed, gray, ctx.RotateCamera)
		// Re-create image (but not backing store) to ensure redraw.
		copy := *s.feed
		s.feed = &copy
		results, err := zbar.Scan(gray)
		s.camera.out <- frame
		if err != nil {
			break
		}
		for _, res := range results {
			v, res := s.parseQR(res)
			if res != ResultNone {
				s.close()
				return v, res
			}
		}
	default:
	}
	th := &cameraTheme
	r := layout.Rectangle{Max: dims}

	op.ImageOp(ops, s.feed)

	op.ImageOp(ops.Begin(), assets.CameraCorners)
	op.Position(ops, ops.End(), r.Center(assets.CameraCorners.Bounds().Size()))

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
	if err := s.camera.err; err != nil {
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
	progress := int(100 * s.decoder.Progress())
	if progress == 0 {
		progress = int(100 * s.nsdecoder.Progress())
	}
	if progress > 0 {
		sz = widget.LabelW(ops.Begin(), ctx.Styles.lead, width, th.Text, fmt.Sprintf("%d%%", progress))
		_, percent := top.CutBottom(sz.Y)
		pos := percent.Center(sz)
		background(ops, ops.End(), image.Rectangle{Min: pos, Max: pos.Add(sz)}, pos)
	}

	nav := func(btn input.Button, icn image.RGBA64Image) {
		nav := layoutNavigation(ctx, ops.Begin(), th, dims,
			NavButton{Button: btn, Style: StyleSecondary, Icon: icn},
		)
		nav = image.Rectangle(layout.Rectangle(nav).Shrink(underlay.Padding()).Shrink(-2, -4, -2, -2))
		background(ops, ops.End(), nav, image.Point{})
	}
	nav(input.Button1, assets.IconBack)
	nav(input.Button2, assets.IconFlip)
	return nil, ResultNone
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

func (s *ScanScreen) parseNonStandard(qr []byte) (any, Result) {
	if err := s.nsdecoder.Add(string(qr)); err != nil {
		s.nsdecoder = nonstandard.Decoder{}
		return qr, ResultComplete
	}
	enc := s.nsdecoder.Result()
	if enc == nil {
		return nil, ResultNone
	}
	return enc, ResultComplete
}

func (s *ScanScreen) parseQR(qr []byte) (any, Result) {
	uqr := strings.ToUpper(string(qr))
	if !strings.HasPrefix(uqr, "UR:") {
		s.decoder = ur.Decoder{}
		return s.parseNonStandard(qr)
	}
	s.nsdecoder = nonstandard.Decoder{}
	if err := s.decoder.Add(uqr); err != nil {
		// Incompatible fragment. Reset decoder and try again.
		s.decoder = ur.Decoder{}
		s.decoder.Add(uqr)
	}
	typ, enc, err := s.decoder.Result()
	if err != nil {
		s.decoder = ur.Decoder{}
		return nil, ResultNone
	}
	if enc == nil {
		return nil, ResultNone
	}
	s.decoder = ur.Decoder{}
	v, err := urtypes.Parse(typ, enc)
	if err != nil {
		return nil, ResultComplete
	}
	return v, ResultComplete
}

type ErrorScreen struct {
	Title string
	Body  string
}

func (s *ErrorScreen) Layout(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) bool {
	for {
		e, ok := ctx.Next()
		if !ok {
			break
		}
		switch e.Button {
		case input.Button3:
			if e.Click {
				return true
			}
		}
	}
	r := layout.Rectangle{Max: dims}
	sz := layoutWarning(ctx, ops.Begin(), th, dims.X, s.Title, s.Body)
	op.Position(ops, ops.End(), r.Center(sz))
	layoutNavigation(ctx, ops, th, dims, NavButton{Button: input.Button3, Style: StylePrimary, Icon: assets.IconCheckmark})
	return false
}

type ConfirmWarningScreen struct {
	Title string
	Body  string
	Icon  image.RGBA64Image

	confirm ConfirmDelay
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
	if !c.Running() {
		return 0.
	}
	now := ctx.Platform.Now()
	d := c.timeout.Sub(now)
	if d <= 0 {
		return 1.
	}
	ctx.WakeupAfter(0)
	return 1. - float32(d.Seconds()/confirmDelay.Seconds())
}

func (c *ConfirmDelay) Running() bool {
	return !c.timeout.IsZero()
}

const confirmDelay = 1 * time.Second

func (s *ConfirmWarningScreen) Layout(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) ConfirmResult {
	var progress float32
	for {
		progress = s.confirm.Progress(ctx)
		if progress == 1 {
			return ConfirmYes
		}
		e, ok := ctx.Next()
		if !ok {
			break
		}
		switch e.Button {
		case input.Button1:
			if e.Click {
				return ConfirmNo
			}
		case input.Button3:
			if e.Pressed {
				ctx.Buttons[input.Button3] = false
				s.confirm.Start(ctx, confirmDelay)
			} else {
				s.confirm = ConfirmDelay{}
			}
		}
	}
	r := layout.Rectangle{Max: dims}
	sz := layoutWarning(ctx, ops.Begin(), th, dims.X, s.Title, s.Body)
	op.Position(ops, ops.End(), r.Center(sz))
	icn := s.Icon
	if s.confirm.Running() {
		icn = ProgressImage{
			Progress: progress,
			Src:      assets.IconProgress,
		}
	}
	layoutNavigation(ctx, ops, th, dims,
		NavButton{Button: input.Button1, Style: StyleSecondary, Icon: assets.IconBack},
		NavButton{Button: input.Button3, Style: StylePrimary, Icon: icn},
	)
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

type EngraveScreen struct {
	Key          urtypes.KeyDescriptor
	instructions []Instruction
	plate        backup.Plate

	cancel *ConfirmWarningScreen
	step   int
	dryRun struct {
		timeout time.Time
		enabled bool
	}
	engrave engraveState
	confirm ConfirmDelay
}

var errKeyNotInDescriptor = errors.New("share not part of descriptor")

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
	case errors.Is(err, errKeyNotInDescriptor):
		return &ErrorScreen{
			Title: "Unknown Share",
			Body:  "The share is not part of the wallet or is passphrase protected.",
		}
	default:
		return &ErrorScreen{
			Title: "Error",
			Body:  err.Error(),
		}
	}

}

func validateDescriptor(desc urtypes.OutputDescriptor) error {
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
	m := make(bip39.Mnemonic, 24)
	m = m.FixChecksum()
	if _, err := engravePlate(desc, 0, m); err != nil {
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

func engravePlate(desc urtypes.OutputDescriptor, keyIdx int, m bip39.Mnemonic) (backup.Plate, error) {
	plateDesc := backup.PlateDesc{
		Descriptor: desc,
		Mnemonic:   m,
		KeyIdx:     keyIdx,
		Font:       &constant.Font,
	}
	return backup.Engrave(mjolnir.Millimeter, mjolnir.StrokeWidth, plateDesc)
}

func NewEngraveScreen(ctx *Context, desc urtypes.OutputDescriptor, m bip39.Mnemonic) (*EngraveScreen, error) {
	keyIdx, ok := descriptorKeyIdx(desc, m, "")
	if !ok {
		return nil, errKeyNotInDescriptor
	}
	plate, err := engravePlate(desc, keyIdx, m)
	if err != nil {
		return nil, err
	}
	s := &EngraveScreen{
		Key:   desc.Keys[keyIdx],
		plate: plate,
	}
	if !ctx.Calibrated {
		s.instructions = append(s.instructions, EngraveFirstSideA...)
	} else {
		s.instructions = append(s.instructions, EngraveSideA...)
	}
	if len(plate.Sides) > 1 {
		s.instructions = append(s.instructions, EngraveSideB...)
	}
	s.instructions = append(s.instructions, EngraveSuccess...)
	args := struct {
		Name  string
		Idx   int
		Total int
	}{
		Name:  plateName(s.plate.Size),
		Total: len(desc.Keys),
		Idx:   keyIdx + 1,
	}
	for i, ins := range s.instructions {
		tmpl := template.Must(template.New("instruction").Parse(ins.Body))
		buf := new(bytes.Buffer)
		tmpl.Execute(buf, args)
		s.instructions[i].resolvedBody = buf.String()
		// As a special case, the SH01 image is a placeholder for the plate-specific image.
		if ins.Image == assets.SH01 {
			s.instructions[i].Image = plateImage(s.plate.Size)
		}
	}
	return s, nil
}

type engraveState struct {
	dev          io.ReadWriteCloser
	cancel       chan struct{}
	progress     <-chan float32
	errs         <-chan error
	lastProgress float32
	warning      *ErrorScreen
	fatal        bool
}

func (s *EngraveScreen) close() {
	e := s.engrave
	s.engrave = engraveState{}
	go func() {
		if e.cancel != nil {
			close(e.cancel)
		}
		// Wait a bit for cancellation.
		if e.errs != nil {
			select {
			case <-e.errs:
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

func (s *EngraveScreen) moveStep(ctx *Context) bool {
	ins := s.instructions[s.step]
	if ins.Type == ConnectInstruction {
		if s.engrave.dev != nil {
			return false
		}
		s.engrave = engraveState{}
		dev, err := ctx.Platform.Engraver()
		if err != nil {
			log.Printf("gui: failed to connect to engraver: %v", err)
			s.engrave.warning = &ErrorScreen{
				Title: "Connection Error",
				Body:  "Failed to establish a connection to the engraver.",
			}
			return false
		}
		s.engrave.dev = dev
	}
	s.step++
	if s.step == len(s.instructions) {
		s.close()
		return true
	}
	ins = s.instructions[s.step]
	if ins.Type == EngraveInstruction {
		prog := &mjolnir.Program{
			DryRun: s.dryRun.enabled,
		}
		s.plate.Sides[ins.Side].Engrave(prog)
		prog.Prepare()
		cancel := make(chan struct{})
		errs := make(chan error, 1)
		progress := make(chan float32, 1)
		s.engrave.cancel = cancel
		s.engrave.errs = WakeupChan(ctx, errs)
		s.engrave.progress = WakeupChan(ctx, progress)
		dev := s.engrave.dev
		go func() {
			defer close(errs)
			defer close(progress)
			defer dev.Close()
			err := mjolnir.Engrave(dev, prog, progress, cancel)
			errs <- err
		}()
		go s.plate.Sides[ins.Side].Engrave(prog)
	}
	return false
}

type Result int

const (
	ResultNone Result = iota
	ResultCancelled
	ResultComplete
)

func (s *EngraveScreen) Layout(ctx *Context, ops op.Ctx, dims image.Point) Result {
loop:
	for {
		select {
		case p := <-s.engrave.progress:
			s.engrave.lastProgress = p
		case err := <-s.engrave.errs:
			// Clear out progress channel.
			for range s.engrave.progress {
			}
			s.engrave = engraveState{}
			if err != nil {
				log.Printf("gui: connection lost to engraver: %v", err)
				s.engrave = engraveState{
					warning: &ErrorScreen{
						Title: "Connection Error",
						Body:  "Connection to the engraver failed.",
					},
					fatal: true,
				}
				break
			}
			ctx.Calibrated = true
			s.step++
			if s.step == len(s.instructions) {
				return ResultComplete
			}
		default:
			break loop
		}
	}

	var progress float32
	th := &engraveTheme
	var ins Instruction
	canPrev := false
	for {
		ins = s.instructions[s.step]
		canPrev = s.step > 0 && s.instructions[s.step-1].Type == PrepareInstruction
		progress = s.confirm.Progress(ctx)
		if progress == 1. {
			s.moveStep(ctx)
			s.confirm = ConfirmDelay{}
			continue
		}
		if !s.dryRun.timeout.IsZero() {
			now := ctx.Platform.Now()
			d := s.dryRun.timeout.Sub(now)
			if d <= 0 {
				ctx.Buttons[input.Button2] = false
				s.dryRun.timeout = time.Time{}
				s.dryRun.enabled = !s.dryRun.enabled
			}
		}
		switch {
		case s.cancel != nil:
			result := s.cancel.Layout(ctx, ops.Begin(), th, dims)
			dialog := ops.End()
			switch result {
			case ConfirmYes:
				s.close()
				return ResultCancelled
			case ConfirmNo:
				s.cancel = nil
				continue
			}
			defer dialog.Add(ops)
		case s.engrave.warning != nil:
			dismissed := s.engrave.warning.Layout(ctx, ops.Begin(), th, dims)
			dialog := ops.End()
			if dismissed {
				s.engrave.warning = nil
				if s.engrave.fatal {
					s.close()
					return ResultCancelled
				}
				continue
			}
			defer dialog.Add(ops)
		}
		e, ok := ctx.Next()
		if !ok {
			break
		}
		switch e.Button {
		case input.Button1:
			if !e.Click {
				break
			}
			if canPrev {
				s.step--
			} else {
				s.cancel = &ConfirmWarningScreen{
					Title: "Cancel?",
					Body:  "This will cancel the engraving process\n\nHold button to confirm.",
					Icon:  assets.IconDiscard,
				}
			}
		case input.Button2:
			if e.Pressed {
				s.dryRun.timeout = ctx.Platform.Now().Add(confirmDelay)
				ctx.WakeupAfter(confirmDelay)
			} else {
				s.dryRun.timeout = time.Time{}
			}
		case input.Button3:
			if ins.Type == ConnectInstruction {
				if e.Pressed {
					ctx.Buttons[input.Button3] = false
					s.confirm.Start(ctx, confirmDelay)
				} else {
					s.confirm = ConfirmDelay{}
				}
				break
			} else if !e.Click || ins.Type == EngraveInstruction {
				break
			}
			if s.moveStep(ctx) {
				return ResultComplete
			}
		}
	}

	op.ColorOp(ops, th.Background)
	layoutTitle(ctx, ops, dims.X, th.Text, fmt.Sprintf("Engrave Plate"))

	r := layout.Rectangle{Max: dims}
	_, subt := r.CutTop(leadingSize)
	subtsz := widget.Label(ops.Begin(), ctx.Styles.body, th.Text, fmt.Sprintf("%.8x", s.Key.MasterFingerprint))
	op.Position(ops, ops.End(), subt.N(subtsz).Sub(image.Pt(0, 4)))

	const margin = 8
	_, content := r.CutTop(leadingSize)
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

	if s.cancel == nil && s.engrave.warning == nil {
		icnBack := assets.IconBack
		if canPrev {
			icnBack = assets.IconLeft
		}
		layoutNavigation(ctx, ops, th, dims, NavButton{Button: input.Button1, Style: StyleSecondary, Icon: icnBack})
		switch ins.Type {
		case EngraveInstruction:
		case ConnectInstruction:
			icn := assets.IconHammer
			if s.confirm.Running() {
				icn = ProgressImage{
					Progress: progress,
					Src:      assets.IconProgress,
				}
			}
			layoutNavigation(ctx, ops, th, dims, NavButton{Button: input.Button3, Style: StylePrimary, Icon: icn})
		default:
			layoutNavigation(ctx, ops, th, dims, NavButton{Button: input.Button3, Style: StylePrimary, Icon: assets.IconRight})
		}
	}
	return ResultNone
}

func plateImage(p backup.PlateSize) image.RGBA64Image {
	switch p {
	case backup.SmallPlate:
		return assets.SH01
	case backup.SquarePlate:
		return assets.SH02
	case backup.LargePlate:
		return assets.SH03
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
			Body:  "Place 2 x {{.Name}}\non top of each other.",
			Image: assets.SH01,
			Lead:  "seedhammer.com/tip#4",
		},
		{
			Body: "Tighten the nuts firmly.",
			Lead: "seedhammer.com/tip#4",
		},
		{
			Body: "Loosen the hammerhead finger screw. Adjust needle distance to 2 mm above the plate.",
			Lead: "seedhammer.com/tip#5",
		},
		{
			Body: "The needle should barely be able to move freely over the nuts.",
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
			Body:  "Place 2 x {{.Name}}\non top of each other.",
			Image: assets.SH01,
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

func NewEmptySeedScreen(ctx *Context, title string) *SeedScreen {
	s := &SeedScreen{}
	s.method = &ChoiceScreen{
		Title:   title,
		Lead:    "Choose input method",
		Choices: []string{"KEYBOARD", "CAMERA"},
	}
	return s
}

func NewSeedScreen(ctx *Context, m bip39.Mnemonic) *SeedScreen {
	return &SeedScreen{
		Mnemonic: m,
	}
}

type SeedScreen struct {
	Mnemonic bip39.Mnemonic
	selected int
	scroll   int
	method   *ChoiceScreen
	seedlen  *ChoiceScreen
	input    *WordKeyboardScreen
	scanner  *ScanScreen
	cancel   *ConfirmWarningScreen
	warning  *ErrorScreen
}

func (s *SeedScreen) empty() bool {
	for _, w := range s.Mnemonic {
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

func (s *SeedScreen) Layout(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) (bip39.Mnemonic, bool) {
	var complete bool
	for {
		complete = len(s.Mnemonic) > 0
		for _, w := range s.Mnemonic {
			if w == -1 {
				complete = false
				break
			}
		}
		if s.warning != nil {
			dismiss := s.warning.Layout(ctx, ops.Begin(), th, dims)
			warning := ops.End()
			if dismiss {
				s.warning = nil
				continue
			}
			defer warning.Add(ops)
		}
		switch {
		case s.scanner != nil:
			res, status := s.scanner.Layout(ctx, ops.Begin(), dims)
			dialog := ops.End()
			switch status {
			case ResultNone:
				dialog.Add(ops)
				return nil, false
			}
			s.scanner = nil
			switch status {
			case ResultCancelled:
				continue
			}
			if b, ok := res.([]byte); ok {
				if sqr, ok := seedqr.Parse(b); ok {
					res = sqr
				} else if sqr, err := bip39.ParseMnemonic(strings.ToLower(string(b))); err == nil {
					res = sqr
				} else if nonstandard.ElectrumSeed(string(b)) {
					s.warning = &ErrorScreen{
						Title: "Invalid Seed",
						Body:  "Electrum seeds are not supported.",
					}
					continue
				}
			}
			seed, ok := res.(bip39.Mnemonic)
			if !ok {
				s.warning = &ErrorScreen{
					Title: "Invalid Seed",
					Body:  "The scanned data does not represent a seed.",
				}
				continue
			}
			s.method = nil
			s.Mnemonic = seed
			continue
		case s.seedlen != nil:
			choice, done := s.seedlen.Layout(ctx, ops.Begin(), th, dims, true)
			dialog := ops.End()
			if !done {
				dialog.Add(ops)
				return nil, false
			}
			s.seedlen = nil
			if choice == -1 {
				continue
			}
			s.method = nil
			nwords := []int{12, 24}[choice]
			s.Mnemonic = emptyMnemonic(nwords)
			s.input = &WordKeyboardScreen{
				Mnemonic: s.Mnemonic,
			}
			continue
		case s.method != nil:
			choice, done := s.method.Layout(ctx, ops.Begin(), th, dims, s.warning == nil)
			dialog := ops.End()
			if !done {
				dialog.Add(ops)
				return nil, false
			}
			switch choice {
			case -1:
				return nil, true
			case 0:
				s.seedlen = &ChoiceScreen{
					Title:   "Input Seed",
					Lead:    "Choose number of words",
					Choices: []string{"12 WORDS", "24 WORDS"},
				}
			case 1:
				s.scanner = &ScanScreen{
					Title: "Scan",
					Lead:  "SeedQR or Mnemonic",
				}
			}
			continue
		case s.input != nil:
			done := s.input.Layout(ctx, ops.Begin(), th, dims)
			dialog := ops.End()
			if !done {
				dialog.Add(ops)
				return nil, false
			}
			s.input = nil
			if s.empty() {
				return nil, true
			}
			continue
		case s.cancel != nil:
			result := s.cancel.Layout(ctx, ops.Begin(), th, dims)
			warning := ops.End()
			switch result {
			case ConfirmYes:
				return nil, true
			case ConfirmNo:
				s.cancel = nil
				continue
			}
			defer warning.Add(ops)
		}
		e, ok := ctx.Next()
		if !ok {
			break
		}
		switch e.Button {
		case input.Button1:
			if !e.Click {
				break
			}
			if s.empty() {
				return nil, true
			}
			s.cancel = &ConfirmWarningScreen{
				Title: "Discard Seed?",
				Body:  "Going back will discard the seed.\n\nHold button to confirm.",
				Icon:  assets.IconDiscard,
			}
		case input.Button2, input.Center:
			if !e.Click {
				break
			}
			s.input = &WordKeyboardScreen{
				Mnemonic: s.Mnemonic,
				selected: s.selected,
			}
			continue
		case input.Button3:
			if !e.Click || !complete {
				break
			}
			if !s.Mnemonic.Valid() {
				s.warning = &ErrorScreen{
					Title: "Invalid Seed",
				}
				var words []string
				for _, w := range s.Mnemonic {
					words = append(words, bip39.LabelFor(w))
				}
				if nonstandard.ElectrumSeed(strings.Join(words, " ")) {
					s.warning.Body = "Electrum seeds are not supported."
				} else {
					s.warning.Body = "The seed phrase is invalid.\nCheck the words and try again."
				}
				break
			}
			return s.Mnemonic, true
		case input.Down:
			if e.Pressed && s.selected < len(s.Mnemonic)-1 {
				s.selected++
			}
		case input.Up:
			if e.Pressed && s.selected > 0 {
				s.selected--
			}
		}
	}

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
	maxScroll := len(s.Mnemonic) - linesPerPage
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	off := content.Min.Add(image.Pt(0, -scroll*lineHeight))
	{
		ops := ops.Begin()
		for i, w := range s.Mnemonic {
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
	clipScroll(ops, ops.End(), image.Rectangle(list))

	if s.cancel == nil && s.warning == nil {
		layoutNavigation(ctx, ops, th, dims,
			NavButton{Button: input.Button1, Style: StyleSecondary, Icon: assets.IconBack},
			NavButton{Button: input.Button2, Style: StyleSecondary, Icon: assets.IconEdit},
		)
		if complete {
			layoutNavigation(ctx, ops, th, dims, NavButton{Button: input.Button3, Style: StylePrimary, Icon: assets.IconCheckmark})
		}
	}
	return nil, false
}

const scrollFadeDist = 16

func clipScroll(ops op.Ctx, w op.CallOp, r image.Rectangle) {
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

func layoutWarning(ctx *Context, ops op.Ctx, th *Colors, width int, title, txt string) image.Point {
	op.ColorOp(ops, color.NRGBA{A: theme.overlayMask})

	const margin = 4
	wbbg := assets.WarningBoxBg
	wbout := assets.WarningBoxBorder
	ptop, pend, _, pstart := wbbg.Padding()
	width -= 2*(assets.NavBtnPrimary.Bounds().Dx()+margin) + pstart + pend
	titlesz := widget.LabelW(ops.Begin(), ctx.Styles.warning, width, th.Text, strings.ToUpper(title)+"\n")
	titlew := ops.End()
	bodysz := widget.LabelW(ops.Begin(), ctx.Styles.body, width, th.Text, txt)
	body := ops.End()
	maxw := bodysz.X
	if titlesz.X > maxw {
		maxw = titlesz.X
	}
	r := image.Rectangle{Max: image.Pt(maxw, titlesz.Y+bodysz.Y)}
	box := wbbg.For(r)
	op.MaskOp(ops.Begin(), wbbg.For(r))
	op.ColorOp(ops, th.Background)
	op.MaskOp(ops, wbout.For(r))
	op.ColorOp(ops, th.Text)
	off := image.Pt(pstart, ptop)
	op.Position(ops, ops.End(), off)
	op.Position(ops, titlew, off.Add(image.Pt((maxw-titlesz.X)/2, 0)))
	op.Position(ops, body, off.Add(image.Pt(0, titlesz.Y)))
	return box.Bounds().Size()
}

type WordKeyboardScreen struct {
	Mnemonic bip39.Mnemonic
	selected int
	kbd      *Keyboard
}

func (s *WordKeyboardScreen) Layout(ctx *Context, ops op.Ctx, th *Colors, dims image.Point) bool {
	if s.kbd == nil {
		s.kbd = NewKeyboard(ctx)
	}
	for {
		e, ok := ctx.Next()
		if !ok {
			break
		}
		switch e.Button {
		case input.Button1:
			if e.Click {
				return true
			}
		case input.Button2:
			if !e.Click {
				break
			}
			w, complete := s.kbd.Complete()
			if !complete {
				break
			}
			s.kbd.Clear()
			s.Mnemonic[s.selected] = w
			for {
				s.selected++
				if s.selected == len(s.Mnemonic) {
					return true
				}
				if s.Mnemonic[s.selected] == -1 {
					break
				}
			}
		default:
			s.kbd.Event(e)
		}
	}
	completedWord, complete := s.kbd.Complete()
	op.ColorOp(ops, th.Background)
	layoutTitle(ctx, ops, dims.X, th.Text, "Input Words")

	screen := layout.Rectangle{Max: dims}
	_, content := screen.CutTop(leadingSize)
	content, _ = content.CutBottom(8)

	kbdsz := s.kbd.Layout(ctx, ops.Begin(), th)
	op.Position(ops, ops.End(), content.S(kbdsz))

	layoutWord := func(ops op.Ctx, n int, word string) image.Point {
		style := ctx.Styles.word
		txt := fmt.Sprintf("%2d: %s", n, word)
		return widget.Label(ops, style, th.Background, txt)
	}

	longest := layoutWord(op.Ctx{}, 24, longestWord)
	hint := s.kbd.Word
	if complete {
		hint = strings.ToUpper(bip39.LabelFor(completedWord))
	}
	layoutWord(ops.Begin(), s.selected+1, hint)
	word := ops.End()
	r := image.Rectangle{Max: longest}
	r.Min.Y -= 3
	op.MaskOp(ops.Begin(), assets.ButtonFocused.For(r))
	op.ColorOp(ops, th.Text)
	word.Add(ops)
	top, _ := content.CutBottom(kbdsz.Y)
	op.Position(ops, ops.End(), top.Center(longest))

	layoutNavigation(ctx, ops, th, dims,
		NavButton{Button: input.Button1, Style: StyleSecondary, Icon: assets.IconBack},
	)
	if complete {
		layoutNavigation(ctx, ops, th, dims, NavButton{Button: input.Button2, Style: StylePrimary, Icon: assets.IconCheckmark})
	}
	return false
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
}

func NewKeyboard(ctx *Context) *Keyboard {
	k := new(Keyboard)
	_, k.widest = ctx.Styles.keyboard.Layout(math.MaxInt, "W")
	bssz := assets.KeyBackspace.Bounds().Size()
	k.backspace = image.Pt(bssz.X, k.widest.Y)
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
	for ; int(w) < len(bip39.Wordlist); w++ {
		bip39w := bip39.Wordlist[w]
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

func (k *Keyboard) Event(e Event) {
	if !e.Pressed {
		return
	}
	switch e.Button {
	case input.Left:
		next := k.col
		row := kbdKeys[k.row]
		n := len(row)
		for {
			next = (next - 1 + n) % n
			if !k.Valid(kbdKeys[k.row][next]) {
				continue
			}
			k.col = next
			k.adjust(true)
			break
		}
	case input.Right:
		next := k.col
		row := kbdKeys[k.row]
		n := len(row)
		for {
			next = (next + 1) % n
			if !k.Valid(kbdKeys[k.row][next]) {
				continue
			}
			k.col = next
			k.adjust(true)
			break
		}
	case input.Up:
		n := len(kbdKeys)
		next := k.row
		for {
			next = (next - 1 + n) % n
			if k.adjustCol(next) {
				k.adjust(true)
				break
			}
		}
	case input.Down:
		n := len(kbdKeys)
		next := k.row
		for {
			next = (next + 1) % n
			if k.adjustCol(next) {
				k.adjust(true)
				break
			}
		}
	case input.Rune:
		k.rune(e.Rune)
	case input.Center, input.Button3:
		r := kbdKeys[k.row][k.col]
		k.rune(r)
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
				bg = k.bgact
				if key == '⌫' {
					bg = k.bsact
				}
				col = th.Background
			}
			var sz image.Point
			if key == '⌫' {
				icn := assets.KeyBackspace
				sz = icn.Bounds().Size()
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

func (s *ChoiceScreen) Layout(ctx *Context, ops op.Ctx, th *Colors, dims image.Point, active bool) (int, bool) {
	for active {
		e, ok := ctx.Next()
		if !ok {
			break
		}
		switch e.Button {
		case input.Button1:
			if e.Click {
				return -1, true
			}
		case input.Button3, input.Center:
			if e.Click {
				return s.choice, true
			}
		case input.Up:
			if e.Pressed {
				if s.choice > 0 {
					s.choice--
				}
			}
		case input.Down:
			if e.Pressed {
				if s.choice < len(s.Choices)-1 {
					s.choice++
				}
			}
		}
	}

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

	if active {
		layoutNavigation(ctx, ops, th, dims,
			NavButton{Button: input.Button1, Style: StyleSecondary, Icon: assets.IconBack},
			NavButton{Button: input.Button3, Style: StylePrimary, Icon: assets.IconCheckmark},
		)
	}
	return 0, false
}

type MainScreen struct {
	mnemonic bip39.Mnemonic
	page     program
	scanner  *ScanScreen
	desc     *DescriptorScreen
	warning  *ErrorScreen
	sdcard   struct {
		warning *ConfirmWarningScreen
		shown   bool
	}
}

func (s *MainScreen) Select(ctx *Context) {
	switch s.page {
	case backupWallet:
		s.scanner = &ScanScreen{
			Title: "Scan",
			Lead:  "Wallet Output Descriptor",
		}
	}
}

func (s *MainScreen) Layout(ctx *Context, ops op.Ctx, dims image.Point, err error) {
	var th *Colors
	var title string
	if s.sdcard.warning != nil && ctx.NoSDCard {
		s.sdcard.warning = nil
		s.Select(ctx)
	}
	for {
		switch s.page {
		case backupWallet:
			title = "Backup Wallet"
			th = &descriptorTheme
		}
		switch {
		case s.scanner != nil:
			res, status := s.scanner.Layout(ctx, ops.Begin(), dims)
			dialog := ops.End()
			switch status {
			case ResultNone:
				dialog.Add(ops)
				return
			}
			s.scanner = nil
			switch status {
			case ResultCancelled:
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
				s.warning = &ErrorScreen{
					Title: "Error",
					Body:  "The scanned data does not represent a wallet output descriptor or XPUB key.",
				}
				continue
			}
			s.desc = &DescriptorScreen{
				Descriptor: desc,
			}
			continue
		case s.desc != nil:
			done := s.desc.Layout(ctx, ops.Begin(), dims)
			dialog := ops.End()
			if !done {
				dialog.Add(ops)
				return
			}
			s.desc = nil
		case s.warning != nil:
			dismissed := s.warning.Layout(ctx, ops.Begin(), th, dims)
			warning := ops.End()
			if dismissed {
				s.warning = nil
				continue
			}
			defer warning.Add(ops)
		case s.sdcard.warning != nil:
			res := s.sdcard.warning.Layout(ctx, ops.Begin(), th, dims)
			warning := ops.End()
			switch res {
			case ConfirmYes:
				s.sdcard.warning = nil
				s.sdcard.shown = true
				s.Select(ctx)
				continue
			case ConfirmNo:
				s.sdcard.warning = nil
				continue
			}
			defer warning.Add(ops)
		}
		e, ok := ctx.Next()
		if !ok {
			break
		}
		switch e.Button {
		case input.Button3, input.Center:
			if !e.Click {
				break
			}
			if ctx.NoSDCard || s.sdcard.shown {
				s.Select(ctx)
			} else {
				s.sdcard.warning = &ConfirmWarningScreen{
					Title: "Remove SD card",
					Body:  "Remove SD card to continue.\n\nHold button to ignore this warning.",
					Icon:  assets.IconRight,
				}
			}
		}

		switch e.Button {
		case input.Left:
			if !e.Pressed {
				break
			}
			s.page--
			if s.page < 0 {
				s.page = backupWallet
			}
		case input.Right:
			if !e.Pressed {
				break
			}
			s.page++
			if s.page > backupWallet {
				s.page = 0
			}
		}
	}
	op.ColorOp(ops, th.Background)

	layoutTitle(ctx, ops, dims.X, th.Text, title)

	r := layout.Rectangle{Max: dims}
	sz := s.layoutPage(ops.Begin(), th, dims.X)
	op.Position(ops, ops.End(), r.Center(sz))

	sz = s.layoutPager(ops.Begin(), th)
	_, footer := r.CutBottom(leadingSize)
	op.Position(ops, ops.End(), footer.Center(sz))

	versz := widget.LabelW(ops.Begin(), ctx.Styles.debug, 100, th.Text, ctx.Version)
	op.Position(ops, ops.End(), r.SE(versz.Add(image.Pt(4, 0))))
	shsz := widget.LabelW(ops.Begin(), ctx.Styles.debug, 100, th.Text, "SeedHammer")
	op.Position(ops, ops.End(), r.SW(shsz).Add(image.Pt(3, 0)))

	if err != nil {
		sz := layoutWarning(ctx, ops.Begin(), th, dims.X,
			"Error",
			err.Error(),
		)
		r := layout.Rectangle{Max: dims}
		op.Position(ops, ops.End(), r.Center(sz))
	} else if s.warning == nil {
		layoutNavigation(ctx, ops, th, dims, NavButton{Button: input.Button3, Style: StylePrimary, Icon: assets.IconCheckmark})
	}
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
	Button input.Button
	Style  ButtonStyle
	Icon   image.Image
}

func layoutNavigation(ctx *Context, ops op.Ctx, th *Colors, dims image.Point, btns ...NavButton) image.Rectangle {
	navsz := assets.NavBtnPrimary.Bounds().Size()
	button := func(ops op.Ctx, b NavButton, pressed bool) {
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
		op.MaskOp(ops, b.Icon)
		switch b.Style {
		case StyleSecondary:
			op.ColorOp(ops, th.Text)
		case StylePrimary:
			op.ColorOp(ops, th.Text)
		}
		if pressed {
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
		idx := int(b.Button - input.Button1)
		button(ops.Begin(), b, ctx.Buttons[b.Button])
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

func (s *MainScreen) layoutPage(ops op.Ctx, th *Colors, width int) image.Point {
	var h layout.Align

	op.MaskOp(ops.Begin(), assets.ArrowLeft)
	op.ColorOp(ops, th.Text)
	left := ops.End()
	leftsz := h.Add(assets.ArrowLeft.Bounds().Size())

	op.MaskOp(ops.Begin(), assets.ArrowRight)
	op.ColorOp(ops, th.Text)
	right := ops.End()
	rightsz := h.Add(assets.ArrowRight.Bounds().Size())

	contentsz := h.Add(s.layoutMainPlates(ops.Begin()))
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

func (s *MainScreen) layoutMainPlates(ops op.Ctx) image.Point {
	switch s.page {
	case backupWallet:
		img := assets.Hammer
		op.ImageOp(ops, img)
		return img.Bounds().Size()
	}
	panic("invalid page")
}

func (s *MainScreen) layoutPager(ops op.Ctx, th *Colors) image.Point {
	const npages = int(backupWallet) + 1
	const space = 4
	if npages <= 1 {
		return image.Point{}
	}
	sz := assets.CircleFilled.Bounds().Size()
	for i := 0; i < npages; i++ {
		op.Offset(ops, image.Pt((sz.X+space)*i, 0))
		mask := assets.Circle
		if i == int(s.page) {
			mask = assets.CircleFilled
		}
		op.MaskOp(ops, mask)
		op.ColorOp(ops, th.Text)
	}
	return image.Pt((sz.X+space)*npages-space, sz.Y)
}

type Platform interface {
	Input(ch chan<- input.Event) error
	Engraver() (io.ReadWriteCloser, error)
	Camera(size image.Point, frames chan camera.Frame, out <-chan camera.Frame) (func(), error)
	Dump(path string, r io.Reader) error
	Now() time.Time
	SDCard() <-chan bool
}

type LCD interface {
	Framebuffer() draw.RGBA64Image
	Dirty(sr image.Rectangle) error
}

type App struct {
	Debug bool

	root op.Ops
	ctx  *Context
	btns <-chan input.Event
	lcd  LCD
	err  error
	scr  MainScreen
	idle struct {
		eatButton bool
		timeout   <-chan time.Time
	}

	screenshotCounter int
}

func NewApp(pl Platform, lcd LCD, version string) *App {
	btns := make(chan input.Event, 10)
	ctx := NewContext(pl)
	ctx.Version = version
	a := &App{
		ctx:  ctx,
		err:  pl.Input(btns),
		btns: WakeupChan(ctx, btns),
		lcd:  lcd,
	}
	return a
}

const idleTimeout = 3 * time.Minute

func (a *App) Frame() {
	select {
	case inserted := <-a.ctx.Platform.SDCard():
		a.ctx.NoSDCard = !inserted
	case <-a.ctx.Wakeup:
	case <-a.idle.timeout:
		a.saveScreen()
		// The screen saver has invalidated the cached
		// frame content.
		a.root = op.Ops{}
		a.idle.eatButton = true
	}
	screenshot := false
loop:
	for {
		select {
		case e := <-a.btns:
			if e.Button == input.Screenshot {
				screenshot = true
				break
			}
			if a.idle.eatButton {
				a.idle.eatButton = false
				break
			}
			a.ctx.Events(e)
		default:
			break loop
		}
	}
	a.ctx.Repeat()
	start := time.Now()
	pressed := false
	for _, b := range a.ctx.Buttons {
		pressed = pressed || b
	}
	a.idle.timeout = nil
	if !pressed {
		a.idle.timeout = time.NewTimer(idleTimeout).C
	}
	ops := a.root.Reset()
	frame := a.lcd.Framebuffer()
	dims := frame.Bounds().Size()
	a.scr.Layout(a.ctx, ops, dims, a.err)
	layoutTime := time.Now()
	dirty := a.root.Draw(frame)
	renderTime := time.Now()
	a.lcd.Dirty(dirty)
	drawTime := time.Now()
	if a.Debug {
		if screenshot {
			a.screenshotCounter++
			name := fmt.Sprintf("screenshot%d.png", a.screenshotCounter)
			dumpImage(a.ctx.Platform, name, frame)
		}
		log.Printf("frame: %v layout: %v render: %v draw: %v %v",
			drawTime.Sub(start), layoutTime.Sub(start), renderTime.Sub(layoutTime), drawTime.Sub(renderTime), dirty)
	}
}

func dumpImage(p Platform, name string, img image.Image) {
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, img); err != nil {
		log.Printf("screenshot: failed to encode: %v", err)
		return
	}
	if err := p.Dump(name, buf); err != nil {
		log.Printf("screenshot: %s: %v", name, err)
		return
	}
	log.Printf("screenshot: dumped %s", name)
}

func (a *App) saveScreen() {
	var s saver.State
	for {
		select {
		case <-a.ctx.Wakeup:
			return
		default:
			frame := a.lcd.Framebuffer()
			saver.Draw(&s, frame)
			a.lcd.Dirty(frame.Bounds())
		}
	}
}

func mustFace(fnt *sfnt.Font, ppem int) font.Face {
	face, err := opentype.NewFace(fnt, &opentype.FaceOptions{
		Size:    float64(ppem),
		DPI:     72, // Size is in pixels.
		Hinting: font.HintingFull,
	})
	if err != nil {
		panic(err)
	}
	return face
}

func face(ttf []byte, ppem int) font.Face {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    float64(ppem),
		DPI:     72, // Size is in pixels.
		Hinting: font.HintingFull,
	})
	if err != nil {
		panic(err)
	}
	return face
}

func rgb(c uint32) color.NRGBA {
	return argb(0xff000000 | c)
}

func argb(c uint32) color.NRGBA {
	return color.NRGBA{A: uint8(c >> 24), R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
}
