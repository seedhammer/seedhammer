//go:build tinygo && rp

// package ili9488 implements a DMA TinyGo driver for ili9488 TFT devices.
package ili9488

import (
	"device/rp"
	"image"
	"machine"
	"runtime"
	"time"
	"unsafe"

	"seedhammer.com/driver/dma"
	"seedhammer.com/driver/pio"
)

type Device struct {
	pio                            *rp.PIO0_Type
	channel                        dma.ChannelID
	dc, cs, rst, wrx, rdx, db0, te machine.Pin
	window                         image.Rectangle
	cmdBuf                         [20]byte
	firstFrame                     bool
	firstDraw                      bool
}

type Config struct {
}

func New(dc, cs, rst, wrx, rdx, db0, te machine.Pin, pio *rp.PIO0_Type) (*Device, error) {
	ch, err := dma.ReserveChannel()
	if err != nil {
		return nil, err
	}
	return &Device{
		channel: ch,
		pio:     pio,
		dc:      dc,
		cs:      cs,
		rst:     rst,
		wrx:     wrx,
		rdx:     rdx,
		db0:     db0,
		te:      te,
	}, nil
}

const MaxDrawSize = 4096

const pioStateMachine = 0

func (d *Device) Configure(c Config) error {
	d.firstFrame = true

	for _, p := range []machine.Pin{d.cs, d.rst, d.dc, d.rdx} {
		p.Configure(machine.PinConfig{Mode: machine.PinOutput})
		p.High()
	}

	// The minimum write cycle, from the ILI9488 datasheet.
	const minWriteCycle = 30 * time.Nanosecond

	const maxWriteSpeed = uint32(time.Second / minWriteCycle)
	const pioFreq = maxWriteSpeed * pioCyclesPerWrite

	progOff := uint8(0)
	conf := ili9488ProgramDefaultConfig(progOff)
	conf.SidesetBase = uint8(d.wrx)
	conf.OutBase = uint8(d.db0)
	conf.OutCount = 8
	conf.InBase = uint8(d.te)
	conf.InCount = 1
	conf.FIFOMode = pio.FIFOJoinTX
	conf.PullThreshold = 8
	conf.Freq = pioFreq
	pio.Program(d.pio, progOff, ili9488Instructions)
	pio.Configure(d.pio, pioStateMachine, conf.Build())

	// Start state machine.
	pio.Enable(d.pio, 0b1<<pioStateMachine)

	// Set up pins.
	d.te.Configure(machine.PinConfig{Mode: machine.PinInput})
	d.configurePIOPins()
	pio.Pindirs(d.pio, pioStateMachine, d.db0, 8, machine.PinOutput)
	pio.Pindirs(d.pio, pioStateMachine, d.wrx, 8, machine.PinOutput)

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

func (d *Device) ReadSerial() [9]byte {
	var serial [9]byte
	d.read(RDDID, serial[:3])
	d.read(RDID1, serial[3:4])
	d.read(RDID2, serial[4:5])
	d.read(RDID3, serial[5:6])
	d.read(RDID4, serial[6:9])
	return serial
}

func (d *Device) read(cmd byte, buf []byte) {
	d.sendCommand(cmd)

	// Configure pins for input.
	pin := d.db0
	for range 8 {
		pin.Configure(machine.PinConfig{Mode: machine.PinInput})
		pin++
	}
	defer d.configurePIOPins()
	// Skip garbage first byte.
	d.readByte()
	for i := range buf {
		buf[i] = d.readByte()
	}
}

func (d *Device) readByte() byte {
	d.rdx.Low()
	time.Sleep(trdlfm)
	defer time.Sleep(trdhfm)
	defer d.rdx.High()

	var b byte
	pin := d.db0
	for range 8 {
		b >>= 1
		if pin.Get() {
			b |= 0x80
		}
		pin++
	}
	return b
}

func (d *Device) configurePIOPins() {
	pio.ConfigurePins(d.pio, pioStateMachine, d.db0, 8)
	pio.ConfigurePins(d.pio, pioStateMachine, d.wrx, 1)
}

func (d *Device) sendByte(data byte) {
	for d.pio.GetFSTAT_TXFULL()&(0b1<<pioStateMachine) != 0 {
		runtime.Gosched()
	}
	pio.Tx(d.pio, pioStateMachine).Set(uint32(data))
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
	pio.Restart(d.pio, 0b1<<pioStateMachine)
	pio.PullThreshold(d.pio, pioStateMachine, thres)
}

func (d *Device) flushFIFO() {
	pio.WaitTxStall(d.pio, 0b1<<pioStateMachine)
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
		d.firstDraw = false
		// Wait for V-SYNC if this is the first draw of the frame
		// and the LCD is on (!firstFrame).
		if !d.firstFrame {
			// Wait for V-sync.
			pio.Instr(d.pio, pioStateMachine).Set(waitForVSYNCInst)
		}
	} else {
		// Wait for previous draw to complete.
		d.waitDMA()
	}

	ch := dma.ChannelAt(d.channel)
	ch.READ_ADDR.Set(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(buf)))))
	ch.WRITE_ADDR.Set(uint32(uintptr(unsafe.Pointer(pio.Tx(d.pio, pioStateMachine)))))
	ch.TRANS_COUNT.Set(uint32(len(buf)))
	ch.CTRL_TRIG.Set(
		// Increment read address on each transfer.
		rp.DMA_CH0_CTRL_TRIG_INCR_READ |
			// Pixels are big endian, 16 bits.
			rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_SIZE_HALFWORD<<rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_Pos |
			0b1<<rp.DMA_CH0_CTRL_TRIG_BSWAP_Pos |
			uint32(d.channel)<<rp.DMA_CH0_CTRL_TRIG_CHAIN_TO_Pos |
			// Pace transfers by the PIO TX FIFO.
			pio.DreqTx(d.pio, pioStateMachine)<<rp.DMA_CH0_CTRL_TRIG_TREQ_SEL_Pos |
			// Start transfer.
			rp.DMA_CH0_CTRL_TRIG_EN,
	)
}

