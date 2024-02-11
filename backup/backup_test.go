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
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/bc/urtypes"
	"seedhammer.com/bip32"
	"seedhammer.com/bip39"
	"seedhammer.com/driver/mjolnir"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
)

var update = flag.Bool("update", false, "update golden files")

func TestEngraveErrors(t *testing.T) {
	p2wsh := []uint32{
		hdkeychain.HardenedKeyStart + 48,
		hdkeychain.HardenedKeyStart + 0,
		hdkeychain.HardenedKeyStart + 0,
		hdkeychain.HardenedKeyStart + 2,
	}
	tests := []struct {
		threshold int
		keys      int
		side      int
		path      []uint32
		seedLen   int
		err       error
	}{
		{1, 5, 0, p2wsh, 24, ErrDescriptorTooLarge},
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("error-%d", i), func(t *testing.T) {
			desc := urtypes.OutputDescriptor{
				Title:     "Satoshi Stash",
				Script:    urtypes.P2WSH,
				Threshold: test.threshold,
				Type:      urtypes.SortedMulti,
				Keys:      make([]urtypes.KeyDescriptor, test.keys),
			}
			_, descDesc := genTestPlate(t, desc, test.path, test.seedLen, 0, LargePlate)
			const ppmm = 4
			_, err := EngraveDescriptor(mjolnir.Millimeter, mjolnir.StrokeWidth, descDesc)
			if err == nil {
				t.Fatalf("no error reported by Engrave, expected %v", test.err)
			}
			if !errors.Is(err, test.err) {
				t.Fatalf("got error %v, expected %v", err, test.err)
			}
		})
	}
}

func TestEngrave(t *testing.T) {
	tests := []struct {
		threshold int
		keys      int
		side      int
		script    urtypes.Script
		seedLen   int
		size      PlateSize
	}{
		// Seed only variants.
		{1, 1, 0, urtypes.P2SH, 12, SmallPlate},
		{1, 1, 0, urtypes.P2TR, 24, SquarePlate},
		{1, 1, 1, urtypes.P2WPKH, 24, SquarePlate},

		{1, 1, 0, urtypes.P2WSH, 12, SmallPlate},
		{1, 1, 0, urtypes.P2WSH, 24, SquarePlate},
		{3, 5, 1, urtypes.P2SH_P2WSH, 24, LargePlate},

		// Descriptor variants, seed side.
		{1, 1, 1, urtypes.P2SH_P2WSH, 12, SquarePlate},
		{1, 1, 1, urtypes.P2SH_P2WSH, 24, SquarePlate},
		{1, 2, 1, urtypes.P2SH_P2WSH, 12, LargePlate},
		{3, 5, 1, urtypes.P2SH_P2WSH, 24, LargePlate},
		// Descriptor side.
		{1, 1, 0, urtypes.P2SH_P2WSH, 12, SquarePlate},
		{1, 2, 0, urtypes.P2SH_P2WSH, 12, LargePlate},
		{2, 3, 0, urtypes.P2SH_P2WSH, 12, SquarePlate},
		{3, 5, 0, urtypes.P2SH_P2WSH, 12, LargePlate},
		{9, 10, 0, urtypes.P2SH_P2WSH, 12, SquarePlate},
	}
	for i, test := range tests {
		i, test := i, test
		name := fmt.Sprintf("%d-%d-of-%d-%d-words", i, test.threshold, test.keys, test.seedLen)
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			desc := urtypes.OutputDescriptor{
				Title:     "Satoshi Stash",
				Script:    test.script,
				Threshold: test.threshold,
				Type:      urtypes.Singlesig,
				Keys:      make([]urtypes.KeyDescriptor, test.keys),
			}
			if len(desc.Keys) > 1 {
				desc.Type = urtypes.SortedMulti
			}
			path := desc.Script.DerivationPath()
			seedDesc, descDesc := genTestPlate(t, desc, path, test.seedLen, 0, test.size)
			const ppmm = 4
			var side engrave.Plan
			var err error
			if test.side == 0 {
				side, err = EngraveDescriptor(mjolnir.Millimeter, mjolnir.StrokeWidth, descDesc)
			} else {
				side, err = EngraveSeed(mjolnir.Millimeter, mjolnir.StrokeWidth, seedDesc)
			}
			if err != nil {
				t.Fatal(err)
			}
			bounds := test.size.Bounds()
			bounds = image.Rectangle{
				Min: bounds.Min.Mul(ppmm),
				Max: bounds.Max.Mul(ppmm),
			}
			name := fmt.Sprintf("plate-%d-side-%d-%d-of-%d-words-%d.png", i, test.side, desc.Threshold, len(desc.Keys), test.seedLen)
			golden := filepath.Join("testdata", name)
			got := image.NewAlpha(bounds)
			r := engrave.NewRasterizer(got, bounds, float32(ppmm)/mjolnir.Millimeter, mjolnir.StrokeWidth*ppmm/mjolnir.Millimeter)
			se := side
			se(r.Command)
			r.Rasterize()
			// Binarize to minimize golden image sizes.
			for i, p := range got.Pix {
				if p < 128 {
					p = 0
				} else {
					p = 255
				}
				got.Pix[i] = p
			}
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
			const maxErrors = 30
			if mismatches > maxErrors {
				t.Errorf("%d/%d pixels golden image mismatches", mismatches, pixels)
			}
		})
	}
}

