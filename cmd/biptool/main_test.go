package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"seedhammer.com/codex32"
)

func TestRand(t *testing.T) {
	oldr := rand.Reader
	defer func() {
		rand.Reader = oldr
	}()
	randBytes := make([]byte, 10000)
	if _, err := io.ReadFull(rand.Reader, randBytes); err != nil {
		t.Fatal(err)
	}
	rand.Reader = bytes.NewReader(randBytes)
	const n = 32
	out := exec(t, nil, "rand -n %d", n)
	if !bytes.Equal(out, randBytes[:n]) {
		t.Errorf("command rand did not read from crypto/rand.Reader")
	}
}

func TestShare(t *testing.T) {
	tests := []string{
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqqtum9pgv99ycma",
		"ms13casha320zyxwvutsrqpnmlkjhgfedca2a8d0zehn8a0t",
		"ms13cashcacdefghjklmnpqrstuvwxyz023949xq35my48dr",
		"MS100C8VSM32ZXFGUHPCHTLUPZRY9X8GF2TVDW0S3JN54KHCE6MUA7LQPZYGSFJD6AN074RXVCEMLH8WU3TK925ACDEFGHJKLMNPQRSTUVWXY06FHPV80UNDVARHRAK",
		"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqd6hekpea5n0y5j",
	}
	for _, test := range tests {
		share, err := codex32.New(test)
		if err != nil {
			t.Fatal(err)
		}
		seed := share.Seed()
		id, thres, idx := share.Split()
		gotShare, err := codex32.New(string(bytes.TrimSpace(exec(t,
			seed,
			"seed -id %s -threshold %d -seedlen %d -idx %c",
			id, thres, len(seed), idx,
		))))
		if err != nil {
			t.Fatal(err)
		}
		gotSeed := gotShare.Seed()
		if !bytes.Equal(seed, gotSeed) {
			t.Errorf("generated seed %x, expected %x", gotSeed, seed)
		}
	}
}

func TestXprv(t *testing.T) {
	tests := []struct {
		entropy string
		xprv    string
	}{
		{
			"000102030405060708090a0b0c0d0e0f",
			"xprv9s21ZrQH143K3QTDL4LXw2F7HEK3wJUD2nW2nRk4stbPy6cq3jPPqjiChkVvvNKmPGJxWUtg6LnF5kejMRNNU3TGtRBeJgk33yuGBxrMPHi",
		},
		{
			"4b381541583be4423346c643850da4b320e46a87ae3d2a4e6da11eba819cd4acba45d239319ac14f863b8d5ab5a0d0c64d2e8a1e7d1457df2e5a3c51c73235be",
			"xprv9s21ZrQH143K25QhxbucbDDuQ4naNntJRi4KUfWT7xo4EKsHt2QJDu7KXp1A3u7Bi1j8ph3EGsZ9Xvz9dGuVrtHHs7pXeTzjuxBrCmmhgC6",
		},
		{
			"fffcf9f6f3f0edeae7e4e1dedbd8d5d2cfccc9c6c3c0bdbab7b4b1aeaba8a5a29f9c999693908d8a8784817e7b7875726f6c696663605d5a5754514e4b484542",
			"xprv9s21ZrQH143K31xYSDQpPDxsXRTUcvj2iNHm5NUtrGiGG5e2DtALGdso3pGz6ssrdK4PFmM8NSpSBHNqPqm55Qn3LqFtT2emdEXVYsCzC2U",
		},
		{
			"3ddd5602285899a946114506157c7997e5444528f3003f6134712147db19b678",
			"xprv9s21ZrQH143K48vGoLGRPxgo2JNkJ3J3fqkirQC2zVdk5Dgd5w14S7fRDyHH4dWNHUgkvsvNDCkvAwcSHNAQwhwgNMgZhLtQC63zxwhQmRv",
		},
		{
			"4b381541583be4423346c643850da4b320e46a87ae3d2a4e6da11eba819cd4acba45d239319ac14f863b8d5ab5a0d0c64d2e8a1e7d1457df2e5a3c51c73235be",
			"xprv9s21ZrQH143K25QhxbucbDDuQ4naNntJRi4KUfWT7xo4EKsHt2QJDu7KXp1A3u7Bi1j8ph3EGsZ9Xvz9dGuVrtHHs7pXeTzjuxBrCmmhgC6",
		},
	}
	for _, test := range tests {
		seed, err := hex.DecodeString(test.entropy)
		if err != nil {
			t.Fatal(err)
		}
		share, err := codex32.NewSeed("ms", 0, "test", 'S', seed)
		if err != nil {
			t.Fatal(err)
		}
		const cmdXprv = "derive -path m xprv"
		gotXprv := string(bytes.TrimSpace(exec(t, []byte(share.String()), cmdXprv)))
		if test.xprv != gotXprv {
			t.Errorf("%q generated xprv %s, expected %s", cmdXprv, gotXprv, test.xprv)
		}
		xprv, err := hdkeychain.NewKeyFromString(test.xprv)
		if err != nil {
			t.Fatal(err)
		}
		xpub, err := xprv.Neuter()
		if err != nil {
			t.Fatal(err)
		}
		const cmdXpub = "derive -path m xpub"
		gotXpub := string(bytes.TrimSpace(exec(t, []byte(share.String()), cmdXpub)))
		if want := xpub.String(); want != gotXpub {
			t.Errorf("%q generated xpub %s, expected %s", cmdXpub, gotXpub, want)
		}
		const cmdpub = "derive -path m pubkey"
		gotpub := string(bytes.TrimSpace(exec(t, []byte(share.String()), cmdpub)))
		pubkey, err := xprv.ECPubKey()
		if err != nil {
			t.Fatal(err)
		}
		if want := hex.EncodeToString(pubkey.SerializeCompressed()); want != gotpub {
			t.Errorf("%q generated public key %s, expected %s", cmdpub, gotpub, want)
		}
		const cmdprv = "derive -path m privkey"
		gotprv := string(bytes.TrimSpace(exec(t, []byte(share.String()), cmdprv)))
		prvkey, err := xprv.ECPrivKey()
		if err != nil {
			t.Fatal(err)
		}
		if want := hex.EncodeToString(prvkey.Serialize()); want != gotprv {
			t.Errorf("%q generated private key %s, expected %s", cmdprv, gotprv, want)
		}
	}
}

