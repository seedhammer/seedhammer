//go:build tinygo && rp

package main

import (
	"device/rp"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"log"
	"machine"
	"slices"
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
	TOUCH_INT = machine.GPIO16

	LCD_RS  = machine.NoPin
	LCD_CS  = machine.NoPin
	LCD_TE  = machine.GPIO13
	LCD_DC  = machine.GPIO17
	LCD_WRX = machine.GPIO18
	LCD_DB0 = machine.GPIO19

	DRV_ENABLE = machine.GPIO10

	STEPPER_UART = machine.GPIO9
	X_ADDR       = 0b00
	Y_ADDR       = 0b01
	X_DIAG       = machine.GPIO8
	Y_DIAG       = machine.GPIO7

	engraverBasePin = machine.GPIO2
	// Stepper and needle pins are assumed to
	// be at constant offsets from engraver base pin.
	Y_DIR  = engraverBasePin + 0
	X_DIR  = engraverBasePin + 1
	NEEDLE = engraverBasePin + 2
	Y_STEP = engraverBasePin + 3
	X_STEP = engraverBasePin + 4

	NEEDLE_VREF = machine.GPIO11
	DATA_INT    = machine.GPIO27
	DATA_SDA    = machine.GPIO28
	DATA_SCL    = machine.GPIO29

	lcdDMAChannel = 0
)

var (
	needleVREFPWM = machine.PWM5
	touchI2C      = machine.I2C1
	// Data I2C bus for the USB PD and NFC peripherals.
	dataI2C     = machine.I2C0
	lcdPIO      = rp.PIO0
	stepperPIO  = rp.PIO1
	engraverPIO = rp.PIO2
)

const (
	// The period of a needle cycle.
	needlePeriod = 20 * time.Millisecond
	// The duration of a needle cycle turned on.
	needleActivation = 45 * time.Millisecond / 10
	// needleCurrentLimit in millisamperes (mA).
	needleCurrentLimit = 1_000
	// needleSenseScale is the current limit
	// in milliamperes (mA) that corresponds to a
	// 100% PWM duty cycle output to NEEDLE_VREF.
	needleSenseScale = 32_500

	// Maximum supply voltage in volts.
	maxVoltage = 28

	// stallThreshold is the TMC2209 SGTHRS for triggering a
	// stall.
	stallThreshold = 110
	// minimumStallVelocity is the speed in full-steps/second for
	// StallGuard to be enabled.
	minimumStallVelocity = 250
	// fullStepsPerRevolution is the number of full-steps for a full
	// motor revolution.
	fullStepsPerRevolution = 200
	// mmPerRevolution is the axis movement in millimeters per revolution.
	mmPerRevolution = 8
	// mm is the number of (micro-)steps per millimeter.
	mm = fullStepsPerRevolution / mmPerRevolution * tmc2209.Microsteps
	// The coordinates of the top-left plate corner relative to the
	// homing zero.
	originX, originY = 2.1 * mm, 2.1 * mm
	// Maximum distance to travel before giving up homing.
	homingDist = 100 * mm
	// strokeWidth of engraving lines.
	strokeWidth = 0.3 * mm
	// Speeds in steps/second.
	topSpeed       = 40 * mm
	engravingSpeed = 8 * mm
	homingSpeed    = 15 * mm
	// acceleration in steps/s².
	acceleration = 100 * mm
	invertX      = true
	invertY      = false
)

func Init() (*Platform, error) {
	if err := dataI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: DATA_SDA, SCL: DATA_SCL}); err != nil {
		return nil, fmt.Errorf("data I2C: %w", err)
	}
	DATA_INT.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	usbpd := ap33772s.New(dataI2C)
	if err := usbpd.AdjustVoltage(maxVoltage * 1000); err != nil {
		log.Printf("error: %v", err)
	}
	nfc := clrc663.New(dataI2C)
	if err := nfc.Configure(); err != nil {
		return nil, fmt.Errorf("data I2C: %w", err)
	}
	nfc.SetPadEnable(0b1 << 4)
	// for {
	// 	for DATA_INT.Get() {
	// 	}
	// 	nfc.SetPadEnable(0b1 << 4)
	// 	st, err := usbpd.ReadStatus()
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// 	println(st)
	// 	nfc.SetPadEnable(0b0 << 4)
	// }

	// return nil, func() error {
	// 	fmt.Println("******* Reading NFC tag ******")
	// 	const prot = clrc663.ISO14443a
	// 	// const prot = clrc663.ISO15693
	// 	if err := nfc.RadioOn(prot); err != nil {
	// 		return err
	// 	}
	// 	defer nfc.RadioOff()
	// 	// tag, err := iso15693.Open(nfc, clrc663.FIFOSize)
	// 	tag, err := iso14443a.Open(nfc)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	// fmt.Println("tag.UID", tag.UID)
	// 	// buf := make([]byte, 1024)
	// 	// n, err := tag.Read(buf)
	// 	// if err != nil && !errors.Is(err, io.EOF) {
	// 	// 	return err
	// 	// }
	// 	// fmt.Println("data", n, buf[:n])
	// 	contents := ndef.NewReader(tag)
	// 	if err := contents.Next(); err != nil {
	// 		return err
	// 	}
	// 	// buf := make([]byte, clrc663.FIFOSize)
	// 	// // buf := make([]byte, 32)
	// 	// accum := new(bytes.Buffer)
	// 	// for {
	// 	// 	n, err := tag.Read(buf)
	// 	// 	if err != nil {
	// 	// 		if errors.Is(err, io.EOF) {
	// 	// 			break
	// 	// 		}
	// 	// 		log.Printf("nfcread : %v\n", err)
	// 	// 		break
	// 	// 	}
	// 	// 	fmt.Println("data", n, buf[:n])
	// 	// 	accum.Write(buf[:n])
	// 	// }
	// 	// all := accum.Bytes()
	// 	// return fmt.Errorf("NFC done (%d): %v", len(all), all)
	// 	return errors.New("not done yet?")
	// }()

	if err := touchI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: TOUCH_SDA, SCL: TOUCH_SCL}); err != nil {
		return nil, fmt.Errorf("touch I2C: %w", err)
	}

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
		p.engraver.dev = e
	} else {
		log.Printf("pico: %v", err)
		p.engraver.err = err
	}

	return p, nil
}

