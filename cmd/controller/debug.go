//go:build debug

package main

import (
	"log"
	"os"
	"runtime/pprof"
	"strings"

	"seedhammer.com/gui"
)

func init() {
	debug = true
}

func click(btn gui.Button) []gui.ButtonEvent {
	return []gui.ButtonEvent{
		{
			Button:  btn,
			Pressed: true,
		},
		{
			Button:  btn,
			Pressed: false,
		},
	}
}

func debugCommand(cmd string) []gui.ButtonEvent {
	var evts []gui.ButtonEvent
	switch {
	case strings.HasPrefix(cmd, "runes "):
		cmd = strings.ToUpper(cmd[len("runes "):])
		for _, r := range cmd {
			if r == ' ' {
				evts = append(evts, click(gui.Button2)...)
				continue
			}
			evts = append(evts, gui.ButtonEvent{
				Button:  gui.Rune,
				Rune:    r,
				Pressed: true,
			})
		}
		evts = append(evts, click(gui.Button2)...)
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
			evts = append(evts, click(btn)...)
		}
	case cmd == "goroutines":
		pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
	default:
		log.Printf("debug: unrecognized command: %s", cmd)
	}
	return evts
}
