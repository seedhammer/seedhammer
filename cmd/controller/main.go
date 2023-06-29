// command controller is the user interface for engraving SeedHammer plates.
// It runs on a Raspberry Pi Zero, in the same configuration as SeedSigner.
package main

import (
	"fmt"
	"log"
	"os"

	"seedhammer.com/gui"
	"seedhammer.com/lcd"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v", err)
		os.Exit(2)
	}
}

func run() error {
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))
	version := os.Getenv("sh_version")
	log.Printf("seedhammer: loading %s...\n", version)
	if err := Init(); err != nil {
		return err
	}
	lcd, err := lcd.Open()
	if err != nil {
		return err
	}
	defer lcd.Close()
	a := gui.NewApp(newPlatform(), lcd, version)
	a.Debug = Debug
	for {
		a.Frame()
	}
}
