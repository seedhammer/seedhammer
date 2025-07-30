package backup

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/seedhammer/kortschak-qr"
	"seedhammer.com/bip32"
	"seedhammer.com/bip39"
	"seedhammer.com/driver/mjolnir"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
	"seedhammer.com/font/sh"
)

var update = flag.Bool("update", false, "update golden files")

func TestEngraveErrors(t *testing.T) {
	longText := strings.Repeat("ABC", 1000)
	tests := []struct {
		paragraphs []Paragraph
		err        error
	}{
		{
			[]Paragraph{{Text: longText}},
			ErrTooLarge,
		},
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("error-%d", i), func(t *testing.T) {
			txt := Text{
				Paragraphs: test.paragraphs,
				Font:       constant.Font,
				Size:       LargePlate,
			}
			_, err := EngraveText(mjolnir.Params, txt)
			if err == nil {
				t.Fatalf("no error reported by EngraveText, expected %v", test.err)
			}
			if !errors.Is(err, test.err) {
				t.Fatalf("got error %v, expected %v", err, test.err)
			}
		})
	}
}

func BenchmarkEngraving(b *testing.B) {
	longText := strings.Repeat("ABC", 100)
	qrcode, err := qr.Encode(longText, qr.M)
	if err != nil {
		b.Fatal(err)
	}
	txt := Text{
		Paragraphs: []Paragraph{{Text: longText, QR: qrcode}},
		Font:       constant.Font,
		Size:       SquarePlate,
	}
	seed := genSeed(b, "Satoshi Stash", 24, SquarePlate)
	for b.Loop() {
		plan, err := EngraveText(mjolnir.Params, txt)
		if err != nil {
			b.Fatal(err)
		}
		for range plan {
		}
		plan, err = EngraveSeed(mjolnir.Params, seed)
		if err != nil {
			b.Fatal(err)
		}
		for range plan {
		}
	}
}

const ppmm = 4

var params = engrave.Params{
	StrokeWidth: 38,
	Millimeter:  126,
}