func TestInterpolate(t *testing.T) {
	tests := []struct {
		shares []string
		idx    rune
		result string
	}{
		{
			[]string{
				"ms13cashsllhdmn9m42vcsamx24zrxgs3qqjzqud4m0d6nln",
				"ms13casha320zyxwvutsrqpnmlkjhgfedca2a8d0zehn8a0t",
				"ms13cashcacdefghjklmnpqrstuvwxyz023949xq35my48dr",
			},
			'D',
			"ms13cashd0wsedstcdcts64cd7wvy4m90lm28w4ffupqs7rm",
		},
		{
			[]string{
				"ms13casha320zyxwvutsrqpnmlkjhgfedca2a8d0zehn8a0t",
				"ms13cashcacdefghjklmnpqrstuvwxyz023949xq35my48dr",
				"ms13cashd0wsedstcdcts64cd7wvy4m90lm28w4ffupqs7rm",
			},
			'e',
			"ms13casheekgpemxzshcrmqhaydlp6yhms3ws7320xyxsar9",
		},
		{
			[]string{
				"ms13cashcacdefghjklmnpqrstuvwxyz023949xq35my48dr",
				"ms13cashd0wsedstcdcts64cd7wvy4m90lm28w4ffupqs7rm",
				"ms13casheekgpemxzshcrmqhaydlp6yhms3ws7320xyxsar9",
			},
			'f',
			"ms13cashf8jh6sdrkpyrsp5ut94pj8ktehhw2hfvyrj48704",
		},
	}
	for _, test := range tests {
		stdin := strings.Join(test.shares, "\n")
		gotShare := string(bytes.TrimSpace(exec(t,
			[]byte(stdin),
			"interpolate -idx %c", test.idx,
		)))
		if gotShare != test.result {
			t.Errorf("interpolate returned %s, expected %s", gotShare, test.result)
		}
	}
}

