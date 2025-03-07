//go:build tinygo && (pico || pico2)

package main

import (
	"device/rp"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"log"
	"machine"
	"time"
	"unsafe"

	"seedhammer.com/backup"
	"seedhammer.com/driver/ap33772s"
	"seedhammer.com/driver/clrc663"
	"seedhammer.com/driver/ft6x36"
	"seedhammer.com/driver/ili9488"
	"seedhammer.com/driver/mjolnir2"
	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
	"seedhammer.com/gui"
	"seedhammer.com/image/rgb565"
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
	timer   *time.Timer

	lcdDev   *ili9488.Device
	engraver struct {
		dev gui.Engraver
		err error
	}

	touch struct {
		dev     *ft6x36.Device
		ints    chan struct{}
		last    bool
		lastPos image.Point
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

const (
	TOUCH_SDA = machine.GPIO14
	TOUCH_SCL = machine.GPIO15
	TOUCH_INT = machine.GPIO12

	LCD_RS  = machine.NoPin
	LCD_CS  = machine.NoPin
	LCD_TE  = machine.GPIO13
	LCD_DC  = machine.GPIO16
	LCD_WRX = machine.GPIO17
	LCD_DB0 = machine.GPIO18

	DRV_ENABLE = machine.GPIO11

	NEEDLE       = machine.GPIO4
	NEEDLE_SENSE = machine.GPIO26

	STEPPER_UART = machine.GPIO10
	X_ADDR       = 0b00
	X_DIAG       = machine.GPIO8
	X_STEP       = machine.GPIO6
	X_DIR        = machine.GPIO3
	Y_ADDR       = 0b01
	Y_DIAG       = machine.GPIO7
	Y_STEP       = machine.GPIO5
	Y_DIR        = machine.GPIO2

	DATA_INT = machine.GPIO27
	DATA_SDA = machine.GPIO28
	DATA_SCL = machine.GPIO29

	lcdDMAChannel = 0
)

var (
	needleSenseADC = machine.ADC{Pin: NEEDLE_SENSE}
	needlePWM      = machine.PWM2
	touchI2C       = machine.I2C1
	// Data I2C bus for the USB PD and NFC peripherals.
	dataI2C    = machine.I2C0
	lcdPIO     = rp.PIO0
	stepperPIO = rp.PIO1
)

const (
	needleActivation = 45 * time.Millisecond / 10
	maxVoltagemV     = 28_000
)

func Init() (*Platform, error) {
	if err := dataI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: DATA_SDA, SCL: DATA_SCL}); err != nil {
		return nil, fmt.Errorf("data I2C: %w", err)
	}
	nfc := clrc663.New(dataI2C)
	if err := nfc.Configure(); err != nil {
		return nil, fmt.Errorf("data I2C: %w", err)
	}

	// Some platforms won't recognize the USB v1.1 interface
	// of the microcontroller when there's also a USB PD
	// controller present. For example, the builtin USB drive
	// for firmware upgrades won't show up in macOS.
	//
	// To work around this, the CC1 and CC2 data pins are
	// disconnected at startup (and therefore in firmware
	// upgrade mode). An OUT pin on the CLRC663 controls the
	// connection.
	const usbPDCCEnablePin = 7
	// Set pin (really, all output enabled pins) to output low.
	if err := nfc.SetPadOutput(0b0 << usbPDCCEnablePin); err != nil {
		return nil, fmt.Errorf("data I2C: %w", err)
	}
	// Set pin to output mode.
	if err := nfc.SetPadEnable(0b1 << usbPDCCEnablePin); err != nil {
		return nil, fmt.Errorf("data I2C: %w", err)
	}

	if err := touchI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: TOUCH_SDA, SCL: TOUCH_SCL}); err != nil {
		return nil, fmt.Errorf("touch I2C: %w", err)
	}
	usbpd := ap33772s.New(dataI2C)
	if err := usbpd.AdjustVoltage(maxVoltagemV); err != nil {
		log.Printf("error: %v", err)
	}
	// DATA_INT.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	// for {
	// 	time.Sleep(100 * time.Millisecond)
	// 	fmt.Println("DATA_INT:", DATA_INT.Get())
	// }
	// vol, err := usbpd.ReadVoltage()
	// fmt.Println(vol, err)
	// NEEDLE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	// NEEDLE.High()
	// time.Sleep(100 * time.Millisecond)
	// cur, err := usbpd.ReadCurrent()
	// NEEDLE.Low()
	// if err != nil {
	// 	panic(err)
	// }
	// fmt.Println("current", cur)
	// for {
	// 	temp, err := usbpd.ReadTemperature()
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// 	fmt.Println("temp", temp)
	// 	time.Sleep(200 * time.Millisecond)
	// }

	// if err := nfc.TestDump(); err != nil {
	// 	log.Printf("error: %v\n", err)
	// }

	p := &Platform{
		wakeups: make(chan struct{}, 1),
		timer:   time.NewTimer(0),
	}
	for i := range p.display.buffers {
		p.display.buffers[i] = make([][2]byte, ili9488.MaxDrawSize/int(unsafe.Sizeof([2]byte{})))
	}

	p.lcdDev = ili9488.New(lcdDMAChannel, LCD_DC, LCD_CS, LCD_RS, LCD_WRX, LCD_DB0, LCD_TE, lcdPIO)
	if err := p.lcdDev.Configure(ili9488.Config{}); err != nil {
		return nil, err
	}
	touch := ft6x36.New(touchI2C)
	TOUCH_INT.Configure(machine.PinConfig{Mode: machine.PinInput})
	TOUCH_INT.SetInterrupt(machine.PinFalling, p.touchInterrupt)
	p.touch.ints = make(chan struct{}, 1)
	p.touch.dev = touch
	// Setup both drivers for sharing their UART pin.
	e, err := configEngraver()
	if err == nil {
		p.engraver.dev = engraver{e}
	} else {
		log.Printf("pico: %v", err)
		p.engraver.err = err
	}
	// {
	// 	machine.InitADC()
	// 	needleSenseADC.Configure(machine.ADCConfig{})
	// 	NEEDLE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	// 	NEEDLE.Low()
	// 	samples := make([]uint16, 10000)
	// 	samples = samples[:0]
	// 	for range 2 {
	// 		now := time.Now()
	// 		NEEDLE.High()
	// 		for time.Since(now) < 5*time.Millisecond {
	// 			samples = append(samples, needleSenseADC.Get())
	// 		}
	// 		NEEDLE.Low()
	// 		for time.Since(now) < 20*time.Millisecond {
	// 			samples = append(samples, needleSenseADC.Get())
	// 		}
	// 		fmt.Println(len(samples), samples)
	// 		samples = samples[:0]
	// 	}
	// }

	return p, nil
}

