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
		t.Error("expected error for oversize input (QR overflow)")
	}
}

// TestMdmkNoModeFitsRejected covers the no-plate-fits branch: ~1200 chars is
// within QR byte capacity (so qr.Encode succeeds) but far too long to fit any
// plate, so validateMdmk returns the toPlate error — distinct from the
// QR-overflow path above.
func TestMdmkNoModeFitsRejected(t *testing.T) {
	ctx := NewContext(newPlatform())
	big := "md1" + strings.Repeat("q", 1200)
	if _, _, err := validateMdmk(ctx.Platform.EngraverParams(), big); err == nil {
		t.Error("expected error: no engraving variant fits a plate")
	}
}
