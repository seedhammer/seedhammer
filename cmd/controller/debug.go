//go:build debug

package main

import (
	"io"
	"log"
	"os"
	"runtime/pprof"
	"strings"

	"seedhammer.com/input"
	"seedhammer.com/mjolnir"
)

const Debug = true

type Platform struct{}

var inputCh chan<- input.Event

func (p *Platform) Input(ch chan<- input.Event) error {
	inputCh = ch
	return input.Open(ch)
}

func (p *Platform) Engraver() (io.ReadWriteCloser, error) {
	return mjolnir.NewSimulator(), nil
}

func newPlatform() *Platform {
	return new(Platform)
}

func debugCommand(cmd string) error {
	switch {
	case strings.HasPrefix(cmd, "runes "):
		cmd = cmd[len("input "):]
		for _, r := range cmd {
			inputCh <- input.Event{
				Button:  input.Rune,
				Rune:    r,
				Pressed: true,
			}
		}
		inputCh <- input.Event{
			Button:  input.Button2,
			Pressed: true,
		}
		inputCh <- input.Event{
			Button:  input.Button2,
			Pressed: false,
		}
	case strings.HasPrefix(cmd, "input "):
		cmd = cmd[len("input "):]
		for _, name := range strings.Split(cmd, " ") {
			name = strings.TrimSpace(name)
			var btn input.Button
			switch name {
			case "up":
				btn = input.Up
			case "down":
				btn = input.Down
			case "left":
				btn = input.Left
			case "right":
				btn = input.Right
			case "center":
				btn = input.Center
			case "b1":
				btn = input.Button1
			case "b2":
				btn = input.Button2
			case "b3":
				btn = input.Button3
			default:
				log.Printf("debug: unknown button: %s", name)
				continue
			}
			inputCh <- input.Event{
				Button:  btn,
				Pressed: true,
			}
			inputCh <- input.Event{
				Button:  btn,
				Pressed: false,
			}
		}
	case cmd == "goroutines":
		pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
	case cmd == "screenshot":
		inputCh <- input.Event{
			Button:  input.Screenshot,
			Pressed: true,
		}
	default:
		log.Printf("debug: unrecognized command: %s", cmd)
	}
	return nil
}
