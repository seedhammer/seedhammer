//go:build !debug

package main

import (
	"errors"
	"io"

	"seedhammer.com/input"
	"seedhammer.com/mjolnir"
)

const Debug = false

func dbgInit() error {
	return nil
}

type Platform struct{}

func (p *Platform) Input(ch chan<- input.Event) error {
	return input.Open(ch)
}

func (p *Platform) Engraver() (io.ReadWriteCloser, error) {
	return mjolnir.Open("")
}

func (p *Platform) Dump(path string, r io.Reader) error {
	return errors.New("not available in production")
}

func newPlatform() *Platform {
	return new(Platform)
}
