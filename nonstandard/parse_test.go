package nonstandard

import (
	"reflect"
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/bc/urtypes"
)

func TestOutputDescriptors(t *testing.T) {
	tests := []struct {
		encoded string
		desc    urtypes.OutputDescriptor
	}{
		{
			"wsh(sortedmulti(2,[dc567276/48h/0h/0h/2h]xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan/0/*,[f245ae38/48h/0h/0h/2h]xpub6DnT4E1fT8VxuAZW29avMjr5i99aYTHBp9d7fiLnpL5t4JEprQqPMbTw7k7rh5tZZ2F5g8PJpssqrZoebzBChaiJrmEvWwUTEMAbHsY39Ge/0/*,[c5d87297/48h/0h/0h/2h]xpub6DjrnfAyuonMaboEb3ZQZzhQ2ZEgaKV2r64BFmqymZqJqviLTe1JzMr2X2RfQF892RH7MyYUbcy77R7pPu1P71xoj8cDUMNhAMGYzKR4noZ/0/*))#hfwurrvt",
			urtypes.OutputDescriptor{
				Script:    urtypes.P2WSH,
				Threshold: 2,
				Type:      urtypes.SortedMulti,
				Keys: []urtypes.KeyDescriptor{
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0xdc567276,
						DerivationPath:    []uint32{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
						Children:          []urtypes.Derivation{{Index: 0}, {Type: urtypes.WildcardDerivation}},
						KeyData:           []uint8{0x2, 0x1c, 0xb, 0x47, 0x9e, 0xcf, 0x6e, 0x67, 0x71, 0x3d, 0xdf, 0xc, 0x43, 0xb6, 0x34, 0x59, 0x2f, 0x51, 0xc0, 0x37, 0xb6, 0xf9, 0x51, 0xfb, 0x1d, 0xc6, 0x36, 0x1a, 0x98, 0xb1, 0xe5, 0x73, 0x5e},
						ChainCode:         []uint8{0x6b, 0x3a, 0x4c, 0xfb, 0x6a, 0x45, 0xf6, 0x30, 0x5e, 0xfe, 0x6e, 0xe, 0x97, 0x6b, 0x5d, 0x26, 0xba, 0x27, 0xf7, 0xc3, 0x44, 0xd7, 0xfc, 0x7a, 0xbe, 0xf7, 0xbe, 0x2d, 0x6, 0xd5, 0x2d, 0xfd},
						ParentFingerprint: 0x18f8c2e7,
					},
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0xf245ae38,
						DerivationPath:    []uint32{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
						Children:          []urtypes.Derivation{{Index: 0}, {Type: urtypes.WildcardDerivation}},
						KeyData:           []uint8{0x3, 0x97, 0xfc, 0xf2, 0x27, 0x4a, 0xbd, 0x24, 0x3d, 0x42, 0xd4, 0x2d, 0x3c, 0x24, 0x86, 0x8, 0xc6, 0xd1, 0x93, 0x5e, 0xfc, 0xa4, 0x61, 0x38, 0xaf, 0xef, 0x43, 0xaf, 0x8, 0xe9, 0x71, 0x28, 0x96},
						ChainCode:         []uint8{0xc8, 0x87, 0xc7, 0x2d, 0x9d, 0x8a, 0xc2, 0x9c, 0xdd, 0xd5, 0xb2, 0xb0, 0x60, 0xe8, 0xb0, 0x23, 0x90, 0x39, 0xa1, 0x49, 0xc7, 0x84, 0xab, 0xe6, 0x7, 0x9e, 0x24, 0x44, 0x5d, 0xb4, 0xaa, 0x8a},
						ParentFingerprint: 0x221eb5a0,
					},
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0xc5d87297,
						DerivationPath:    []uint32{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
						Children:          []urtypes.Derivation{{Index: 0}, {Type: urtypes.WildcardDerivation}},
						KeyData:           []uint8{0x2, 0x83, 0x42, 0xf5, 0xf7, 0x77, 0x3f, 0x6f, 0xab, 0x37, 0x4e, 0x1c, 0x2d, 0x3c, 0xcd, 0xba, 0x26, 0xbc, 0x9, 0x33, 0xfc, 0x4f, 0x63, 0x82, 0x8b, 0x66, 0x2b, 0x43, 0x57, 0xe4, 0xcc, 0x37, 0x91},
						ChainCode:         []uint8{0x5a, 0xfe, 0xd5, 0x6d, 0x75, 0x5c, 0x8, 0x83, 0x20, 0xec, 0x9b, 0xc6, 0xac, 0xd8, 0x4d, 0x33, 0x73, 0x7b, 0x58, 0x0, 0x83, 0x75, 0x9e, 0xa, 0xf, 0xf8, 0xf2, 0x6e, 0x42, 0x9e, 0xb, 0x77},
						ParentFingerprint: 0x1c0ae906,
					},
				},
			},
		},
		{
			`{"label": "Test Multisig 2-of-3", "blockheight": 481824, "descriptor": "wsh(sortedmulti(2,[dc567276/48h/0h/0h/2h]xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan/0/*,[f245ae38/48h/0h/0h/2h]xpub6DnT4E1fT8VxuAZW29avMjr5i99aYTHBp9d7fiLnpL5t4JEprQqPMbTw7k7rh5tZZ2F5g8PJpssqrZoebzBChaiJrmEvWwUTEMAbHsY39Ge/0/*,[c5d87297/48h/0h/0h/2h]xpub6DjrnfAyuonMaboEb3ZQZzhQ2ZEgaKV2r64BFmqymZqJqviLTe1JzMr2X2RfQF892RH7MyYUbcy77R7pPu1P71xoj8cDUMNhAMGYzKR4noZ/0/*))#hfwurrvt", "devices": [{"type": "other", "label": "Test Multisig 2-of-3 Cosigner 1"}, {"type": "other", "label": "Test Multisig 2-of-3 Cosigner 2"}, {" type": "other", "label": "Test Multisig 2-of-3 Cosigner 3"}] }`,
			urtypes.OutputDescriptor{
				Script:    urtypes.P2WSH,
				Threshold: 2,
				Type:      urtypes.SortedMulti,
				Keys: []urtypes.KeyDescriptor{
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0xdc567276,
						DerivationPath:    []uint32{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
						Children:          []urtypes.Derivation{{Index: 0}, {Type: urtypes.WildcardDerivation}},
						KeyData:           []uint8{0x2, 0x1c, 0xb, 0x47, 0x9e, 0xcf, 0x6e, 0x67, 0x71, 0x3d, 0xdf, 0xc, 0x43, 0xb6, 0x34, 0x59, 0x2f, 0x51, 0xc0, 0x37, 0xb6, 0xf9, 0x51, 0xfb, 0x1d, 0xc6, 0x36, 0x1a, 0x98, 0xb1, 0xe5, 0x73, 0x5e},
						ChainCode:         []uint8{0x6b, 0x3a, 0x4c, 0xfb, 0x6a, 0x45, 0xf6, 0x30, 0x5e, 0xfe, 0x6e, 0xe, 0x97, 0x6b, 0x5d, 0x26, 0xba, 0x27, 0xf7, 0xc3, 0x44, 0xd7, 0xfc, 0x7a, 0xbe, 0xf7, 0xbe, 0x2d, 0x6, 0xd5, 0x2d, 0xfd},
						ParentFingerprint: 0x18f8c2e7,
					},
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0xf245ae38,
						DerivationPath:    []uint32{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
						Children:          []urtypes.Derivation{{Index: 0}, {Type: urtypes.WildcardDerivation}},
						KeyData:           []uint8{0x3, 0x97, 0xfc, 0xf2, 0x27, 0x4a, 0xbd, 0x24, 0x3d, 0x42, 0xd4, 0x2d, 0x3c, 0x24, 0x86, 0x8, 0xc6, 0xd1, 0x93, 0x5e, 0xfc, 0xa4, 0x61, 0x38, 0xaf, 0xef, 0x43, 0xaf, 0x8, 0xe9, 0x71, 0x28, 0x96},
						ChainCode:         []uint8{0xc8, 0x87, 0xc7, 0x2d, 0x9d, 0x8a, 0xc2, 0x9c, 0xdd, 0xd5, 0xb2, 0xb0, 0x60, 0xe8, 0xb0, 0x23, 0x90, 0x39, 0xa1, 0x49, 0xc7, 0x84, 0xab, 0xe6, 0x7, 0x9e, 0x24, 0x44, 0x5d, 0xb4, 0xaa, 0x8a},
						ParentFingerprint: 0x221eb5a0,
					},
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0xc5d87297,
						DerivationPath:    []uint32{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
						Children:          []urtypes.Derivation{{Index: 0}, {Type: urtypes.WildcardDerivation}},
						KeyData:           []uint8{0x2, 0x83, 0x42, 0xf5, 0xf7, 0x77, 0x3f, 0x6f, 0xab, 0x37, 0x4e, 0x1c, 0x2d, 0x3c, 0xcd, 0xba, 0x26, 0xbc, 0x9, 0x33, 0xfc, 0x4f, 0x63, 0x82, 0x8b, 0x66, 0x2b, 0x43, 0x57, 0xe4, 0xcc, 0x37, 0x91},
						ChainCode:         []uint8{0x5a, 0xfe, 0xd5, 0x6d, 0x75, 0x5c, 0x8, 0x83, 0x20, 0xec, 0x9b, 0xc6, 0xac, 0xd8, 0x4d, 0x33, 0x73, 0x7b, 0x58, 0x0, 0x83, 0x75, 0x9e, 0xa, 0xf, 0xf8, 0xf2, 0x6e, 0x42, 0x9e, 0xb, 0x77},
						ParentFingerprint: 0x1c0ae906,
					},
				},
			},
		},
		{
			"sh(wpkh(xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan))",
			urtypes.OutputDescriptor{
				Script:    urtypes.P2SH_P2WPKH,
				Type:      urtypes.Singlesig,
				Threshold: 1,
				Keys: []urtypes.KeyDescriptor{
					{
						Network:           &chaincfg.MainNetParams,
						KeyData:           []uint8{0x2, 0x1c, 0xb, 0x47, 0x9e, 0xcf, 0x6e, 0x67, 0x71, 0x3d, 0xdf, 0xc, 0x43, 0xb6, 0x34, 0x59, 0x2f, 0x51, 0xc0, 0x37, 0xb6, 0xf9, 0x51, 0xfb, 0x1d, 0xc6, 0x36, 0x1a, 0x98, 0xb1, 0xe5, 0x73, 0x5e},
						ChainCode:         []uint8{0x6b, 0x3a, 0x4c, 0xfb, 0x6a, 0x45, 0xf6, 0x30, 0x5e, 0xfe, 0x6e, 0xe, 0x97, 0x6b, 0x5d, 0x26, 0xba, 0x27, 0xf7, 0xc3, 0x44, 0xd7, 0xfc, 0x7a, 0xbe, 0xf7, 0xbe, 0x2d, 0x6, 0xd5, 0x2d, 0xfd},
						ParentFingerprint: 0x18f8c2e7,
						DerivationPath:    urtypes.P2SH_P2WPKH.DerivationPath(),
					},
				},
			},
		},
		{
			"wpkh(tpubDE77mtPH9LnL5r2mFHjEXM2KZ6P2YyHcyCtjAXroj9jnQDbwtsRim3CoXTv2pQUaJinqoBFAhXguGhZcL4JDVD7JShCnV9MfAfSpke4Ja58)",
			urtypes.OutputDescriptor{
				Script:    urtypes.P2WPKH,
				Type:      urtypes.Singlesig,
				Threshold: 1,
				Keys: []urtypes.KeyDescriptor{
					{
						Network:           &chaincfg.TestNet3Params,
						KeyData:           []uint8{0x3, 0x46, 0x6d, 0xc4, 0xf, 0x23, 0x5c, 0x13, 0xa7, 0x3d, 0x2a, 0x65, 0x18, 0x16, 0x89, 0x62, 0x94, 0xd8, 0x75, 0x44, 0xdb, 0x71, 0x6c, 0x28, 0xde, 0x4a, 0x19, 0x58, 0xfb, 0xb8, 0xc5, 0xe9, 0x66},
						ChainCode:         []uint8{0x7a, 0xb6, 0x2, 0x11, 0xef, 0xd1, 0x25, 0x52, 0x1d, 0xdb, 0x57, 0x77, 0x57, 0xf0, 0xad, 0xb2, 0x94, 0xd7, 0x81, 0xd5, 0x58, 0x3d, 0x94, 0x31, 0xe6, 0x24, 0x35, 0x18, 0x1, 0x6c, 0x7b, 0x75},
						ParentFingerprint: 0x1b2bf3a6,
						DerivationPath:    urtypes.P2WPKH.DerivationPath(),
					},
				},
			},
		},
	}
	for _, test := range tests {
		got, err := OutputDescriptor([]byte(test.encoded))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, test.desc) {
			t.Errorf("%q\ndecoded to\n%#v\nexpected\n%#v\n", test.encoded, got, test.desc)
		}
	}
}

