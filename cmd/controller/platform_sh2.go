//go:build tinygo && rp

package main

import (
	"device/rp"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"machine"
	"runtime"
	"slices"
	"time"
	"unsafe"

	"seedhammer.com/backup"
	"seedhammer.com/driver/ap33772s"
	"seedhammer.com/driver/ft6x36"
	"seedhammer.com/driver/ili9488"
	"seedhammer.com/driver/mjolnir2"
	"seedhammer.com/driver/st25r3916"
	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
	"seedhammer.com/gui"
	"seedhammer.com/image/rgb565"
	"seedhammer.com/nfc/poller"
	"seedhammer.com/nfc/type5"
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
	engraver gui.Engraver
	nfc      *nfcDev
	touch    struct {
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
	TOUCH_INT = machine.GPIO13
	TOUCH_SDA = machine.GPIO14
	TOUCH_SCL = machine.GPIO15

	LCD_RS  = machine.NoPin
	LCD_CS  = machine.NoPin
	LCD_TE  = machine.GPIO12
	LCD_DC  = machine.GPIO16
	LCD_WRX = machine.GPIO17
	LCD_RDX = machine.GPIO11
	LCD_DB0 = machine.GPIO18

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

	USBPD_INT = machine.GPIO27
	NFC_INT   = machine.GPIO26
	DATA_SDA  = machine.GPIO28
	DATA_SCL  = machine.GPIO29
)

var (
	touchI2C = machine.I2C1
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
	needleActivationMinVoltage = 5 * time.Millisecond
	needleActivationMaxVoltage = 4 * time.Millisecond
	// needleCurrentLimit in millisamperes (mA).
	needleCurrentLimit = 5_000

	idleVoltage = 5
	// Voltage range for engraving.
	minVoltage = 20_000
	maxVoltage = 28_000

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
	mi2c := &multiplexI2C{
		Bus: make(chan *machine.I2C, 1),
	}
	mi2c.Bus <- dataI2C
	e, err := configEngraver(mi2c)
	if err != nil {
		return nil, err
	}
	// Home and move needle to eject position.
	go e.engrave(nil, nil)

	p := &Platform{
		wakeups:  make(chan struct{}, 1),
		timer:    time.NewTimer(0),
		engraver: e,
	}
	for i := range p.display.buffers {
		p.display.buffers[i] = make([][2]byte, ili9488.MaxDrawSize/int(unsafe.Sizeof([2]byte{})))
	}

	// LCD RDX pin unused.
	LCD_RDX.Configure(machine.PinConfig{Mode: machine.PinOutput})
	LCD_RDX.High()

	lcd, err := ili9488.New(LCD_DC, LCD_CS, LCD_RS, LCD_WRX, LCD_DB0, LCD_TE, lcdPIO)
	if err != nil {
		return nil, err
	}
	if err := lcd.Configure(ili9488.Config{}); err != nil {
		return nil, err
	}
	p.lcdDev = lcd
	if err := touchI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: TOUCH_SDA, SCL: TOUCH_SCL}); err != nil {
		return nil, fmt.Errorf("touch: %w", err)
	}

	touch := ft6x36.New(touchI2C)
	TOUCH_INT.Configure(machine.PinConfig{Mode: machine.PinInput})
	TOUCH_INT.SetInterrupt(machine.PinFalling, p.touchInterrupt)
	p.touch.ints = make(chan struct{}, 1)
	p.touch.dev = touch

	nfc := st25r3916.New(mi2c, NFC_INT)
	p.nfc = newNFCDevice(nfc)
	return p, nil
}

type nfcDev struct {
	*st25r3916.Device
	trans    *type5.Transceiver
	iso15693 bool
}

func newNFCDevice(d *st25r3916.Device) *nfcDev {
	return &nfcDev{
		Device: d,
		trans:  type5.NewTransceiver(d, st25r3916.FIFOSize),
	}
}

func (d *nfcDev) SetProtocol(mode poller.Protocol) error {
	d.iso15693 = false
	var prot st25r3916.Protocol
	switch mode {
	case poller.ISO14443a:
		prot = st25r3916.ISO14443a
	case poller.ISO15693:
		d.iso15693 = true
		prot = st25r3916.ISO15693
	default:
		panic("unsupported mode")
	}
	return d.Device.SetProtocol(prot)
}

func (d *nfcDev) Write(buf []byte) (int, error) {
	if d.iso15693 {
		return d.trans.Write(buf)
	}
	return d.Device.Write(buf)
}

func (d *nfcDev) Read(buf []byte) (int, error) {
	if d.iso15693 {
		return d.trans.Read(buf)
	}
	return d.Device.Read(buf)
}

func (d nfcDev) ReadCapacity() int {
	if d.iso15693 {
		return d.trans.ReadCapacity()
	}
	return st25r3916.FIFOSize
}

