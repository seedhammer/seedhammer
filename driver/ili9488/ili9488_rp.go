//go:build tinygo && rp

// package ili9488 implements a DMA TinyGo driver for ili9488 TFT devices.
package ili9488

import (
	"device/rp"
	"image"
	"machine"
	"runtime/volatile"
	"time"
	"unsafe"
)

type Device struct {
	pio                       *rp.PIO0_Type
	dc, cs, rst, wrx, db0, te machine.Pin
	window                    image.Rectangle
	cmdBuf                    [20]byte
	firstFrame                bool
	firstDraw                 bool
}

type Config struct {
}

func New(dmaChannel uint8, dc, cs, rst, wrx, db0, te machine.Pin, pio *rp.PIO0_Type) *Device {
	// Hard code to DMA channel 0 for convenience.
	if dmaChannel != 0 {
		panic("only DMA channel 0 i supported")
	}
	return &Device{
		pio: pio,
		dc:  dc,
		cs:  cs,
		rst: rst,
		wrx: wrx,
		db0: db0,
		te:  te,
	}
}

const MaxDrawSize = 4096

const pioStateMachine = 0

func (d *Device) Configure(c Config) error {
	d.firstFrame = true

	for _, p := range []machine.Pin{d.cs, d.rst, d.dc} {
		p.Configure(machine.PinConfig{Mode: machine.PinOutput})
	}
	mode := machine.PinPIO0
	switch d.pio {
	case rp.PIO0:
	case rp.PIO1:
		mode = machine.PinPIO1
	default:
		panic("unknown PIO")
	}
	d.wrx.Configure(machine.PinConfig{Mode: mode})
	d.te.Configure(machine.PinConfig{Mode: mode})
	for i := 0; i < 8; i++ {
		(d.db0 + machine.Pin(i)).Configure(machine.PinConfig{Mode: mode})
	}
	for _, p := range []machine.Pin{d.cs, d.rst, d.dc} {
		p.High()
	}

	instMem := []*volatile.Register32{
		&d.pio.INSTR_MEM0,
		&d.pio.INSTR_MEM1,
		&d.pio.INSTR_MEM2,
		&d.pio.INSTR_MEM3,
		&d.pio.INSTR_MEM4,
		&d.pio.INSTR_MEM5,
		&d.pio.INSTR_MEM6,
		&d.pio.INSTR_MEM7,
		&d.pio.INSTR_MEM8,
		&d.pio.INSTR_MEM9,
		&d.pio.INSTR_MEM10,
		&d.pio.INSTR_MEM11,
		&d.pio.INSTR_MEM12,
		&d.pio.INSTR_MEM13,
		&d.pio.INSTR_MEM14,
		&d.pio.INSTR_MEM15,
		&d.pio.INSTR_MEM16,
		&d.pio.INSTR_MEM17,
		&d.pio.INSTR_MEM18,
		&d.pio.INSTR_MEM19,
		&d.pio.INSTR_MEM20,
		&d.pio.INSTR_MEM21,
		&d.pio.INSTR_MEM22,
		&d.pio.INSTR_MEM23,
		&d.pio.INSTR_MEM24,
		&d.pio.INSTR_MEM25,
		&d.pio.INSTR_MEM26,
		&d.pio.INSTR_MEM27,
		&d.pio.INSTR_MEM28,
		&d.pio.INSTR_MEM29,
		&d.pio.INSTR_MEM30,
		&d.pio.INSTR_MEM31,
	}
	instMem[0].Set(0x90e0)
	instMem[1].Set(0x6008)
	d.pio.SetSM0_PINCTRL_SIDESET_BASE(uint32(d.wrx))
	d.pio.SetSM0_PINCTRL_SIDESET_COUNT(1)
	d.pio.SetSM0_PINCTRL_OUT_BASE(uint32(d.db0))
	d.pio.SetSM0_PINCTRL_OUT_COUNT(8)
	d.pio.SetSM0_PINCTRL_IN_BASE(uint32(d.te))
	d.pio.SetSM0_EXECCTRL_WRAP_BOTTOM(0)
	d.pio.SetSM0_EXECCTRL_WRAP_TOP(1)
	// The minimum write cycle time.
	const writeCycle = 30 * time.Nanosecond
	// The number of PIO cycles per write.
	const cyclesPerWrite = 2
	// The target PIO cycle time.
	const pioCycle = writeCycle / cyclesPerWrite
	// The target PIO clock speed.
	const pioClock = uint64(time.Second / pioCycle)
	const fracBits = 8
	// Compute fractional clock divisor, rounded up.
	clkDiv := (uint64(machine.CPUFrequency())*(1<<fracBits) + pioClock - 1) / pioClock
	d.pio.SM0_CLKDIV.Set(uint32(clkDiv) << 8)
	d.pio.SetSM0_SHIFTCTRL_FJOIN_TX(0b1)
	d.pio.SetSM0_SHIFTCTRL_PULL_THRESH(8)

	// Start state machine.
	d.pio.SetCTRL_SM_ENABLE(0b1 << pioStateMachine)

	// Set up pindirs.
	d.setPindirs(d.db0, 8, 0b1)
	d.setPindirs(d.wrx, 1, 0b1)

	d.cs.Low()
	defer d.cs.High()

	if d.rst != machine.NoPin {
		// Reset LCD.
		d.rst.High()
		time.Sleep(50 * time.Millisecond)
		d.rst.Low()
		time.Sleep(50 * time.Millisecond)
		d.rst.High()
		time.Sleep(50 * time.Millisecond)
	} else {
		d.sendCommand(SWRESET)
		time.Sleep(150 * time.Millisecond)
	}

	initCmd := []byte{
		GMCTRP1, 15, 0x00, 0x08, 0x0C, 0x02, 0x0E, 0x04, 0x30, 0x45, 0x47, 0x04, 0x0C, 0x0A, 0x2E, 0x34, 0x0F,
		GMCTRN1, 15, 0x00, 0x11, 0x0D, 0x01, 0x0F, 0x05, 0x39, 0x36, 0x51, 0x06, 0x0F, 0x0D, 0x33, 0x37, 0x0F,
		PWCTR1, 2, 0x0f, 0x0f,
		PWCTR2, 1, 0x41,
		PWCTR3, 1, 0x22,
		VMCTR1, 3, 0x00, 0x53, 0x80,
		MADCTL, 1, MADCTL_MV | MADCTL_BGR,
		TEON, 1, 0x00,
		PIXFMT, 1, 0x55,
		IFMODE, 1, 0x00,
		FRMCTR1, 1, 0xA0,
		INVCTR, 1, 0x02,
		INVON, 0,
		DFUNCTR, 2, 0x02, 0x02,
		SETIMAGE, 1, 0x00,
		ADJCTR3, 4, 0xA9, 0x51, 0x2C, 0x82,
		SLPOUT, 0x80, //Sleep out
		0x00,
	}
	for i, c := 0, len(initCmd); i < c; {
		cmd := initCmd[i]
		if cmd == 0x00 {
			break
		}
		x := initCmd[i+1]
		numArgs := int(x & 0x7F)
		d.sendCommand(cmd, initCmd[i+2:i+2+numArgs]...)
		if x&0x80 > 0 {
			time.Sleep(150 * time.Millisecond)
		}
		i += numArgs + 2
	}
	return nil
}

