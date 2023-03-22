package mjolnir

import (
	"testing"

	"golang.org/x/image/math/f32"
)

func TestEndToEnd(t *testing.T) {
	s := NewSimulator()
	defer s.Close()

	prog := &Program{}
	design := func() {
		for i := 0; i < 2000; i++ {
			prog.Line(f32.Vec2{float32(i), float32(i) * 2})
			prog.Line(f32.Vec2{float32(i) * 4, float32(i) * 3})
			prog.Move(f32.Vec2{float32(i), float32(i)})
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
