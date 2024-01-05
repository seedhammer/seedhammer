package gui

import (
	"fmt"
	"image"

	"seedhammer.com/gui/op"
)

type EventRouter struct {
	events  []event
	filters []Filter
	pointer struct {
		pressedTag op.Tag
		pressed    bool
	}
}

type Button int

const (
	None Button = iota
	Up
	Down
	Left
	Right
	Center
	Button1
	Button2
	Button3
	MaxButton
)

func (b Button) String() string {
	switch b {
	case Up:
		return "up"
	case Down:
		return "down"
	case Left:
		return "left"
	case Right:
		return "right"
	case Center:
		return "center"
	case Button1:
		return "b1"
	case Button2:
		return "b2"
	case Button3:
		return "b3"
	default:
		panic("invalid button")
	}
}

func FrameFilter() Filter {
	return Filter{
		typ: frameEvent,
	}
}

func RuneFilter() Filter {
	return Filter{
		typ: runeEvent,
	}
}

func ButtonFilter(b Button) Filter {
	return Filter{
		typ: buttonEvent,
		btn: b,
	}
}

func PointerFilter(t op.Tag) Filter {
	return Filter{
		typ: pointerEvent,
		tag: t,
	}
}

type FrameEvent struct {
	Error error
	Image image.Image
}

type PointerEvent struct {
	Pressed bool
	Entered bool
	Pos     image.Point
}

type ScanEvent struct {
	Content any
}

type event struct {
	Event
	BoundedTag
}

type Event struct {
	typ  eventKind
	data [3]uint32
	refs [2]any
}

type Filter struct {
	typ eventKind
	btn Button
	tag any
}

type eventKind int

const (
	buttonEvent eventKind = 1 + iota
	frameEvent
	runeEvent
	pointerEvent
)

type ButtonEvent struct {
	Button  Button
	Pressed bool
}

type RuneEvent struct {
	Rune rune
}

type BoundedTag struct {
	Tag    op.Tag
	Bounds image.Rectangle
}

func (f Filter) matches(e event) (Event, bool) {
	if f.typ != e.typ {
		return Event{}, false
	}
	switch f.typ {
	case pointerEvent:
		if e.Tag == nil || e.Tag != f.tag {
			return Event{}, false
		}
		pe, _ := e.AsPointer()
		pe.Entered = pe.Pos.In(e.Bounds)
		if pe.Pressed {
			pe.Pos = pe.Pos.Sub(e.Bounds.Min)
		}
		return pe.Event(), true
	case buttonEvent:
		e, _ := e.AsButton()
		if e.Button != f.btn {
			return Event{}, false
		}
	}
	return e.Event, true
}

func (r RuneEvent) Event() Event {
	e := Event{typ: runeEvent}
	e.data[0] = uint32(r.Rune)
	return e
}

func (f FrameEvent) Event() Event {
	e := Event{typ: frameEvent}
	e.refs[0] = f.Error
	e.refs[1] = f.Image
	return e
}

func (b ButtonEvent) Event() Event {
	pressed := uint32(0)
	if b.Pressed {
		pressed = 1
	}
	e := Event{typ: buttonEvent}
	e.data[0] = uint32(b.Button)
	e.data[1] = pressed
	return e
}

const (
	pressedBit = 0b1 << iota
	enteredBit
)

func (p PointerEvent) Event() Event {
	e := Event{typ: pointerEvent}
	if p.Pressed {
		e.data[0] |= pressedBit
	}
	if p.Entered {
		e.data[0] |= enteredBit
	}
	e.data[1] = uint32(int32(p.Pos.X))
	e.data[2] = uint32(int32(p.Pos.Y))
	return e
}

func (e Event) String() string {
	if e, ok := e.AsFrame(); ok {
		return fmt.Sprintf("FrameEvent%+v", e)
	}
	if e, ok := e.AsButton(); ok {
		return fmt.Sprintf("ButtonEvent%+v", e)
	}
	if e, ok := e.AsPointer(); ok {
		return fmt.Sprintf("PointerEvent%+v", e)
	}
	if e, ok := e.AsRune(); ok {
		return fmt.Sprintf("RuneEvent%+v", e)
	}
	return "Event{}"
}

func (e Event) AsFrame() (FrameEvent, bool) {
	if e.typ != frameEvent {
		return FrameEvent{}, false
	}
	f := FrameEvent{}
	if r := e.refs[0]; r != nil {
		f.Error = r.(error)
	}
	if r := e.refs[1]; r != nil {
		f.Image = r.(image.Image)
	}
	return f, true
}

func (e Event) AsButton() (ButtonEvent, bool) {
	if e.typ != buttonEvent {
		return ButtonEvent{}, false
	}
	return ButtonEvent{
		Button:  Button(e.data[0]),
		Pressed: e.data[1] != 0,
	}, true
}

func (e Event) AsPointer() (PointerEvent, bool) {
	if e.typ != pointerEvent {
		return PointerEvent{}, false
	}
	return PointerEvent{
		Pressed: e.data[0]&(pressedBit) != 0,
		Entered: e.data[0]&(enteredBit) != 0,
		Pos:     image.Point{X: int(int32(e.data[1])), Y: int(int32(e.data[2]))},
	}, true
}

func (e Event) AsRune() (RuneEvent, bool) {
	if e.typ != runeEvent {
		return RuneEvent{}, false
	}
	return RuneEvent{
		Rune: rune(e.data[0]),
	}, true
}

func (r *EventRouter) Next(filters ...Filter) (Event, bool) {
	r.filters = append(r.filters, filters...)
	if len(r.events) == 0 {
		return Event{}, false
	}
	e := r.events[0]
	for _, f := range filters {
		if e, ok := f.matches(e); ok {
			r.events = append(r.events[:0], r.events[1:]...)
			return e, true
		}
	}
	return Event{}, false
}

func (r *EventRouter) Reset() bool {
discard:
	for len(r.events) > 0 {
		e := r.events[0]
		for _, f := range r.filters {
			if _, ok := f.matches(e); ok {
				break discard
			}
		}
		r.events = append(r.events[:0], r.events[1:]...)
	}
	r.filters = r.filters[:0]
	return len(r.events) > 0
}

func (r *EventRouter) Events(o *op.Ops, evts ...Event) {
	for _, e := range evts {
		pe, ok := e.AsPointer()
		if !ok {
			r.events = append(r.events, event{
				Event: e,
			})
			continue
		}
		pctx := &r.pointer
		var pressedBounds image.Rectangle
		b, ok := o.TagBounds(pctx.pressedTag)
		if !ok {
			pctx.pressedTag = nil
		}
		pressedBounds = b
		var t BoundedTag
		if pctx.pressed {
			t = BoundedTag{
				Tag:    pctx.pressedTag,
				Bounds: pressedBounds,
			}
		} else {
			t.Tag, t.Bounds, _ = o.Hit(pe.Pos)
			pctx.pressedTag = t.Tag
		}
		pctx.pressed = pe.Pressed
		if !pctx.pressed {
			pctx.pressedTag = nil
		}
		r.events = append(r.events, event{
			Event:      e,
			BoundedTag: t,
		})
	}
}
