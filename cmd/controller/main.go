// command controller is the user interface for engraving SeedHammer plates.
// It runs on a Raspberry Pi Zero, in the same configuration as SeedSigner.
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"seedhammer.com/gui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "controller: %v\n", err)
		os.Exit(2)
	}
}

func run() error {
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))
	version := os.Getenv("sh_version")
	p, err := Init()
	if err != nil {
		return err
	}
	for range gui.Run(p, version) {
	}
	return nil
}

var debug = false

func (p *Platform) Debug() bool {
	return debug
}

func (p *Platform) Now() time.Time {
	return time.Now()
}
