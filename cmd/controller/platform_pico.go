//go:build pico

package main

import (
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

	tft    *ili9488.Device
	touch  *ft6x36.Device
	needle func(bool)

	input struct {
		// jogStep track the steps of a jog wheel turn.
		jogStep int
		// jogDir tracks the button that determines the
		// jog turn direction.
		jogDir     int
		touchPoint image.Point
		debounce   <-chan time.Time
		buttons    [1]button
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

const (
	CC1_DIR  = machine.GPIO5
	CC2_DIR  = machine.GPIO4
	CC1_DATA = machine.GPIO3
	CC2_DATA = machine.GPIO2

	TOUCH_SDA = machine.GPIO16
	TOUCH_SCL = machine.GPIO17

	LCD_RS  = machine.NoPin
	LCD_CS  = machine.NoPin
	LCD_TE  = machine.GPIO18
	LCD_DC  = machine.GPIO19
	LCD_WRX = machine.GPIO20
	LCD_DB0 = machine.GPIO21

	DRV_ENABLE = machine.GPIO13

	NEEDLE_PHASE  = machine.GPIO14
	NEEDLE_ENABLE = machine.GPIO15
	NEEDLE_SENSE  = machine.GPIO29

	STEPPER_UART = machine.GPIO6
	X_ADDR       = 0b00
	X_DIAG       = machine.GPIO12
	X_STEP       = machine.GPIO11
	X_DIR        = machine.GPIO10
	Y_ADDR       = 0b01
	Y_DIAG       = machine.GPIO9
	Y_STEP       = machine.GPIO8
	Y_DIR        = machine.GPIO7
)

var (
	needleSenseADC = machine.ADC{Pin: NEEDLE_SENSE}
	needlePWM      = machine.PWM7
	touchI2C       = machine.I2C0
)

const (
	needleActivation = 45 * time.Millisecond / 10
)

func Init() (*Platform, error) {
	// CC1_DIR.Configure(machine.PinConfig{Mode: machine.PinOutput})
	// CC2_DIR.Configure(machine.PinConfig{Mode: machine.PinOutput})
	// CC1_DATA.Configure(machine.PinConfig{Mode: machine.PinInput})
	// CC2_DATA.Configure(machine.PinConfig{Mode: machine.PinInput})
	// CC1_DIR.Low()
	// CC2_DIR.Low()
	// var pdd *usbpd.Device
	// for {
	// 	if CC1_DATA.Get() {
	// 		pdd = usbpd.New(CC1_DATA, CC1_DIR)
	// 		break
	// 	}
	// 	if CC2_DATA.Get() {
	// 		pdd = usbpd.New(CC2_DATA, CC2_DIR)
	// 		break
	// 	}
	// }
	// pdd.Run()

	NEEDLE_PHASE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	NEEDLE_ENABLE.Configure(machine.PinConfig{Mode: machine.PinOutput})
	NEEDLE_PHASE.Low()
	NEEDLE_ENABLE.Low()

	err := needlePWM.Configure(machine.PWMConfig{
		Period: uint64(mjolnir2.NeedlePeriod),
	})
	if err != nil {
		return nil, err
	}
	ch, err := needlePWM.Channel(NEEDLE_ENABLE)
	if err != nil {
		return nil, err
	}
	needlePWM.Set(ch, 0)
	needlePWMThreshold := time.Duration(needlePWM.Top()) * needleActivation / mjolnir2.NeedlePeriod
	// machine.InitADC()
	// const (
	// 	// ADC reference voltage in millivolt (mV).
	// 	ADCRefVoltage = 3_300
	// 	// Sense resistor in milliohm (mΩ).
	// 	R_IPROPI = 1200
	// 	// A_IPROPI constant from the datasheet, in μA/A.
	// 	A_IPROPI = 450
	// 	// According to the datasheet, the relationship between the
	// 	// sense voltage and the current flowing in the low side MOSFETS
	// 	// is related by the equations
	// 	//
	// 	// I_PROPI (μA) = (ILS1 + ILS2) (A) x A_IPROPI (μA/A)
	// 	// V_IPROPI (V) = I_PROPI (A) x R_IPROPI (Ω)
	// 	//
	// 	// Solving for the low sides currents:
	// 	//
	// 	// (ILS1 + ILS2) (A) = I_PROPI (μA) / A_IPROPI (μA/A) = V_IPROPI (V) / (R_IPROPI (Ω) * A_IPROPI (A/A))
	// 	//                   = V_IPROPI (V) * s_sense (A/V)
	// 	//
	// 	// Compute 1/s_sense in mV/A for integer calculations.
	// 	s_sense_inv = R_IPROPI * A_IPROPI / 1e3
	// )
	// needlePWM.Set(ch, uint32(needlePWMThreshold))
	// measurements := make([]uint16, 20000)
	// for {
	// 	before := time.Now()
	// 	const on = 12 * time.Millisecond
	// 	for time.Since(before) < on {
	// 		const currentLimit = 4500
	// 		// Sense voltage in mV.
	// 		v_ipropi := uint32(needleSenseADC.Get()) * ADCRefVoltage / 0xffff
	// 		// MOSFET current in mA.
	// 		i_ls1_ls2 := v_ipropi * 1e3 / s_sense_inv
	// 		if i_ls1_ls2 > currentLimit {
	// 			NEEDLE_ENABLE.Low()
	// 			delay.Sleep(25 * time.Microsecond)
	// 		} else {
	// 			NEEDLE_ENABLE.High()
	// 		}
	// 		// for i := range measurements {
	// 		// 	// Sense voltage in mV.
	// 		// 	v_ipropi := uint32(needleSenseADC.Get()) * ADCRefVoltage / 0xffff
	// 		// 	// MOSFET current in mA.
	// 		// 	i_ls1_ls2 := v_ipropi * 1e3 / s_sense_inv
	// 		// 	measurements[i] = uint16(i_ls1_ls2)
	// 		// 	// fmt.Printf("Vs: %d mV %d mA\n", v_ipropi, i_ls1_ls2)
	// 		// }
	// 	}
	// 	NEEDLE_ENABLE.Low()
	// 	// println("off")
	// 	delay.Sleep(20*time.Millisecond - on)
	// }
	// needlePWM.Set(ch, 0)
	// fmt.Println("Time taken:", time.Since(before))
	// for _, m := range measurements {
	// 	fmt.Print(m, " ")
	// }
	// fmt.Println()

	if err := touchI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: TOUCH_SDA, SCL: TOUCH_SCL}); err != nil {
		return nil, err
	}
	touch := ft6x36.New(touchI2C)
	touch.Configure(ft6x36.Config{})
	p := &Platform{
		touch:   touch,
		wakeups: make(chan struct{}, 1),
		needle: func(enable bool) {
			t := needlePWMThreshold
			if !enable {
				t = 0
			}
			needlePWM.Set(ch, uint32(t))
		},
	}
	inp := &p.input
	inp.wakeups = make(chan struct{}, 1)
	for i := range p.display.buffers {
		p.display.buffers[i] = make([][2]byte, ili9488.MaxDrawSize/int(unsafe.Sizeof([2]byte{})))
	}

	p.tft = ili9488.New(0, LCD_DC, LCD_CS, LCD_RS, LCD_WRX, LCD_DB0)
	if err := p.tft.Configure(ili9488.Config{}); err != nil {
		return nil, err
	}

	// Trigger reading of the initial state of input.
	inp.wakeups <- struct{}{}

	LCD_TE.Configure(machine.PinConfig{Mode: machine.PinInput})
	// vsyncs := 0
	// LCD_TE.SetInterrupt(machine.PinRising, func(machine.Pin) {
	// 	vsyncs++
	// })
	// go func() {
	// 	for {
	// 		time.Sleep(time.Second)
	// 		vs := vsyncs
	// 		vsyncs = 0
	// 		fmt.Println("vsyncs", vs)
	// 	}
	// }()
	go func() {
		for {
			select {
			case p.input.wakeups <- struct{}{}:
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	return p, nil
}

func (p *Platform) processInput() {
	const debounceTimeout = 10 * time.Millisecond

	tp, touched := p.touch.ReadTouchPoint()
	buttons := [...]bool{touched}
	timeout := time.Now().Add(debounceTimeout)
	inp := &p.input
	for i, state := range buttons {
		btn := &inp.buttons[i]
		if old := btn.state; old != state {
			if btn.debounce.IsZero() {
				btn.debounce = timeout
				if state && i == len(buttons)-1 {
					inp.touchPoint = tp
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
		case 0: // touchpad
			if btn.state {
				pt := image.Point{
					X: lcdWidth - inp.touchPoint.Y,
					Y: inp.touchPoint.X,
				}
				fmt.Println("touch", pt)
			}
			evts = append(evts, gui.ButtonEvent{Button: gui.Button3, Pressed: btn.state}.Event())
		}
	}
	p.scheduleDebounce()
	return evts
}

func (p *Platform) AppendEvents(deadline time.Time, evts []gui.Event) []gui.Event {
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
			d := time.Until(deadline)
			if p.timer == nil {
				p.timer = time.NewTimer(d)
			} else {
				p.timer.Reset(d)
			}
			select {
			case <-p.input.debounce:
				evts = p.processDebounce(evts)
			case <-p.input.wakeups:
				p.processInput()
			case <-p.wakeups:
				return evts
			case <-p.timer.C:
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
	X, err := tmc2209.New(X_ADDR, STEPPER_UART, X_DIAG, X_DIR, X_STEP)
	if err != nil {
		return nil, fmt.Errorf("pico: x-axis stepper: %w", err)
	}
	Y, err := tmc2209.New(Y_ADDR, STEPPER_UART, Y_DIAG, Y_DIR, Y_STEP)
	if err != nil {
		return nil, fmt.Errorf("pico: y-axis stepper: %w", err)
	}
	dev, err := mjolnir2.New(DRV_ENABLE, X, Y, p.needle)
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
	return p.tft.BeginFrame(r)
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
