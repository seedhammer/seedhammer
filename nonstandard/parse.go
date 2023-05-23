// package nonstandard implements parsing of non-standard bitcoin output
// descriptors.
package nonstandard

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"seedhammer.com/bc/urtypes"
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

func OutputDescriptor(enc []byte) (urtypes.OutputDescriptor, error) {
	header, _, _ := bytes.Cut(enc, []byte("\n"))
	switch {
	case bytes.HasPrefix(header, []byte("# ")) &&
		(bytes.Contains(header, []byte("Multisig setup file")) || bytes.Contains(header, []byte("Exported from Nunchuk"))):
		return parseBlueWalletDescriptor(string(enc))
	default:
		return urtypes.OutputDescriptor{}, errors.New("nonstandard: unrecognized output descriptor format")
	}
}

func parseBlueWalletDescriptor(txt string) (urtypes.OutputDescriptor, error) {
	lines := strings.Split(txt, "\n")
	desc := urtypes.OutputDescriptor{
		Type: urtypes.SortedMulti,
	}
	var nkeys int
	var path []uint32
	seenKeys := make(map[string]string)
	for len(lines) > 0 {
		l := strings.TrimSpace(lines[0])
		lines = lines[1:]
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		header := strings.SplitN(l, ": ", 2)
		if len(header) != 2 {
			return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid header: %q", l)
		}
		key, val := header[0], header[1]
		if old, seen := seenKeys[key]; seen {
			if old != val {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: inconsistent header value %q", key)
			}
			continue
		}
		seenKeys[key] = val
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
				desc.Script = urtypes.P2WSH
			case "P2SH":
				desc.Script = urtypes.P2SH
			case "P2WSH-P2SH":
				desc.Script = urtypes.P2SH_P2WSH
			default:
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: unknown format %q", val)
			}
		default:
			xpub, err := hdkeychain.NewKeyFromString(val)
			if err != nil {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid xpub: %q", val)
			}
			pub, err := xpub.ECPubKey()
			if err != nil {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid xpub: %q: %v", xpub, err)
			}
			fp, err := hex.DecodeString(key)
			if err != nil {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid fingerprint: %q", key)
			}
			if len(fp) > 4 {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid fingerprint: %q", key)
			}
			desc.Keys = append(desc.Keys, urtypes.KeyDescriptor{
				MasterFingerprint: binary.BigEndian.Uint32(fp),
				DerivationPath:    path,
				KeyData:           pub.SerializeCompressed(),
				ChainCode:         xpub.ChainCode(),
				ParentFingerprint: xpub.ParentFingerprint(),
			})
		}
	}
	if nkeys != len(desc.Keys) {
		return urtypes.OutputDescriptor{}, fmt.Errorf("ur: expected %d keys, but got %d", nkeys, len(desc.Keys))
	}
	return desc, nil
}