func (d *Device) setPindirs(base machine.Pin, npins int, dir uint8) {
	for npins > 0 {
		n := min(5, npins)
		d.pio.SetSM0_PINCTRL_SET_BASE(uint32(base))
		d.pio.SetSM0_PINCTRL_SET_COUNT(uint32(n))
		// set pindir 0b11111 side 1.
		const setPinDirInst = 0b111_10000_100_11111
		d.pio.SM0_INSTR.Set(setPinDirInst)
		npins -= n
		base += machine.Pin(n)
	}
}

func (d *Device) sendCommand(cmd byte, data ...byte) {
	d.flushFIFO()
	d.dc.Low()
	d.sendByte(cmd)
	d.flushFIFO()
	d.dc.High()
	for _, b := range data {
		d.sendByte(b)
	}
}

func (d *Device) sendByte(data byte) {
	for d.pio.GetFSTAT_TXFULL()&(0b1<<pioStateMachine) != 0 {
	}
	d.pio.TXF0.Set(uint32(data))
}

func (d *Device) BeginFrame(sr image.Rectangle) error {
	if sr.Empty() {
		return nil
	}
	d.flushFIFO()
	d.cs.Low()
	d.setWindow(sr)
	d.flushFIFO()
	d.setPullThreshold(16)
	d.firstDraw = true
	return nil
}