func (d *Device) waitDMA() {
	// Wait for DMA completion.
	ch := dma.ChannelAt(d.channel)
	for ch.CTRL_TRIG.Get()&rp.DMA_CH0_CTRL_TRIG_BUSY_Msk != 0 {
		runtime.Gosched()
	}
}

func (d *Device) setWindow(r image.Rectangle) {
	if d.window != r {
		d.window = r
		d.sendCommand(CASET, byte(r.Min.X>>8), byte(r.Min.X), byte((r.Max.X-1)>>8), byte(r.Max.X-1))
		d.sendCommand(PASET, byte(r.Min.Y>>8), byte(r.Min.Y), byte((r.Max.Y-1)>>8), byte(r.Max.Y-1))
	}
	d.sendCommand(RAMWR)
}

const (
	// register constants based on source:
	// https://github.com/adafruit/Adafruit_ILI9341/blob/master/Adafruit_ILI9341.h

	NOP     = 0x00 // No-op register
	SWRESET = 0x01 // Software reset register
	RDDID   = 0x04 // Read display identification information
	RDDST   = 0x09 // Read Display Status

	SLPIN  = 0x10 // Enter Sleep Mode
	SLPOUT = 0x11 // Sleep Out
	PTLON  = 0x12 // Partial Mode ON
	NORON  = 0x13 // Normal Display Mode ON

	RDMODE     = 0x0A // Read Display Power Mode
	RDMADCTL   = 0x0B // Read Display MADCTL
	RDPIXFMT   = 0x0C // Read Display Pixel Format
	RDIMGFMT   = 0x0D // Read Display Image Format
	RDSELFDIAG = 0x0F // Read Display Self-Diagnostic Result

	INVOFF   = 0x20 // Display Inversion OFF
	INVON    = 0x21 // Display Inversion ON
	GAMMASET = 0x26 // Gamma Set
	DISPOFF  = 0x28 // Display OFF
	DISPON   = 0x29 // Display ON

	CASET = 0x2A // Column Address Set
	PASET = 0x2B // Page Address Set
	RAMWR = 0x2C // Memory Write
	RAMRD = 0x2E // Memory Read

	PTLAR    = 0x30 // Partial Area
	VSCRDEF  = 0x33 // Vertical Scrolling Definition
	TEOFF    = 0x34 // TEOFF: Tearing Effect Line OFF
	TEON     = 0x35 // TEON: Tearing Effect Line ON
	MADCTL   = 0x36 // Memory Access Control
	VSCRSADD = 0x37 // Vertical Scrolling Start Address
	PIXFMT   = 0x3A // COLMOD: Pixel Format Set

	IFMODE  = 0xB0
	FRMCTR1 = 0xB1 // Frame Rate Control (In Normal Mode/Full Colors)
	FRMCTR2 = 0xB2 // Frame Rate Control (In Idle Mode/8 colors)
	FRMCTR3 = 0xB3 // Frame Rate control (In Partial Mode/Full Colors)
	INVCTR  = 0xB4 // Display Inversion Control
	DFUNCTR = 0xB6 // Display Function Control

	PWCTR1 = 0xC0 // Power Control 1
	PWCTR2 = 0xC1 // Power Control 2
	PWCTR3 = 0xC2 // Power Control 3
	PWCTR4 = 0xC3 // Power Control 4
	PWCTR5 = 0xC4 // Power Control 5
	VMCTR1 = 0xC5 // VCOM Control 1
	VMCTR2 = 0xC7 // VCOM Control 2

	RDID1 = 0xDA // Read ID 1
	RDID2 = 0xDB // Read ID 2
	RDID3 = 0xDC // Read ID 3
	RDID4 = 0xDD // Read ID 4

	GMCTRP1 = 0xE0 // Positive Gamma Correction
	GMCTRN1 = 0xE1 // Negative Gamma Correction

	SETIMAGE = 0xE9
	ADJCTR3  = 0xF7

	MADCTL_RGB = 0x00 // Red-Green-Blue pixel order
)

const (
	MADCTL_MY  = 1 << 7
	MADCTL_MX  = 1 << 6
	MADCTL_MV  = 1 << 5
	MADCTL_ML  = 1 << 4
	MADCTL_BGR = 1 << 3
	MADCTL_MH  = 1 << 2
)

// Datasheet section 17.4.1. "DBI Type B Timing Characteristics".
const (
	// Read access time.
	tratfm = 340 * time.Nanosecond
	// Read output disable time.
	trod = 80 * time.Nanosecond
	// Read Control H duration (FM)
	trdhfm = 90 * time.Nanosecond
	// Read Control L duration (FM).
	trdlfm = 355 * time.Nanosecond
)
