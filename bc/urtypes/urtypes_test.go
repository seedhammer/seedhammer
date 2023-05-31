package urtypes

import (
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		_type string
		enc   string
		want  any
	}{
		{
			"crypto-seed",
			"a1015066e9060071faeaeed5d045363a868ef4",
			seed{Payload: []byte{102, 233, 6, 0, 113, 250, 234, 238, 213, 208, 69, 54, 58, 134, 142, 244}},
		},
	}
	for _, test := range tests {
		enc, err := hex.DecodeString(test.enc)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Parse(test._type, enc)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s decoded to\n%#v\nwanted\n%#v", test.enc, got, test.want)
		}
	}
}

func TestOutputDescriptor(t *testing.T) {
	twoOfThree := OutputDescriptor{
		Script:    P2WSH,
		Threshold: 2,
		Type:      SortedMulti,
		Keys: []KeyDescriptor{
			{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0xdd4fadee,
				DerivationPath:    Path{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
				KeyData:           []byte{0x2, 0x21, 0x96, 0xad, 0xc2, 0x5f, 0xde, 0x16, 0x9f, 0xe9, 0x2e, 0x70, 0x76, 0x90, 0x59, 0x10, 0x22, 0x75, 0xd2, 0xb4, 0xc, 0xc9, 0x87, 0x76, 0xea, 0xab, 0x92, 0xb8, 0x2a, 0x86, 0x13, 0x5e, 0x92},
				ChainCode:         []byte{0x43, 0x8e, 0xff, 0x7b, 0x3b, 0x36, 0xb6, 0xd1, 0x1a, 0x60, 0xa2, 0x2c, 0xcb, 0x93, 0x6, 0xee, 0xa3, 0x5, 0xb0, 0x43, 0x9f, 0x1e, 0xa0, 0x9d, 0x59, 0x28, 0x1, 0x5d, 0xe3, 0x73, 0x81, 0x16},
				ParentFingerprint: 0x22969377,
			},
			{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0x9bacd5c0,
				DerivationPath:    Path{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
				KeyData:           []byte{0x2, 0xfb, 0x72, 0x50, 0x7f, 0xc2, 0xd, 0xdb, 0xa9, 0x29, 0x91, 0xb1, 0x7c, 0x4b, 0xb4, 0x66, 0x13, 0xa, 0xd9, 0x3a, 0x88, 0x6e, 0x73, 0x17, 0x50, 0x33, 0xbb, 0x43, 0xe3, 0xbc, 0x78, 0x5a, 0x6d},
				ChainCode:         []byte{0x95, 0xb3, 0x49, 0x13, 0x93, 0x7f, 0xa5, 0xf1, 0xc6, 0x20, 0x5b, 0x52, 0x5b, 0xb5, 0x7d, 0xe1, 0x51, 0x76, 0x25, 0xe0, 0x45, 0x86, 0xb5, 0x95, 0xbe, 0x68, 0xe7, 0x13, 0x62, 0xd3, 0xed, 0xc5},
				ParentFingerprint: 0x97ec38f9,
			},
			{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0x5a0804e3,
				DerivationPath:    Path{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
				KeyData:           []byte{0x3, 0xa9, 0x39, 0x4a, 0x2f, 0x1a, 0x4f, 0x99, 0x61, 0x3a, 0x71, 0x69, 0x56, 0xc8, 0x54, 0xf, 0x6d, 0xba, 0x6f, 0x18, 0x93, 0x1c, 0x26, 0x39, 0x10, 0x72, 0x21, 0xb2, 0x67, 0xd7, 0x40, 0xaf, 0x23},
				ChainCode:         []byte{0xdb, 0xe8, 0xc, 0xbb, 0x4e, 0xe, 0x41, 0x8b, 0x6, 0xf4, 0x70, 0xd2, 0xaf, 0xe7, 0xa8, 0xc1, 0x7b, 0xe7, 0x1, 0xab, 0x20, 0x6c, 0x59, 0xa6, 0x5e, 0x65, 0xa8, 0x24, 0x1, 0x6a, 0x6c, 0x70},
				ParentFingerprint: 0xc7bce7a8,
			},
		},
	}
	tests := []struct {
		desc OutputDescriptor
		want string
	}{
		{
			OutputDescriptor{
				Script:    P2WSH,
				Threshold: 1,
				Type:      Multi,
				Keys: []KeyDescriptor{
					{
						Network: &chaincfg.MainNetParams,
						Children: []Derivation{
							{Index: 1},
							{Index: 0},
							{Type: WildcardDerivation},
						},
						KeyData:           []byte{0x3, 0xcb, 0xca, 0xa9, 0xc9, 0x8c, 0x87, 0x7a, 0x26, 0x97, 0x7d, 0x0, 0x82, 0x5c, 0x95, 0x6a, 0x23, 0x8e, 0x8d, 0xdd, 0xfb, 0xd3, 0x22, 0xcc, 0xe4, 0xf7, 0x4b, 0xb, 0x5b, 0xd6, 0xac, 0xe4, 0xa7},
						ChainCode:         []byte{0x60, 0x49, 0x9f, 0x80, 0x1b, 0x89, 0x6d, 0x83, 0x17, 0x9a, 0x43, 0x74, 0xae, 0xb7, 0x82, 0x2a, 0xae, 0xac, 0xea, 0xa0, 0xdb, 0x1f, 0x85, 0xee, 0x3e, 0x90, 0x4c, 0x4d, 0xef, 0xbd, 0x96, 0x89},
						ParentFingerprint: 0x00000000,
					},
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0xbd16bee5,
						DerivationPath:    Path{0},
						Children: []Derivation{
							{Index: 0},
							{Index: 0},
							{Type: WildcardDerivation},
						},
						KeyData:           []byte{0x2, 0xfc, 0x9e, 0x5a, 0xf0, 0xac, 0x8d, 0x9b, 0x3c, 0xec, 0xfe, 0x2a, 0x88, 0x8e, 0x21, 0x17, 0xba, 0x3d, 0x8, 0x9d, 0x85, 0x85, 0x88, 0x6c, 0x9c, 0x82, 0x6b, 0x6b, 0x22, 0xa9, 0x8d, 0x12, 0xea},
						ChainCode:         []byte{0xf0, 0x90, 0x9a, 0xff, 0xaa, 0x7e, 0xe7, 0xab, 0xe5, 0xdd, 0x4e, 0x10, 0x5, 0x98, 0xd4, 0xdc, 0x53, 0xcd, 0x70, 0x9d, 0x5a, 0x5c, 0x2c, 0xac, 0x40, 0xe7, 0x41, 0x2f, 0x23, 0x2f, 0x7c, 0x9c},
						ParentFingerprint: 0x00000000,
					},
				},
			},
			"d90191d90196a201010282d9012fa303582103cbcaa9c98c877a26977d00825c956a238e8dddfbd322cce4f74b0b5bd6ace4a704582060499f801b896d83179a4374aeb7822aaeaceaa0db1f85ee3e904c4defbd968907d90130a1018601f400f480f4d9012fa403582102fc9e5af0ac8d9b3cecfe2a888e2117ba3d089d8585886c9c826b6b22a98d12ea045820f0909affaa7ee7abe5dd4e100598d4dc53cd709d5a5c2cac40e7412f232f7c9c06d90130a2018200f4021abd16bee507d90130a1018600f400f480f4",
		},
		{
			twoOfThree,
			"d90191d90197a201020283d9012fa4035821022196adc25fde169fe92e70769059102275d2b40cc98776eaab92b82a86135e92045820438eff7b3b36b6d11a60a22ccb9306eea305b0439f1ea09d5928015de373811606d90130a201881830f500f500f502f5021add4fadee081a22969377d9012fa403582102fb72507fc20ddba92991b17c4bb466130ad93a886e73175033bb43e3bc785a6d04582095b34913937fa5f1c6205b525bb57de1517625e04586b595be68e71362d3edc506d90130a201881830f500f500f502f5021a9bacd5c0081a97ec38f9d9012fa403582103a9394a2f1a4f99613a716956c8540f6dba6f18931c2639107221b267d740af23045820dbe80cbb4e0e418b06f470d2afe7a8c17be701ab206c59a65e65a824016a6c7006d90130a201881830f500f500f502f5021a5a0804e3081ac7bce7a8",
		},
		{
			OutputDescriptor{
				Script: P2WPKH, Threshold: 1, Keys: []KeyDescriptor{
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0x9c43e6c2,
						DerivationPath:    Path{hdkeychain.HardenedKeyStart + 84, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart},
						KeyData:           []uint8{0x3, 0x3e, 0xd5, 0x1b, 0xcf, 0xf9, 0x30, 0xc6, 0x14, 0xe8, 0x61, 0xbf, 0xed, 0xff, 0x57, 0x69, 0x9b, 0x67, 0x8, 0x5a, 0x9f, 0x19, 0x77, 0x75, 0xbc, 0xc5, 0x41, 0xa9, 0xeb, 0xe8, 0x26, 0x8d, 0xe9},
						ChainCode:         []uint8{0x21, 0x23, 0x99, 0xa8, 0xdb, 0x12, 0x5c, 0x85, 0xf9, 0x41, 0xea, 0x12, 0x23, 0x1d, 0x8b, 0x5c, 0x7a, 0x76, 0xb8, 0x3e, 0x1, 0xd0, 0x3d, 0x16, 0xc5, 0x39, 0x58, 0xc5, 0x18, 0x28, 0x4f, 0x45},
						ParentFingerprint: 0xd1e5a62d,
					},
				},
			},
			"d90194d9012fa4035821033ed51bcff930c614e861bfedff57699b67085a9f197775bcc541a9ebe8268de9045820212399a8db125c85f941ea12231d8b5c7a76b83e01d03d16c53958c518284f4506d90130a201861854f500f500f5021a9c43e6c2081ad1e5a62d",
		},
		{
			OutputDescriptor{
				Script: P2SH_P2WPKH, Threshold: 1, Keys: []KeyDescriptor{
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0x9866232b,
						DerivationPath:    Path{hdkeychain.HardenedKeyStart + 49, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart},
						KeyData:           []uint8{0x2, 0xb1, 0x1d, 0x60, 0xe0, 0x23, 0x9, 0xc4, 0x80, 0xbb, 0xa1, 0x37, 0x77, 0x1a, 0xd6, 0x14, 0x62, 0x6b, 0xea, 0xf2, 0xef, 0x74, 0x34, 0x4e, 0xd1, 0xf, 0xd8, 0x3b, 0xb6, 0x3f, 0xeb, 0xcf, 0xa7},
						ChainCode:         []uint8{0x65, 0x8c, 0xa1, 0x47, 0x4, 0xcc, 0x49, 0xcb, 0x6, 0x64, 0x9d, 0x1b, 0xdd, 0x74, 0x6b, 0x11, 0x9f, 0x18, 0x5b, 0xf7, 0x7c, 0x1c, 0x48, 0x30, 0x73, 0xbb, 0x81, 0xe3, 0x35, 0x5a, 0xbc, 0x51},
						ParentFingerprint: 0xe986734b,
					},
				},
			},
			"d90190d90194d9012fa403582102b11d60e02309c480bba137771ad614626beaf2ef74344ed10fd83bb63febcfa7045820658ca14704cc49cb06649d1bdd746b119f185bf77c1c483073bb81e3355abc5106d90130a201861831f500f500f5021a9866232b081ae986734b",
		},
		{
			OutputDescriptor{
				Script: P2PKH, Threshold: 1, Keys: []KeyDescriptor{
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0x9866232b,
						DerivationPath:    Path{hdkeychain.HardenedKeyStart + 44, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart},
						KeyData:           []uint8{0x2, 0x72, 0x62, 0x46, 0x42, 0x95, 0xd, 0x14, 0x75, 0xf1, 0x6e, 0x46, 0xcc, 0x8d, 0x2b, 0x75, 0xcc, 0x2d, 0xe1, 0x2d, 0xf2, 0x9f, 0x29, 0xcf, 0x36, 0x97, 0x75, 0xb9, 0x5f, 0x66, 0xd2, 0x8e, 0x28},
						ChainCode:         []uint8{0xab, 0x20, 0x95, 0x8c, 0x7e, 0x9e, 0xd9, 0x9c, 0x91, 0x5d, 0x2c, 0x98, 0x7, 0x37, 0xf3, 0x12, 0x38, 0xd3, 0xb5, 0xab, 0x32, 0xb8, 0x8b, 0xda, 0xaa, 0x61, 0x91, 0x5b, 0xb5, 0xb3, 0xb4, 0xa4},
						ParentFingerprint: 0xb62041ef,
					},
				},
			},
			"d90193d9012fa40358210272624642950d1475f16e46cc8d2b75cc2de12df29f29cf369775b95f66d28e28045820ab20958c7e9ed99c915d2c980737f31238d3b5ab32b88bdaaa61915bb5b3b4a406d90130a20186182cf500f500f5021a9866232b081ab62041ef",
		},
		{
			OutputDescriptor{
				Script: P2TR, Threshold: 1, Keys: []KeyDescriptor{
					{
						Network:           &chaincfg.MainNetParams,
						MasterFingerprint: 0x9866232b,
						DerivationPath:    Path{hdkeychain.HardenedKeyStart + 86, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart},
						KeyData:           []uint8{0x3, 0xd, 0x9f, 0x35, 0x47, 0x53, 0x4d, 0xd3, 0x32, 0x85, 0x56, 0x11, 0xaf, 0x48, 0xae, 0x34, 0x62, 0x25, 0xb0, 0xd4, 0xe1, 0xe5, 0xf8, 0x10, 0x57, 0xaa, 0x9e, 0x4c, 0x20, 0x58, 0x94, 0x87, 0xc5},
						ChainCode:         []uint8{0xc1, 0xaa, 0x32, 0xa1, 0x3d, 0x12, 0xcf, 0x59, 0x52, 0x8b, 0x58, 0x1e, 0x9b, 0x5d, 0x7, 0x4, 0x68, 0x57, 0x2e, 0x20, 0xf, 0x26, 0x4, 0x76, 0xa2, 0xee, 0xb2, 0x3a, 0xdc, 0x48, 0x4a, 0x43},
						ParentFingerprint: 0x7fef547a,
					},
				},
			},
			"d90199d9012fa4035821030d9f3547534dd332855611af48ae346225b0d4e1e5f81057aa9e4c20589487c5045820c1aa32a13d12cf59528b581e9b5d070468572e200f260476a2eeb23adc484a4306d90130a201861856f500f500f5021a9866232b081a7fef547a",
		},
		{
			OutputDescriptor{
				Script: P2TR, Threshold: 1, Keys: []KeyDescriptor{
					{
						Network:           &chaincfg.TestNet3Params,
						KeyData:           []uint8{0x3, 0xd, 0x9f, 0x35, 0x47, 0x53, 0x4d, 0xd3, 0x32, 0x85, 0x56, 0x11, 0xaf, 0x48, 0xae, 0x34, 0x62, 0x25, 0xb0, 0xd4, 0xe1, 0xe5, 0xf8, 0x10, 0x57, 0xaa, 0x9e, 0x4c, 0x20, 0x58, 0x94, 0x87, 0xc5},
						ChainCode:         []uint8{0xc1, 0xaa, 0x32, 0xa1, 0x3d, 0x12, 0xcf, 0x59, 0x52, 0x8b, 0x58, 0x1e, 0x9b, 0x5d, 0x7, 0x4, 0x68, 0x57, 0x2e, 0x20, 0xf, 0x26, 0x4, 0x76, 0xa2, 0xee, 0xb2, 0x3a, 0xdc, 0x48, 0x4a, 0x43},
						ParentFingerprint: 0x7fef547a,
					},
				},
			},
			"d90199d9012fa4035821030d9f3547534dd332855611af48ae346225b0d4e1e5f81057aa9e4c20589487c5045820c1aa32a13d12cf59528b581e9b5d070468572e200f260476a2eeb23adc484a4305d90131a10201081a7fef547a",
		},
	}
	for _, test := range tests {
		got := test.desc.Encode()
		gotHex := hex.EncodeToString(got)
		if gotHex != test.want {
			t.Errorf("key:\n%+v\nencoded to:%s\nwanted:    %s\n", test.desc, gotHex, test.want)
		}
		parsed, err := Parse("crypto-output", got)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(parsed, test.desc) {
			t.Errorf("descriptor:\n%+v\nroundtripped to\n%+v\n", test.desc, parsed)
		}
	}
}

