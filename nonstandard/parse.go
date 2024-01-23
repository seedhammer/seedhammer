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
	if bw, err := parseBlueWalletDescriptor(string(enc)); err == nil && bw.Title != "" {
		return bw, nil
	}
	desc, err := parseTextOutputDescriptor(string(enc))
	if err == nil {
		return desc, nil
	}
	var jsonDesc struct {
		Label      string `json:"label"`
		Descriptor string `json:"descriptor"`
	}
	if err := json.Unmarshal(enc, &jsonDesc); err == nil {
		desc, err := parseTextOutputDescriptor(jsonDesc.Descriptor)
		if err != nil {
			return desc, err
		}
		desc.Title = jsonDesc.Label
		return desc, err
	}
	// If the derivation path of a cosigner key expression matches
	// a single-sig script, convert it to an output descriptor.
	if k, err := parseHDKeyExpr(nil, enc); err == nil {
		for _, s := range []urtypes.Script{urtypes.P2PKH, urtypes.P2WPKH, urtypes.P2SH_P2WPKH} {
			path := s.DerivationPath()
			if !reflect.DeepEqual(path, k.DerivationPath) {
				continue
			}
			return urtypes.OutputDescriptor{
				Type:      urtypes.Singlesig,
				Threshold: 1,
				Script:    s,
				Keys: []urtypes.KeyDescriptor{
					k,
				},
			}, nil
		}
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
		l := lines[0]
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
			desc.Title = val
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
			_, xpub, err := parseHDKey(val)
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
		key, err := parseHDKeyExpr(r.Script.DerivationPath(), []byte(k))
		if err != nil {
			return urtypes.OutputDescriptor{}, fmt.Errorf("hdkey: %w", err)
		}
		r.Keys = append(r.Keys, key)
	}
	return r, nil
}

// parseHDKeyExpr parses an extended key on the form [mfp/path]key.
func parseHDKeyExpr(impliedPath urtypes.Path, enc []byte) (urtypes.KeyDescriptor, error) {
	k := string(enc)
	key := urtypes.KeyDescriptor{
		DerivationPath: impliedPath,
	}
	if len(k) > 0 && k[0] == '[' {
		end := strings.Index(k, "]")
		if end == -1 {
			return urtypes.KeyDescriptor{}, fmt.Errorf("hdkey: missing ']': %q", k)
		}
		originAndPath := k[1:end]
		k = k[end+1:]
		if len(originAndPath) < 9 || originAndPath[8] != '/' {
			return urtypes.KeyDescriptor{}, fmt.Errorf("hdkey: missing or invalid fingerprint: %q", k)
		}
		fp, err := hex.DecodeString(originAndPath[:8])
		if err != nil {
			return urtypes.KeyDescriptor{}, fmt.Errorf("hdkey: invalid fingerprint: %q", k)
		}
		key.MasterFingerprint = binary.BigEndian.Uint32(fp)
		path, err := parseDerivationPath(originAndPath[9:])
		if err != nil {
			return urtypes.KeyDescriptor{}, fmt.Errorf("hdkey: invalid derivation path: %q", k)
		}
		key.DerivationPath = path
	}
	if xpubEnd := strings.Index(k, "/"); xpubEnd != -1 {
		children := k[xpubEnd+1:]
		k = k[:xpubEnd]
		childPath, err := parsePath(children)
		if err != nil {
			return urtypes.KeyDescriptor{}, fmt.Errorf("hdkey: invalid children path: %q", k)
		}
		key.Children = childPath
	}
	script, xpub, err := parseHDKey(k)
	if err != nil {
		return urtypes.KeyDescriptor{}, err
	}
	if key.DerivationPath == nil {
		// This is a key with no implicit or explicit derivation path, fall back
		// to deriving the path from the SLIP-132 version. We support only the
		// common ones, because ideally the derivation path should always be provided.
		key.DerivationPath = script.DerivationPath()
	}
	pub, err := xpub.ECPubKey()
	if err != nil {
		return urtypes.KeyDescriptor{}, fmt.Errorf("hdkey: invalid public key: %q", k)
	}
	network, err := networkFor(xpub)
	if err != nil {
		return urtypes.KeyDescriptor{}, fmt.Errorf("hdkey: invalid network: %q", k)
	}
	key.Network = network
	key.ChainCode = xpub.ChainCode()
	key.KeyData = pub.SerializeCompressed()
	key.ParentFingerprint = xpub.ParentFingerprint()
	return key, nil
}

// parseHDKey parses an extended key, along with its implied script type. It returns
// normalized xpubs where the version bytes matches a network.
func parseHDKey(k string) (urtypes.Script, *hdkeychain.ExtendedKey, error) {
	xpub, err := hdkeychain.NewKeyFromString(k)
	if err != nil {
		return 0, nil, fmt.Errorf("hdkey: invalid extended key: %q", k)
	}
	const (
		xpubVer = "0488b21e"
		zpubVer = "04b24746"
		ypubVer = "049d7cb2"
		YpubVer = "0295b43f"
		ZpubVer = "02aa7ed3"

		tpubVer = "043587cf"
	)
	version := hex.EncodeToString(xpub.Version())
	var script urtypes.Script
	switch version {
	case xpubVer, tpubVer:
		script = urtypes.P2PKH
	case zpubVer:
		script = urtypes.P2WPKH
	case YpubVer:
		script = urtypes.P2SH_P2WSH
	case ZpubVer:
		script = urtypes.P2WSH
	default:
		return 0, nil, fmt.Errorf("hdkey: unsupported version: %s", version)
	}
	// Now we have a derivation path, normalize the version bytes to xpub.
	switch version {
	case zpubVer, ypubVer, YpubVer, ZpubVer:
		xpub.SetNet(&chaincfg.MainNetParams)
	case tpubVer:
		xpub.SetNet(&chaincfg.TestNet3Params)
	}
	return script, xpub, nil
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
