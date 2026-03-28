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
	"time"
	"unsafe"

	"seedhammer.com/driver/ap33772s"
	"seedhammer.com/driver/ft6x36"
	"seedhammer.com/driver/ili9488"
	"seedhammer.com/driver/otp"
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

	TOUCH_INT = machine.GPIO13
	TOUCH_SDA = machine.GPIO14
	TOUCH_SCL = machine.GPIO15

	LCD_RS  = machine.NoPin
	LCD_CS  = machine.NoPin
	LCD_TE  = machine.GPIO12
	LCD_DC  = machine.GPIO16
	LCD_WRX = machine.GPIO17
	LCD_DB0 = machine.GPIO18

	DRV_ENABLE = machine.GPIO10

	STEPPER_UART = machine.GPIO9
	X_ADDR       = 0b00
	Y_ADDR       = 0b01
	X_DIAG       = machine.GPIO8
	Y_DIAG       = machine.GPIO7
	// Before PCB v1.5.0 GPIO11 served as LCD_RDX.
	// To maintain compatibility with older PCBS, this
	// pin must be pulled high.
	S_DIAG = machine.GPIO11

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

// Debug pins. Valid on some boards only.
const (
	// Firmware controlled solenoid current limit in
	// amperes (A).
	// Disable it by setting it to 0 on production
	// boards.
	Ichop = 0
	// The sense resistor in mΩ.
	Rsense = 5
	S_VREF = machine.GPIO30

	// S_SENSE is the current sense output of the DRV8701
	// driver.
	S_SENSE = machine.NoPin

	// Pulse length ADC input pin.
	P_ADC = machine.NoPin
)

// Debug variables.
var (
	// The PWM corresponding to S_VREF.
	pwmS_VREF = machine.PWM7
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
	needlePeriod = 25 * time.Millisecond
	// The duration of a needle cycle turned on.
	needleActivationMinVoltage = 5 * time.Millisecond
	needleActivationMaxVoltage = 4 * time.Millisecond

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
	usbpd := ap33772s.New(mi2c)
	if err := usbpd.Configure(); err != nil {
		return nil, err
	}
	stdin := make(chan gui.Event)
	p := &Platform{
		wakeups: make(chan struct{}, 1),
		timer:   time.NewTimer(0),
		stdin:   stdin,
	}
	// Set up engraver pins regardless of whether the
	// voltage is sufficient.
	configEngraverPins()
	if voltage, err := p.monitorPowerSupply(usbpd); err == nil {
		e, err := configEngraver(voltage)
		if err != nil {
			return nil, err
		}
		p.engraver = e
		// Home and move needle to origin.
		home := func() error {
			e, err := p.Engraver(true)
			if err != nil {
				return err
			}
			return e.Close()
		}
		go home()
	}

	for i := range p.display.buffers {
		p.display.buffers[i] = make([][2]byte, ili9488.MaxDrawSize/int(unsafe.Sizeof([2]byte{})))
	}
	sb, err := isSecureBootEnabled()
	if err == nil && sb {
		p.feats |= gui.FeatureSecureBoot
	}

	lcd, err := ili9488.New(LCD_DC, LCD_CS, LCD_RS, LCD_WRX, machine.NoPin, LCD_DB0, LCD_TE, lcdPIO)
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
	// Immediately wake up, but allow waiting goroutines
	// to run first.
	runtime.Gosched()
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

func (p *Platform) monitorPowerSupply(d *ap33772s.Device) (int, error) {
	USBPD_INT.Configure(machine.PinConfig{Mode: machine.PinInputPullup})

	voltage, err := adjustSupplyVoltage(d, minVoltage, maxVoltage)
	if err != nil {
		// Give up if the power supply doesn't initially offer higher voltages.
		return 0, err
	}

	interrupts := make(chan struct{}, 1)
	USBPD_INT.SetInterrupt(machine.PinFalling, func(machine.Pin) {
		select {
		case interrupts <- struct{}{}:
		default:
		}
	})

	// Monitor the supply and ask for higher voltage whenever a power cycle occurs.
	go func() {
		for {
			<-interrupts
			st, err := d.ReadStatus()
			if err != nil || (st&ap33772s.NEWPDO) == 0 {
				continue
			}
			if _, err := adjustSupplyVoltage(d, voltage, voltage); err != nil {
				continue
			}
		}
	}()
	return voltage, nil
}

func adjustSupplyVoltage(d *ap33772s.Device, minmV, maxmV int) (int, error) {
	const retries = 3
	for range retries {
		mV, err := d.AdjustVoltage(minmV, maxmV)
		if err != nil {
			return 0, err
		}
		// Allow the new contract to settle.
		time.Sleep(100 * time.Millisecond)
		gotmV, err := d.Voltage()
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

type defers struct {
	funcs []func() error
}

func (d *defers) Add(f func() error) {
	d.funcs = append(d.funcs, f)
}

func (d *defers) Call() error {
	var derr error
	for i := range d.funcs {
		f := d.funcs[len(d.funcs)-1-i]
		if err := f(); derr == nil {
			derr = err
		}
	}
	d.funcs = nil
	return derr
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

func (p *Platform) HardwareVersion() string {
	return "v1." + boardVersion()
}

func (p *Platform) Features() gui.Features {
	return p.feats
}

func (p *Platform) NFCReader() io.Reader {
	return poller.New(p.nfc)
}

func (p *Platform) Engraver(stall bool) (gui.Engraver, error) {
	e := p.engraver
	if e == nil {
		return nil, errors.New("engraver unavailable")
	}
	if err := e.Open(); err != nil {
		return nil, err
	}
	mode := modeEngrave
	if !stall {
		mode = modeNostall
	}
	return &homingEngraver{mode: mode, e: e}, nil
}

type homingEngraver struct {
	e        *engraver
	mode     engraveMode
	homed    bool
	writeErr bool
}

func (e *homingEngraver) Write(steps []uint32) (int, error) {
	if !e.homed {
		if err := e.e.home(); err != nil {
			return 0, err
		}
		e.homed = true
		e.e.SwitchMode(e.mode)
	}
	completed, err := e.e.Write(steps)
	e.writeErr = e.writeErr || err != nil
	return completed, err
}

func (e *homingEngraver) Close() (cerr error) {
	d := e.e
	e.e = nil
	defer func() {
		d.Dev.Reset()
		if err := d.Close(); cerr == nil {
			cerr = err
		}
	}()
	if e.writeErr {
		return nil
	}
	if err := d.Dev.Flush(); err != nil {
		return err
	}
	return d.home()
}

func (e *homingEngraver) Stats() gui.EngraverStats {
	return e.e.EngraverStats()
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
		{otp.INDEX_SCSI_INQUIRY_VERSION_STRDEF, boardVersion()},
	}
	for _, inf := range infos {
		if err := otp.WriteWhiteLabelString(inf.Index, inf.Value); err != nil {
			return err
		}
	}
	_, err = otp.AddBootKey(khash)
	return err
}

func boardVersion() string {
	rev, err := otp.ReadWhiteLabelString(otp.INDEX_SCSI_INQUIRY_VERSION_STRDEF)
	if err == nil && rev != "" {
		return rev
	}
	// Detect version from the package.
	switch rp.SYSINFO.GetPACKAGE_SEL() {
	case 1: // RP235xA
		return "4"
	default: // RP235xB
		return "5"
	}
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
