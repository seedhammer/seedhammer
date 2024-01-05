//go:build tinygo && stm32

// package st7789 implements a DMA TinyGo driver for st7789/st7796 TFT devices.
package st7789

import (
	"device/stm32"
	"fmt"
	"image"
	"machine"
	"time"
	"unsafe"
)

type Device struct {
	bus             *machine.SPI
	dma             *stm32.DMA_ST_Type
	dc, cs, rst, bl machine.Pin
	window          image.Rectangle
	cmdBuf          [20]byte
	backlight       bool
}

type Config struct {
}

func NewSPI(bus *machine.SPI, dma *stm32.DMA_ST_Type, dc, cs, rst, bl machine.Pin) *Device {
	return &Device{
		bus: bus,
		dma: dma,
		dc:  dc,
		cs:  cs,
		rst: rst,
		bl:  bl,
	}
}

const MaxDrawSize = 4096

func (d *Device) Configure(c Config) error {
	// Enable DMA send to SPI.
	d.bus.Bus.SetCR2_TXDMAEN(1)
	d.dma.SetCR_CHSEL(3)
	// Configure memory-to-peripheral.
	d.dma.SetCR_DIR(0b01)
	// Increment memory address after each transfer.
	d.dma.SetCR_MINC(0b1)
	// Enable FIFO.
	d.dma.SetFCR_DMDIS(0b1)
	// 16-bit values to peripheral.
	d.dma.SetCR_PSIZE(0b01)
	// 16-bit values from memory.
	d.dma.SetCR_MSIZE(0b01)
	// Set SPI1 DR register as target.
	d.dma.SetPAR(uint32(uintptr(unsafe.Pointer(&d.bus.Bus.DR.Reg))))

	for _, p := range []machine.Pin{d.cs, d.rst, d.dc, d.bl} {
		p.Configure(machine.PinConfig{Mode: machine.PinOutput})
	}
	for _, p := range []machine.Pin{d.cs, d.rst, d.dc} {
		p.High()
	}

	// Turn off backlight during setup.
	d.bl.Low()

	d.cs.Low()
	defer d.cs.High()
	// Reset LCD.
	d.rst.High()
	time.Sleep(100 * time.Millisecond)
	d.rst.Low()
	time.Sleep(100 * time.Millisecond)
	d.rst.High()
	time.Sleep(100 * time.Millisecond)

	var cmdErr error
	sendCommand := func(cmd byte, data ...byte) {
		if cmdErr != nil {
			return
		}
		cmdErr = d.sendCommand(cmd, data...)
	}
	// Set rotation.
	const (
		MADCTL_MY  = 1 << 7
		MADCTL_MX  = 1 << 6
		MADCTL_MV  = 1 << 5
		MADCTL_ML  = 1 << 4
		MADCTL_BGR = 1 << 3
		MADCTL_MH  = 1 << 2
	)
	sendCommand(0x36 /*MADCTL*/, MADCTL_BGR|MADCTL_MV|MADCTL_MH)

	// Initialize LCD registers.
	sendCommand(0x11 /*SLPOUT*/)
	time.Sleep(120 * time.Millisecond)
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
	d.cmdBuf[0] = cmd
	d.dc.Low()
	if err := d.bus.Tx(d.cmdBuf[:1], nil); err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	d.dc.High()
	buf := d.cmdBuf[:len(data)]
	copy(buf, data)
	return d.bus.Tx(buf, nil)
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
	d.dc.High()
	// DMA transfers are 16-bit.
	d.bus.Bus.SetCR1_DFF(0b1)
	return nil
}

func (d *Device) EndFrame() {
	d.waitDMA()
	d.cs.High()
	// Commands are 8-bit.
	d.bus.Bus.SetCR1_DFF(0b0)
	// Turn on backlight if necessary.
	if !d.backlight {
		d.bl.High()
		d.backlight = true
	}
}

func (d *Device) Draw(buf [][2]byte) {
	d.waitDMA()
	stm32.DMA2.LIFCR.Set(^uint32(0)) // Clear status bits.
	d.dma.SetM0AR(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(buf)))))
	d.dma.SetNDTR_NDT(uint32(len(buf)))
	d.dma.SetCR_EN(1)
}

func (d *Device) waitDMA() {
	// Wait for DMA completion.
	for d.dma.GetNDTR_NDT() > 0 {
	}
	// Wait for SPI to settle.
	for !d.bus.Bus.SR.HasBits(stm32.SPI_SR_TXE) {
	}
	for d.bus.Bus.SR.HasBits(stm32.SPI_SR_BSY) {
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
	sendCommand(0x2a /* CASET */, byte(r.Min.X>>8), byte(r.Min.X), byte((r.Max.X-1)>>8), byte((r.Max.X)-1))
	sendCommand(0x2b /* RASET */, byte(r.Min.Y>>8), byte(r.Min.Y), byte((r.Max.Y-1)>>8), byte((r.Max.Y)-1))
	sendCommand(0x2c /* RAMWR */)
	return cmdErr
}
