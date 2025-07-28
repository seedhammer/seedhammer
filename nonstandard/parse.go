// package nonstandard implements parsing of non-standard bitcoin output
// descriptors.
package nonstandard

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"seedhammer.com/bip32"
	"seedhammer.com/bip380"
)

// ElectrumSeed reports whether the seed phrase is a valid Electrum
// seed.
func ElectrumSeed(phrase string) bool {
	// Compute version number.
	// From https://electrum.readthedocs.io/en/latest/seedphrase.html#version-number
	mac := hmac.New(sha512.New, []byte("Seed version"))
	mac.Write([]byte(phrase))
	hsum := hex.EncodeToString(mac.Sum(nil))
	switch {
	case strings.HasPrefix(hsum, "01"), strings.HasPrefix(hsum, "100"), strings.HasPrefix(hsum, "101"):
		return true
	}
	return false
}

func OutputDescriptor(enc []byte) (*bip380.Descriptor, error) {
	if bw, err := parseBlueWalletDescriptor(string(enc)); err == nil && bw.Title != "" {
		return bw, nil
	}
	desc, err := bip380.Parse(string(enc))
	if err == nil {
		return desc, nil
	}
	var jsonDesc struct {
		Label      string `json:"label"`
		Descriptor string `json:"descriptor"`
	}
	if err := json.Unmarshal(enc, &jsonDesc); err == nil {
		desc, err := bip380.Parse(jsonDesc.Descriptor)
		if err != nil {
			return desc, err
		}
		desc.Title = jsonDesc.Label
		return desc, err
	}
	// If the derivation path of a cosigner key expression matches
	// a single-sig script, convert it to an output descriptor.
	if k, err := bip380.ParseKey(nil, enc); err == nil {
		for _, s := range []bip380.Script{bip380.P2PKH, bip380.P2WPKH, bip380.P2SH_P2WPKH} {
			path := s.DerivationPath()
			if !reflect.DeepEqual(path, k.DerivationPath) {
				continue
			}
			return &bip380.Descriptor{
				Type:      bip380.Singlesig,
				Threshold: 1,
				Script:    s,
				Keys: []bip380.Key{
					k,
				},
			}, nil
		}
	}
	return nil, errors.New("nonstandard: unrecognized output descriptor format")
}

func parseBlueWalletDescriptor(txt string) (*bip380.Descriptor, error) {
	lines := strings.Split(txt, "\n")
	desc := &bip380.Descriptor{
		Type: bip380.SortedMulti,
	}
	var nkeys int
	var path bip32.Path
	seenKeys := make(map[string]string)
	for len(lines) > 0 {
		l := lines[0]
		lines = lines[1:]
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		header := strings.SplitN(l, ": ", 2)
		if len(header) != 2 {
			return nil, fmt.Errorf("bluewallet: invalid header: %q", l)
		}
		key, val := header[0], header[1]
		if old, seen := seenKeys[key]; seen {
			if old != val {
				return nil, fmt.Errorf("bluewallet: inconsistent header value %q", key)
			}
			continue
		}
		seenKeys[key] = val
		switch key {
		case "Name":
			desc.Title = val
		case "Policy":
			if _, err := fmt.Sscanf(val, "%d of %d", &desc.Threshold, &nkeys); err != nil {
				return nil, fmt.Errorf("bluewallet: invalid Policy header: %q", val)
			}
		case "Derivation":
			if !strings.HasPrefix(val, "m/") {
				return nil, fmt.Errorf("bluewallet: invalid derivation: %q", val)
			}
			p, err := bip32.ParsePath(val[2:])
			if err != nil {
				return nil, fmt.Errorf("bluewallet: invalid derivation: %q", val)
			}
			path = p
		case "Format":
			switch val {
			case "P2WSH":
				desc.Script = bip380.P2WSH
			case "P2SH":
				desc.Script = bip380.P2SH
			case "P2WSH-P2SH":
				desc.Script = bip380.P2SH_P2WSH
			default:
				return nil, fmt.Errorf("bluewallet: unknown format %q", val)
			}
		default:
			_, xpub, err := bip380.ParseExtendedKey(val)
			if err != nil {
				return nil, fmt.Errorf("bluewallet: invalid xpub: %q", val)
			}
			pub, err := xpub.ECPubKey()
			if err != nil {
				return nil, fmt.Errorf("bluewallet: invalid xpub: %q: %v", xpub, err)
			}
			fp, err := hex.DecodeString(key)
			if err != nil {
				return nil, fmt.Errorf("bluewallet: invalid fingerprint: %q", key)
			}
			if len(fp) > 4 {
				return nil, fmt.Errorf("bluewallet: invalid fingerprint: %q", key)
			}
			network, err := bip32.NetworkFor(xpub)
			if err != nil {
				return nil, fmt.Errorf("bluewallet: unknown network: %q", key)
			}
			desc.Keys = append(desc.Keys, bip380.Key{
				Network:           network,
				MasterFingerprint: binary.BigEndian.Uint32(fp),
				DerivationPath:    path,
				KeyData:           pub.SerializeCompressed(),
				ChainCode:         xpub.ChainCode(),
				ParentFingerprint: xpub.ParentFingerprint(),
			})
		}
	}
	if nkeys != len(desc.Keys) {
		return nil, fmt.Errorf("bluewallet: expected %d keys, but got %d", nkeys, len(desc.Keys))
	}
	return desc, nil
}

type Decoder struct {
	parts [][]byte
}

func (d *Decoder) Add(part string) error {
	header, rem, ok := strings.Cut(part, " ")
	if !ok {
		return errors.New("nonstandard: invalid animated QR part")
	}
	var m, n int
	if _, err := fmt.Sscanf(header, "p%dof%d", &m, &n); err != nil {
		return errors.New("nonstandard: invalid animated QR part")
	}
	if m < 1 || m > n {
		return errors.New("nonstandard: invalid animated QR part")
	}
	if n != len(d.parts) {
		d.parts = make([][]byte, n)
	}
	if d.parts[m-1] == nil {
		d.parts[m-1] = []byte(rem)
	}
	return nil
}

func (d *Decoder) Progress() float32 {
	if len(d.parts) == 0 {
		return 0
	}
	n := 0
	for _, p := range d.parts {
		if p != nil {
			n++
		}
	}
	return float32(n) / float32(len(d.parts))
}

func (d *Decoder) Result() []byte {
	var res []byte
	for _, p := range d.parts {
		if p == nil {
			return nil
		}
		res = append(res, p...)
	}
	return res
}
