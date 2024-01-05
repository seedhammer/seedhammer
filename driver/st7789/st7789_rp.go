//go:build tinygo && rp

// package st7789 implements a DMA TinyGo driver for st7789/st7796 TFT devices.
package st7789

import (
	"device/rp"
	"fmt"
	"image"
	"machine"
	"time"
	"unsafe"
)

type Device struct {
	bus             *machine.SPI
	dc, cs, rst, bl machine.Pin
	window          image.Rectangle
	cmdBuf          [20]byte
	backlight       bool
}

type Config struct {
}

func NewSPI(bus *machine.SPI, dmaChannel uint8, dc, cs, rst, bl machine.Pin) *Device {
	// Hard code to DMA channel 0 for convenience.
	if dmaChannel != 0 {
		panic("only DMA channel 0 i supported")
	}
	return &Device{
		bus: bus,
		dc:  dc,
		cs:  cs,
		rst: rst,
		bl:  bl,
	}
}

const MaxDrawSize = 4096

func (d *Device) Configure(c Config) error {
	// Commands are 8-bit.
	d.bus.Bus.SetSSPCR0_DSS(8 - 1)

	for _, p := range []machine.Pin{d.cs, d.rst, d.dc, d.bl} {
		p.Configure(machine.PinConfig{Mode: machine.PinOutput})
	}
	for _, p := range []machine.Pin{d.cs, d.rst, d.dc} {
		p.High()
	}

	d.backlight = false
	// Turn off backlight during setup.
	d.bl.Low()

	d.cs.Low()
	defer d.cs.High()

	var cmdErr error
	sendCommand := func(cmd byte, data ...byte) {
		if cmdErr != nil {
			return
		}
		cmdErr = d.sendCommand(cmd, data...)
	}

	if d.rst != machine.NoPin {
		// Reset LCD.
		d.rst.High()
		time.Sleep(50 * time.Millisecond)
		d.rst.Low()
		time.Sleep(50 * time.Millisecond)
		d.rst.High()
		time.Sleep(50 * time.Millisecond)
	} else {
		sendCommand(0x01 /*SWRESET*/)
		time.Sleep(150 * time.Millisecond)
	}

	// const (

	// 	// register constants based on source:
	// 	// https://github.com/adafruit/Adafruit_ILI9341/blob/master/Adafruit_ILI9341.h

	// 	TFTWIDTH  = 240 ///< ILI9341 max TFT width
	// 	TFTHEIGHT = 320 ///< ILI9341 max TFT height

	// 	NOP     = 0x00 ///< No-op register
	// 	SWRESET = 0x01 ///< Software reset register
	// 	RDDID   = 0x04 ///< Read display identification information
	// 	RDDST   = 0x09 ///< Read Display Status

	// 	SLPIN  = 0x10 ///< Enter Sleep Mode
	// 	SLPOUT = 0x11 ///< Sleep Out
	// 	PTLON  = 0x12 ///< Partial Mode ON
	// 	NORON  = 0x13 ///< Normal Display Mode ON

	// 	RDMODE     = 0x0A ///< Read Display Power Mode
	// 	RDMADCTL   = 0x0B ///< Read Display MADCTL
	// 	RDPIXFMT   = 0x0C ///< Read Display Pixel Format
	// 	RDIMGFMT   = 0x0D ///< Read Display Image Format
	// 	RDSELFDIAG = 0x0F ///< Read Display Self-Diagnostic Result

	// 	INVOFF   = 0x20 ///< Display Inversion OFF
	// 	INVON    = 0x21 ///< Display Inversion ON
	// 	GAMMASET = 0x26 ///< Gamma Set
	// 	DISPOFF  = 0x28 ///< Display OFF
	// 	DISPON   = 0x29 ///< Display ON

	// 	CASET = 0x2A ///< Column Address Set
	// 	PASET = 0x2B ///< Page Address Set
	// 	RAMWR = 0x2C ///< Memory Write
	// 	RAMRD = 0x2E ///< Memory Read

	// 	PTLAR    = 0x30 ///< Partial Area
	// 	VSCRDEF  = 0x33 ///< Vertical Scrolling Definition
	// 	TEOFF    = 0x34 ///< TEOFF: Tearing Effect Line OFF
	// 	TEON     = 0x35 ///< TEON: Tearing Effect Line ON
	// 	MADCTL   = 0x36 ///< Memory Access Control
	// 	VSCRSADD = 0x37 ///< Vertical Scrolling Start Address
	// 	PIXFMT   = 0x3A ///< COLMOD: Pixel Format Set

	// 	FRMCTR1 = 0xB1 ///< Frame Rate Control (In Normal Mode/Full Colors)
	// 	FRMCTR2 = 0xB2 ///< Frame Rate Control (In Idle Mode/8 colors)
	// 	FRMCTR3 = 0xB3 ///< Frame Rate control (In Partial Mode/Full Colors)
	// 	INVCTR  = 0xB4 ///< Display Inversion Control
	// 	DFUNCTR = 0xB6 ///< Display Function Control

	// 	PWCTR1 = 0xC0 ///< Power Control 1
	// 	PWCTR2 = 0xC1 ///< Power Control 2
	// 	PWCTR3 = 0xC2 ///< Power Control 3
	// 	PWCTR4 = 0xC3 ///< Power Control 4
	// 	PWCTR5 = 0xC4 ///< Power Control 5
	// 	VMCTR1 = 0xC5 ///< VCOM Control 1
	// 	VMCTR2 = 0xC7 ///< VCOM Control 2

	// 	RDID1 = 0xDA ///< Read ID 1
	// 	RDID2 = 0xDB ///< Read ID 2
	// 	RDID3 = 0xDC ///< Read ID 3
	// 	RDID4 = 0xDD ///< Read ID 4

	// 	GMCTRP1 = 0xE0 ///< Positive Gamma Correction
	// 	GMCTRN1 = 0xE1 ///< Negative Gamma Correction
	// 	//PWCTR6     0xFC

	// 	MADCTL_RGB = 0x00 ///< Red-Green-Blue pixel order

	// )
	// var initCmd = []byte{
	// 	0xEF, 3, 0x03, 0x80, 0x02,
	// 	0xCF, 3, 0x00, 0xC1, 0x30,
	// 	0xED, 4, 0x64, 0x03, 0x12, 0x81,
	// 	0xE8, 3, 0x85, 0x00, 0x78,
	// 	0xCB, 5, 0x39, 0x2C, 0x00, 0x34, 0x02,
	// 	0xF7, 1, 0x20,
	// 	0xEA, 2, 0x00, 0x00,
	// 	PWCTR1, 1, 0x23, // Power control VRH[5:0]
	// 	PWCTR2, 1, 0x10, // Power control SAP[2:0];BT[3:0]
	// 	VMCTR1, 2, 0x3e, 0x28, // VCM control
	// 	VMCTR2, 1, 0x86, // VCM control2
	// 	MADCTL, 1, 0x48, // Memory Access Control
	// 	VSCRSADD, 1, 0x00, // Vertical scroll zero
	// 	PIXFMT, 1, 0x55,
	// 	FRMCTR1, 2, 0x00, 0x18,
	// 	DFUNCTR, 3, 0x08, 0x82, 0x27, // Display Function Control
	// 	// 0xF2, 1, 0x00, // 3Gamma Function Disable
	// 	// GAMMASET, 1, 0x01, // Gamma curve selected
	// 	// GMCTRP1, 15, 0x0F, 0x31, 0x2B, 0x0C, 0x0E, 0x08, // Set Gamma
	// 	// 0x4E, 0xF1, 0x37, 0x07, 0x10, 0x03, 0x0E, 0x09, 0x00,
	// 	// GMCTRN1, 15, 0x00, 0x0E, 0x14, 0x03, 0x11, 0x07, // Set Gamma
	// 	// 0x31, 0xC1, 0x48, 0x08, 0x0F, 0x0C, 0x31, 0x36, 0x0F,
	// }

	// initCmd = append(initCmd,
	// 	SLPOUT, 0x80, // Exit Sleep
	// 	DISPON, 0x80, // Display on
	// 	0x00, // End of list
	// )
	// for i, c := 0, len(initCmd); i < c; {
	// 	cmd := initCmd[i]
	// 	if cmd == 0x00 {
	// 		break
	// 	}
	// 	x := initCmd[i+1]
	// 	numArgs := int(x & 0x7F)
	// 	d.sendCommand(cmd, initCmd[i+2:i+2+numArgs]...)
	// 	if x&0x80 > 0 {
	// 		time.Sleep(150 * time.Millisecond)
	// 	}
	// 	i += numArgs + 2
	// }
	// // Set rotation.
	// const (
	// 	MADCTL_MY  = 1 << 7
	// 	MADCTL_MX  = 1 << 6
	// 	MADCTL_MV  = 1 << 5
	// 	MADCTL_ML  = 1 << 4
	// 	MADCTL_BGR = 1 << 3
	// 	MADCTL_MH  = 1 << 2
	// )
	// sendCommand(0x36 /*MADCTL*/, MADCTL_BGR|MADCTL_MV|MADCTL_MH)
	// if cmdErr != nil {
	// 	return fmt.Errorf("lcd: SPI command: %w", cmdErr)
	// }
	// return nil

	const (
		MADCTL_MY  = 1 << 7
		MADCTL_MX  = 1 << 6
		MADCTL_MV  = 1 << 5
		MADCTL_ML  = 1 << 4
		MADCTL_BGR = 1 << 3
		MADCTL_MH  = 1 << 2
	)
	// Initialize LCD registers.
	sendCommand(0x11 /*SLPOUT*/)
	time.Sleep(120 * time.Millisecond)
	// Set rotation.
	// const (
	// 	MADCTL_MY  = 1 << 7
	// 	MADCTL_MX  = 1 << 6
	// 	MADCTL_MV  = 1 << 5
	// 	MADCTL_ML  = 1 << 4
	// 	MADCTL_BGR = 1 << 3
	// 	MADCTL_MH  = 1 << 2
	// )
	sendCommand(0x36 /*MADCTL*/, MADCTL_BGR|MADCTL_MV|MADCTL_MH)

	sendCommand(0x3a /*COLMOD*/, 0x05)
	sendCommand(0xb2 /*PORCTRL*/, 0x0c, 0x0c, 0x00, 0x33, 0x33)
	sendCommand(0xb7 /*GCTRL*/, 0x35)
	sendCommand(0xbb /*VCOMS*/, 0x37)
	sendCommand(0xc0 /*LCMCTRL*/, 0x2c)
	sendCommand(0xc2 /*VDVVRHEN*/, 0x01)
	sendCommand(0xc3 /*VRHS*/, 0x12)
	sendCommand(0xc4 /*VDVS*/, 0x20)
	sendCommand(0xc6 /*FRCTRL2*/, 0x0f)
	sendCommand(0xd0 /*PWCTRL1*/, 0xa4, 0xa1)
	const defaultGammaSettings = true
	if !defaultGammaSettings {
		sendCommand(0xe0 /*PVGAMCTRL*/, 0xd0, 0x04, 0x0d, 0x11, 0x13, 0x2b, 0x3f, 0x54, 0x4c, 0x18, 0x0d, 0x0b, 0x1f, 0x23)
		sendCommand(0xe1 /*NVGAMCTRL*/, 0xd0, 0x04, 0x0c, 0x11, 0x13, 0x2C, 0x3F, 0x44, 0x51, 0x2F, 0x1F, 0x1F, 0x20, 0x23)
	}
	sendCommand(0xba /*DGMEN: Enable Gamma*/, 0x04)
	// sendCommand(0x21 /*INVON*/)
	sendCommand(0x29 /*DISPON*/)
	if cmdErr != nil {
		return fmt.Errorf("lcd: SPI command: %w", cmdErr)
	}
	return nil
}