func configEngraver(bus *multiplexI2C) (*engraver, error) {
	DRV_ENABLE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	DRV_ENABLE.Set(false)
	usbpd := ap33772s.New(bus, USBPD_INT)
	if err := usbpd.Configure(); err != nil {
		return nil, err
	}

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
		Pio:            engraverPIO,
		BasePin:        engraverBasePin,
		XDiag:          X_DIAG,
		YDiag:          Y_DIAG,
		Home:           home,
		TopSpeed:       topSpeed,
		EngravingSpeed: engravingSpeed,
		HomingSpeed:    homingSpeed,
		Acceleration:   acceleration,
		NeedlePeriod:   needlePeriod,
	}
	if err := d.Configure(); err != nil {
		return nil, err
	}
	ready := make(chan struct{}, 1)
	ready <- struct{}{}
	return &engraver{
		Dev:   d,
		PD:    usbpd,
		XAxis: X,
		YAxis: Y,
		ready: ready,
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
	PD           *ap33772s.Device
	Dev          *mjolnir2.Device
	ready        chan struct{}
}

func (e *engraver) Close() {}

func (e *engraver) Engrave(_ backup.PlateSize, plan engrave.Plan, quit <-chan struct{}) error {
	return e.engrave(plan, quit)
}

func (e *engraver) adjustVoltage(minmV, maxmV int) (int, error) {
	const retries = 3
	for range retries {
		mV, err := e.PD.AdjustVoltage(minmV, maxmV)
		if err != nil {
			return 0, err
		}
		// Allow the new contract to settle.
		time.Sleep(100 * time.Millisecond)
		gotmV, err := e.PD.Voltage()
		if err != nil {
			return 0, err
		}
		if gotmV == mV {
			return mV, nil
		}
		// Contract switches immediately after a previous switch
		// are ignored. Sleep a little and try again.
		time.Sleep(500 * time.Millisecond)
	}
	return 0, errors.New("power negotiation timed out")
}

func (e *engraver) engrave(plan engrave.Plan, quit <-chan struct{}) error {
	<-e.ready
	defer func() {
		e.ready <- struct{}{}
	}()
	voltage, err := e.adjustVoltage(minVoltage, maxVoltage)
	if err != nil {
		return err
	}
	defer e.adjustVoltage(idleVoltage*1000, idleVoltage*1000)

	// Staggered power up.
	// At this point, the USB power supply should be ready to supply the
	// promised current. To be safe, wait a bit before drawing power.
	time.Sleep(500 * time.Millisecond)
	// Enable the power circuitry, in particular charge the engraving capacitors.
	DRV_ENABLE.Set(true)
	defer func() {
		// Disable the power circuitry.
		DRV_ENABLE.Set(false)
		// Wait a bit for the discharge circuit to empty the capacitors.
		time.Sleep(500 * time.Millisecond)
	}()

	// Wait a bit before enabling each stepper,
	time.Sleep(200 * time.Millisecond)
	if err := e.XAxis.Enable(true); err != nil {
		return err
	}
	defer e.XAxis.Enable(false)
	time.Sleep(200 * time.Millisecond)
	if err := e.YAxis.Enable(true); err != nil {
		return err
	}
	defer e.YAxis.Enable(false)
	// Wait for standstill tuning of the drivers.
	time.Sleep(tmc2209.StandstillTuningPeriod)

	if plan != nil {
		plan = engrave.Offset(originX, originY, plan)
		// Perform a linear interpolation of the voltage into the range of needle
		// activation durations.
		act := (needleActivationMinVoltage*time.Duration(maxVoltage-voltage) +
			needleActivationMaxVoltage*time.Duration(voltage-minVoltage)) / (maxVoltage - minVoltage)
		if err := e.execute(act, plan, quit); err != nil {
			return err
		}
	}
	moveToOrigin := engrave.Plan(slices.Values([]engrave.Command{engrave.Move(image.Pt(originX, originY))}))
	return e.execute(0, moveToOrigin, nil)
}

func (e *engraver) execute(needleActivation time.Duration, plan engrave.Plan, quit <-chan struct{}) error {
	if err := e.Dev.Engrave(needleActivation, plan, quit); err != nil {
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

func (p *Platform) Features() gui.Features {
	return 0
}

func (p *Platform) NFCDevice() (poller.Device, func()) {
	return p.nfc, p.nfc.Device.Interrupt
}

func (p *Platform) Engraver() (gui.Engraver, error) {
	return p.engraver, nil
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
		// Keep DMA buffers alive.
		runtime.KeepAlive(d)
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

type multiplexI2C struct {
	Bus chan *machine.I2C
}

func (m *multiplexI2C) Tx(addr uint16, tx, rx []byte) error {
	bus := <-m.Bus
	err := bus.Tx(addr, tx, rx)
	m.Bus <- bus
	return err
}
