package codex32

import (
	"bytes"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

func TestBIPVector1(t *testing.T) {
	const secret = "ms10testsxxxxxxxxxxxxxxxxxxxxxxxxxx4nzvca9cmczlw"
	c32 := mustFromString(t, secret)
	// Don't test the separator "1" which is not stored anywhere.

	// Don't check master node xpriv; this is implied by the master seed
	// and would require extra dependencies to compute.

	want := &parts{
		hrp:       "ms",
		threshold: 0,
		shareIdx:  feS,
		id:        "test",
		payload:   "xxxxxxxxxxxxxxxxxxxxxxxxxx",
		checksum:  "4nzvca9cmczlw",
	}

	got := c32.parts()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s split into\n%+v\nexpected\n%+v", secret, got, want)
	}
	const wantData = "318c6318c6318c6318c6318c6318c631"
	data := hex.EncodeToString(got.data())
	if wantData != data {
		t.Errorf("data %s, want %s", data, wantData)
	}
}

func mustFromString(t *testing.T, s string) String {
	t.Helper()
	cs, err := New(s)
	if err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return cs
}

func mustInterpolateAt(t *testing.T, shares []String, target rune) String {
	t.Helper()
	s, err := Interpolate(shares, target)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBIPVector2(t *testing.T) {
	shares := []String{
		mustFromString(t, "MS12NAMEA320ZYXWVUTSRQPNMLKJHGFEDCAXRPP870HKKQRM"),
		mustFromString(t, "MS12NAMECACDEFGHJKLMNPQRSTUVWXYZ023FTR2GDZMPY6PN"),
	}

	gotShareD := mustInterpolateAt(t, shares, 'D')
	const shareD = "MS12NAMEDLL4F8JLH4E5VDVULDLFXU2JHDNLSM97XVENRXEG"
	if gotShareD.String() != shareD {
		t.Errorf("interpolated share D %s, expected %s", gotShareD, shareD)
	}

	gotSeed := mustInterpolateAt(t, shares, 'S')
	const seed = "MS12NAMES6XQGUZTTXKEQNJSJZV4JV3NZ5K3KWGSPHUH6EVW"
	if seed != gotSeed.String() {
		t.Errorf("interpolated seed %s, expected %s", gotSeed, seed)
	}
	p := gotSeed.parts()
	const seedHex = "d1808e096b35b209ca12132b264662a5"
	if got := hex.EncodeToString(p.data()); got != seedHex {
		t.Errorf("got data %s, expected %s", got, seedHex)
	}
}

func TestBIPVector3(t *testing.T) {
	shares := []String{
		mustFromString(t, "ms13cashsllhdmn9m42vcsamx24zrxgs3qqjzqud4m0d6nln"),
		mustFromString(t, "ms13casha320zyxwvutsrqpnmlkjhgfedca2a8d0zehn8a0t"),
		mustFromString(t, "ms13cashcacdefghjklmnpqrstuvwxyz023949xq35my48dr"),
	}

	tests := []struct {
		target rune
		share  string
	}{
		{'D', "ms13cashd0wsedstcdcts64cd7wvy4m90lm28w4ffupqs7rm"},
		{'E', "ms13casheekgpemxzshcrmqhaydlp6yhms3ws7320xyxsar9"},
		{'F', "ms13cashf8jh6sdrkpyrsp5ut94pj8ktehhw2hfvyrj48704"},
	}
	for _, test := range tests {
		share := mustInterpolateAt(t, shares, test.target)
		if share.String() != test.share {
			t.Errorf("share %v: %s, expected %s", test.target, share, test.share)
		}
	}
}

func TestBIPVector4(t *testing.T) {
	seedB, err := hex.DecodeString("ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100")
	if err != nil {
		t.Fatal(err)
	}
	seed, err := NewSeed("ms", 0, "leet", 'S', seedB)
	if err != nil {
		t.Fatal(err)
	}
	const wantSeed = "ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqqtum9pgv99ycma"
	if seed.String() != wantSeed {
		t.Errorf("got seed %s, want %s", seed, wantSeed)
	}
	// Our code sticks 0s onto the bitstring to get a multiple of 5 bits. Confirm that
	// other choices would've worked.
	data := seed.parts().data()
	if !bytes.Equal(data, seedB) {
		t.Errorf("decoded seed %x, want %x", data, seedB)
	}
	altEncodings := []string{
		wantSeed,
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqpj82dp34u6lqtd",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqzsrs4pnh7jmpj5",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqrfcpap2w8dqezy",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqy5tdvphn6znrf0",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyq9dsuypw2ragmel",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqx05xupvgp4v6qx",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyq8k0h5p43c2hzsk",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqgum7hplmjtr8ks",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqf9q0lpxzt5clxq",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyq28y48pyqfuu7le",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqt7ly0paesr8x0f",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqvrvg7pqydv5uyz",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqd6hekpea5n0y5j",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqwcnrwpmlkmt9dt",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyq0pgjxpzx0ysaam",
	}
	for _, alt := range altEncodings {
		seed := mustFromString(t, alt)
		data := seed.parts().data()
		if !bytes.Equal(data, seedB) {
			t.Errorf("%s: decoded seed %x, want %x", alt, data, seedB)
		}
	}
}

func TestBIPVector5(t *testing.T) {
	longSeed := mustFromString(t,
		"MS100C8VSM32ZXFGUHPCHTLUPZRY9X8GF2TVDW0S3JN54KHCE6MUA7LQPZYGSFJD6AN074RXVCEMLH8WU3TK925ACDEFGHJKLMNPQRSTUVWXY06FHPV80UNDVARHRAK",
	)
	const want = "dc5423251cb87175ff8110c8531d0952d8d73e1194e95b5f19d6f9df7c01111104c9baecdfea8cccc677fb9ddc8aec5553b86e528bcadfdcc201c17c638c47e9"
	got := hex.EncodeToString(longSeed.parts().data())
	if got != want {
		t.Errorf("got seed %s, want %s", got, want)
	}
}

func TestBIPBadChecksums(t *testing.T) {
	badChecksums := []string{
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxve740yyge2ghq",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxve740yyge2ghp",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxlk3yepcstwr",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxx6pgnv7jnpcsp",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxx0cpvr7n4geq",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxm5252y7d3lr",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxrd9sukzl05ej",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxc55srw5jrm0",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxgc7rwhtudwc",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxx4gy22afwghvs",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxme084q0vpht7pe0",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxme084q0vpht7pew",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxqyadsp3nywm8a",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxzvg7ar4hgaejk",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxcznau0advgxqe",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxch3jrc6j5040j",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx52gxl6ppv40mcv",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx7g4g2nhhle8fk",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx63m45uj8ss4x8",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxy4r708q7kg65x",
	}
	for _, chk := range badChecksums {
		_, err := New(chk)
		if !errors.Is(err, errInvalidChecksum) {
			t.Errorf("%s parsed with error %v, expected %v", chk, err, errInvalidChecksum)
		}
	}
}

func TestBIPWrongChecksums(t *testing.T) {
	wrongChecksums := []string{
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxurfvwmdcmymdufv",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxcsyppjkd8lz4hx3",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxu6hwvl5p0l9xf3c",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxwqey9rfs6smenxa",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxv70wkzrjr4ntqet",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx3hmlrmpa4zl0v",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxrfggf88znkaup",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxpt7l4aycv9qzj",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxus27z9xtyxyw3",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxcwm4re8fs78vn",
	}
	for _, chk := range wrongChecksums {
		_, err := New(chk)
		if !errors.Is(err, errInvalidChecksum) && !errors.Is(err, errInvalidLength) {
			t.Errorf("%s parsed with error %v, expected %v or %v", chk, err, errInvalidChecksum, errInvalidLength)
		}
	}
}

func TestBIPInvalidImproperLength(t *testing.T) {
	badLength := []string{
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxw0a4c70rfefn4",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxk4pavy5n46nea",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxx9lrwar5zwng4w",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxr335l5tv88js3",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxvu7q9nz8p7dj68v",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxpq6k542scdxndq3",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxkmfw6jm270mz6ej",
		"ms12fauxxxxxxxxxxxxxxxxxxxxxxxxxxzhddxw99w7xws",
		"ms12fauxxxxxxxxxxxxxxxxxxxxxxxxxxxx42cux6um92rz",
		"ms12fauxxxxxxxxxxxxxxxxxxxxxxxxxxxxxarja5kqukdhy9",
		"ms12fauxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxky0ua3ha84qk8",
		"ms12fauxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx9eheesxadh2n2n9",
		"ms12fauxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx9llwmgesfulcj2z",
		"ms12fauxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx02ev7caq6n9fgkf",
	}
	for _, chk := range badLength {
		_, err := New(chk)
		if !errors.Is(err, errInvalidLength) && !errors.Is(err, errIncompleteGroup) {
			t.Errorf("%s parsed with error %v, expected %v or %v", chk, err, errInvalidLength, errIncompleteGroup)
		}
	}
}

func TestBIPInvalidShareIndex(t *testing.T) {
	invalids := []string{
		"ms10fauxxxxxxxxxxxxxxxxxxxxxxxxxxxx0z26tfn0ulw3p",
	}
	for _, chk := range invalids {
		_, err := New(chk)
		if !errors.Is(err, errInvalidShareIndex) {
			t.Errorf("%s parsed with error %v, expected %v", chk, err, errInvalidShareIndex)
		}
	}
}

func TestBIPInvalidThreshold(t *testing.T) {
	invalids := []string{
		"ms1fauxxxxxxxxxxxxxxxxxxxxxxxxxxxxxda3kr3s0s2swg",
	}
	for _, chk := range invalids {
		_, err := New(chk)
		if !errors.Is(err, errInvalidThreshold) {
			t.Errorf("%s parsed with error %v, expected %v", chk, err, errInvalidThreshold)
		}
	}
}

// Skip tho "missing ms prefix" tests because this library is HRP-agnostic
// FIXME it probably should not be, and should probably enforce the ms

func TestBIPInvalidCase(t *testing.T) {
	badCase := []string{
		"Ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxuqxkk05lyf3x2",
		"mS10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxuqxkk05lyf3x2",
		"MS10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxuqxkk05lyf3x2",
		"ms10FAUXsxxxxxxxxxxxxxxxxxxxxxxxxxxuqxkk05lyf3x2",
		"ms10fauxSxxxxxxxxxxxxxxxxxxxxxxxxxxuqxkk05lyf3x2",
		"ms10fauxsXXXXXXXXXXXXXXXXXXXXXXXXXXuqxkk05lyf3x2",
		"ms10fauxsxxxxxxxxxxxxxxxxxxxxxxxxxxUQXKK05LYF3X2",
	}
	for _, chk := range badCase {
		_, err := New(chk)
		if !errors.Is(err, errInvalidCase) {
			t.Errorf("%s parsed with error %v, expected %v", chk, err, errInvalidCase)
		}
	}
}