func (d *Device) sendCommand(cmd byte, data ...byte) error {
	const fifoSize = 8
	if 1+len(data) > fifoSize {
		panic("command too long")
	}
	d.dc.Low()
	d.bus.Bus.SSPDR.Set(uint32(cmd))
	for d.bus.Bus.GetSSPSR_BSY() != 0 {
	}
	d.dc.High()
	for _, b := range data {
		d.bus.Bus.SSPDR.Set(uint32(b))
	}
	for d.bus.Bus.GetSSPSR_BSY() != 0 {
	}
	return nil
}

func (d *Device) BeginFrame(sr image.Rectangle) error {
	if sr.Empty() {
		return nil
	}
	d.waitDMA()
	d.cs.Low()
	if err := d.setWindow(sr); err != nil {
		return err
	}
	// DMA transfers are 16-bit.
	d.bus.Bus.SetSSPCR0_DSS(16 - 1)
	return nil
}

func (d *Device) EndFrame() {
	d.waitDMA()
	d.cs.High()
	// Commands are 8-bit.
	d.bus.Bus.SetSSPCR0_DSS(8 - 1)
	// Turn on backlight if necessary.
	if !d.backlight {
		d.bl.High()
		d.backlight = true
	}
}

func (d *Device) Draw(buf [][2]byte) {
	d.waitDMA()
	dma := rp.DMA
	dma.CH0_READ_ADDR.Set(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(buf)))))
	dma.CH0_WRITE_ADDR.Set(uint32(uintptr(unsafe.Pointer(&d.bus.Bus.SSPDR))))
	dma.CH0_TRANS_COUNT.Set(uint32(len(buf)))
	const DREQ_SPI1_TX = 18
	dma.CH0_CTRL_TRIG.Set(
		// Increment read address on each transfer.
		rp.DMA_CH0_CTRL_TRIG_INCR_READ |
			// Transfer size is 16 bits.
			rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_SIZE_HALFWORD<<rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_Pos |
			// Pace transfers by the SPI transmit readyness signal.
			DREQ_SPI1_TX<<rp.DMA_CH0_CTRL_TRIG_TREQ_SEL_Pos |
			// Start transfer.
			rp.DMA_CH0_CTRL_TRIG_EN,
	)
}

func (d *Device) waitDMA() {
	dma := rp.DMA
	// Wait for DMA completion.
	for dma.GetCH0_CTRL_TRIG_BUSY() != 0 {
	}
	// Wait for SPI to settle.
	for d.bus.Bus.GetSSPSR_BSY() != 0 {
	}
}

func (d *Device) setWindow(r image.Rectangle) error {
	if d.window == r {
		return nil
	}
	d.window = r

	var cmdErr error
	sendCommand := func(cmd byte, data ...byte) {
		if cmdErr != nil {
			return
		}
		cmdErr = d.sendCommand(cmd, data...)
	}
	sendCommand(0x2a /* CASET */, byte(r.Min.X>>8), byte(r.Min.X), byte((r.Max.X-1)>>8), byte(r.Max.X-1))
	sendCommand(0x2b /* RASET */, byte(r.Min.Y>>8), byte(r.Min.Y), byte((r.Max.Y-1)>>8), byte(r.Max.Y-1))
	sendCommand(0x2c /* RAMWR */)
	return cmdErr
}
