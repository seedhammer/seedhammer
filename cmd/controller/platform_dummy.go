//go:build !linux || !arm

package main

import (
	"errors"
	"image"
	"image/draw"
	"time"

	"seedhammer.com/backup"
	"seedhammer.com/engrave"
	"seedhammer.com/gui"
)

type Platform struct{}

func Init() (*Platform, error) {
	return new(Platform), nil
}

func (p *Platform) PlateSizes() []backup.PlateSize {
	return nil
}

func (p *Platform) EngraverParams() engrave.Params {
	return engrave.Params{}
}

func (p *Platform) Engraver() (gui.Engraver, error) {
	return nil, errors.New("Engraver not implemented")
}

func (p *Platform) DisplaySize() image.Point {
	return image.Pt(1, 1)
}

func (p *Platform) Dirty(r image.Rectangle) error {
	return nil
}

func (p *Platform) NextChunk() (draw.RGBA64Image, bool) {
	return nil, false
}

func (p *Platform) Wakeup() {
}

func (p *Platform) AppendEvents(deadline time.Time, evts []gui.Event) []gui.Event {
	return evts
}

func (p *Platform) CameraFrame(dims image.Point) {
}

func (p *Platform) ScanQR(img *image.Gray) ([][]byte, error) {
	return nil, errors.New("ScanQR not implemented")
}
