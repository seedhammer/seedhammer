package mjolnir

import (
	"image"
	"testing"

	"seedhammer.com/engrave"
)

func TestEndToEnd(t *testing.T) {
	s := NewSimulator()
	defer s.Close()

	prog := &Program{}
	design := func() {
		for i := 0; i < 2000; i++ {
			prog.Command(engrave.Line(image.Pt(i, i*2)))
			prog.Command(engrave.Line(image.Pt(i*4, i*3)))
			prog.Command(engrave.Move(image.Pt(i, i)))
		}
	}
	design()
	prog.Prepare()
	engraveErr := make(chan error)
	go func() {
		engraveErr <- Engrave(s, prog, nil, nil)
	}()
	design()
	if err := <-engraveErr; err != nil {
		t.Error(err)
	}
}
