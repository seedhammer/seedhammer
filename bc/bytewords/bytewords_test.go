package bytewords

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	want := make([]byte, 255)
	for i := range want {
		want[i] = byte(i)
	}
	enc := Encode(want)
	got, err := Decode(enc)
	if err != nil {
		t.Fatalf("%v encoded to %s, but failed to decode: %v", want, enc, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%v encoded to %s, but roundtripped to %v", want, enc, got)
	}
}

func TestEncoding(t *testing.T) {
	tests := []struct {
		bw      string
		wanthex string
		error   bool
	}{
		{"aeadaolazmjendeoti", "00010280ff", false},
		{"taaddwoeadgdstaslplabghydrpfmkbggufgludprfgmaotpiecffltntddwgmrp", "d9012ca20150c7098580125e2ab0981253468b2dbc5202d8641947da", false},
		// Bad checksum.
		{"taaddwoeadgdstaslplabghydrpfmkbggufgludprfgmaotpiecffltntddwgmrs", "", true},
		{"", "", true},
	}
	for _, test := range tests {
		got, err := Decode(test.bw)
		if err != nil {
			if !test.error {
				t.Errorf("failed to decode %q: %v", test.bw, err)
			}
		} else {
			if test.error {
				t.Errorf("unexpected successful decoding of %q", test.bw)
			}
		}
		if test.error {
			continue
		}
		want, err := hex.DecodeString(test.wanthex)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("decoding %q got %#x, expected %#x", test.bw, got, want)
		}
		roundtrip := Encode(want)
		if roundtrip != test.bw {
			t.Errorf("encoding %s got %s, expected %s", test.wanthex, roundtrip, test.bw)
		}
	}
}
