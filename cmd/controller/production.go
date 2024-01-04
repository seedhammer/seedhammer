//go:build !debug

package main

import (
	"io"

	"seedhammer.com/mjolnir"
)

func dbgInit() error {
	return nil
}

type Platform struct{}

func (p *Platform) Debug() bool {
	return false
}

func (p *Platform) Engraver() (io.ReadWriteCloser, error) {
	return mjolnir.Open("")
}

func newPlatform() *Platform {
	return new(Platform)
}
