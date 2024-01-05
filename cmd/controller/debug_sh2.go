//go:build tinygo && rp && debug

package main

import (
	"log"
	"os"

	"seedhammer.com/gui"
)

func init() {
	initHook = dbgInit
}

// terminal converts terminal input to UI events.
// It recognizes a small set of ANSI escape codes.
type terminal struct {
	state  escapeState
	events chan<- gui.Event
}

type escapeState int

const (
	stateNone escapeState = iota
	stateEscape
	stateCSI
	stateG3
)

func (t *terminal) Char(c byte) {
	switch t.state {
	case stateEscape:
		switch c {
		case '[':
			t.state = stateCSI
		case 'O': // SSE3 selects the G3 character set.
			t.state = stateG3
		default:
			t.state = stateNone
		}
	case stateCSI:
		switch c {
		case 'A':
			t.button(gui.Up)
		case 'B':
			t.button(gui.Down)
		case 'C':
			t.button(gui.Right)
		case 'D':
			t.button(gui.Left)
		}
		t.state = stateNone
	case stateG3:
		switch c {
		case 'P': // F1
			t.button(gui.Button1)
		case 'Q': // F2
			t.button(gui.Button2)
		case 'R': // F3
			t.button(gui.Button3)
		}
		t.state = stateNone
	case stateNone:
		switch c {
		case '\x1b': // Escape.
			t.state = stateEscape
		case '\r': // Return.
			t.button(gui.Center)
		case ' ':
			t.button(gui.Button2)
		case '\x7f': // Backspace.
			t.events <- gui.RuneEvent{Rune: '⌫'}.Event()
		default:
			t.events <- gui.RuneEvent{Rune: rune(c)}.Event()
		}
	}
}

func (t *terminal) button(btn gui.Button) {
	t.events <- gui.ButtonEvent{Button: btn, Pressed: true}.Event()
	t.events <- gui.ButtonEvent{Button: btn, Pressed: false}.Event()
}

func dbgInit(output chan<- gui.Event) {
	go func() {
		buf := make([]byte, 200)
		term := &terminal{
			events: output,
		}
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				log.Printf("stdin read failed: %v", err)
				break
			}
			for _, c := range buf[:n] {
				term.Char(c)
			}
		}
	}()
}
