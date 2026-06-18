package bip39

import (
	"bytes"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
)

func TestVectors(t *testing.T) {
	for _, v := range testVectors {
		m, err := ParseMnemonic(v.mnemonic)
		if err != nil {
			t.Fatalf("ParseMnemonic failed to parse %q: %v", v.mnemonic, err)
		}
		e, err := hex.DecodeString(v.entropy)
		if err != nil {
			t.Error(err)
		}
		ent, check := splitMnemonic(m)
		if !bytes.Equal(e, ent) {
			t.Errorf("entropy mismatch")
		}
		if want := checksum(ent); want != check {
			t.Errorf("checksum mismatch, got %d, want %d", check, want)
		}
		checkWord := m[len(m)-1]
		if want := ChecksumWord(ent); want != checkWord {
			t.Errorf("checksum word mismatch, got %d, want %d", checkWord, want)
		}
		m2, err := Parse([]byte(v.mnemonic))
		if err != nil {
			t.Fatalf("Parse failed to parse %q: %v", v.mnemonic, err)
		}
		if !slices.Equal(m, m2) {
			t.Fatalf("Parse parsed differently than ParseMnemonic for %q", v.mnemonic)
		}
		shortWords := new(bytes.Buffer)
		// Shorten words to 3 or 4 characters.
		for w := range strings.SplitSeq(v.mnemonic, " ") {
			if len(w) > 4 {
				w = w[:4]
			}
			if shortWords.Len() > 0 {
				shortWords.WriteByte(' ')
			}
			shortWords.WriteString(w)
		}
		sw := shortWords.Bytes()
		m3, err := Parse(sw)
		if err != nil {
			t.Fatalf("Parse failed to parse %q: %v", sw, err)
		}
		if !slices.Equal(m, m3) {
			t.Fatalf("Parse parsed differently than ParseMnemonic for %q", v.mnemonic)
		}
		m4 := New(ent)
		if got := m4.String(); got != v.mnemonic {
			t.Errorf("%s: round-tripped to %s", v.mnemonic, got)
		}
		swu := bytes.ToUpper(sw)
		m5, err := Parse(swu)
		if err != nil {
			t.Fatalf("Parse failed to parse %q: %v", swu, err)
		}
		if !slices.Equal(m, m5) {
			t.Fatalf("Parse parsed differently than ParseMnemonic for %q", v.mnemonic)
		}
		m6, err := ParseMnemonic(strings.ToUpper(v.mnemonic))
		if err != nil {
			t.Fatalf("ParseMnemonic failed to parse %q: %v", swu, err)
		}
		if !slices.Equal(m, m6) {
			t.Fatalf("Parse parsed differently than ParseMnemonic for %q", v.mnemonic)
		}
	}
}

func TestInvalidSeeds(t *testing.T) {
	tests := []string{
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon",
	}
	for _, test := range tests {
		if _, err := ParseMnemonic(test); err == nil {
			t.Errorf("ParseMnemonic parsed invalid seed %q", test)
		}
		if _, err := Parse([]byte(test)); err == nil {
			t.Errorf("Parse parsed invalid seed %q", test)
		}
	}
}

func TestChecksumWord(t *testing.T) {
	mnemonic := make(Mnemonic, 12)
	for range int(1e4) {
		for j := range mnemonic {
			mnemonic[j] = RandomWord()
		}
		want, _ := splitMnemonic(mnemonic)
		got := mnemonic.FixChecksum().Entropy()
		if !bytes.Equal(want, got) {
			t.Errorf("checksum word changed the entropy")
		}
	}
}