func configEngraver() (gui.Engraver, error) {
	DRV_ENABLE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	DRV_ENABLE.Set(true)
	vrefCh, err := needleVREFPWM.Channel(NEEDLE_VREF)
	if err != nil {
		// This should never happen with a proper match
		// between the PWM unit and the vref pin.
		panic(err)
	}
	// The needle current sense is a voltage reference.
	// Since we can't generate an (analog) voltage
	// directly, an external low-pass filter converts a
	// PWM signal to a voltage from 0-3.3V. The PWM frequency
	// simply needs to be large enough to minimize voltage
	// ripples.
	const vrefPWMFreq = 100 * machine.KHz
	if err := needleVREFPWM.Configure(machine.PWMConfig{
		Period: uint64(time.Second / vrefPWMFreq),
	}); err != nil {
		panic(err)
	}
	// Compute duty cycle that corresponds to the limit.
	duty := uint32(uint64(needleVREFPWM.Top()) * needleCurrentLimit / needleSenseScale)
	needleVREFPWM.Set(vrefCh, duty)

	uart, err := tmc2209.NewUART(stepperPIO, STEPPER_UART)
	if err != nil {
		return nil, err
	}
	X := &tmc2209.Device{
		Bus:    uart,
		Addr:   X_ADDR,
		Invert: invertX,
	}
	Y := &tmc2209.Device{
		Bus:    uart,
		Addr:   Y_ADDR,
		Invert: invertY,
	}
	for i, axis := range []*tmc2209.Device{X, Y} {
		axis.SetupSharedUART()
		if err := axis.Configure(); err != nil {
			return nil, fmt.Errorf("configuring stepper %d: %w", i, err)
		}
		if err := axis.SetStallThreshold(stallThreshold); err != nil {
			return nil, fmt.Errorf("configuring stepper stall threshold %d: %w", i, err)
		}
		if err := axis.SetMinimumStallVelocity(minimumStallVelocity); err != nil {
			return nil, fmt.Errorf("configuring stepper stall velocity %d: %w", i, err)
		}
	}
	home := image.Point{
		X: -homingDist,
		Y: -homingDist,
	}
	d := &mjolnir2.Device{
		Pio:              engraverPIO,
		BasePin:          engraverBasePin,
		XDiag:            X_DIAG,
		YDiag:            Y_DIAG,
		Home:             home,
		TopSpeed:         topSpeed,
		EngravingSpeed:   engravingSpeed,
		HomingSpeed:      homingSpeed,
		Acceleration:     acceleration,
		NeedlePeriod:     needlePeriod,
		NeedleActivation: needleActivation,
	}
	if err := d.Configure(); err != nil {
		return nil, err
	}
	return &engraver{
		Device: d,
		XAxis:  X,
		YAxis:  Y,
	}, nil
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
	return engrave.Params{
		StrokeWidth: strokeWidth,
		Millimeter:  mm,
	}
}

type engraver struct {
	XAxis, YAxis *tmc2209.Device
	*mjolnir2.Device
}

func (e *engraver) Close() {}

func (e *engraver) Engrave(_ backup.PlateSize, plan engrave.Plan, quit <-chan struct{}) error {
	// Set up pins.
	DRV_ENABLE.Set(false)
	defer DRV_ENABLE.Set(true)
	// Wait for standstill tuning of the drivers.
	time.Sleep(tmc2209.StandstillTuningPeriod)

	ejectPos := image.Point{}
	plan = engrave.Commands(
		plan,
		// Return to "eject" position.
		engrave.Plan(slices.Values([]engrave.Command{engrave.Move(ejectPos)})),
	)
	plan = engrave.Offset(originX, originY, plan)
	if err := e.Device.Engrave(plan, quit); err != nil {
		if err := e.XAxis.Error(); err != nil {
			return fmt.Errorf("X axis: %w", err)
		}
		if err := e.YAxis.Error(); err != nil {
			return fmt.Errorf("Y axis: %w", err)
		}
		return err
	}
	return nil
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
