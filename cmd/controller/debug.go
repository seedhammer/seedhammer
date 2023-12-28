//go:build debug

package main

import (
	"io"
	"log"
	"os"
	"runtime/pprof"
	"strings"

	"seedhammer.com/gui"
	"seedhammer.com/mjolnir"
)

const Debug = true

type Platform struct{}

var inputCh chan<- gui.Event

func (p *Platform) Input(ch chan<- gui.Event) error {
	inputCh = ch
	return inputOpen(ch)
}

func (p *Platform) Engraver() (io.ReadWriteCloser, error) {
	return mjolnir.NewSimulator(), nil
}

func newPlatform() *Platform {
	return new(Platform)
}

func click(btn gui.Button) {
	inputCh <- gui.Event{
		Button:  btn,
		Pressed: true,
	}
	inputCh <- gui.Event{
		Button:  btn,
		Pressed: false,
	}
}

func debugCommand(cmd string) error {
	switch {
	case strings.HasPrefix(cmd, "runes "):
		cmd = strings.ToUpper(cmd[len("runes "):])
		for _, r := range cmd {
			if r == ' ' {
				click(gui.Button2)
				continue
			}
			inputCh <- gui.Event{
				Button:  gui.Rune,
				Rune:    r,
				Pressed: true,
			}
		}
		click(gui.Button2)
	case strings.HasPrefix(cmd, "input "):
		cmd = cmd[len("input "):]
		for _, name := range strings.Split(cmd, " ") {
			name = strings.TrimSpace(name)
			var btn gui.Button
			switch name {
			case "up":
				btn = gui.Up
			case "down":
				btn = gui.Down
			case "left":
				btn = gui.Left
			case "right":
				btn = gui.Right
			case "center":
				btn = gui.Center
			case "b1":
				btn = gui.Button1
			case "b2":
				btn = gui.Button2
			case "b3":
				btn = gui.Button3
			default:
				log.Printf("debug: unknown button: %s", name)
				continue
			}
			click(btn)
		}
	case cmd == "goroutines":
		pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
	case cmd == "screenshot":
		inputCh <- gui.Event{
			Button:  gui.Screenshot,
			Pressed: true,
		}
	default:
		log.Printf("debug: unrecognized command: %s", cmd)
	}
	return nil
}