func TestProprietaryDescriptors(t *testing.T) {
	tests := []struct {
		encoded string
		desc    string
	}{
		{
			`# BlueWallet Multisig setup file
# this file contains only public keys and is safe to
# distribute among cosigners
#
Name: sh
Policy: 2 of 3
Derivation: m/48'/0'/0'/2'
Format: P2WSH

5A0804E3: xpub6F148LnjUhGrHfEN6Pa8VkwF8L6FJqYALxAkuHfacfVhMLVY4MRuUVMxr9pguAv67DHx1YFxqoKN8s4QfZtD9sR2xRCffTqi9E8FiFLAYk8

DD4FADEE: xpub6DnediUuY8Pcc6Fej8Yt2ZntPCyFdpbHBkNV7EawesRMbc6i9MKKMhKEv4JMMzwDJckaV4czBvNdc6ikwLiZqdUqMd5ZKQGYaQT4cXMeVjf

9BACD5C0: xpub6EefrCrMAduhNwnsHb3dAs8DYZSw4f63WyR6DaEByUHjwvPDdhczj15FyBBG4tbEJtf4vRKTv1ng5SPPnWv1Pve1f15EJfiBY5oYDN6VLEC
`,
			"wsh(sortedmulti(2,[5A0804E3/48'/0'/0'/2']xpub6F148LnjUhGrHfEN6Pa8VkwF8L6FJqYALxAkuHfacfVhMLVY4MRuUVMxr9pguAv67DHx1YFxqoKN8s4QfZtD9sR2xRCffTqi9E8FiFLAYk8,[DD4FADEE/48'/0'/0'/2']xpub6DnediUuY8Pcc6Fej8Yt2ZntPCyFdpbHBkNV7EawesRMbc6i9MKKMhKEv4JMMzwDJckaV4czBvNdc6ikwLiZqdUqMd5ZKQGYaQT4cXMeVjf,[9BACD5C0/48'/0'/0'/2']xpub6EefrCrMAduhNwnsHb3dAs8DYZSw4f63WyR6DaEByUHjwvPDdhczj15FyBBG4tbEJtf4vRKTv1ng5SPPnWv1Pve1f15EJfiBY5oYDN6VLEC))",
		},
		{
			`# Exported from Nunchuk
Name: test
Policy: 2 of 3
Format: P2WSH

Derivation: m/48'/0'/0'/2'
dc567276: xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan

Derivation: m/48'/0'/0'/2'
f245ae38: xpub6DnT4E1fT8VxuAZW29avMjr5i99aYTHBp9d7fiLnpL5t4JEprQqPMbTw7k7rh5tZZ2F5g8PJpssqrZoebzBChaiJrmEvWwUTEMAbHsY39Ge

Derivation: m/48'/0'/0'/2'
c5d87297: xpub6DjrnfAyuonMaboEb3ZQZzhQ2ZEgaKV2r64BFmqymZqJqviLTe1JzMr2X2RfQF892RH7MyYUbcy77R7pPu1P71xoj8cDUMNhAMGYzKR4noZ
`,
			"wsh(sortedmulti(2,[dc567276/48'/0'/0'/2']xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan,[f245ae38/48'/0'/0'/2']xpub6DnT4E1fT8VxuAZW29avMjr5i99aYTHBp9d7fiLnpL5t4JEprQqPMbTw7k7rh5tZZ2F5g8PJpssqrZoebzBChaiJrmEvWwUTEMAbHsY39Ge,[c5d87297/48'/0'/0'/2']xpub6DjrnfAyuonMaboEb3ZQZzhQ2ZEgaKV2r64BFmqymZqJqviLTe1JzMr2X2RfQF892RH7MyYUbcy77R7pPu1P71xoj8cDUMNhAMGYzKR4noZ))",
		},
		{
			"[4bbaa801/84'/0'/0']zpub6qpFgGWoG7bKmDDMvmwHBvg6inZAb2KF2Vg8h4fKJ2ickSZ71PsMmRg1FyRWAS6PqPCSzd5CB6PHixx64k6q5svZNZd9bEoCWJuMSkSRzJx",
			"wpkh([4bbaa801/84'/0'/0']xpub6C9j4wAxxkWN4cq8G4N2mkV6NrGGhnLFCGdh8GsYY1xreEveW5YEXJMjDZWLAcnZ26xqVft5FmgBxPixdMGoVQZMdtEJRRADxrn4facoGnx)",
		},
		{
			"zpub6qpFgGWoG7bKmDDMvmwHBvg6inZAb2KF2Vg8h4fKJ2ickSZ71PsMmRg1FyRWAS6PqPCSzd5CB6PHixx64k6q5svZNZd9bEoCWJuMSkSRzJx",
			"wpkh([00000000/84'/0'/0']xpub6C9j4wAxxkWN4cq8G4N2mkV6NrGGhnLFCGdh8GsYY1xreEveW5YEXJMjDZWLAcnZ26xqVft5FmgBxPixdMGoVQZMdtEJRRADxrn4facoGnx)",
		},
		{
			"xpub6C9j4wAxxkWN4cq8G4N2mkV6NrGGhnLFCGdh8GsYY1xreEveW5YEXJMjDZWLAcnZ26xqVft5FmgBxPixdMGoVQZMdtEJRRADxrn4facoGnx",
			"pkh(xpub6C9j4wAxxkWN4cq8G4N2mkV6NrGGhnLFCGdh8GsYY1xreEveW5YEXJMjDZWLAcnZ26xqVft5FmgBxPixdMGoVQZMdtEJRRADxrn4facoGnx)",
		},
	}
	for _, test := range tests {
		got, err := OutputDescriptor([]byte(test.encoded))
		if err != nil {
			t.Fatal(err)
		}
		want, err := parseTextOutputDescriptor(test.desc)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q\ndecoded to\n%#v\nexpected\n%#v\n", test.encoded, got, want)
		}
	}
}

func TestDecoder(t *testing.T) {
	parts := []string{
		"p1of3 abc",
		"p2of3 def",
		"p3of3 g",
	}
	var d Decoder
	for _, p := range parts {
		if err := d.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	if p := d.Progress(); p != 1. {
		t.Errorf("decoder progress %f, want 1.", p)
	}
	got := string(d.Result())
	if want := "abcdefg"; got != want {
		t.Errorf("decoded %q, want %q", got, want)
	}
}

func TestElectrumSeed(t *testing.T) {
	phrase := "head orient raw shoulder size fancy front cycle lamp giant camera jacket"
	if !ElectrumSeed(phrase) {
		t.Fatal("failed to detect Electrum seed")
	}
}
