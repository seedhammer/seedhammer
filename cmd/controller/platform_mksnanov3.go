//go:build mksnanov3

package main

import (
	"device/stm32"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"machine"
	"time"
	"unsafe"

	"seedhammer.com/backup"
	"seedhammer.com/driver/bts7960"
	"seedhammer.com/driver/mjolnir2"
	"seedhammer.com/driver/st7789"
	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
	"seedhammer.com/gui"
	"seedhammer.com/image/rgb565"
	"tinygo.org/x/drivers/touch"
	"tinygo.org/x/drivers/xpt2046"
)

const (
	lcdWidth  = 480
	lcdHeight = 320
)

type button struct {
	state    bool
	debounce time.Time
}

type Platform struct {
	wakeups chan struct{}

	spi   *machine.SPI
	tft   *st7789.Device
	touch xpt2046.Device

	input struct {
		// jogStep track the steps of a jog wheel turn.
		jogStep int
		// jogDir tracks the button that determines the
		// jog turn direction.
		jogDir     int
		touchPoint touch.Point
		debounce   <-chan time.Time
		buttons    [4]button
		wakeups    chan struct{}
	}

	display struct {
		minx, maxx         int
		row, nrows, endrow int
		buffered           bool
		buffers            [2][][2]byte
		remaining          int
		fb                 rgb565.Image
	}
}

func Init() (*Platform, error) {
	machine.BUTTON.Configure(machine.PinConfig{Mode: machine.PinInput})
	machine.BUTTON_JOG_CW.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	machine.BUTTON_JOG_CCW.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	machine.BEEPER.Configure(machine.PinConfig{Mode: machine.PinOutput})

	spi := machine.SPI1
	// Enable DMA2 clock.
	stm32.RCC.SetAHB1ENR_DMA2EN(1)
	// Stream 3, channel 3 sends to SPI1.
	dma := &stm32.DMA2.ST[3]
	touchIRQ := machine.TOUCH_IRQ
	p := &Platform{
		spi:     spi,
		touch:   xpt2046.New(machine.TOUCH_CLK, machine.TOUCH_CS, machine.TOUCH_DIN, machine.TOUCH_DOUT, touchIRQ),
		wakeups: make(chan struct{}, 1),
	}
	inp := &p.input
	inp.wakeups = make(chan struct{}, 1)
	// Trigger reading of the initial state of input.
	inp.wakeups <- struct{}{}
	for i := range p.display.buffers {
		p.display.buffers[i] = make([][2]byte, st7789.MaxDrawSize/int(unsafe.Sizeof([2]byte{})))
	}
	p.switchToTFT()
	p.tft = st7789.NewSPI(spi, dma, machine.LCD_DC, machine.LCD_CS, machine.LCD_RS, machine.LCD_BACKLIGHT)
	if err := p.tft.Configure(st7789.Config{}); err != nil {
		return nil, err
	}
	p.switchToTouch()

	touchIRQ.SetInterrupt(machine.PinToggle, p.inputInterrupt)
	machine.BUTTON.SetInterrupt(machine.PinToggle, p.inputInterrupt)
	machine.BUTTON_JOG_CW.SetInterrupt(machine.PinToggle, p.inputInterrupt)
	machine.BUTTON_JOG_CCW.SetInterrupt(machine.PinToggle, p.inputInterrupt)

	_, err := p.Engraver()
	return p, err
}

func (p *Platform) inputInterrupt(machine.Pin) {
	p.inputWakeup()
}

func (p *Platform) inputWakeup() {
	select {
	case p.input.wakeups <- struct{}{}:
	default:
	}
}

func (p *Platform) processInput() {
	const debounceTimeout = 10 * time.Millisecond

	buttons := [...]bool{!machine.BUTTON_JOG_CCW.Get(), !machine.BUTTON_JOG_CW.Get(), !machine.BUTTON.Get(), p.touch.Touched()}
	timeout := time.Now().Add(debounceTimeout)
	inp := &p.input
	for i, state := range buttons {
		btn := &inp.buttons[i]
		if old := btn.state; old != state {
			if btn.debounce.IsZero() {
				btn.debounce = timeout
				if state && i == 3 {
					inp.touchPoint = p.touch.ReadTouchPoint()
				}
			}
		} else {
			btn.debounce = time.Time{}
		}
	}
	p.scheduleDebounce()
}

func (p *Platform) scheduleDebounce() {
	var earliest time.Time
	inp := &p.input
	for i := range inp.buttons {
		btn := &inp.buttons[i]
		if !btn.debounce.IsZero() && (earliest.IsZero() || btn.debounce.Before(earliest)) {
			earliest = btn.debounce
		}
	}
	if !earliest.IsZero() {
		inp.debounce = time.After(time.Until(earliest))
	}
}

func (p *Platform) processDebounce(evts []gui.Event) []gui.Event {
	inp := &p.input
	now := time.Now()
	for i := range inp.buttons {
		btn := &inp.buttons[i]
		if t := btn.debounce; t.IsZero() || t.After(now) {
			continue
		}
		btn.debounce = time.Time{}
		btn.state = !btn.state
		switch i {
		case 0, 1: // machine.BUTTON_JOG_CCW/CW
			opposite := inp.buttons[1-i].state
			ccw, cw := inp.buttons[0].state, inp.buttons[1].state
			dir := inp.buttons[inp.jogDir].state
			oppoDir := inp.buttons[1-inp.jogDir].state
			s := inp.jogStep
			switch {
			case s == 0 && btn.state && !opposite:
				inp.jogDir = i
				inp.jogStep++
			case s == 1 && cw && ccw:
				inp.jogStep++
			case s == 2 && !dir && oppoDir:
				inp.jogStep++
			case s == 3 && !cw && !ccw:
				btn := gui.CCW
				if inp.jogDir == 1 {
					btn = gui.CW
				}
				evts = append(evts, gui.ButtonEvent{Button: btn, Pressed: true})
				fallthrough
			default:
				inp.jogStep = 0
			}
		case 2: // machine.BUTTON
			evts = append(evts, gui.ButtonEvent{Button: gui.Button3, Pressed: btn.state})
		case 3: // touchpad
			if btn.state {
				p := image.Point{
					X: inp.touchPoint.Y * lcdWidth / 0xffff,
					Y: inp.touchPoint.X * lcdHeight / 0xffff,
				}
				fmt.Println("touch", p, inp.touchPoint)
			}
			evts = append(evts, gui.ButtonEvent{Button: gui.Button1, Pressed: btn.state})
		}
	}
	p.scheduleDebounce()
	return evts
}