var testVectors = []struct {
	entropy  string
	mnemonic string
}{
	{
		entropy:  "00000000000000000000000000000000",
		mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
	},
	{
		entropy:  "7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f",
		mnemonic: "legal winner thank year wave sausage worth useful legal winner thank yellow",
	},
	{
		entropy:  "80808080808080808080808080808080",
		mnemonic: "letter advice cage absurd amount doctor acoustic avoid letter advice cage above",
	},
	{
		entropy:  "ffffffffffffffffffffffffffffffff",
		mnemonic: "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong",
	},
	{
		entropy:  "000000000000000000000000000000000000000000000000",
		mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon agent",
	},
	{
		entropy:  "7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f",
		mnemonic: "legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal will",
	},
	{
		entropy:  "808080808080808080808080808080808080808080808080",
		mnemonic: "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic avoid letter always",
	},
	{
		entropy:  "ffffffffffffffffffffffffffffffffffffffffffffffff",
		mnemonic: "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo when",
	},
	{
		entropy:  "0000000000000000000000000000000000000000000000000000000000000000",
		mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art",
	},
	{
		entropy:  "7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f",
		mnemonic: "legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth title",
	},
	{
		entropy:  "8080808080808080808080808080808080808080808080808080808080808080",
		mnemonic: "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic bless",
	},
	{
		entropy:  "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		mnemonic: "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo vote",
	},
	{
		entropy:  "9e885d952ad362caeb4efe34a8e91bd2",
		mnemonic: "ozone drill grab fiber curtain grace pudding thank cruise elder eight picnic",
	},
	{
		entropy:  "6610b25967cdcca9d59875f5cb50b0ea75433311869e930b",
		mnemonic: "gravity machine north sort system female filter attitude volume fold club stay feature office ecology stable narrow fog",
	},
	{
		entropy:  "68a79eaca2324873eacc50cb9c6eca8cc68ea5d936f98787c60c7ebc74e6ce7c",
		mnemonic: "hamster diagram private dutch cause delay private meat slide toddler razor book happy fancy gospel tennis maple dilemma loan word shrug inflict delay length",
	},
	{
		entropy:  "c0ba5a8e914111210f2bd131f3d5e08d",
		mnemonic: "scheme spot photo card baby mountain device kick cradle pact join borrow",
	},
	{
		entropy:  "6d9be1ee6ebd27a258115aad99b7317b9c8d28b6d76431c3",
		mnemonic: "horn tenant knee talent sponsor spell gate clip pulse soap slush warm silver nephew swap uncle crack brave",
	},
	{
		entropy:  "9f6a2878b2520799a44ef18bc7df394e7061a224d2c33cd015b157d746869863",
		mnemonic: "panda eyebrow bullet gorilla call smoke muffin taste mesh discover soft ostrich alcohol speed nation flash devote level hobby quick inner drive ghost inside",
	},
	{
		entropy:  "23db8160a31d3e0dca3688ed941adbf3",
		mnemonic: "cat swing flag economy stadium alone churn speed unique patch report train",
	},
	{
		entropy:  "8197a4a47f0425faeaa69deebc05ca29c0a5b5cc76ceacc0",
		mnemonic: "light rule cinnamon wrap drastic word pride squirrel upgrade then income fatal apart sustain crack supply proud access",
	},
	{
		entropy:  "066dca1a2bb7e8a1db2832148ce9933eea0f3ac9548d793112d9a95c9407efad",
		mnemonic: "all hour make first leader extend hole alien behind guard gospel lava path output census museum junior mass reopen famous sing advance salt reform",
	},
	{
		entropy:  "f30f8c1da665478f49b001d94c5fc452",
		mnemonic: "vessel ladder alter error federal sibling chat ability sun glass valve picture",
	},
	{
		entropy:  "c10ec20dc3cd9f652c7fac2f1230f7a3c828389a14392f05",
		mnemonic: "scissors invite lock maple supreme raw rapid void congress muscle digital elegant little brisk hair mango congress clump",
	},
	{
		entropy:  "f585c11aec520db57dd353c69554b21a89b20fb0650966fa0a9d6f74fd989d8f",
		mnemonic: "void come effort suffer camp survey warrior heavy shoot primary clutch crush open amazing screen patrol group space point ten exist slush involve unfold",
	},
}

func TestLastWordCandidates(t *testing.T) {
	build := func(n int) Mnemonic {
		m := make(Mnemonic, n)
		for i := range m {
			m[i] = Word(i % int(NumWords))
		}
		return m.FixChecksum()
	}

	// 24-word: exactly 8 candidates, all valid, including the real last word.
	v24 := build(24)
	c24 := LastWordCandidates(v24)
	if len(c24) != 8 {
		t.Fatalf("24-word: got %d candidates, want 8", len(c24))
	}
	foundLast := false
	for _, w := range c24 {
		m := make(Mnemonic, len(v24))
		copy(m, v24)
		m[len(m)-1] = w
		if !m.Valid() {
			t.Errorf("24-word candidate %d is not checksum-valid", w)
		}
		if w == v24[len(v24)-1] {
			foundLast = true
		}
	}
	if !foundLast {
		t.Errorf("24-word candidates %v do not include the real last word %d", c24, v24[len(v24)-1])
	}

	// 12-word: exactly 128 candidates.
	v12 := build(12)
	if c12 := LastWordCandidates(v12); len(c12) != 128 {
		t.Fatalf("12-word: got %d candidates, want 128", len(c12))
	}

	// Incomplete prefix (an earlier word unset) -> nil.
	bad := make(Mnemonic, len(v24))
	copy(bad, v24)
	bad[5] = -1
	if got := LastWordCandidates(bad); got != nil {
		t.Errorf("incomplete prefix: got %v, want nil", got)
	}

	// Unsupported length (len%3 != 0) -> nil.
	if got := LastWordCandidates(make(Mnemonic, 13)); got != nil {
		t.Errorf("len 13: got %v, want nil", got)
	}

	// Must not mutate the input's final slot.
	before := v24[len(v24)-1]
	_ = LastWordCandidates(v24)
	if v24[len(v24)-1] != before {
		t.Errorf("LastWordCandidates mutated input final slot: %d -> %d", before, v24[len(v24)-1])
	}
}

func TestDiceToWord(t *testing.T) {
	counts := make([]int, len(index))
	dice := Roll{1, 1, 1, 1, 1}
loop:
	for {
		word, valid := DiceToWord(dice)
		// Increment roll.
		for i := len(dice) - 1; ; i-- {
			if i < 0 {
				break loop
			}
			dice[i]++
			if dice[i] <= 6 {
				break
			}
			dice[i] = 1
		}
		if valid {
			counts[word]++
		}
	}
	for word, count := range counts {
		if count != 3 {
			t.Errorf("word %v chosen %d times, expected 3", word, count)
		}
	}
}
