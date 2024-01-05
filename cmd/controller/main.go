// command controller is the user interface for engraving SeedHammer plates.
package main

import (
	"fmt"
	"log"
	"os"

	"seedhammer.com/gui"
)

// Version is set by the Go linker with -ldflags='-X main.Version=...'.
var Version string

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "controller: %v\n", err)
		os.Exit(2)
	}
}

func run() error {
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))
	ver := Version
	if ver == "" {
		ver = os.Getenv("sh_version")
	}
	p, err := Init()
	if err != nil {
		return err
	}
	for range gui.Run(p, ver) {
	}
	return nil
}
