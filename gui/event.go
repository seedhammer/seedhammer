package gui

import "image"

type Button int

const (
	Up Button = iota
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

func SDCardFilter() Filter {
	return Filter{
		typ: sdcardEvent,
	}
}

type FrameEvent struct {
	Error error
	Image image.Image
}

type Event struct {
	typ  eventKind
	data [2]uint32
	refs [2]any
}

type Filter struct {
	typ eventKind
	btn Button
}

type filterKind int

type eventKind int

const (
	buttonEvent eventKind = 1 + iota
	sdcardEvent
	frameEvent
	runeEvent
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

func (f Filter) matches(e Event) bool {
	if f.typ != e.typ {
		return false
	}
	switch f.typ {
	case buttonEvent:
		e, _ := e.AsButton()
		return e.Button == f.btn
	}
	return true
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

func (e Event) AsSDCard() (SDCardEvent, bool) {
	if e.typ != sdcardEvent {
		return SDCardEvent{}, false
	}
	return SDCardEvent{
		Inserted: e.data[0] != 0,
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
