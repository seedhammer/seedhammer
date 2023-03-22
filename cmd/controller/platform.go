package main

import (
	"image"
	"time"

	"seedhammer.com/camera"
)

var sdcard = make(chan bool, 1)

func (p *Platform) Camera(dims image.Point, frames chan camera.Frame, out <-chan camera.Frame) (func(), error) {
	return camera.Open(dims, frames, out)
}

func (p *Platform) Now() time.Time {
	return time.Now()
}

// SDCard returns a channel that is notified whenever
// an microSD card is inserted or removed.
func (p *Platform) SDCard() <-chan bool {
	return sdcard
}
