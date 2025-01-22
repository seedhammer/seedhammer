//go:build tinygo && (pico || pico2)

package main

import (
	"device/rp"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"machine"
	"time"
	"unsafe"

	"seedhammer.com/backup"
	"seedhammer.com/driver/ft6x36"
	"seedhammer.com/driver/ili9488"
	"seedhammer.com/driver/mjolnir2"
	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
	"seedhammer.com/gui"
	"seedhammer.com/image/rgb565"
	"tinygo.org/x/drivers/delay"
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

	lcdDev   *ili9488.Device
	touchDev *ft6x36.Device
	engraver *mjolnir2.Device

	touch struct {
		last    bool
		lastPos image.Point
		current bool
		pos     image.Point
		tim     time.Time
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

	LCD_RS  = machine.NoPin
	LCD_CS  = machine.NoPin
	LCD_TE  = machine.GPIO13
	LCD_DC  = machine.GPIO16
	LCD_WRX = machine.GPIO17
	LCD_DB0 = machine.GPIO18

	DRV_ENABLE = machine.GPIO10

	NEEDLE       = machine.GPIO8
	NEEDLE_SENSE = machine.GPIO26

	STEPPER_UART = machine.GPIO9
	X_ADDR       = 0b00
	X_DIAG       = machine.GPIO7
	X_STEP       = machine.GPIO6
	X_DIR        = machine.GPIO5
	Y_ADDR       = 0b01
	Y_DIAG       = machine.GPIO4
	Y_STEP       = machine.GPIO3
	Y_DIR        = machine.GPIO2

	lcdDMAChannel = 0
)

var (
	needleSenseADC = machine.ADC{Pin: NEEDLE_SENSE}
	// needlePWM      = machine.PWM7
	touchI2C = machine.I2C1
	lcdPIO   = rp.PIO0
)

const (
	needleActivation = 45 * time.Millisecond / 10
)

func Init() (*Platform, error) {
	// err := needlePWM.Configure(machine.PWMConfig{
	// 	Period: uint64(mjolnir2.NeedlePeriod),
	// })
	// if err != nil {
	// 	return nil, err
	// }
	// ch, err := needlePWM.Channel(NEEDLE)
	// if err != nil {
	// 	return nil, err
	// }
	// needlePWM.Set(ch, 0)
	// needlePWMThreshold := time.Duration(needlePWM.Top()) * needleActivation / mjolnir2.NeedlePeriod

	if err := touchI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: TOUCH_SDA, SCL: TOUCH_SCL}); err != nil {
		return nil, err
	}
	touch := ft6x36.New(touchI2C)
	touch.Configure(ft6x36.Config{})
	p := &Platform{
		touchDev: touch,
		wakeups:  make(chan struct{}, 1),
	}
	for i := range p.display.buffers {
		p.display.buffers[i] = make([][2]byte, ili9488.MaxDrawSize/int(unsafe.Sizeof([2]byte{})))
	}

	p.lcdDev = ili9488.New(lcdDMAChannel, LCD_DC, LCD_CS, LCD_RS, LCD_WRX, LCD_DB0, LCD_TE, lcdPIO)
	if err := p.lcdDev.Configure(ili9488.Config{}); err != nil {
		return nil, err
	}
	X, err := tmc2209.New(X_ADDR, STEPPER_UART, X_DIAG, X_DIR, X_STEP)
	if err != nil {
		return nil, fmt.Errorf("pico: x-axis stepper: %w", err)
	}
	Y, err := tmc2209.New(Y_ADDR, STEPPER_UART, Y_DIAG, Y_DIR, Y_STEP)
	if err != nil {
		return nil, fmt.Errorf("pico: y-axis stepper: %w", err)
	}
	needle := func(enable bool) {
		// t := needlePWMThreshold
		// if !enable {
		// 	t = 0
		// }
		// needlePWM.Set(ch, uint32(t))
	}
	engraver, err := mjolnir2.New(DRV_ENABLE, X, Y, needle)
	if err != nil {
		return nil, err
	}
	p.engraver = engraver
	// NEEDLE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	// NEEDLE.Low()
	// for range 10 {
	// 	NEEDLE.High()
	// 	delay.Sleep(5 * time.Millisecond)
	// 	NEEDLE.Low()
	// 	delay.Sleep(15 * time.Millisecond)
	// }

	return p, nil
}

func (p *Platform) AppendEvents(deadline time.Time, evts []gui.Event) []gui.Event {
	const debounce = 5 * time.Millisecond

	inp := &p.touch
	for {
		tp, touching := p.touchDev.ReadTouchPoint()
		if touching {
			inp.pos = tp
		}
		if touching != inp.current {
			inp.tim = time.Now()
			inp.current = touching
		}
		if inp.current != inp.last && time.Since(inp.tim) > debounce || inp.pos != inp.lastPos {
			inp.last = inp.current
			inp.lastPos = inp.pos
			pt := image.Point{
				X: inp.pos.Y,
				Y: lcdHeight - inp.pos.X,
			}
			fmt.Println("touch", pt)
			return evts
		}
		if !time.Now().Before(deadline) {
			return evts
		}
		select {
		case <-p.wakeups:
			return evts
		default:
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
	return engraver{p.engraver}, nil
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
