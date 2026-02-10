// Package seedqr encodes and decodes [SeedQR] and CompactSeedQR formats.
//
// [SeedQR]: https://github.com/SeedSigner/seedsigner/blob/dev/docs/seed_qr/README.md
package seedqr

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"seedhammer.com/bip39"
)

func Parse(qr []byte) (bip39.Mnemonic, bool) {
	if seed, ok := parseSeedQR(string(qr)); ok {
		return seed, true
	}
	return parseCompactSeedQR(qr)
}

// QR encodes a bip39 menmonic into the SeedQR format.
// It panics if m is invalid.
func QR(m bip39.Mnemonic) []byte {
	if !m.Valid() {
		panic("invalid mnemonic")
	}
	var qr bytes.Buffer
	for _, w := range m {
		fmt.Fprintf(&qr, "%04d", w)
	}
	return qr.Bytes()
}

// CompactQR encodes a bip39 mnemonic into the CompactSeedQR format.
// It panics if m is invalid.
func CompactQR(m bip39.Mnemonic) []byte {
	if !m.Valid() {
		panic("invalid mnemonic")
	}
	return m.Entropy()
}

func parseSeedQR(qr string) (bip39.Mnemonic, bool) {
	if len(qr)%4 != 0 {
		return nil, false
	}
	m := make(bip39.Mnemonic, len(qr)/4)
	for i := range m {
		word, err := strconv.ParseUint(qr[i*4:(i+1)*4], 10, 16)
		if err != nil {
			return nil, false
		}
		m[i] = bip39.Word(word)
	}
	if !m.Valid() {
		return nil, false
	}
	return m, true
}

func parseCompactSeedQR(qr []byte) (bip39.Mnemonic, bool) {
	switch len(qr) {
	case 128 / 8, 256 / 8:
	default:
		return nil, false
	}
	bits := len(qr) * 8
	checksum := bits / 32
	n := (bits + checksum) / 11
	var buf strings.Builder
	for _, b := range qr {
		buf.WriteString(fmt.Sprintf("%.8b", b))
	}
	for range checksum {
		buf.WriteRune('0')
	}
	bitstream := buf.String()
	m := make(bip39.Mnemonic, n)
	for i := range m {
		w, err := strconv.ParseUint(bitstream[i*11:(i+1)*11], 2, 16)
		if err != nil {
			return nil, false
		}
		m[i] = bip39.Word(w)
	}
	return m.FixChecksum(), true
}
