package xoshiro256

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestGenerator(t *testing.T) {
	tests := []struct {
		seed string
		want string
	}{
		{
			"ea858afbf837aae714617e89a36524aced28f7de921f7798e72810fd8839a462",
			"2a51550852544c494658024a28304d36580705582519520d453b1e270b5213632d571e0f2016592c5c4d1d4e045c2c445c45012a593225543f222003113e28625259182b55270f03631d142a1b0a554232234546464a1e0d48360b0546375b340a2b2b34",
		},
		{
			"530c1f0542883298051e4efa4adbf209c7f9d8e794fb62fd3fd4b48739694080",
			"582c5e4a0063074d44232f4e1315320f2a245b0b55274016390b190c015b114b1d2f580b443a1b4115362f364953173a4b1b1a0f3c241e1537394d4c4b2f354c095b0e45035f0b491463443d0362246238410e504a393f4433381827355039335103011e",
		},
	}
	for _, test := range tests {
		seed, err := hex.DecodeString(test.seed)
		if err != nil {
			t.Fatal(err)
		}
		want, err := hex.DecodeString(test.want)
		if err != nil {
			t.Fatal(err)
		}
		var s Source
		s.Seed(([32]byte)(seed))
		got := make([]byte, len(want))
		for i := 0; i < len(want); i++ {
			got[i] = byte(s.Uint64() % 100)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("unexpected random number sequence for seed %x", seed)
		}
	}
}
