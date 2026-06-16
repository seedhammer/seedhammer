package gui

import (
	"strings"
	"testing"

	"seedhammer.com/codex32"
)

// Drives the "Input Seed" menu to the CODEX32 choice (index 2) and enters a
// valid codex32 string on the keypad, asserting newInputFlow returns it.
//
// Without "CODEX32" in the menu this does NOT pass: with only two choices the
// Down-key selection caps at index 1 ("24 WORDS"), so the run enters 24-word
// BIP-39 input which never completes on a codex32 string and the test fails via
// the test timeout — it never reaches the codex32 path. With "CODEX32" added,
// index 2 routes to inputCodex32Flow, which returns the entered codex32 string.
//
// NOTE: the keypad stores typed runes UPPERCASE, so the returned string is the
// uppercase form of what we type; compare against strings.ToUpper(share).
func TestInputSeedCodex32(t *testing.T) {
	// A valid "ms" codex32 string from the codex32 package's own test corpus.
	const share = "ms10testsxxxxxxxxxxxxxxxxxxxxxxxxxx4nzvca9cmczlw"

	ctx := NewContext(newPlatform())
	// Menu: move the selection 0 -> 2 (CODEX32) with two Down presses, confirm
	// with Button3 (the ChoiceScreen "choose" button).
	click(&ctx.Router, Down, Down, Button3)
	// Keypad: type the share, then confirm with Button2 (OK).
	runes(&ctx.Router, share)
	click(&ctx.Router, Button2)

	obj, ok := newInputFlow(ctx, &descriptorTheme)
	if !ok {
		t.Fatal("newInputFlow did not return a value")
	}
	s, isCodex := obj.(codex32.String)
	if !isCodex {
		t.Fatalf("newInputFlow returned %T, want codex32.String", obj)
	}
	want := strings.ToUpper(share) // keypad uppercases typed runes
	if got := s.String(); got != want {
		t.Errorf("codex32 entry returned %q, want %q", got, want)
	}
}
