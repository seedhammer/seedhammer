package codex32

import (
	"strings"
	"testing"
)

// Golden vectors are RUST-SOURCED (md-codec 0.36 / mk-codec v0.1.json), never
// Go-self-generated — they are the only guard against a wrong initial residue
// (POLYMOD_INIT vs codex32's 1). mk1Long has a 108-char data part (the 15-symbol
// long code, BCH(108,93,8)); mk1Regular has a 77-char data part (regular code).
const (
	md1Regular = "md1yqpqqxqq8xtwhw4xwn4qh"
	mk1Regular = "mk1qpzg69ppsnz4v7cjv3qfjhf76k4t5pt96u0psdrqfqvll8qh7h5athg837pmkf3dpug2mmjtfel6x"
	mk1Long    = "mk1qp0zgpzp3xqgpqqgqjyty8ssyqcq0tdd4kk6mtdd4kk6mtdd4kk6mtdd4kk6mtdd4kk6mtdd4kk6mtddq2vfczmkedtrj2rjl6la2h9ek48q"
)

func TestMDMKValid(t *testing.T) {
	if !ValidMD(md1Regular) {
		t.Error("ValidMD rejected a valid md1 (check POLYMOD_INIT / md target)")
	}
	if !ValidMK(mk1Regular) {
		t.Error("ValidMK rejected a valid regular mk1 (check mk regular target hi/lo)")
	}
	if !ValidMK(mk1Long) {
		t.Error("ValidMK rejected a valid long mk1 (check long code path + mk long target hi/lo)")
	}
}

func TestMDMKWrongHRP(t *testing.T) {
	if ValidMD(mk1Regular) {
		t.Error("ValidMD accepted an mk1 string")
	}
	if ValidMK(md1Regular) {
		t.Error("ValidMK accepted an md1 string")
	}
}

func TestMDMKRejectsTamper(t *testing.T) {
	// A single flipped data symbol must be REJECTED — this is a pure verifier,
	// no error correction (unlike mk-codec's decode_string, which auto-corrects).
	flipLast := func(s string) string {
		b := []byte(s)
		i := len(b) - 1
		if b[i] == 'q' {
			b[i] = 'p'
		} else {
			b[i] = 'q'
		}
		return string(b)
	}
	if ValidMD(flipLast(md1Regular)) {
		t.Error("tampered md1 accepted")
	}
	if ValidMK(flipLast(mk1Regular)) {
		t.Error("tampered mk1 accepted")
	}
	if ValidMK(flipLast(mk1Long)) {
		t.Error("tampered long mk1 accepted")
	}
}

func TestMDMKRejectsAllZeros(t *testing.T) {
	// All-"q" (zero) data of a valid regular length must not self-validate
	// (NUMS-derived target — anti-trivial property).
	mdDataLen := len(md1Regular) - 3
	if ValidMD("md1" + strings.Repeat("q", mdDataLen)) {
		t.Error("all-zero md1 self-validated")
	}
	mkDataLen := len(mk1Regular) - 3
	if ValidMK("mk1" + strings.Repeat("q", mkDataLen)) {
		t.Error("all-zero mk1 self-validated")
	}
}

func TestMDMKLengthBracket(t *testing.T) {
	// Data-part lengths in the reserved gap (94..95) or out of range are rejected.
	if ValidMK("mk1" + strings.Repeat("q", 94)) {
		t.Error("mk1 with reserved-gap (94) data length accepted")
	}
	if ValidMK("mk1" + strings.Repeat("q", 5)) {
		t.Error("mk1 with too-short data length accepted")
	}
}

func TestMDMKNoPanicOnMalformed(t *testing.T) {
	// verifyMDMK must REJECT (return false) and never panic on malformed input:
	// no separator, empty, empty/short data, invalid bech32 chars (b/i/o/!),
	// mixed case, and over-long strings.
	for _, s := range []string{
		"", "1", "md1", "mk1", "noseparator", "md", "mk",
		"md1!!!", "mk1qqq i o b",
		"md1" + strings.Repeat("q", 20) + "b", // len-OK data with an invalid char
		"MD1qPqQ",                             // mixed case
		"md1" + strings.Repeat("q", 300),      // very long
		"mk1" + strings.Repeat("q", 300),
	} {
		if ValidMD(s) {
			t.Errorf("ValidMD(%q) = true, want false", s)
		}
		if ValidMK(s) {
			t.Errorf("ValidMK(%q) = true, want false", s)
		}
	}
}