func textAndQR(t *testing.T, s string) Paragraph {
	t.Helper()
	qrc, err := qr.Encode(s, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	return Paragraph{Text: s, QR: qrc}
}

func QR(t *testing.T, s string) *qr.Code {
	qrc, err := qr.Encode(s, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	return qrc
}

func TestText(t *testing.T) {
	const (
		singlesig        = "wpkh([dc567276/48h/0h/0h/2h]xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan/0/*)#ap6v6zth"
		compactSinglesig = "wpkh(xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan/0/*)"
		multisig         = "wsh(sortedmulti(2,[dc567276/48h/0h/0h/2h]xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan/<0;1>/1/*,[f245ae38/48h/0h/0h/2h]xpub6DnT4E1fT8VxuAZW29avMjr5i99aYTHBp9d7fiLnpL5t4JEprQqPMbTw7k7rh5tZZ2F5g8PJpssqrZoebzBChaiJrmEvWwUTEMAbHsY39Ge/<0;1>/0h/*,[c5d87297/48h/0h/0h/2h]xpub6DjrnfAyuonMaboEb3ZQZzhQ2ZEgaKV2r64BFmqymZqJqviLTe1JzMr2X2RfQF892RH7MyYUbcy77R7pPu1P71xoj8cDUMNhAMGYzKR4noZ/<0;1>/*h))#qjs07xve"
		compactMultisig  = "wsh(sortedmulti(2,xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan/<0;1>/1/*,xpub6DnT4E1fT8VxuAZW29avMjr5i99aYTHBp9d7fiLnpL5t4JEprQqPMbTw7k7rh5tZZ2F5g8PJpssqrZoebzBChaiJrmEvWwUTEMAbHsY39Ge/<0;1>/0h/*,xpub6DjrnfAyuonMaboEb3ZQZzhQ2ZEgaKV2r64BFmqymZqJqviLTe1JzMr2X2RfQF892RH7MyYUbcy77R7pPu1P71xoj8cDUMNhAMGYzKR4noZ/<0;1>/*h))"
	)

	tests := []struct {
		data []Paragraph
		size PlateSize
	}{
		{[]Paragraph{textAndQR(t, "UR:CRYPTO-OUTPUT/TAADMHTAADMETAADDLOXAXHDCLAOLBAOTTVYCXLRCXFLATSAKBMUVWLUOTOSRDOTRSHYZMJNADIELPTBCSPMAOFZPABNAAHDCXHTRDDAOYRYSGUYHLIDHGDMAAGEKIRFRTJZLOFSSRONUYIOJTKOMKTLSBCMIALBTIAMTAADDYOEADLOCSDYYKAEYKAEYKADYKAOCYCFWYAAPAAYCYWYAYDRTBJKREFHKB")}, SquarePlate},
		{[]Paragraph{textAndQR(t, "UR:CRYPTO-OUTPUT/TAADMHTAADMETAADMSOEADADAOLFTAADDLOXAXHDCLAOLBAOTTVYCXLRCXFLATSAKBMUVWLUOTOSRDOTRSHYZMJNADIELPTBCSPMAOFZPABNAAHDCXHTRDDAOYRYSGUYHLIDHGDMAAGEKIRFRTJZLOFSSRONUYIOJTKOMKTLSBCMIALBTIAMTAADDYOEADLOCSDYYKAEYKAEYKADYKAOCYCFWYAAPAAYCYWYAYDRTBTAADDLOXAXHDCLAXSKURKKMDRFRNIYSFLRDAAYJOOXCKKNEESNEETEHSOYMYECENKGRHJYMYJPINCPAOAAHDCXUTWNIMCFPLDNOEBBKSVWGAWNMKYASFKPJYOYSELDMECWRDHDJKENQZCAZCDETIRYAMTAADDYOEADLOCSDYYKAEYKAEYKADYKAOCYIYGYGWHTAYCYIMCLGACWNLGORYYK")}, LargePlate},
		{[]Paragraph{textAndQR(t, "UR:CRYPTO-OUTPUT/1-2/LPADAOCFADFXCYDAPRLRMSHDOETAADMHTAADMETAADMSOEADAOAOLSTAADDLOXAXHDCLAOLBAOTTVYCXLRCXFLATSAKBMUVWLUOTOSRDOTRSHYZMJNADIELPTBCSPMAOFZPABNAAHDCXHTRDDAOYRYSGUYHLIDHGDMAAGEKIRFRTJZLOFSSRONUYIOJTKOMKTLSBCMIALBTIAMTAADDYOEADLOCSDYYKAEYKAEYKADYKAOCYCFWYAAPAAYCYWYAYDRTBTAADDLOXAXHDCLAXSKURKKMDRFRNIYSFLRDAAYJOOXCKKNEESNEETEHSOYMYECENKGRHJYMYJPINCPAOAAHDCXUTWNSFKGIHUY")}, SquarePlate},
		{[]Paragraph{textAndQR(t, "UR:CRYPTO-OUTPUT/1-6/LPADAMCFAOBYCYAHSKHGGLHDHKTAADMHTAADMETAADMSOEADAXAOLPTAADDLOXAXHDCLAOLBAOTTVYCXLRCXFLATSAKBMUVWLUOTOSRDOTRSHYZMJNADIELPTBCSPMAOFZPABNAAHDCXHTRDDAOYRYSGUYHLIDHGDMAAGEKIRFRTJZLOFSSRONUYIOJTKOMKTLSBCMIALBTINSSOBTIE"), textAndQR(t, "UR:CRYPTO-OUTPUT/48-6/LPCSDYAMCFAOBYCYAHSKHGGLHDHKGHTAUYLSADDNFXZSUOBBGYJLRSURMKWFBDDNCNSNPKBZHNBBWLISOTNSWTUTFZURROKIRHNBHFMWYAWSWTDEAHNDJKZCSEFELDOLNSOYZSISHETIOLOTENCEHDROMNWNGELNECCMCWMHYADTUOTYTNPETKPTRYUYDEFYBSJENYPERPFDJKBSMKESWY")}, LargePlate},
		// bip380 alphabets, except the characters $%&+=?!^_|~`"\, enough to cover the descriptors we generate.
		{[]Paragraph{textAndQR(t, "0123456789()[],'/*abcdefgh@:{}IJKLMNOPQRSTUVWXYZ-.;<>ijklmnopqrstuvwxyzABCDEFGH#"+"qpzry9x8gf2tvdw0s3jn54khce6mua7l")}, SquarePlate},
		// Singlesig descriptor with QR.
		{[]Paragraph{{Text: singlesig, QR: QR(t, compactSinglesig), QRScale: 3}}, SquarePlate},
		// Standalone large descriptor.
		{[]Paragraph{{Text: multisig}}, SquarePlate},
		// Desctriptor QR.
		{[]Paragraph{{QR: QR(t, compactMultisig), QRScale: 3}}, SquarePlate},
	}
	for i, test := range tests {
		i, test := i, test
		name := fmt.Sprintf("%d-shards-%d", i, len(test.data))
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			txt := Text{
				Paragraphs: test.data,
				Font:       sh.Font,
				Size:       test.size,
			}
			p, err := EngraveText(params, txt)
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, "text-"+name+".png", test.size, p)
		})
	}
}

