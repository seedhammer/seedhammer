//go:build !linux || !arm

package camera

import (
	"errors"
	"image"
)

type Camera struct {
}

type Frame struct {
	Err   error
	Image image.Image
}

func (c *Camera) Close() {
}

func Open(dims image.Point, frames chan Frame, out <-chan Frame) (func(), error) {
	return nil, errors.New("not implemented")
}
