//go:build tinygo

// Package ft3x36 implements a TinyGo driver for the ft6x36 capacitive
// touch controllers.
//
// Datasheet: https://focuslcds.com/content/FT6236.pdf
package ft6x36

import (
	"image"
	"machine"
)

type Device struct {
	bus *machine.I2C
	// Allocate enough space for a touch event read.
	buf [1 + 5]byte
}

type Config struct {
}

func New(bus *machine.I2C) *Device {
	return &Device{
		bus: bus,
	}
}

const (
	_Address = 0x38

	_TD_STATUS = 0x02
	_G_MODE    = 0xa4
)

func (d *Device) Configure(_ Config) {
	// Set interrupt to polling mode.
	d.buf[0] = _G_MODE
	d.buf[1] = 0x00
	d.bus.Tx(_Address, d.buf[:], nil)
}

func (d *Device) ReadTouchPoint() (image.Point, bool) {
	wr := d.buf[:1]
	rd := d.buf[1:]
	wr[0] = _TD_STATUS
	if err := d.bus.Tx(_Address, wr, rd); err != nil {
		return image.Point{}, false
	}

	switch rd[0] {
	case 0, 255:
		return image.Point{}, false
	}

	return image.Point{
		X: int(rd[1]&0x0F)<<8 + int(rd[2]),
		Y: int(rd[3]&0x0F)<<8 + int(rd[4]),
	}, true
}