func TestDeriveSeed(t *testing.T) {
	tests := []struct {
		input   string
		cmdline string
		output  string
	}{
		{
			"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqqtum9pgv99ycma",
			"-path m seed -n 32",
			"ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100",
		},
		{
			"xprv9s21ZrQH143K2LBWUUQRFXhucrQqBpKdRRxNVq2zBqsx8HVqFk2uYo8kmbaLLHRdqtQpUm98uKfu3vca1LqdGhUtyoFnCNkfmXRyPXLjbKb",
			"-path m/83696968h/0h/0h seed -n 64",
			"efecfbccffea313214232d29e71563d941229afb4338c21f9517c41aaa0d16f00b83d2a09ef747e7a64e8e2bd5a14869e693da66ce94ac2da570ab7ee48618f7",
		},
	}
	for _, test := range tests {
		output := hex.EncodeToString(exec(t,
			[]byte(test.input),
			"derive %s", test.cmdline,
		))
		if output != test.output {
			t.Errorf("'derive %s' returned %s, expected %s", test.cmdline, output, test.output)
		}
	}
}

func TestDerive(t *testing.T) {
	tests := []struct {
		input   string
		cmdline string
		output  string
	}{
		{
			"xprv9s21ZrQH143K2LBWUUQRFXhucrQqBpKdRRxNVq2zBqsx8HVqFk2uYo8kmbaLLHRdqtQpUm98uKfu3vca1LqdGhUtyoFnCNkfmXRyPXLjbKb",
			"-path m/83696968h/32h/0h xprv",
			"xprv9s21ZrQH143K2srSbCSg4m4kLvPMzcWydgmKEnMmoZUurYuBuYG46c6P71UGXMzmriLzCCBvKQWBUv3vPB3m1SATMhp3uEjXHJ42jFg7myX",
		},
		{
			"xprv9s21ZrQH143K2LBWUUQRFXhucrQqBpKdRRxNVq2zBqsx8HVqFk2uYo8kmbaLLHRdqtQpUm98uKfu3vca1LqdGhUtyoFnCNkfmXRyPXLjbKb",
			"-path m/83696968h/32h/0h xpub",
			"xpub661MyMwAqRbcFMvuhDygRu1UtxDrQ5Epzugv3AmPMu1tjMELT5aJeQQrxEx84a3XFegMz3jY7EdohY3ogWELWhmixQKTFJK1rxXRtP8aoWr",
		},
		{
			"xprv9s21ZrQH143K2srSbCSg4m4kLvPMzcWydgmKEnMmoZUurYuBuYG46c6P71UGXMzmriLzCCBvKQWBUv3vPB3m1SATMhp3uEjXHJ42jFg7myX",
			"-path m xpub",
			"xpub661MyMwAqRbcFMvuhDygRu1UtxDrQ5Epzugv3AmPMu1tjMELT5aJeQQrxEx84a3XFegMz3jY7EdohY3ogWELWhmixQKTFJK1rxXRtP8aoWr",
		},
		{
			"xpub661MyMwAqRbcFMvuhDygRu1UtxDrQ5Epzugv3AmPMu1tjMELT5aJeQQrxEx84a3XFegMz3jY7EdohY3ogWELWhmixQKTFJK1rxXRtP8aoWr",
			"-path m pubkey",
			"021ea8a255954f9d342243152db303044542e5b778c676b254da6f16d660aa030c",
		},
		{
			"xprv9s21ZrQH143K2srSbCSg4m4kLvPMzcWydgmKEnMmoZUurYuBuYG46c6P71UGXMzmriLzCCBvKQWBUv3vPB3m1SATMhp3uEjXHJ42jFg7myX",
			"-path m pubkey",
			"021ea8a255954f9d342243152db303044542e5b778c676b254da6f16d660aa030c",
		},
		{
			"xprv9s21ZrQH143K2srSbCSg4m4kLvPMzcWydgmKEnMmoZUurYuBuYG46c6P71UGXMzmriLzCCBvKQWBUv3vPB3m1SATMhp3uEjXHJ42jFg7myX",
			"-path m privkey",
			"ead0b33988a616cf6a497f1c169d9e92562604e38305ccd3fc96f2252c177682",
		},
		{
			"xpub661MyMwAqRbcFMvuhDygRu1UtxDrQ5Epzugv3AmPMu1tjMELT5aJeQQrxEx84a3XFegMz3jY7EdohY3ogWELWhmixQKTFJK1rxXRtP8aoWr",
			"-path m xpub",
			"xpub661MyMwAqRbcFMvuhDygRu1UtxDrQ5Epzugv3AmPMu1tjMELT5aJeQQrxEx84a3XFegMz3jY7EdohY3ogWELWhmixQKTFJK1rxXRtP8aoWr",
		},
		{
			"xprv9s21ZrQH143K2LBWUUQRFXhucrQqBpKdRRxNVq2zBqsx8HVqFk2uYo8kmbaLLHRdqtQpUm98uKfu3vca1LqdGhUtyoFnCNkfmXRyPXLjbKb",
			"-path m/83696968h/39h/0h/12h/0h bip39 -words 12",
			"girl mad pet galaxy egg matter matrix prison refuse sense ordinary nose",
		},
		{
			"xprv9s21ZrQH143K2LBWUUQRFXhucrQqBpKdRRxNVq2zBqsx8HVqFk2uYo8kmbaLLHRdqtQpUm98uKfu3vca1LqdGhUtyoFnCNkfmXRyPXLjbKb",
			"-path m/83696968h/39h/0h/18h/0h bip39 -words 18",
			"near account window bike charge season chef number sketch tomorrow excuse sniff circle vital hockey outdoor supply token",
		},
		{
			"xprv9s21ZrQH143K2LBWUUQRFXhucrQqBpKdRRxNVq2zBqsx8HVqFk2uYo8kmbaLLHRdqtQpUm98uKfu3vca1LqdGhUtyoFnCNkfmXRyPXLjbKb",
			"-path m/83696968h/39h/0h/24h/0h bip39 -words 24",
			"puppy ocean match cereal symbol another shed magic wrap hammer bulb intact gadget divorce twin tonight reason outdoor destroy simple truth cigar social volcano",
		},
		{
			"ms13cashsllhdmn9m42vcsamx24zrxgs3qqjzqud4m0d6nln",
			"-path m xprv",
			"xprv9s21ZrQH143K266qUcrDyYJrSG7KA3A7sE5UHndYRkFzsPQ6xwUhEGK1rNuyyA57Vkc1Ma6a8boVqcKqGNximmAe9L65WsYNcNitKRPnABd",
		},
		{
			"MS12NAMEA320ZYXWVUTSRQPNMLKJHGFEDCAXRPP870HKKQRM\nMS12NAMECACDEFGHJKLMNPQRSTUVWXYZ023FTR2GDZMPY6PN\n",
			"-path m xprv",
			"xprv9s21ZrQH143K2NkobdHxXeyFDqE44nJYvzLFtsriatJNWMNKznGoGgW5UMTL4fyWtajnMYb5gEc2CgaKhmsKeskoi9eTimpRv2N11THhPTU",
		},
		{
			"ms10leetsllhdmn9m42vcsamx24zrxgs3qrl7ahwvhw4fnzrhve25gvezzyqqtum9pgv99ycma",
			"-path m xprv",
			"xprv9s21ZrQH143K3s41UCWxXTsU4TRrhkpD1t21QJETan3hjo8DP5LFdFcB5eaFtV8x6Y9aZotQyP8KByUjgLTbXCUjfu2iosTbMv98g8EQoqr",
		},
	}
	for _, test := range tests {
		output := string(bytes.TrimSpace(exec(t,
			[]byte(test.input),
			"derive %s", test.cmdline,
		)))
		if output != test.output {
			t.Errorf("'derive %s %s' returned %s, expected %s", test.cmdline, test.input, output, test.output)
		}
	}
}

func exec(t *testing.T, stdin []byte, cmd string, args ...any) []byte {
	t.Helper()
	cmdline := fmt.Sprintf(cmd, args...)
	stdout, err := execErr(stdin, cmdline)
	if err != nil {
		t.Fatalf("'biptool %s' reported '%v'", cmdline, err)
	}
	return stdout
}

func execErr(stdin []byte, cmd string) ([]byte, error) {
	stdout := new(bytes.Buffer)
	err := run(stdout, bytes.NewReader(stdin), strings.Split(cmd, " "))
	return stdout.Bytes(), err
}
