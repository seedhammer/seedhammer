//go:build !linux || !arm

package main

import (
	"errors"
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
