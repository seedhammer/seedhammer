//go:build tinygo && rp

package main

import (
	"bytes"
	"device/rp"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"machine"
	"runtime"
	"slices"
	"sync/atomic"
	"time"
	"unsafe"

	"seedhammer.com/bezier"
	"seedhammer.com/bspline"
	"seedhammer.com/driver/ap33772s"
	"seedhammer.com/driver/ft6x36"
	"seedhammer.com/driver/ili9488"
	"seedhammer.com/driver/mjolnir2"
	"seedhammer.com/driver/otp"
	"seedhammer.com/driver/st25r3916"
	"seedhammer.com/driver/tmc2209"
	"seedhammer.com/engrave"
	"seedhammer.com/gui"
	"seedhammer.com/image/rgb565"
	"seedhammer.com/nfc/poller"
	"seedhammer.com/nfc/type5"
	"seedhammer.com/stepper"
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

	feats    gui.Features
	lcdDev   *ili9488.Device
	engraver *engraver
	nfc      *nfcDev
	stdin    <-chan gui.Event
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
	// signKeyHash is the SHA256 hash of the public signing key for secure boot enabled
	// devices.
	signKeyHash = "c8314536d6af61ac2e62e5991e3e4711629c54696ba8c4af08965a1d319a473b"

	// White label information.
	otpVolumeLabel  = "SHII"
	otpRedirectURL  = "https://seedhammer.com/doc/?d=SHII"
	otpRedirectName = "SeedHammer II Manual"
	otpModel        = "SeedHammer II"
	otpBoardID      = "SHII"
	otpVendor       = "SH"
	otpVersion      = "4"

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

	idleVoltage = 5_000
	// Voltage range for engraving.
	minVoltage = 20_000
	maxVoltage = 28_000

	// senseResistance is the value of the stepper driver
	// sense resistors (in mΩ).
	senseResistance = 150
	// stepperPower is the driving power of the stepper drivers,
	// in mW.
	stepperPower = 18_000
	// stallThreshold is the TMC2209 SGTHRS for triggering a
	// stall.
	stallThreshold = 110
	// minimumStallVelocity is the speed in steps/second for
	// StallGuard to be enabled.
	minimumStallVelocity = 8 * mm
	// fullStepsPerRevolution is the number of full-steps for a full
	// motor revolution.
	fullStepsPerRevolution = 200
	// mmPerRevolution is the axis movement in millimeters per revolution.
	mmPerRevolution = 8
	// mm is the number of (micro-)steps per millimeter.
	mm = fullStepsPerRevolution / mmPerRevolution * tmc2209.Microsteps
	// The coordinates of the top-left plate corner relative to the
	// homing zero.
	originX, originY = 5.0 * mm, 3.2 * mm
	// Maximum distance to travel before giving up homing.
	homingDist = 200 * mm
	// strokeWidth of engraving lines.
	strokeWidth = 0.3 * mm
	// Speeds in steps/second.
	topSpeed       = 30 * mm
	engravingSpeed = 8 * mm
	homingSpeed    = 15 * mm
	// acceleration in steps/s².
	acceleration = 250 * mm
	// jerk in steps/s³.
	jerk    = 2600 * mm
	invertX = true
	invertY = false
)

// Debug hooks.
var (
	initHook func(events chan<- gui.Event)
)