func TestSplitUR(t *testing.T) {
	t.Parallel()

	maxShares := 15
	if testing.Short() {
		maxShares = 10
	}
	for n := 1; n <= maxShares; n++ {
		n := n
		name := fmt.Sprintf("%d-shares", n)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for m := 1; m <= n; m++ {
				desc := urtypes.OutputDescriptor{
					Title:     "Some title",
					Script:    urtypes.P2WSH,
					Threshold: m,
					Type:      urtypes.Singlesig,
					Keys:      make([]urtypes.KeyDescriptor, n),
				}
				if len(desc.Keys) > 1 {
					desc.Type = urtypes.SortedMulti
				}
				genTestPlate(t, desc, desc.Script.DerivationPath(), 12, 0, LargePlate)
				if !Recoverable(desc) {
					t.Errorf("%d-of-%d: failed to recover", m, n)
				}
			}
		})
	}
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

func genTestPlate(t *testing.T, desc urtypes.OutputDescriptor, path []uint32, seedlen int, keyIdx int, plateSize PlateSize) (Seed, Descriptor) {
	var mnemonic bip39.Mnemonic
	for i := range desc.Keys {
		m := make(bip39.Mnemonic, seedlen)
		for j := range m {
			m[j] = bip39.Word(i*seedlen + j)
		}
		m = m.FixChecksum()
		seed := bip39.MnemonicSeed(m, "")
		network := &chaincfg.MainNetParams
		mk, err := hdkeychain.NewMaster(seed, network)
		if err != nil {
			t.Fatal(err)
		}
		if len(path) == 0 {
			// Ensure the master fingerprint is derived.
			path = urtypes.Path{0}
		}
		mfp, xpub, err := bip32.Derive(mk, path)
		if err != nil {
			t.Fatal(err)
		}
		pub, err := xpub.ECPubKey()
		if err != nil {
			t.Fatal(err)
		}
		desc.Keys[i] = urtypes.KeyDescriptor{
			Network:           network,
			MasterFingerprint: mfp,
			DerivationPath:    path,
			ParentFingerprint: xpub.ParentFingerprint(),
			ChainCode:         xpub.ChainCode(),
			KeyData:           pub.SerializeCompressed(),
		}
		if i == keyIdx {
			mnemonic = m
		}
	}
	return Seed{
			Title:             desc.Title,
			KeyIdx:            keyIdx,
			Mnemonic:          mnemonic,
			Keys:              len(desc.Keys),
			MasterFingerprint: desc.Keys[keyIdx].MasterFingerprint,
			Font:              constant.Font,
			Size:              plateSize,
		}, Descriptor{
			Descriptor: desc,
			KeyIdx:     keyIdx,
			Font:       constant.Font,
			Size:       plateSize,
		}
}
