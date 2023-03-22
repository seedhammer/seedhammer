// package nonstandard implements parsing of non-standard bitcoin output
// descriptors.
package nonstandard

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"seedhammer.com/bc/urtypes"
)

func OutputDescriptor(enc []byte) (any, error) {
	switch {
	case bytes.HasPrefix(enc, []byte("# BlueWallet Multisig setup file")):
		return parseBlueWalletDescriptor(string(enc))
	default:
		return nil, errors.New("ur: unrecognized bytes format")
	}
}

func parseBlueWalletDescriptor(txt string) (urtypes.OutputDescriptor, error) {
	lines := strings.Split(txt, "\n")
	var desc urtypes.OutputDescriptor
	var nkeys int
	var path []uint32
	seenKeys := make(map[string]bool)
	// Parse header.
	for len(lines) > 0 {
		l := lines[0]
		lines = lines[1:]
		if strings.HasPrefix(l, "#") {
			continue
		}
		if l == "" {
			break
		}
		header := strings.SplitN(l, ": ", 2)
		if len(header) != 2 {
			return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid header: %q", l)
		}
		key, val := header[0], header[1]
		if seenKeys[key] {
			return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: duplicate header %q", key)
		}
		seenKeys[key] = true
		switch key {
		case "Name":
		case "Policy":
			if _, err := fmt.Sscanf(val, "%d of %d", &desc.Threshold, &nkeys); err != nil {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid Policy header: %q", val)
			}
		case "Derivation":
			parts := strings.Split(val, "/")
			if len(parts) == 0 || parts[0] != "m" {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid derivation: %q", val)
			}
			parts = parts[1:]
			for _, p := range parts {
				var err error
				offset := uint32(0)
				if strings.HasSuffix(p, "h") || strings.HasSuffix(p, "'") {
					offset = hdkeychain.HardenedKeyStart
					p = p[:len(p)-1]
				}
				idx, err := strconv.ParseInt(p, 10, 0)
				if err != nil {
					return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid derivation: %q", val)
				}
				iu32 := uint32(idx)
				if int64(iu32) != idx || iu32+offset < iu32 {
					return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: derivation out of range: %q", val)
				}
				path = append(path, iu32+offset)
			}
		case "Format":
			switch val {
			case "P2WSH":
				desc.Type = urtypes.P2WSH
			case "P2SH":
				desc.Type = urtypes.P2SH
			case "P2WSH-P2SH":
				desc.Type = urtypes.P2SH_P2WSH
			default:
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: unknown format %q", val)
			}
		}
	}
	// Parse keys.
	for len(lines) > 0 {
		l := lines[0]
		lines = lines[1:]
		if strings.HasPrefix(l, "#") {
			continue
		}
		if l == "" {
			continue
		}
		fpAndxpub := strings.SplitN(l, ": ", 2)
		if len(fpAndxpub) != 2 {
			return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid xpub: %q", l)
		}
		fpHex, xpub := fpAndxpub[0], fpAndxpub[1]
		key, err := hdkeychain.NewKeyFromString(xpub)
		if err != nil {
			return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid xpub: %q", xpub)
		}
		pub, err := key.ECPubKey()
		if err != nil {
			return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid xpub: %q: %v", xpub, err)
		}
		fp, err := hex.DecodeString(fpHex)
		if err != nil {
			return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid fingerprint: %q", fpHex)
		}
		if len(fp) > 4 {
			return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid fingerprint: %q", fpHex)
		}
		desc.Keys = append(desc.Keys, urtypes.KeyDescriptor{
			MasterFingerprint: binary.BigEndian.Uint32(fp),
			DerivationPath:    path,
			KeyData:           pub.SerializeCompressed(),
			ChainCode:         key.ChainCode(),
			ParentFingerprint: key.ParentFingerprint(),
		})
	}
	if nkeys != len(desc.Keys) {
		return urtypes.OutputDescriptor{}, fmt.Errorf("ur: expected %d keys, but got %d", nkeys, len(desc.Keys))
	}
	sortKeys(desc.Keys)
	return desc, nil
}

// sortKeys lexicographically as specified in BIP 383.
func sortKeys(keys []urtypes.KeyDescriptor) {
	sort.Slice(keys, func(i, j int) bool {
		return bytes.Compare(keys[i].KeyData, keys[j].KeyData) == -1
	})
}
