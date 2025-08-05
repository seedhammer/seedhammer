package codex32

import (
	"strings"
	"testing"
)

func TestNumericString(t *testing.T) {
	s := new(strings.Builder)
	for e := range fe(32) {
		s.WriteByte(e.rune())
	}
	const want = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	if got := s.String(); got != want {
		t.Errorf("elements [0..32) encoded to %q, expected %q", got, want)
	}
}

func TestTranslationWheelMul(t *testing.T) {
	// Produce the translation wheel by multiplying.
	const logbase = fe(20)
	init := fe(1)
	s := new(strings.Builder)
	for range 31 {
		s.WriteByte(init.rune())
		init = init.Mul(logbase)
	}
	// Can be verified against the multiplication disc, starting with P and moving
	// clockwise.
	const mulDisc = "p529kt3uw8hlmecvxr470na6djfsgyz"
	if got := s.String(); got != mulDisc {
		t.Errorf("multiplication disc: %q, expected %s", got, mulDisc)
	}
}

func TestTranslationWheelDiv(t *testing.T) {
	// Produce the translation wheel by division.
	const logbase = fe(20)
	init := fe(1)
	s := new(strings.Builder)
	for range 31 {
		s.WriteByte(init.rune())
		init = init.Div(logbase)
	}
	// Same deal as the multiplication disc, but counterclockwise.
	const divDisc = "pzygsfjd6an074rxvcemlh8wu3tk925"
	if got := s.String(); got != divDisc {
		t.Errorf("division disc: %q, expected %s", got, divDisc)
	}
}

func TestRecoveryWheel(t *testing.T) {
	// Remarkably, the recovery wheel can be produced in the same way as the
	// multiplication wheel, though with a different log base and with every
	// element added by S.
	//
	// We spent quite some time deriving this, but honestly we probably could've
	// just guessed it if we'd known a priori that a wheel existed.
	const logbase = fe(10)
	init := fe(1)
	s := new(strings.Builder)
	for range 31 {
		s.WriteByte(init.Add(16).rune())
		init = init.Mul(logbase)
	}
	// To verify, start with 3 and move clockwise on the Recovery Wheel
	const recDisc = "36xp78tgk9ldaecjy4mvh0funwr2zq5"
	if got := s.String(); got != recDisc {
		t.Errorf("recovery disc: %q, expected %s", got, recDisc)
	}
}
