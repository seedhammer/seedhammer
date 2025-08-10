package bip85

import (
	"encoding/hex"
	"testing"
)

func TestEntropy(t *testing.T) {
	tests := []struct {
		pkey    string
		entropy string
	}{
		{
			"cca20ccb0e9a90feb0912870c3323b24874b0ca3d8018c4b96d0b97c0e82ded0",
			"efecfbccffea313214232d29e71563d941229afb4338c21f9517c41aaa0d16f00b83d2a09ef747e7a64e8e2bd5a14869e693da66ce94ac2da570ab7ee48618f7",
		},
	}
	for _, test := range tests {
		k, err := hex.DecodeString(test.pkey)
		if err != nil {
			t.Fatal(err)
		}
		e := Entropy(k)
		if got := hex.EncodeToString(e); got != test.entropy {
			t.Errorf("%s: derived entropy %s, want %s", test.pkey, got, test.entropy)
		}
	}
}
