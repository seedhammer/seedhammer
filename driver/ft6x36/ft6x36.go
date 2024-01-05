//go:build tinygo

// Package ft3x36 implements a TinyGo driver for the ft6x36 capacitive
// touch controllers.
//
// Datasheet: https://www.buydisplay.com/download/ic/FT6236-FT6336-FT6436L-FT6436_Datasheet.pdf
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

func New(bus *machine.I2C) *Device {
	return &Device{
		bus: bus,
	}
}

const (
	_Address = 0x38

	_TD_STATUS = 0x02
	_G_MODE    = 0xa4
	_TH_GROUP  = 0x80
	_TH_DIFF   = 0x85
)

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
