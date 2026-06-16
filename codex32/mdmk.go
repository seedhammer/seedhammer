package codex32

import "strings"

// md1 (HRP "md") and mk1 (HRP "mk") are sibling formats that reuse codex32's
// BCH machinery (the BIP-93 BCH(93,80,8) regular code and BCH(108,93,8) long
// code) with NUMS-derived target residues and a NON-codex32 initial residue.
//
// CRITICAL: the md/mk BCH initial residue is POLYMOD_INIT = 0x23181b3, NOT
// codex32's 1. Copying newShortChecksum's residue field and only swapping
// target would compute every checksum against the wrong starting state and
// silently mis-validate. The parity test (mdmk_test.go) against Rust-sourced
// golden vectors is the guard against that bug.
//
// This is a PURE verifier: it rejects corruption but performs no error
// correction (unlike mk-codec's decode_string, which auto-corrects up to t=4
// substitutions). The string is engraved verbatim; we only reject corruption.
//
// Constants (sources of truth, verified against the Rust codecs in recon and by
// the parity test; bit widths confirmed):
//
//	POLYMOD_INIT      = 0x23181b3             (26 bits) — both md-codec & mk-codec bch.rs
//	MD regular target = 0x0815c07747a3392e7   (64 bits) — descriptor-mnemonic md-codec bch.rs:17
//	MK regular target = 0x1062435f91072fa5c   (65 bits) — mnemonic-key consts.rs:18
//	MK long target    = 0x41890d7e441cbe97273 (75 bits) — mnemonic-key consts.rs:21
//
// 65- and 75-bit targets do not fit a single uint64, so they are passed to
// unpackSyms as a hi/lo uint64 pair:
//
//	MK regular: hi=0x1,   lo=0x62435f91072fa5c   (0x1<<64   | lo == 0x1062435f91072fa5c)
//	MK long:    hi=0x418, lo=0x90d7e441cbe97273  (0x418<<64 | lo == 0x41890d7e441cbe97273)
//
// md1 is regular-only (md-codec dropped the long code). TinyGo-safe: uint64 only,
// no math/big.

const (
	// mdmkPolymodInitLo is the md/mk BCH initial residue POLYMOD_INIT (< 2^64,
	// so hi is 0). It replaces codex32's initial residue of 1.
	mdmkPolymodInitLo uint64 = 0x23181b3

	mdmkShortSyms = 13 // regular code: 13-symbol checksum (BCH(93,80,8))
	mdmkLongSyms  = 15 // long code: 15-symbol checksum (BCH(108,93,8))

	// MK data-part length gate, from mk-codec's bch_code_for_length
	// (mnemonic-key string_layer/bch.rs:117): regular for 14..=93, long for
	// 96..=108, with 94..=95 and out-of-range lengths reserved-invalid.
	mkRegularMinLen = 14
	mkRegularMaxLen = 93
	mkLongMinLen    = 96
	mkLongMaxLen    = 108
)

// md/mk NUMS target residues, split into hi/lo uint64 pairs for unpackSyms.
const (
	mdRegularTargetHi uint64 = 0x0
	mdRegularTargetLo uint64 = 0x0815c07747a3392e7

	mkRegularTargetHi uint64 = 0x1
	mkRegularTargetLo uint64 = 0x62435f91072fa5c

	mkLongTargetHi uint64 = 0x418
	mkLongTargetLo uint64 = 0x90d7e441cbe97273
)

// unpackSyms returns n 5-bit GF(32) symbols of (hi<<64 | lo), MSB-first
// (symbol 0 = the top 5 bits). This matches the codex32 engine's residue/target
// layout, where index 0 is the highest power. hi is 0 for any value < 2^64.
func unpackSyms(hi, lo uint64, n int) []fe {
	out := make([]fe, n)
	for i := 0; i < n; i++ {
		shift := uint(5 * (n - 1 - i))
		var v uint64
		switch {
		case shift >= 64:
			v = hi >> (shift - 64)
		case shift == 0:
			v = lo
		default:
			v = (lo >> shift) | (hi << (64 - shift))
		}
		out[i] = fe(v & 0x1f)
	}
	return out
}

// verifyMDMK validates s against the given HRP, generator, and target residue,
// using the md/mk initial residue (POLYMOD_INIT) for an n-symbol checksum (13
// regular / 15 long). It mirrors codex32.New's verify path: split off the HRP,
// feed inputHRP + inputData (the engine decodes runes internally, so the data
// part is passed as a string, not a pre-decoded []fe), then check isValid.
// Pure verify, no error correction.
func verifyMDMK(s, hrp string, generator []fe, targetHi, targetLo uint64, n int) bool {
	gotHRP, data := splitHRP(s)
	// Case-insensitive HRP match, like codex32.New: a consistently upper- or
	// lower-cased string is valid; mixed case is rejected below by the engine.
	if !strings.EqualFold(gotHRP, hrp) {
		return false
	}
	// A valid string carries at least the n-symbol checksum in its data part.
	if len(data) < n {
		return false
	}
	e := &engine{
		generator: generator,
		residue:   unpackSyms(0, mdmkPolymodInitLo, n), // POLYMOD_INIT — NOT codex32's 1
		target:    unpackSyms(targetHi, targetLo, n),
	}
	// Feed the ORIGINAL-cased HRP (not the lowercase literal) so the engine's
	// case state matches the data; this is what makes uppercase strings validate
	// and mixed-case ones fail (errInvalidCase), exactly like codex32.New.
	if err := e.inputHRP(gotHRP); err != nil {
		return false
	}
	if err := e.inputData(data); err != nil {
		return false
	}
	return e.isValid()
}

// ValidMD reports whether s is a structurally valid, BCH-correct md1 string.
// md1 uses the regular code only (md-codec dropped the long code). The data part
// must be at least the 13-symbol checksum; md-codec applies no further data-part
// length bracket (md-codec codex32.rs:144-155).
func ValidMD(s string) bool {
	return verifyMDMK(s, "md", newShortChecksum().generator,
		mdRegularTargetHi, mdRegularTargetLo, mdmkShortSyms)
}

// ValidMK reports whether s is a structurally valid, BCH-correct mk1 string,
// regular (13-symbol checksum) or long (15-symbol checksum). The regular/long
// selection and the rejection of out-of-bracket lengths follow mk-codec's
// bch_code_for_length (mnemonic-key string_layer/bch.rs:117), keyed on the
// data-part length (the chars after "mk1"). Lengths 94..=95 and anything
// outside 14..=108 are reserved-invalid and rejected here (not corrected),
// because this verifier does no error correction.
func ValidMK(s string) bool {
	_, data := splitHRP(s)
	switch n := len(data); {
	case n >= mkRegularMinLen && n <= mkRegularMaxLen:
		return verifyMDMK(s, "mk", newShortChecksum().generator,
			mkRegularTargetHi, mkRegularTargetLo, mdmkShortSyms)
	case n >= mkLongMinLen && n <= mkLongMaxLen:
		return verifyMDMK(s, "mk", newLongChecksum().generator,
			mkLongTargetHi, mkLongTargetLo, mdmkLongSyms)
	default:
		// 94..=95 reserved-invalid, or out of the BIP-93 valid range.
		return false
	}
}
