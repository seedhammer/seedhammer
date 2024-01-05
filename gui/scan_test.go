package gui

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/bip32"
	"seedhammer.com/bip380"
	"seedhammer.com/bip39"
	"seedhammer.com/codex32"
)

func TestScan(t *testing.T) {
	c32, err := codex32.New("MS12NAMEA320ZYXWVUTSRQPNMLKJHGFEDCAXRPP870HKKQRM")
	if err != nil {
		t.Fatal(err)
	}
	b39, err := bip39.ParseMnemonic("legal winner thank year wave sausage worth useful legal winner thank yellow")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		Name    string
		Encoded string
		Err     error
		Content any
	}{
		{
			Name:    "Unknown Format",
			Encoded: "yadda yadda",
			Err:     errScanUnknownFormat,
		},
		{
			Name:    "Too Long",
			Encoded: strings.Repeat("TOOLONG", 8000),
			Err:     errScanOverflow,
		},
		{
			Name:    "Codex32",
			Encoded: c32.String(),
			Content: c32,
		},
		{
			Name:    "BIP39",
			Encoded: b39.String(),
			Content: b39,
		},
		{
			Name:    "Command",
			Encoded: "command: sudo-make-me-a-sandwich!",
			Content: debugCommand{"sudo-make-me-a-sandwich!"},
		},
		{
			Name:    "Descriptor",
			Encoded: "wpkh([dc567276/48h/0h/0h/2h]xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan/0/*)#ap6v6zth",
			Content: &bip380.Descriptor{
				Script:    bip380.P2WPKH,
				Threshold: 1,
				Type:      bip380.Singlesig,
				Keys: []bip380.Key{{
					Network:           &chaincfg.MainNetParams,
					MasterFingerprint: 0xdc567276,
					DerivationPath:    bip32.Path{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
					Children: []bip380.Derivation{
						{Index: 0x0},
						{Type: bip380.WildcardDerivation},
					},
					KeyData:           []uint8{0x2, 0x1c, 0xb, 0x47, 0x9e, 0xcf, 0x6e, 0x67, 0x71, 0x3d, 0xdf, 0xc, 0x43, 0xb6, 0x34, 0x59, 0x2f, 0x51, 0xc0, 0x37, 0xb6, 0xf9, 0x51, 0xfb, 0x1d, 0xc6, 0x36, 0x1a, 0x98, 0xb1, 0xe5, 0x73, 0x5e},
					ChainCode:         []uint8{0x6b, 0x3a, 0x4c, 0xfb, 0x6a, 0x45, 0xf6, 0x30, 0x5e, 0xfe, 0x6e, 0xe, 0x97, 0x6b, 0x5d, 0x26, 0xba, 0x27, 0xf7, 0xc3, 0x44, 0xd7, 0xfc, 0x7a, 0xbe, 0xf7, 0xbe, 0x2d, 0x6, 0xd5, 0x2d, 0xfd},
					ParentFingerprint: 0x18f8c2e7,
				}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			buf.WriteString(test.Encoded)
			s := new(scanner)
			for {
				got, err := s.Scan(buf)
				if err != nil || test.Err != nil {
					if err == errScanInProgress {
						continue
					}
					if !errors.Is(err, test.Err) {
						t.Fatalf("scanner failed: %v", err)
					}
				}
				if want := test.Content; !reflect.DeepEqual(got, want) {
					t.Errorf("scanner decoded\n%#v\nexpected\n%#v", got, want)
				}
				break
			}
		})
	}
}
