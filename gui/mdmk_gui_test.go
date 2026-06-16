package gui

import (
	"strings"
	"testing"
)

// TestMdmkEngrave checks that a valid md1 string lays out into engraving
// variants (the TEXT+QR / TEXT / QR-ONLY plates mdmkFlow offers).
func TestMdmkEngrave(t *testing.T) {
	ctx := NewContext(newPlatform())
	labels, plates, err := validateMdmk(ctx.Platform.EngraverParams(), "md1yqpqqxqq8xtwhw4xwn4qh")
	if err != nil {
		t.Fatal(err)
	}
	if len(plates) == 0 || len(labels) != len(plates) {
		t.Fatalf("got %d labels / %d plates, want >=1 of each", len(labels), len(plates))
	}
}

// TestMdmkOversizeRejected checks that an input too large for any plate (or for
// a QR code) is rejected with an error rather than producing a bad plate.
func TestMdmkOversizeRejected(t *testing.T) {
	ctx := NewContext(newPlatform())
	huge := "md1" + strings.Repeat("q", 5000)
	if _, _, err := validateMdmk(ctx.Platform.EngraverParams(), huge); err == nil {
		t.Error("expected error for oversize input (no plate fits / QR overflow)")
	}
}
