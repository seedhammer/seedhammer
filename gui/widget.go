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

type ClickableEvent struct {
	Clicked bool
}

func (c *Clickable) For(btns ...Button) *Clickable {
	if len(btns) > 0 {
		c.Button = btns[0]
	}
	if len(btns) > 1 {
		c.AltButton = btns[1]
	}
	return c
}

func (c *Clickable) Clicked(ctx *Context) bool {
	for {
		e, ok := c.Next(ctx)
		if !ok {
			break
		}
		if e.Clicked {
			return true
		}
	}
	return false
}

func (c *Clickable) Next(ctx *Context) (ClickableEvent, bool) {
	now := time.Now()
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
			return ClickableEvent{Clicked: true}, true
		}
	}

	e, ok := ctx.Router.Next(ButtonFilter(c.Button), ButtonFilter(c.AltButton), PointerFilter(c))
	if !ok {
		return ClickableEvent{}, false
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
	return ClickableEvent{Clicked: clicked}, true
}