func (d *Device) setPullThreshold(thres int) {
	// Reset output shift counter before changing the threshold.
	d.pio.SetCTRL_SM_RESTART(0b1 << pioStateMachine)
	d.pio.SetSM0_SHIFTCTRL_PULL_THRESH(uint32(thres))
}

func (d *Device) flushFIFO() {
	// Clear stall flag.
	d.pio.SetFDEBUG_TXSTALL(0b1 << pioStateMachine)
	for d.pio.GetFDEBUG_TXSTALL()&(0b1<<pioStateMachine) == 0 {
	}
}

func (d *Device) EndFrame() {
	d.waitDMA()
	d.flushFIFO()
	d.cs.High()
	d.setPullThreshold(8)
	if d.firstFrame {
		d.firstFrame = false
		d.sendCommand(DISPON)
	}
}

func (d *Device) Draw(buf [][2]byte) {
	if d.firstDraw {
		// Wait for V-SYNC.
		// wait 1 pin 0 side 1.
		const waitInst = 0b001_10000_1_01_00000
		d.pio.SM0_INSTR.Set(waitInst)
		d.firstDraw = false
	} else {
		d.waitDMA()
	}

	dma := rp.DMA
	dma.CH0_READ_ADDR.Set(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(buf)))))
	dma.CH0_WRITE_ADDR.Set(uint32(uintptr(unsafe.Pointer(&d.pio.TXF0))))
	dma.CH0_TRANS_COUNT.Set(uint32(len(buf)))
	const (
		DREQ_PIO0_TX0 = 0
		DREQ_PIO1_TX0 = 8
	)
	dreq := uint32(DREQ_PIO0_TX0)
	switch d.pio {
	case rp.PIO0:
	case rp.PIO1:
		dreq = DREQ_PIO1_TX0
	}
	dma.CH0_CTRL_TRIG.Set(
		// Increment read address on each transfer.
		rp.DMA_CH0_CTRL_TRIG_INCR_READ |
			// Transfer size is 16 bits.
			rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_SIZE_HALFWORD<<rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_Pos |
			// Pace transfers by the PIO TX FIFO.
			dreq<<rp.DMA_CH0_CTRL_TRIG_TREQ_SEL_Pos |
			// Start transfer.
			rp.DMA_CH0_CTRL_TRIG_EN,
	)
}

func (d *Device) waitDMA() {
	dma := rp.DMA
	// Wait for DMA completion.
	for dma.GetCH0_CTRL_TRIG_BUSY() != 0 {
	}
}

func (d *Device) setWindow(r image.Rectangle) {
	if d.window == r {
		return
	}
	d.window = r

	d.sendCommand(CASET, byte(r.Min.X>>8), byte(r.Min.X), byte((r.Max.X-1)>>8), byte(r.Max.X-1))
	d.sendCommand(PASET, byte(r.Min.Y>>8), byte(r.Min.Y), byte((r.Max.Y-1)>>8), byte(r.Max.Y-1))
	d.sendCommand(RAMWR)
}