func (p *Platform) Events() []gui.Event {
	var evts []gui.Event
	for {
		select {
		case <-p.input.debounce:
			evts = p.processDebounce(evts)
		case <-p.input.wakeups:
			p.processInput()
		default:
			if len(evts) > 0 {
				return evts
			}
			select {
			case <-p.input.debounce:
				evts = p.processDebounce(evts)
			case <-p.input.wakeups:
				p.processInput()
			case <-p.wakeups:
				return evts
			}
		}
	}
}

func (p *Platform) Wakeup() {
	select {
	case p.wakeups <- struct{}{}:
	default:
	}
}

func (p *Platform) PlateSizes() []backup.PlateSize {
	return []backup.PlateSize{backup.SmallPlate}
}

func (p *Platform) EngraverParams() engrave.Params {
	return mjolnir2.Params
}

type engraver struct {
	*mjolnir2.Device
}

func beep() {
	machine.BEEPER.Set(true)
	time.Sleep(10 * time.Millisecond)
	machine.BEEPER.Set(false)
	time.Sleep(200 * time.Millisecond)
	machine.BEEPER.Set(true)
	time.Sleep(10 * time.Millisecond)
	machine.BEEPER.Set(false)
}

func (e engraver) Engrave(_ backup.PlateSize, plan engrave.Plan, quit <-chan struct{}) error {
	defer beep()
	return e.Device.Engrave(plan, quit)
}

func (p *Platform) Engraver() (gui.Engraver, error) {
	const (
		VCC  = machine.Z_ENABLE
		R_EN = machine.E0_DIR
		L_EN = machine.E0_STEP
		RPWM = machine.Z_DIR
		LPWM = machine.Z_STEP
	)
	needle, err := bts7960.New(VCC, R_EN, L_EN, RPWM, LPWM, &machine.TIM3)
	if err != nil {
		return nil, err
	}
	X, err := tmc2209.New(0, machine.X_UART, machine.X_DIAG, machine.X_ENABLE, machine.X_DIR, machine.X_STEP)
	if err != nil {
		return nil, err
	}
	Y, err := tmc2209.New(0, machine.Y_UART, machine.Y_DIAG, machine.Y_ENABLE, machine.Y_DIR, machine.Y_STEP)
	if err != nil {
		return nil, err
	}
	dev, err := mjolnir2.New(X, Y, needle, &machine.TIM8)
	if err != nil {
		return nil, err
	}
	return engraver{dev}, nil
}

func (p *Platform) ScanQR(img *image.Gray) ([][]byte, error) {
	return nil, errors.New("ScanQR not implemented")
}

func (p *Platform) CameraFrame(dims image.Point) {
}

func (p *Platform) DisplaySize() image.Point {
	return image.Pt(lcdWidth, lcdHeight)
}

func (p *Platform) Dirty(r image.Rectangle) error {
	r = r.Intersect(image.Rectangle{Max: p.DisplaySize()})
	if r.Empty() {
		return nil
	}
	// Round buffer sizes to a whole number of rows.
	rowSize := r.Dx()
	d := &p.display
	d.nrows = cap(d.buffers[0]) / rowSize
	d.minx, d.maxx = r.Min.X, r.Max.X
	d.row = r.Min.Y
	d.endrow = r.Max.Y
	chunkSize := d.nrows * rowSize
	for i := range d.buffers {
		d.buffers[i] = d.buffers[i][:chunkSize]
	}
	d.remaining = (r.Dy() + d.nrows - 1) / d.nrows
	d.fb.Stride = r.Dx()
	p.switchToTFT()
	return p.tft.BeginFrame(r)
}

func (p *Platform) switchToTFT() {
	p.spi.Configure(machine.SPIConfig{Frequency: 20 * machine.MHz})
}

func (p *Platform) switchToTouch() {
	p.touch.Configure(&xpt2046.Config{})
	// Read touch state once to cover for missed interrupts.
	p.inputWakeup()
}

func (p *Platform) NextChunk() (draw.RGBA64Image, bool) {
	d := &p.display
	if d.buffered {
		r := d.fb.Rect
		buf := d.buffers[0][:r.Dx()*r.Dy()]
		p.tft.Draw(buf)
		d.buffers[0], d.buffers[1] = d.buffers[1], d.buffers[0]
		d.buffered = false
		if d.remaining == 0 {
			p.tft.EndFrame()
			p.switchToTouch()
		}
	}
	if d.remaining == 0 {
		return nil, false
	}
	d.buffered = true
	d.remaining--
	buf := d.buffers[0]
	d.fb.Pix = unsafe.Slice((*rgb565.Color)(unsafe.Pointer(unsafe.SliceData(buf))), len(buf))
	maxy := d.row + d.nrows
	if maxy > d.endrow {
		maxy = d.endrow
	}
	d.fb.Rect = image.Rect(d.minx, d.row, d.maxx, maxy)
	d.row = maxy
	return &d.fb, true
}
