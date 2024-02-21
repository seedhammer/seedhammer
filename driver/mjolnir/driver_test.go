package mjolnir

import (
	"image"
	"testing"

	"seedhammer.com/engrave"
)

func TestEndToEnd(t *testing.T) {
	s := NewSimulator()
	defer s.Close()

	design := func(yield func(engrave.Command)) {
		for i := 0; i < 2000; i++ {
			yield(engrave.Line(image.Pt(i, i*2)))
			yield(engrave.Line(image.Pt(i*4, i*3)))
			yield(engrave.Move(image.Pt(i, i)))
		}
	}
	if err := Engrave(s, Options{}, design, nil); err != nil {
		t.Error(err)
	}
}