const (
	// register constants based on source:
	// https://github.com/adafruit/Adafruit_ILI9341/blob/master/Adafruit_ILI9341.h

	NOP     = 0x00 ///< No-op register
	SWRESET = 0x01 ///< Software reset register
	RDDID   = 0x04 ///< Read display identification information
	RDDST   = 0x09 ///< Read Display Status

	SLPIN  = 0x10 ///< Enter Sleep Mode
	SLPOUT = 0x11 ///< Sleep Out
	PTLON  = 0x12 ///< Partial Mode ON
	NORON  = 0x13 ///< Normal Display Mode ON

	RDMODE     = 0x0A ///< Read Display Power Mode
	RDMADCTL   = 0x0B ///< Read Display MADCTL
	RDPIXFMT   = 0x0C ///< Read Display Pixel Format
	RDIMGFMT   = 0x0D ///< Read Display Image Format
	RDSELFDIAG = 0x0F ///< Read Display Self-Diagnostic Result

	INVOFF   = 0x20 ///< Display Inversion OFF
	INVON    = 0x21 ///< Display Inversion ON
	GAMMASET = 0x26 ///< Gamma Set
	DISPOFF  = 0x28 ///< Display OFF
	DISPON   = 0x29 ///< Display ON

	CASET = 0x2A ///< Column Address Set
	PASET = 0x2B ///< Page Address Set
	RAMWR = 0x2C ///< Memory Write
	RAMRD = 0x2E ///< Memory Read

	PTLAR    = 0x30 ///< Partial Area
	VSCRDEF  = 0x33 ///< Vertical Scrolling Definition
	TEOFF    = 0x34 ///< TEOFF: Tearing Effect Line OFF
	TEON     = 0x35 ///< TEON: Tearing Effect Line ON
	MADCTL   = 0x36 ///< Memory Access Control
	VSCRSADD = 0x37 ///< Vertical Scrolling Start Address
	PIXFMT   = 0x3A ///< COLMOD: Pixel Format Set

	IFMODE  = 0xB0
	FRMCTR1 = 0xB1 ///< Frame Rate Control (In Normal Mode/Full Colors)
	FRMCTR2 = 0xB2 ///< Frame Rate Control (In Idle Mode/8 colors)
	FRMCTR3 = 0xB3 ///< Frame Rate control (In Partial Mode/Full Colors)
	INVCTR  = 0xB4 ///< Display Inversion Control
	DFUNCTR = 0xB6 ///< Display Function Control

	PWCTR1 = 0xC0 ///< Power Control 1
	PWCTR2 = 0xC1 ///< Power Control 2
	PWCTR3 = 0xC2 ///< Power Control 3
	PWCTR4 = 0xC3 ///< Power Control 4
	PWCTR5 = 0xC4 ///< Power Control 5
	VMCTR1 = 0xC5 ///< VCOM Control 1
	VMCTR2 = 0xC7 ///< VCOM Control 2

	RDID1 = 0xDA ///< Read ID 1
	RDID2 = 0xDB ///< Read ID 2
	RDID3 = 0xDC ///< Read ID 3
	RDID4 = 0xDD ///< Read ID 4

	GMCTRP1 = 0xE0 ///< Positive Gamma Correction
	GMCTRN1 = 0xE1 ///< Negative Gamma Correction

	SETIMAGE = 0xE9
	ADJCTR3  = 0xF7

	MADCTL_RGB = 0x00 ///< Red-Green-Blue pixel order
)

const (
	MADCTL_MY  = 1 << 7
	MADCTL_MX  = 1 << 6
	MADCTL_MV  = 1 << 5
	MADCTL_ML  = 1 << 4
	MADCTL_BGR = 1 << 3
	MADCTL_MH  = 1 << 2
)
