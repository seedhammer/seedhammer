//go:build !debug

package main

import (
	"errors"
	"io"

	"seedhammer.com/gui"
	"seedhammer.com/mjolnir"
)

func dbgInit() error {
	return nil
}

type Platform struct{}

func (p *Platform) Debug() bool {
	return false
}

func (p *Platform) Input(ch chan<- gui.Event) error {
	return inputOpen(ch)
}

func (p *Platform) Engraver() (io.ReadWriteCloser, error) {
	return mjolnir.Open("")
}

func newPlatform() *Platform {
	return new(Platform)
}
