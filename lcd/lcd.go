// package lcd implements an LCD driver for the Waveshare 1.3" 240x240 HAT.
package lcd

import (
	"fmt"
	"image"
	"time"
	"unsafe"

	"periph.io/x/conn/v3"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/physic"
	"periph.io/x/conn/v3/spi"
	"periph.io/x/conn/v3/spi/spireg"
	"periph.io/x/host/v3"
	"periph.io/x/host/v3/bcm283x"
	"seedhammer.com/rgb16"
)

type LCD struct {
	dims      image.Point
	spi       spi.PortCloser
	conn      spi.Conn
	window    image.Rectangle
	txBuf     []byte
	backlight bool
}

func (l *LCD) Close() {
	l.spi.Close()
	l.spi = nil
	l.conn = nil
}

const (
	lcdWidth  = 240
	lcdHeight = 240
)

func Open() (*LCD, error) {
	if _, err := host.Init(); err != nil {
		return nil, err
	}
	// Use spireg SPI port registry to find the first available SPI bus.
	p, err := spireg.Open("")
	if err != nil {
		return nil, fmt.Errorf("lcd: %w", err)
	}
	// Convert the spi.Port into a spi.Conn so it can be used for communication.
	c, err := p.Connect(40*physic.MegaHertz, spi.Mode0, 8)
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("lcd: %w", err)
	}

	lcd := &LCD{
		dims: image.Pt(lcdWidth, lcdHeight),
		spi:  p,
		conn: c,
	}
	maxTx := 4096
	if lim, ok := c.(conn.Limits); ok {
		maxTx = lim.MaxTxSize()
	}
	lcd.txBuf = make([]byte, maxTx)
	if err := lcd.setup(); err != nil {
		lcd.Close()
		return nil, err
	}
	return lcd, nil
}

var (
	LCD_CS  = bcm283x.GPIO8
	LCD_RST = bcm283x.GPIO27
	LCD_DC  = bcm283x.GPIO25
	LCD_BL  = bcm283x.GPIO24
)

func (l *LCD) sendCommand(cmd byte, data ...byte) error {
	LCD_DC.FastOut(gpio.Low)
	if err := l.conn.Tx([]byte{cmd}, make([]byte, 1)); err != nil {
		return err
	}
	if len(data) > 0 {
		LCD_DC.FastOut(gpio.High)
		if err := l.conn.Tx(data, nil); err != nil {
			return err
		}
	}
	return nil
}

func (l *LCD) setup() error {
	for _, p := range []gpio.PinOut{LCD_CS, LCD_RST, LCD_DC} {
		if err := p.Out(gpio.High); err != nil {
			return fmt.Errorf("lcd: %w", err)
		}
	}

	// Turn off backlight during setup.
	LCD_BL.Out(gpio.Low)

	// Reset LCD.
	LCD_RST.FastOut(gpio.High)
	time.Sleep(100 * time.Millisecond)
	LCD_RST.FastOut(gpio.Low)
	time.Sleep(100 * time.Millisecond)
	LCD_RST.FastOut(gpio.High)
	time.Sleep(100 * time.Millisecond)

	var cmdErr error
	sendCommand := func(cmd byte, data ...byte) {
		if cmdErr != nil {
			return
		}
		cmdErr = l.sendCommand(cmd, data...)
	}
	// Set horizontal scanout.
	sendCommand(0x36 /*MADCTL*/, 0x70 /* MX, MY, RGB mode */)

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
	sendCommand(0x21 /*INVON*/)
	sendCommand(0x29 /*DISPON*/)
	if cmdErr != nil {
		return fmt.Errorf("lcd: SPI command: %w", cmdErr)
	}
	return nil
}

func (l *LCD) Dims() image.Point {
	return l.dims
}

func (l *LCD) Draw(img *rgb16.Image, sr image.Rectangle) error {
	sr = sr.Intersect(img.Bounds())
	if sr.Empty() {
		return nil
	}
	if err := l.setWindow(sr); err != nil {
		return err
	}

	LCD_DC.FastOut(gpio.High)

	sz := sr.Size()
	idx := 0
	start := img.PixOffset(sr.Min.X, sr.Min.Y)
	end := img.PixOffset(sr.Max.X, sr.Max.Y-1)
	pix := img.Pix[start:end]
	for idx < sz.X*sz.Y {
		bufIdx := 0
		buf := l.txBuf
		for bufIdx < len(buf) && idx < sz.X*sz.Y {
			x, y := idx%sz.X, idx/sz.X
			start := x + y*img.Stride
			pix := pix[start:]
			if sz.X != img.Stride {
				// Pixel rows are not contiguous.
				pix = pix[:sz.X-x]
			}
			byteview := unsafe.Slice((*byte)(unsafe.Pointer(&pix[0])), len(pix)*2)
			remaining := (sz.X*sz.Y - idx) * 2
			if remaining > len(buf) {
				remaining = len(buf)
			}
			var n int
			if bufIdx != 0 || len(byteview) < remaining {
				n = copy(buf[bufIdx:], byteview)
			} else {
				n = remaining
				buf = byteview[:n]
			}
			idx += n / 2
			bufIdx += n
		}
		buf = buf[:bufIdx]
		if err := l.conn.Tx(buf, nil); err != nil {
			return fmt.Errorf("lcd: blit: %w", err)
		}
	}

	// Turn on backlight if necessary.
	if !l.backlight {
		LCD_BL.Out(gpio.High)
		l.backlight = true
	}

	return nil
}

func (l *LCD) setWindow(r image.Rectangle) error {
	if l.window == r {
		return nil
	}
	l.window = r

	var cmdErr error
	sendCommand := func(cmd byte, data ...byte) {
		if cmdErr != nil {
			return
		}
		cmdErr = l.sendCommand(cmd, data...)
	}
	sendCommand(0x2a /* CASET */, byte(r.Min.X>>8), byte(r.Min.X), byte((r.Max.X-1)>>8), byte((r.Max.X)-1))
	sendCommand(0x2b /* RASET */, byte(r.Min.Y>>8), byte(r.Min.Y), byte((r.Max.Y-1)>>8), byte((r.Max.Y)-1))
	sendCommand(0x2c /* RAMWR */)
	return cmdErr
}