func Init() (*Platform, error) {
	if err := dataI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: DATA_SDA, SCL: DATA_SCL}); err != nil {
		return nil, fmt.Errorf("data I2C: %w", err)
	}
	mi2c := newMultiplexI2C(dataI2C)
	e, err := configEngraver(mi2c)
	if err != nil {
		return nil, err
	}
	// Home and move needle to origin.
	go e.engrave(modeHoming, nil)

	stdin := make(chan gui.Event)
	p := &Platform{
		wakeups:  make(chan struct{}, 1),
		timer:    time.NewTimer(0),
		engraver: e,
		stdin:    stdin,
	}
	for i := range p.display.buffers {
		p.display.buffers[i] = make([][2]byte, ili9488.MaxDrawSize/int(unsafe.Sizeof([2]byte{})))
	}
	sb, err := isSecureBootEnabled()
	if err == nil && sb {
		p.feats |= gui.FeatureSecureBoot
	}

	lcd, err := ili9488.New(LCD_DC, LCD_CS, LCD_RS, LCD_WRX, LCD_RDX, LCD_DB0, LCD_TE, lcdPIO)
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
	if initHook != nil {
		initHook(stdin)
	}
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
		Sense:  senseResistance,
	}
	Y := &tmc2209.Device{
		Bus:    uart,
		Addr:   Y_ADDR,
		Invert: invertY,
		Sense:  senseResistance,
	}
	axes := []*tmc2209.Device{X, Y}
	for i, axis := range axes {
		if err := axis.SetupSharedUART(); err != nil {
			return nil, fmt.Errorf("stepper %d: configuring UART: %w", i, err)
		}
	}
	for i, axis := range axes {
		if err := axis.Configure(); err != nil {
			return nil, fmt.Errorf("stepper %d: configure: %w", i, err)
		}
		if err := axis.SetStallThreshold(stallThreshold); err != nil {
			return nil, fmt.Errorf("stepper %d: stall threshold: %w", i, err)
		}
		if err := axis.SetMinimumStallVelocity(minimumStallVelocity); err != nil {
			return nil, fmt.Errorf("stepper %d: stepper stall velocity: %w", i, err)
		}
	}

	X_DIAG.Configure(machine.PinConfig{Mode: machine.PinInput})
	Y_DIAG.Configure(machine.PinConfig{Mode: machine.PinInput})
	d := &mjolnir2.Device{
		Pio:            engraverPIO,
		BasePin:        engraverBasePin,
		TicksPerSecond: engraverConf.TicksPerSecond,
	}
	// dmaSize is a compromise: larger buffers decrease interrupt
	// frequency at the cost of longer interrupt pauses because
	// buffers are filled in the interrupt handler.
	const dmaSize = 128
	if err := d.Configure(dmaSize); err != nil {
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
		case e := <-p.stdin:
			return append(evts, e)
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

var (
	engraverConf = engrave.StepperConfig{
		TicksPerSecond: topSpeed,
		Speed:          topSpeed,
		EngravingSpeed: engravingSpeed,
		Acceleration:   acceleration,
		Jerk:           jerk,
	}
	engraverParams = engrave.Params{
		StrokeWidth:   strokeWidth,
		Millimeter:    mm,
		StepperConfig: engraverConf,
	}
)

func (p *Platform) EngraverParams() engrave.Params {
	return engraverParams
}

type engraver struct {
	XAxis, YAxis     *tmc2209.Device
	PD               *ap33772s.Device
	Dev              *mjolnir2.Device
	xstalls, ystalls atomic.Uint32
	mode             engraverMode
	diag             chan axis
	ready            chan struct{}
	status           chan<- gui.EngraverStatus
	quit             <-chan struct{}
}

type engraverMode uint32

const (
	modeEngrave engraverMode = iota
	modeHoming
	modeNostall
)

func (e *engraver) Close() {}

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

type axis uint8

const (
	xaxis axis = 0b1 << iota
	yaxis
)

func (e *engraver) handleDiag(pin machine.Pin) {
	var stepPin, otherPin machine.Pin
	var a axis
	switch pin {
	case X_DIAG:
		e.xstalls.Add(1)
		stepPin = X_STEP
		otherPin = Y_STEP
		a = xaxis
	case Y_DIAG:
		e.ystalls.Add(1)
		stepPin = Y_STEP
		otherPin = X_STEP
		a = yaxis
	default:
		return
	}
	if e.mode == modeNostall {
		return
	}
	// Disconnect the step pin from the PIO program and set it low.
	stepPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	stepPin.Low()
	// And the needle pin, in case of an active engraving.
	NEEDLE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	NEEDLE.Low()
	if e.mode != modeHoming {
		// Disable both axes in case of blockage.
		otherPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
		otherPin.Low()
	}
	select {
	case e.diag <- a:
	default:
	}
}

func (e *engraver) engrave(mode engraverMode, spline bspline.Curve) error {
	<-e.ready
	defer func() {
		e.ready <- struct{}{}
	}()
	// Return to idle voltage.
	defer e.adjustVoltage(idleVoltage, idleVoltage)
	defer func() {
		// Disable the power circuitry.
		DRV_ENABLE.Set(false)
		// Wait a bit for the discharge circuit to empty the capacitors.
		time.Sleep(500 * time.Millisecond)
	}()
	// Staggered power up: ramp up the voltage while charging the engraving capacitors.
	var voltage int
	voltageRamp := []int{ /* 5_000, 9_000, 15_000, 20_000, */ maxVoltage}
	for i, maxV := range voltageRamp {
		maxV = min(maxV, maxVoltage)
		minV := idleVoltage
		if maxV == maxVoltage {
			minV = minVoltage
		}
		v, err := e.adjustVoltage(minV, maxV)
		if err != nil {
			return err
		}
		voltage = v
		time.Sleep(500 * time.Millisecond)
		if i == 0 {
			// Enable the power circuitry, in particular charge the engraving capacitors.
			DRV_ENABLE.Set(true)
			time.Sleep(500 * time.Millisecond)
		}
	}

	current := stepperPower * 1000 / voltage
	// Wait a bit before enabling each stepper.
	time.Sleep(200 * time.Millisecond)
	if err := e.XAxis.Enable(current); err != nil {
		return err
	}
	defer e.XAxis.Enable(0)
	time.Sleep(200 * time.Millisecond)
	if err := e.YAxis.Enable(current); err != nil {
		return err
	}
	defer e.YAxis.Enable(0)
	// Wait for standstill tuning of the drivers.
	time.Sleep(tmc2209.StandstillTuningPeriod)

	// Perform a linear interpolation of the voltage into the range of needle
	// activation durations.
	act := (needleActivationMinVoltage*time.Duration(maxVoltage-voltage) +
		needleActivationMaxVoltage*time.Duration(voltage-minVoltage)) / (maxVoltage - minVoltage)
	if err := e.home(act); err != nil {
		return err
	}
	if spline == nil {
		return nil
	}
	defer e.home(act)
	if err := e.execute(act, mode, spline, e.quit); err != nil {
		return err
	}
	return nil
}

func (e *engraver) home(needleActivation time.Duration) error {
	home := func(yield func(engrave.Command) bool) {
		home := bezier.Point{
			X: -homingDist,
			Y: -homingDist,
		}
		yield(engrave.Move(home))
	}
	conf := engraverConf
	conf.Speed = homingSpeed
	spline := engrave.PlanEngraving(conf, home)
	if err := e.execute(needleActivation, modeHoming, spline, nil); err != nil {
		return err
	}
	moveToOrigin := engrave.Engraving(slices.Values([]engrave.Command{
		engrave.Move(bezier.Pt(originX, originY)),
	}))
	spline = engrave.PlanEngraving(conf, moveToOrigin)
	return e.execute(needleActivation, modeEngrave, spline, nil)
}

func (e *engraver) execute(needleActivation time.Duration, mode engraverMode, spline bspline.Curve, quit <-chan struct{}) error {
	e.xstalls.Store(0)
	e.ystalls.Store(0)
	e.mode = mode
	// Leave room for both axis diag pins without blocking the interrupt handler.
	const naxes = 2
	e.diag = make(chan axis, naxes)

	for _, pin := range []machine.Pin{X_DIAG, Y_DIAG} {
		if err := pin.SetInterrupt(machine.PinRising, e.handleDiag); err != nil {
			return fmt.Errorf("mjolnir2: engrave: %w", err)
		}
		defer pin.SetInterrupt(0, nil)
	}
	if err := e.execute0(needleActivation, mode, spline, quit); err != nil {
		return err
	}
	if err := e.XAxis.Error(); err != nil {
		return fmt.Errorf("X axis: %w", err)
	}
	if err := e.YAxis.Error(); err != nil {
		return fmt.Errorf("Y axis: %w", err)
	}
	return nil
}

func (e *engraver) execute0(needleActivation time.Duration, mode engraverMode, spline bspline.Curve, quit <-chan struct{}) error {
	done := make(chan struct{})
	quitStatus := make(chan struct{})
	defer func() {
		close(quitStatus)
		<-done
	}()
	var blocked axis
	go func() {
		defer close(done)
		for {
			xload, err1 := e.XAxis.Load()
			yload, err2 := e.YAxis.Load()
			xstep, err3 := e.XAxis.StepDuration()
			ystep, err4 := e.YAxis.StepDuration()
			err := err1
			switch {
			case err2 != nil:
				err = err2
			case err3 != nil:
				err = err3
			case err4 != nil:
				err = err4
			}
			xspeed, yspeed := 0, 0
			if xstep > 0 {
				xspeed = int(time.Second / (mm * xstep))
			}
			if ystep > 0 {
				yspeed = int(time.Second / (mm * ystep))
			}
			st := gui.EngraverStatus{
				StallSpeed: minimumStallVelocity / mm,
				XSpeed:     xspeed,
				YSpeed:     yspeed,
				XLoad:      xload,
				YLoad:      yload,
				XStalls:    int(e.xstalls.Load()),
				YStalls:    int(e.ystalls.Load()),
				Error:      err,
			}
			select {
			case axis := <-e.diag:
				blocked |= axis
				if mode != modeHoming || blocked == (xaxis|yaxis) {
					return
				}
			case e.status <- st:
			case <-quit:
				return
			case <-quitStatus:
				return
			}
		}
	}()
	needleAct := uint(needleActivation * time.Duration(engraverConf.TicksPerSecond) / time.Second)
	needlePeriod := uint(needlePeriod * time.Duration(engraverConf.TicksPerSecond) / time.Second)
	d := stepper.Engrave(e.Dev, done, spline)
	if err := e.Dev.Enable(d.HandleTransferCompleted, needleAct, needlePeriod); err != nil {
		return err
	}
	defer e.Dev.Disable()
	d.Run()
	if mode == modeHoming {
		if blocked != (xaxis | yaxis) {
			return errors.New("mjolnir2: homing timed out")
		}
		return nil
	}
	switch {
	case blocked&xaxis != 0:
		return errors.New("mjolnir2: x-axis blocked")
	case blocked&yaxis != 0:
		return errors.New("mjolnir2: y-axis blocked")
	default:
		return nil
	}
}

func (p *Platform) LockBoot() error {
	if err := writeOTPValues(); err != nil {
		return err
	}
	if err := otp.EnableSecureBoot(); err != nil {
		return err
	}
	return nil
}

func (p *Platform) Features() gui.Features {
	return p.feats
}

func (p *Platform) NFCReader() io.Reader {
	return poller.New(p.nfc)
}

func (p *Platform) Engrave(stall bool, spline bspline.Curve, status chan<- gui.EngraverStatus, quit <-chan struct{}) error {
	p.engraver.status = status
	p.engraver.quit = quit
	mode := modeEngrave
	if !stall {
		mode = modeNostall
	}
	return p.engraver.engrave(mode, spline)
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
	bus chan *machine.I2C
}

func newMultiplexI2C(bus *machine.I2C) *multiplexI2C {
	busCh := make(chan *machine.I2C, 1)
	busCh <- bus
	return &multiplexI2C{
		bus: busCh,
	}
}

func (m *multiplexI2C) Tx(addr uint16, tx, rx []byte) error {
	bus := <-m.bus
	err := bus.Tx(addr, tx, rx)
	m.bus <- bus
	return err
}

// writeOTPValues write the white label information and our signing
// key to OTP memory.
func writeOTPValues() error {
	khash, err := hex.DecodeString(signKeyHash)
	if err != nil {
		panic(err)
	}
	if err := otp.WriteWhiteLabelAddr(otp.FirstUserRow); err != nil {
		fmt.Printf("label addr err: %v", err)
	}
	infos := []struct {
		Index uint8
		Value string
	}{
		{otp.INDEX_VOLUME_LABEL_STRDEF, otpVolumeLabel},
		{otp.INDEX_INDEX_HTM_REDIRECT_URL_STRDEF, otpRedirectURL},
		{otp.INDEX_INDEX_HTM_REDIRECT_NAME_STRDEF, otpRedirectName},
		{otp.INDEX_INFO_UF2_TXT_MODEL_STRDEF, otpModel},
		{otp.INDEX_INFO_UF2_TXT_BOARD_ID_STRDEF, otpBoardID},
		{otp.INDEX_SCSI_INQUIRY_PRODUCT_STRDEF, otpBoardID},
		{otp.INDEX_SCSI_INQUIRY_VENDOR_STRDEF, otpVendor},
		{otp.INDEX_SCSI_INQUIRY_VERSION_STRDEF, otpVersion},
	}
	for _, inf := range infos {
		if err := otp.WriteWhiteLabelString(inf.Index, inf.Value); err != nil {
			return err
		}
	}
	_, err = otp.AddBootKey(khash)
	return err
}

// isSecureBootEnabled reports whether secure boot is enabled and that the
// signing key is the only valid key.
func isSecureBootEnabled() (bool, error) {
	khash, err := hex.DecodeString(signKeyHash)
	if err != nil {
		panic(err)
	}
	enabled, err := otp.IsSecureBootEnabled()
	if err != nil {
		return false, err
	}
	existingKey := make([]byte, 32)
	nvalid := 0
	ours := false
	for slot := range otp.NumBootKeySlots {
		v, err := otp.IsBootKeyValid(slot)
		if err != nil {
			return false, err
		}
		if !v {
			continue
		}
		nvalid++
		if err := otp.ReadBootKey(existingKey, slot); err != nil {
			return false, err
		}
		ours = ours || bytes.Equal(existingKey, khash)
	}
	return enabled && ours && nvalid == 1, nil
}
