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

func (p *Platform) Input(ch chan<- gui.Event) error {
	return nil
}

func (p *Platform) Display() (gui.LCD, error) {
	return nil, errors.New("Display not implemented")
}

func (p *Platform) Camera(dims image.Point, frames chan gui.Frame, out <-chan gui.Frame) func() {
	return func() {}
}

func (p *Platform) ScanQR(img *image.Gray) ([][]byte, error) {
	return nil, errors.New("ScanQR not implemented")
}
