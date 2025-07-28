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
	"seedhammer.com/bip32"
	"seedhammer.com/bip39"
	"seedhammer.com/driver/mjolnir"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
)

var update = flag.Bool("update", false, "update golden files")

func TestEngraveErrors(t *testing.T) {
	longText := strings.Repeat("ABC", 1000)
	tests := []struct {
		data []string
		err  error
	}{
		{
			[]string{longText},
			ErrTooLarge,
		},
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("error-%d", i), func(t *testing.T) {
			txt := Text{
				Data: test.data,
				Font: constant.Font,
				Size: LargePlate,
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
	txt := Text{
		Data: []string{longText},
		Font: constant.Font,
		Size: SquarePlate,
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

func TestText(t *testing.T) {
	tests := []struct {
		data []string
		size PlateSize
	}{
		{[]string{"UR:CRYPTO-OUTPUT/TAADMHTAADMETAADDLOXAXHDCLAOLBAOTTVYCXLRCXFLATSAKBMUVWLUOTOSRDOTRSHYZMJNADIELPTBCSPMAOFZPABNAAHDCXHTRDDAOYRYSGUYHLIDHGDMAAGEKIRFRTJZLOFSSRONUYIOJTKOMKTLSBCMIALBTIAMTAADDYOEADLOCSDYYKAEYKAEYKADYKAOCYCFWYAAPAAYCYWYAYDRTBJKREFHKB"}, SquarePlate},
		{[]string{"UR:CRYPTO-OUTPUT/TAADMHTAADMETAADMSOEADADAOLFTAADDLOXAXHDCLAOLBAOTTVYCXLRCXFLATSAKBMUVWLUOTOSRDOTRSHYZMJNADIELPTBCSPMAOFZPABNAAHDCXHTRDDAOYRYSGUYHLIDHGDMAAGEKIRFRTJZLOFSSRONUYIOJTKOMKTLSBCMIALBTIAMTAADDYOEADLOCSDYYKAEYKAEYKADYKAOCYCFWYAAPAAYCYWYAYDRTBTAADDLOXAXHDCLAXSKURKKMDRFRNIYSFLRDAAYJOOXCKKNEESNEETEHSOYMYECENKGRHJYMYJPINCPAOAAHDCXUTWNIMCFPLDNOEBBKSVWGAWNMKYASFKPJYOYSELDMECWRDHDJKENQZCAZCDETIRYAMTAADDYOEADLOCSDYYKAEYKAEYKADYKAOCYIYGYGWHTAYCYIMCLGACWNLGORYYK"}, LargePlate},
		{[]string{"UR:CRYPTO-OUTPUT/1-2/LPADAOCFADFXCYDAPRLRMSHDOETAADMHTAADMETAADMSOEADAOAOLSTAADDLOXAXHDCLAOLBAOTTVYCXLRCXFLATSAKBMUVWLUOTOSRDOTRSHYZMJNADIELPTBCSPMAOFZPABNAAHDCXHTRDDAOYRYSGUYHLIDHGDMAAGEKIRFRTJZLOFSSRONUYIOJTKOMKTLSBCMIALBTIAMTAADDYOEADLOCSDYYKAEYKAEYKADYKAOCYCFWYAAPAAYCYWYAYDRTBTAADDLOXAXHDCLAXSKURKKMDRFRNIYSFLRDAAYJOOXCKKNEESNEETEHSOYMYECENKGRHJYMYJPINCPAOAAHDCXUTWNSFKGIHUY"}, SquarePlate},
		{[]string{"UR:CRYPTO-OUTPUT/1-6/LPADAMCFAOBYCYAHSKHGGLHDHKTAADMHTAADMETAADMSOEADAXAOLPTAADDLOXAXHDCLAOLBAOTTVYCXLRCXFLATSAKBMUVWLUOTOSRDOTRSHYZMJNADIELPTBCSPMAOFZPABNAAHDCXHTRDDAOYRYSGUYHLIDHGDMAAGEKIRFRTJZLOFSSRONUYIOJTKOMKTLSBCMIALBTINSSOBTIE", "UR:CRYPTO-OUTPUT/48-6/LPCSDYAMCFAOBYCYAHSKHGGLHDHKGHTAUYLSADDNFXZSUOBBGYJLRSURMKWFBDDNCNSNPKBZHNBBWLISOTNSWTUTFZURROKIRHNBHFMWYAWSWTDEAHNDJKZCSEFELDOLNSOYZSISHETIOLOTENCEHDROMNWNGELNECCMCWMHYADTUOTYTNPETKPTRYUYDEFYBSJENYPERPFDJKBSMKESWY"}, LargePlate},
	}
	for i, test := range tests {
		i, test := i, test
		name := fmt.Sprintf("%d-shards-%d", i, len(test.data))
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			txt := Text{
				Data: test.data,
				Font: constant.Font,
				Size: test.size,
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