func configEngraver() (*mjolnir2.Device, error) {
	err := needlePWM.Configure(machine.PWMConfig{
		Period: uint64(mjolnir2.NeedlePeriod),
	})
	if err != nil {
		return nil, err
	}
	ch, err := needlePWM.Channel(NEEDLE)
	if err != nil {
		return nil, err
	}
	needlePWM.Set(ch, 0)
	needlePWMThreshold := time.Duration(needlePWM.Top()) * needleActivation / mjolnir2.NeedlePeriod
	uart, err := tmc2209.NewUART(stepperPIO, STEPPER_UART)
	if err != nil {
		return nil, err
	}
	X := tmc2209.New(uart, X_ADDR, X_DIAG, X_DIR, X_STEP)
	Y := tmc2209.New(uart, Y_ADDR, Y_DIAG, Y_DIR, Y_STEP)
	X.SetupSharedUART()
	Y.SetupSharedUART()
	if err := X.Configure(); err != nil {
		return nil, fmt.Errorf("x-axis stepper: %w", err)
	}
	if err := Y.Configure(); err != nil {
		return nil, fmt.Errorf("y-axis stepper: %w", err)
	}
	needle := func(enable bool) {
		t := needlePWMThreshold
		if !enable {
			t = 0
		}
		needlePWM.Set(ch, uint32(t))
	}
	return mjolnir2.New(DRV_ENABLE, X, Y, needle)
}

func (p *Platform) touchInterrupt(machine.Pin) {
	select {
	case p.touch.ints <- struct{}{}:
	default:
	}
}

func (p *Platform) AppendEvents(deadline time.Time, evts []gui.Event) []gui.Event {
	// Don't starve touch input.
	select {
	case <-p.touch.ints:
		e, ok := p.processTouch()
		if ok {
			return append(evts, e.Event())
		}
	default:
	}
	p.timer.Reset(time.Until(deadline))
	for {
		select {
		case <-p.timer.C:
			return evts
		case <-p.wakeups:
			return evts
		case <-p.touch.ints:
			e, ok := p.processTouch()
			if !ok {
				break
			}
			return append(evts, e.Event())
		}
	}
}

func (p *Platform) processTouch() (gui.PointerEvent, bool) {
	inp := &p.touch
	tp, touching := p.touch.dev.ReadTouchPoint()
	if touching == inp.last && tp == inp.lastPos {
		return gui.PointerEvent{}, false
	}
	inp.last = touching
	inp.lastPos = tp
	var pt image.Point
	if touching {
		pt = image.Point{
			X: tp.Y,
			Y: lcdHeight - tp.X,
		}
	}
	return gui.PointerEvent{
		Pressed: inp.last,
		Entered: true,
		Pos:     pt,
	}, true
}

func (p *Platform) Wakeup() {
	select {
	case p.wakeups <- struct{}{}:
	default:
	}
}

func (p *Platform) PlateSizes() []backup.PlateSize {
	return []backup.PlateSize{backup.SquarePlate}
}

func (p *Platform) EngraverParams() engrave.Params {
	return mjolnir2.Params
}

type engraver struct {
	*mjolnir2.Device
}

func (e engraver) Engrave(_ backup.PlateSize, plan engrave.Plan, quit <-chan struct{}) error {
	return e.Device.Engrave(plan, quit)
}

func (p *Platform) Engraver() (gui.Engraver, error) {
	return p.engraver.dev, p.engraver.err
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
	return p.lcdDev.BeginFrame(r)
}

func (p *Platform) NextChunk() (draw.RGBA64Image, bool) {
	d := &p.display
	if d.buffered {
		r := d.fb.Rect
		buf := d.buffers[0][:r.Dx()*r.Dy()]
		p.lcdDev.Draw(buf)
		d.buffers[0], d.buffers[1] = d.buffers[1], d.buffers[0]
		d.buffered = false
		if d.remaining == 0 {
			p.lcdDev.EndFrame()
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