func TestBytes(t *testing.T) {
	tests := []struct {
		enc  string
		want string
	}{
		{
			"5902282320426c756557616c6c6574204d756c74697369672073657475702066696c650a2320746869732066696c6520636f6e7461696e73206f6e6c79207075626c6963206b65797320616e64206973207361666520746f0a23206469737472696275746520616d6f6e6720636f7369676e6572730a230a4e616d653a2073680a506f6c6963793a2032206f6620330a44657269766174696f6e3a206d2f3438272f30272f30272f32270a466f726d61743a2050325753480a0a35413038303445333a207870756236463134384c6e6a556847724866454e36506138566b7746384c36464a7159414c78416b75486661636656684d4c5659344d527555564d7872397067754176363744487831594678716f4b4e38733451665a74443973523278524366665471693945384669464c41596b380a0a44443446414445453a207870756236446e656469557559385063633646656a385974325a6e745043794664706248426b4e56374561776573524d62633669394d4b4b4d684b4576344a4d4d7a77444a636b615634637a42764e646336696b774c695a716455714d64355a4b5147596151543463584d65566a660a0a39424143443543303a2078707562364565667243724d416475684e776e734862336441733844595a53773466363357795236446145427955486a777650446468637a6a31354679424247347462454a74663476524b5476316e67355350506e57763150766531663135454a66694259356f59444e36564c45430a0a",
			"2320426c756557616c6c6574204d756c74697369672073657475702066696c650a2320746869732066696c6520636f6e7461696e73206f6e6c79207075626c6963206b65797320616e64206973207361666520746f0a23206469737472696275746520616d6f6e6720636f7369676e6572730a230a4e616d653a2073680a506f6c6963793a2032206f6620330a44657269766174696f6e3a206d2f3438272f30272f30272f32270a466f726d61743a2050325753480a0a35413038303445333a207870756236463134384c6e6a556847724866454e36506138566b7746384c36464a7159414c78416b75486661636656684d4c5659344d527555564d7872397067754176363744487831594678716f4b4e38733451665a74443973523278524366665471693945384669464c41596b380a0a44443446414445453a207870756236446e656469557559385063633646656a385974325a6e745043794664706248426b4e56374561776573524d62633669394d4b4b4d684b4576344a4d4d7a77444a636b615634637a42764e646336696b774c695a716455714d64355a4b5147596151543463584d65566a660a0a39424143443543303a2078707562364565667243724d416475684e776e734862336441733844595a53773466363357795236446145427955486a777650446468637a6a31354679424247347462454a74663476524b5476316e67355350506e57763150766531663135454a66694259356f59444e36564c45430a0a",
		},
	}
	for _, test := range tests {
		enc, err := hex.DecodeString(test.enc)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Parse("bytes", enc)
		if err != nil {
			t.Fatal(err)
		}
		gotHex := hex.EncodeToString(got.([]byte))
		if gotHex != test.want {
			t.Errorf("%s\ndecoded to:\n%s\nwanted\n%s", test.enc, gotHex, test.want)
		}
	}
}

