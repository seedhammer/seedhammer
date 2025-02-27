package gui

import (
	"time"
)

type Clickable struct {
	Button     Button
	AltButton  Button
	Repeatable bool

	Pressed bool
	Entered bool

	repeat time.Time
}

const repeatStartDelay = 400 * time.Millisecond
const repeatDelay = 100 * time.Millisecond

func (c *Clickable) Clicked(ctx *Context) bool {
	now := ctx.Platform.Now()
	switch c.Button {
	case Up, Down, Right, Left:
		if !c.Pressed {
			break
		}
		wakeup := c.repeat
		if wakeup.IsZero() {
			wakeup = now.Add(repeatStartDelay)
		}
		repeat := !now.Before(wakeup)
		if repeat {
			wakeup = now.Add(repeatDelay)
		}
		c.repeat = wakeup
		ctx.WakeupAt(wakeup)
		if repeat {
			return true
		}
	}

	for {
		e, ok := ctx.Next(ButtonFilter(c.Button), ButtonFilter(c.AltButton), PointerFilter(c))
		if !ok {
			return false
		}
		clicked := false
		if e, ok := e.AsButton(); ok {
			clicked = !e.Pressed && c.Pressed
			c.Pressed = e.Pressed
		}
		if e, ok := e.AsPointer(); ok {
			clicked = !e.Pressed && c.Pressed && c.Entered
			c.Entered = e.Entered
			c.Pressed = e.Pressed
		}
		if !c.Pressed {
			c.repeat = time.Time{}
		}
		if clicked {
			return true
		}
	}
}
