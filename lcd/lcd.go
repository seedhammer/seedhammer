//go:build !linux

package lcd

import (
	"errors"
	"image"
	"image/draw"
)

type LCD struct{}

func Open() (*LCD, error) {
	return nil, errors.New("not implemented")
}

func (l *LCD) Framebuffer() draw.RGBA64Image {
	panic("not implemented")
}

func (l *LCD) Dirty(sr image.Rectangle) error {
	panic("not implemented")
}

func (l *LCD) Close() {
}