func TestHDKey(t *testing.T) {
	tests := []struct {
		k    KeyDescriptor
		want string
	}{
		{
			KeyDescriptor{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0xdd4fadee,
				DerivationPath:    Path{hdkeychain.HardenedKeyStart + 48, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart + 2},
				KeyData:           []byte{0x2, 0x21, 0x96, 0xad, 0xc2, 0x5f, 0xde, 0x16, 0x9f, 0xe9, 0x2e, 0x70, 0x76, 0x90, 0x59, 0x10, 0x22, 0x75, 0xd2, 0xb4, 0xc, 0xc9, 0x87, 0x76, 0xea, 0xab, 0x92, 0xb8, 0x2a, 0x86, 0x13, 0x5e, 0x92},
				ChainCode:         []byte{0x43, 0x8e, 0xff, 0x7b, 0x3b, 0x36, 0xb6, 0xd1, 0x1a, 0x60, 0xa2, 0x2c, 0xcb, 0x93, 0x6, 0xee, 0xa3, 0x5, 0xb0, 0x43, 0x9f, 0x1e, 0xa0, 0x9d, 0x59, 0x28, 0x1, 0x5d, 0xe3, 0x73, 0x81, 0x16},
				ParentFingerprint: 0x22969377,
			},
			"a4035821022196adc25fde169fe92e70769059102275d2b40cc98776eaab92b82a86135e92045820438eff7b3b36b6d11a60a22ccb9306eea305b0439f1ea09d5928015de373811606d90130a201881830f500f500f502f5021add4fadee081a22969377",
		},
		{
			KeyDescriptor{
				Network:           &chaincfg.MainNetParams,
				MasterFingerprint: 0xbd16bee5,
				DerivationPath:    Path{0},
				Children: []Derivation{
					{Index: 0},
					{Index: 0},
					{Type: WildcardDerivation},
				},
				KeyData:           []byte{0x2, 0xfc, 0x9e, 0x5a, 0xf0, 0xac, 0x8d, 0x9b, 0x3c, 0xec, 0xfe, 0x2a, 0x88, 0x8e, 0x21, 0x17, 0xba, 0x3d, 0x8, 0x9d, 0x85, 0x85, 0x88, 0x6c, 0x9c, 0x82, 0x6b, 0x6b, 0x22, 0xa9, 0x8d, 0x12, 0xea},
				ChainCode:         []byte{0xf0, 0x90, 0x9a, 0xff, 0xaa, 0x7e, 0xe7, 0xab, 0xe5, 0xdd, 0x4e, 0x10, 0x5, 0x98, 0xd4, 0xdc, 0x53, 0xcd, 0x70, 0x9d, 0x5a, 0x5c, 0x2c, 0xac, 0x40, 0xe7, 0x41, 0x2f, 0x23, 0x2f, 0x7c, 0x9c},
				ParentFingerprint: 0x00000000,
			},
			"a403582102fc9e5af0ac8d9b3cecfe2a888e2117ba3d089d8585886c9c826b6b22a98d12ea045820f0909affaa7ee7abe5dd4e100598d4dc53cd709d5a5c2cac40e7412f232f7c9c06d90130a2018200f4021abd16bee507d90130a1018600f400f480f4",
		},
	}
	for _, test := range tests {
		got := test.k.Encode()
		gotHex := hex.EncodeToString(got)
		if gotHex != test.want {
			t.Errorf("key:\n%+v\nencoded to:%s\nwanted:    %s\n", test.k, gotHex, test.want)
		}
		parsed, err := Parse("crypto-hdkey", got)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(parsed, test.k) {
			t.Errorf("key:\n%+v\nroundtripped to\n%+v\n", test.k, parsed)
		}
	}
}