func TestSeed(t *testing.T) {
	tests := []struct {
		seedLen int
		size    PlateSize
	}{
		{24, SquarePlate},
		{24, LargePlate},
		{12, SquarePlate},
		{12, LargePlate},
		{24, LargePlate},
	}
	for i, test := range tests {
		i, test := i, test
		name := fmt.Sprintf("%d-words-%d-size-%v", i, test.seedLen, test.size)
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			seedDesc := genSeed(t, "Satoshi Stash", test.seedLen, test.size)
			p, err := EngraveSeed(params, seedDesc)
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, "seed-"+name+".png", test.size, p)
		})
	}
}

func runGolden(t *testing.T, name string, f func(t *testing.T)) {
	t.Run(name, func(t *testing.T) {
		t.Parallel()
		f(t)
	})
}

func TestTitleString(t *testing.T) {
	tests := []struct {
		test  string
		title string
	}{
		{"Satoshi's Wallet", "SATOSHI'S WALLET"},
		{"Anø de:Æby09 . asd asd asd as das d asd asdf sdf s fd", "AN DE:BY09 . ASD A"},
		{"Æg", "G"},
		{"🤡 💩", " "},
		{"$€#,", "#,"},
	}
	for _, test := range tests {
		s := TitleString(constant.Font, test.test)
		if s != test.title {
			t.Fatalf("got %q, wanted %q", s, test.title)
		}
	}
}

func genSeed(t testing.TB, title string, seedlen int, plateSize PlateSize) Seed {
	m := make(bip39.Mnemonic, seedlen)
	for j := range m {
		m[j] = bip39.Word(j)
	}
	m = m.FixChecksum()
	seed := bip39.MnemonicSeed(m, "")
	network := &chaincfg.MainNetParams
	mk, err := hdkeychain.NewMaster(seed, network)
	if err != nil {
		t.Fatal(err)
	}
	path := bip32.Path{0}
	mfp, _, err := bip32.Derive(mk, path)
	if err != nil {
		t.Fatal(err)
	}
	return Seed{
		Title:             title,
		Mnemonic:          m,
		MasterFingerprint: mfp,
		Font:              constant.Font,
		Size:              plateSize,
	}
}

func compareGolden(t *testing.T, name string, psz PlateSize, plan engrave.Plan) {
	bounds := image.Rectangle{
		Max: psz.Dims().Mul(ppmm),
	}
	golden := filepath.Join("testdata", name)
	got := image.NewAlpha(bounds)
	scaled := func(yield func(engrave.Command) bool) {
		for c := range plan {
			c.Coord = c.Coord.Mul(ppmm).Div(params.Millimeter)
			if !yield(c) {
				return
			}
		}
	}
	engrave.Rasterize(got, scaled)
	if *update {
		var buf bytes.Buffer
		if err := png.Encode(&buf, got); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o640); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Open(golden)
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if w, g := want.Bounds().Size(), got.Bounds().Size(); w != g {
		t.Fatalf("golden image bounds mismatch: got %v, want %v", g, w)
	}
	mismatches := 0
	pixels := 0
	width, height := want.Bounds().Dx(), want.Bounds().Dy()
	gotOff := bounds.Min
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			wanty16, _, _, _ := want.At(x, y).RGBA()
			want := wanty16 != 0
			got := got.AlphaAt(gotOff.X+x, gotOff.Y+y).A != 0
			if want {
				pixels++
			}
			if got != want {
				mismatches++
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("%d/%d pixels golden image mismatches", mismatches, pixels)
	}
}
