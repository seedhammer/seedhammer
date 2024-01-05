package gui

import (
	"slices"
	"testing"
)

func TestEventOrdering(t *testing.T) {
	r := new(EventRouter)
	r.Events(nil,
		ButtonEvent{
			Button:  Button3,
			Pressed: true,
		}.Event(),
		ButtonEvent{
			Button:  Button2,
			Pressed: true,
		}.Event(),
		ButtonEvent{
			Button:  Button1,
			Pressed: true,
		}.Event(),
	)
	var order []Button
	for {
		for _, btn := range []Button{Button1, Button3} {
			if _, ok := r.Next(ButtonFilter(btn)); ok {
				order = append(order, btn)
			}
		}
		r.Reset()
		if len(r.events) == 0 {
			break
		}
	}
	want := []Button{Button3, Button1}
	if !slices.Equal(order, want) {
		t.Errorf("got ordering %v, expected %v", order, want)
	}
}

func click(r *EventRouter, bs ...Button) {
	for _, b := range bs {
		r.Events(nil,
			ButtonEvent{
				Button:  b,
				Pressed: true,
			}.Event(),
			ButtonEvent{
				Button:  b,
				Pressed: false,
			}.Event(),
		)
	}
}

func press(r *EventRouter, bs ...Button) {
	for _, b := range bs {
		r.Events(nil,
			ButtonEvent{
				Button:  b,
				Pressed: true,
			}.Event(),
		)
	}
}

func runes(r *EventRouter, str string) {
	for _, rn := range str {
		r.Events(nil,
			RuneEvent{
				Rune: rn,
			}.Event(),
		)
	}
}
