package mjolnir

import (
	"image"
	"testing"

	"seedhammer.com/engrave"
)

func TestEndToEnd(t *testing.T) {
	s := NewSimulator()
	defer s.Close()

	design := func(yield func(engrave.Command) bool) {
		for i := 0; i < 2000; i++ {
			cont := yield(engrave.Line(image.Pt(i, i*2))) &&
				yield(engrave.Line(image.Pt(i*4, i*3))) &&
				yield(engrave.Move(image.Pt(i, i)))
			if !cont {
				return
			}
		}
	}
	if err := Engrave(s, Options{}, design, nil); err != nil {
		t.Error(err)
	}
}
