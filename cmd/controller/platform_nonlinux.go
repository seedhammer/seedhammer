//go:build !linux

package main

import "log"

func Init() error {
	if err := dbgInit(); err != nil {
		log.Printf("debug: %v", err)
	}
	return nil
}
