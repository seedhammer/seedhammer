//go:build !linux || !arm

package main

import (
	"errors"
	"image"
	"io"

	"seedhammer.com/gui"
)

type Platform struct{}

func Init() (*Platform, error) {
	return new(Platform), nil
}

func (p *Platform) Engraver() (io.ReadWriteCloser, error) {
	return nil, errors.New("Engraver not implemented")
}

func (p *Platform) Display() (gui.LCD, error) {
	return nil, errors.New("Display not implemented")
}

func (p *Platform) Wakeup() {
}

func (p *Platform) Events() []gui.Event {
	return nil
}

func (p *Platform) CameraFrame(dims image.Point) {
}

func (p *Platform) ScanQR(img *image.Gray) ([][]byte, error) {
	return nil, errors.New("ScanQR not implemented")
}
