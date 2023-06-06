// package nonstandard implements parsing of non-standard bitcoin output
// descriptors.
package nonstandard

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
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
	if bytes.HasPrefix(header, []byte("# ")) && (bytes.Contains(header, []byte("Multisig setup file")) || bytes.Contains(header, []byte("Exported from Nunchuk"))) {
		return parseBlueWalletDescriptor(string(enc))
	}
	desc, err := parseTextOutputDescriptor(string(enc))
	if err == nil {
		return desc, nil
	}
	var jsonDesc struct {
		Descriptor string `json:"descriptor"`
	}
	if err := json.Unmarshal(enc, &jsonDesc); err == nil {
		return parseTextOutputDescriptor(jsonDesc.Descriptor)
	}
	return urtypes.OutputDescriptor{}, errors.New("nonstandard: unrecognized output descriptor format")
}

func parseBlueWalletDescriptor(txt string) (urtypes.OutputDescriptor, error) {
	lines := strings.Split(txt, "\n")
	desc := urtypes.OutputDescriptor{
		Type: urtypes.SortedMulti,
	}
	var nkeys int
	var path urtypes.Path
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
			if !strings.HasPrefix(val, "m/") {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid derivation: %q", val)
			}
			p, err := parseDerivationPath(val[2:])
			if err != nil {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: invalid derivation: %q", val)
			}
			path = p
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
			network, err := networkFor(xpub)
			if err != nil {
				return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: unknown network: %q", key)
			}
			desc.Keys = append(desc.Keys, urtypes.KeyDescriptor{
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
		return urtypes.OutputDescriptor{}, fmt.Errorf("bluewallet: expected %d keys, but got %d", nkeys, len(desc.Keys))
	}
	return desc, nil
}

func networkFor(xpub *hdkeychain.ExtendedKey) (*chaincfg.Params, error) {
	networks := []*chaincfg.Params{
		&chaincfg.MainNetParams,
		&chaincfg.TestNet3Params,
		&chaincfg.SimNetParams,
	}
	for _, n := range networks {
		if xpub.IsForNet(n) {
			return n, nil
		}
	}
	return nil, errors.New("unknown network")
}

func parsePathElement(p string) (uint32, error) {
	offset := uint32(0)
	if strings.HasSuffix(p, "h") || strings.HasSuffix(p, "'") {
		offset = hdkeychain.HardenedKeyStart
		p = p[:len(p)-1]
	}
	idx, err := strconv.ParseInt(p, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("invalid path element: %q", p)
	}
	iu32 := uint32(idx)
	if int64(iu32) != idx || iu32+offset < iu32 {
		return 0, fmt.Errorf("path element out of range: %q", p)
	}
	return iu32 + offset, nil
}

func parseDerivationPath(path string) (urtypes.Path, error) {
	var res urtypes.Path
	parts := strings.Split(path, "/")
	for _, p := range parts {
		p, err := parsePathElement(p)
		if err != nil {
			return nil, err
		}
		res = append(res, p)
	}
	return res, nil
}

func parsePath(path string) ([]urtypes.Derivation, error) {
	var res []urtypes.Derivation
	for _, p := range strings.Split(path, "/") {
		var d urtypes.Derivation
		switch {
		case p == "*":
			d = urtypes.Derivation{Type: urtypes.WildcardDerivation}
		case p == "*'" || p == "*h":
			d = urtypes.Derivation{Type: urtypes.WildcardDerivation, Hardened: true}
		case len(p) > 2 && p[0] == '<' && p[len(p)-1] == '>':
			starts, ends, ok := strings.Cut(p[1:len(p)-1], ";")
			if !ok {
				return nil, fmt.Errorf("invalid range path element: %q", p)
			}
			start, err := parsePathElement(starts)
			if err != nil {
				return nil, err
			}
			end, err := parsePathElement(ends)
			if err != nil {
				return nil, err
			}
			// Assume for now that ranges can't be hardened.
			if start > end || start >= hdkeychain.HardenedKeyStart || end >= hdkeychain.HardenedKeyStart {
				return nil, fmt.Errorf("invalid range path element: %q", p)
			}
			d = urtypes.Derivation{
				Type:  urtypes.RangeDerivation,
				Index: start,
				End:   end,
			}
		default:
			e, err := parsePathElement(p)
			if err != nil {
				return nil, err
			}
			d = urtypes.Derivation{
				Type:  urtypes.ChildDerivation,
				Index: e,
			}
			if d.Index >= hdkeychain.HardenedKeyStart {
				d.Index -= hdkeychain.HardenedKeyStart
				d.Hardened = true
			}
		}
		res = append(res, d)
	}
	return res, nil
}

// parseTextOutputDescriptor parses descriptors in textual form, as described in
// https://github.com/bitcoin/bitcoin/blob/master/doc/descriptors.md.
func parseTextOutputDescriptor(desc string) (urtypes.OutputDescriptor, error) {
	// Chop off checksum, if any.
	if start := len(desc) - 9; start >= 0 && desc[start] == '#' {
		desc = desc[:start]
	}
	parseFunc := func() (string, error) {
		for i, r := range desc {
			if r == '(' {
				f := desc[:i]
				if desc[len(desc)-1] != ')' {
					return "", errors.New("missing ')'")
				}
				desc = desc[i+1 : len(desc)-1]
				return f, nil
			}
		}
		return "", errors.New("missing '('")
	}
	script, err := parseFunc()
	if err != nil {
		return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: script: %w", err)
	}
	r := urtypes.OutputDescriptor{
		Threshold: 1,
	}
	switch script {
	case "wsh":
		r.Script = urtypes.P2WSH
	case "pkh":
		r.Script = urtypes.P2PKH
	case "sh":
		r.Script = urtypes.P2SH
	case "wpkh":
		r.Script = urtypes.P2WPKH
	case "tr":
		r.Script = urtypes.P2TR
	default:
		return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: unknown script type: %q", script)
	}
	if script2, err := parseFunc(); err == nil {
		switch script2 {
		case "wpkh", "wsh":
			if r.Script != urtypes.P2SH {
				return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: invalid wrapped script type: %q", script2)
			}
			if r.Script != urtypes.P2SH {
				return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: invalid wrapped script type: %q", script2)
			}
			switch script2 {
			case "wpkh":
				r.Script = urtypes.P2SH_P2WPKH
			case "wsh":
				r.Script = urtypes.P2SH_P2WSH
			default:
				return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: unknown script type: %q", script2)
			}
			script2, err = parseFunc()
		}
		if err == nil {
			switch script2 {
			case "sortedmulti":
				r.Type = urtypes.SortedMulti
			default:
				return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: unknown script type: %q", script2)
			}
		}
	}
	var keys []string
	switch r.Type {
	case urtypes.Singlesig:
		keys = []string{desc}
	case urtypes.SortedMulti:
		args := strings.Split(desc, ",")
		threshold, err := strconv.Atoi(args[0])
		if err != nil {
			return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: invalid multikey threshold: %q", desc)
		}
		r.Threshold = threshold
		keys = args[1:]
	}
	for _, k := range keys {
		key := urtypes.KeyDescriptor{}
		if len(k) > 0 && k[0] == '[' {
			end := strings.Index(k, "]")
			if end == -1 {
				return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: missing ']': %q", k)
			}
			originAndPath := k[1:end]
			k = k[end+1:]
			if len(originAndPath) < 9 || originAndPath[8] != '/' {
				return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: missing or invalid fingerprint: %q", k)
			}
			fp, err := hex.DecodeString(originAndPath[:8])
			if err != nil {
				return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: invalid fingerprint: %q", k)
			}
			key.MasterFingerprint = binary.BigEndian.Uint32(fp)
			path, err := parseDerivationPath(originAndPath[9:])
			if err != nil {
				return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: invalid derivation path: %q", k)
			}
			key.DerivationPath = path
		}
		if xpubEnd := strings.Index(k, "/"); xpubEnd != -1 {
			children := k[xpubEnd+1:]
			k = k[:xpubEnd]
			childPath, err := parsePath(children)
			if err != nil {
				return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: invalid children path: %q", k)
			}
			key.Children = childPath
		}
		xpub, err := hdkeychain.NewKeyFromString(k)
		if err != nil {
			return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: invalid extended key: %q", k)
		}
		pub, err := xpub.ECPubKey()
		if err != nil {
			return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: invalid public key: %q", k)
		}
		network, err := networkFor(xpub)
		if err != nil {
			return urtypes.OutputDescriptor{}, fmt.Errorf("descriptor: invalid network: %q", k)
		}
		key.Network = network
		key.ChainCode = xpub.ChainCode()
		key.KeyData = pub.SerializeCompressed()
		key.ParentFingerprint = xpub.ParentFingerprint()
		r.Keys = append(r.Keys, key)
	}
	return r, nil
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
