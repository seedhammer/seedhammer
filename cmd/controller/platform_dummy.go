//go:build !linux || !arm

package main

import (
	"errors"
	"image"
	"log"

	"seedhammer.com/gui"
)

func Init() error {
	if err := dbgInit(); err != nil {
		log.Printf("debug: %v", err)
	}
	return nil
}

func (p *Platform) Display() (gui.LCD, error) {
	return nil, errors.New("Display not implemented")
}

func (p *Platform) Camera(dims image.Point, frames chan gui.Frame, out <-chan gui.Frame) func() {
	return func() {}
}
