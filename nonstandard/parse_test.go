package nonstandard

import (
	"reflect"
	"testing"

	"seedhammer.com/bip380"
)

func TestDescriptors(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
		desc    string
	}{
		{
			"Test Multisig 2-of-3",
			`{
				"label": "Test Multisig 2-of-3",
				"blockheight": 481824,
				"descriptor": "wsh(sortedmulti(2,[dc567276/48h/0h/0h/2h]xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan/0/*,[f245ae38/48h/0h/0h/2h]xpub6DnT4E1fT8VxuAZW29avMjr5i99aYTHBp9d7fiLnpL5t4JEprQqPMbTw7k7rh5tZZ2F5g8PJpssqrZoebzBChaiJrmEvWwUTEMAbHsY39Ge/0/*,[c5d87297/48h/0h/0h/2h]xpub6DjrnfAyuonMaboEb3ZQZzhQ2ZEgaKV2r64BFmqymZqJqviLTe1JzMr2X2RfQF892RH7MyYUbcy77R7pPu1P71xoj8cDUMNhAMGYzKR4noZ/0/*))#hfwurrvt",
				"devices": [{"type": "other", "label": "Test Multisig 2-of-3 Cosigner 1"}, {"type": "other", "label": "Test Multisig 2-of-3 Cosigner 2"}, {"type": "other", "label": "Test Multisig 2-of-3 Cosigner 3"}]
			}`,
			"wsh(sortedmulti(2,[dc567276/48h/0h/0h/2h]xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan/0/*,[f245ae38/48h/0h/0h/2h]xpub6DnT4E1fT8VxuAZW29avMjr5i99aYTHBp9d7fiLnpL5t4JEprQqPMbTw7k7rh5tZZ2F5g8PJpssqrZoebzBChaiJrmEvWwUTEMAbHsY39Ge/0/*,[c5d87297/48h/0h/0h/2h]xpub6DjrnfAyuonMaboEb3ZQZzhQ2ZEgaKV2r64BFmqymZqJqviLTe1JzMr2X2RfQF892RH7MyYUbcy77R7pPu1P71xoj8cDUMNhAMGYzKR4noZ/0/*))#hfwurrvt",
		},
		{
			"sh",
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
			// Wallet with Zpub keys.
			"V2",
			`# BlueWallet Multisig setup file
Name: V2
Policy: 2 of 3
Derivation: m/48'/0'/0'/2'
Format: P2WSH

79E1C26F: Zpub753vSk6B5CuYmJBvgBQYmBUghHoApQHtgJWthN7WmrJsaRaCGuQFguZTXdJxCL2rUbFdsVcLuT9ASoKGtRtug3A6SZmhfaMzYH5yc11Da3h

FC68BCE8: Zpub74vSYSU12tQqbxYb7YYwUSHq8bUVSe3iKxG8JHmuLjEu1K3ZjjgH1refsgdUhxR4WttV1NFQzJnZZtueannW6Mau9QXs58wLWvh3ftfkk97

347BCBE3: Zpub74bnCwDLdCa7ytzd2unjhLL842fv4RocsHbRBcpP8Nv2DGp6eCzZfJesd55YvYv1TkVrsyCNSV8HcoHcHpmm1GvmhuYmschCbYcTR1orqKB
`,
			"wsh(sortedmulti(2,[79E1C26F/48'/0'/0'/2']Zpub753vSk6B5CuYmJBvgBQYmBUghHoApQHtgJWthN7WmrJsaRaCGuQFguZTXdJxCL2rUbFdsVcLuT9ASoKGtRtug3A6SZmhfaMzYH5yc11Da3h,[FC68BCE8/48'/0'/0'/2']Zpub74vSYSU12tQqbxYb7YYwUSHq8bUVSe3iKxG8JHmuLjEu1K3ZjjgH1refsgdUhxR4WttV1NFQzJnZZtueannW6Mau9QXs58wLWvh3ftfkk97,[347BCBE3/48'/0'/0'/2']Zpub74bnCwDLdCa7ytzd2unjhLL842fv4RocsHbRBcpP8Nv2DGp6eCzZfJesd55YvYv1TkVrsyCNSV8HcoHcHpmm1GvmhuYmschCbYcTR1orqKB))",
		},
		{
			"test",
			`Name: test
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
			"",
			"[4bbaa801/84'/0'/0']zpub6qpFgGWoG7bKmDDMvmwHBvg6inZAb2KF2Vg8h4fKJ2ickSZ71PsMmRg1FyRWAS6PqPCSzd5CB6PHixx64k6q5svZNZd9bEoCWJuMSkSRzJx",
			"wpkh([4bbaa801/84'/0'/0']xpub6C9j4wAxxkWN4cq8G4N2mkV6NrGGhnLFCGdh8GsYY1xreEveW5YEXJMjDZWLAcnZ26xqVft5FmgBxPixdMGoVQZMdtEJRRADxrn4facoGnx)",
		},
		{
			"",
			"zpub6qpFgGWoG7bKmDDMvmwHBvg6inZAb2KF2Vg8h4fKJ2ickSZ71PsMmRg1FyRWAS6PqPCSzd5CB6PHixx64k6q5svZNZd9bEoCWJuMSkSRzJx",
			"wpkh([00000000/84'/0'/0']xpub6C9j4wAxxkWN4cq8G4N2mkV6NrGGhnLFCGdh8GsYY1xreEveW5YEXJMjDZWLAcnZ26xqVft5FmgBxPixdMGoVQZMdtEJRRADxrn4facoGnx)",
		},
		{
			"",
			"xpub6C9j4wAxxkWN4cq8G4N2mkV6NrGGhnLFCGdh8GsYY1xreEveW5YEXJMjDZWLAcnZ26xqVft5FmgBxPixdMGoVQZMdtEJRRADxrn4facoGnx",
			"pkh(xpub6C9j4wAxxkWN4cq8G4N2mkV6NrGGhnLFCGdh8GsYY1xreEveW5YEXJMjDZWLAcnZ26xqVft5FmgBxPixdMGoVQZMdtEJRRADxrn4facoGnx)",
		},
	}
	for _, test := range tests {
		got, err := OutputDescriptor([]byte(test.encoded))
		if err != nil {
			t.Fatalf("failed to parse:\n%q\nerror: %v", test.encoded, err)
		}
		want, err := bip380.Parse(test.desc)
		if err != nil {
			t.Fatalf("failed to parse reference:\n%q\nerror: %v", test.desc, err)
		}
		want.Title = test.name
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
