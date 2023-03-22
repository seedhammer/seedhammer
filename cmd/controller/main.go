// command controller is the user interface for engraving SeedHammer plates.
// It runs on a Raspberry Pi Zero, in the same configuration as SeedSigner.
package main

import (
	"fmt"
	"log"
	"os"
	"strings"

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
	log.Println("seedhammer: loading...")
	if err := Init(); err != nil {
		return err
	}
	ver, err := readVersion()
	if err != nil {
		return err
	}
	lcd, err := lcd.Open()
	if err != nil {
		return err
	}
	defer lcd.Close()
	a := gui.NewApp(newPlatform(), lcd, ver)
	a.Debug = Debug
	for {
		a.Frame()
	}
}

// readVersion reads the version from the kernel command line.
func readVersion() (string, error) {
	cmdline, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return "", err
	}
	for _, kv := range strings.Split(string(cmdline), " ") {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == "sh_version" {
			return v, nil
		}
	}
	return "", nil
}
