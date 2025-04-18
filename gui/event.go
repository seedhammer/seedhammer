package gui

import (
	"image"

	"seedhammer.com/gui/op"
)

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

func ScanFilter() Filter {
	return Filter{
		typ: scanEvent,
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

func SDCardFilter() Filter {
	return Filter{
		typ: sdcardEvent,
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

type filterKind int

type eventKind int

const (
	buttonEvent eventKind = 1 + iota
	sdcardEvent
	frameEvent
	runeEvent
	pointerEvent
	scanEvent
)

type ButtonEvent struct {
	Button  Button
	Pressed bool
}

type RuneEvent struct {
	Rune rune
}

type SDCardEvent struct {
	Inserted bool
}

type BoundedTag struct {
	Tag    op.Tag
	Bounds image.Rectangle
}

func (f Filter) Matches(t BoundedTag, e Event) (Event, bool) {
	if f.typ != e.typ {
		return Event{}, false
	}
	switch f.typ {
	case pointerEvent:
		if t.Tag == nil || t.Tag != f.tag {
			return Event{}, false
		}
		e, _ := e.AsPointer()
		e.Entered = e.Pos.In(t.Bounds)
		if e.Pressed {
			e.Pos = e.Pos.Sub(t.Bounds.Min)
		}
		return e.Event(), true
	case buttonEvent:
		e, _ := e.AsButton()
		if e.Button != f.btn {
			return Event{}, false
		}
	}
	return e, true
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

func (s SDCardEvent) Event() Event {
	e := Event{typ: sdcardEvent}
	if s.Inserted {
		e.data[0] = 1
	}
	return e
}

func (s ScanEvent) Event() Event {
	e := Event{typ: scanEvent}
	e.refs[0] = s.Content
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

func (e Event) AsSDCard() (SDCardEvent, bool) {
	if e.typ != sdcardEvent {
		return SDCardEvent{}, false
	}
	return SDCardEvent{
		Inserted: e.data[0] != 0,
	}, true
}

func (e Event) AsScanEvent() (ScanEvent, bool) {
	if e.typ != scanEvent {
		return ScanEvent{}, false
	}
	return ScanEvent{
		Content: e.refs[0],
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
