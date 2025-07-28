// package bip380 implements support for [BIP380], output descriptors.
//
// [BIP380]: https://bips.dev/380/
package bip380

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/bip32"
)

type Descriptor struct {
	Title     string
	Script    Script
	Threshold int
	Type      MultisigType
	Keys      []Key
}

type Key struct {
	Network           *chaincfg.Params
	MasterFingerprint uint32
	DerivationPath    bip32.Path
	Children          []Derivation
	KeyData           []byte
	ChainCode         []byte
	ParentFingerprint uint32
}

type Derivation struct {
	Type DerivationType
	// Index is the child index, without the hardening offset.
	// For RangeDerivations, Index is the start of the range.
	Index    uint32
	Hardened bool
	// End represents the end of a RangeDerivation.
	End uint32
}

type DerivationType int

const (
	ChildDerivation DerivationType = iota
	WildcardDerivation
	RangeDerivation
)

type Script int

const (
	UnknownScript Script = iota
	P2SH
	P2SH_P2WSH
	P2SH_P2WPKH
	P2PKH
	P2WSH
	P2WPKH
	P2TR
)

func (s Script) String() string {
	switch s {
	case P2SH:
		return "Legacy (P2SH)"
	case P2SH_P2WSH:
		return "Nested Segwit (P2SH-P2WSH)"
	case P2SH_P2WPKH:
		return "Nested Segwit (P2SH-P2WPKH)"
	case P2PKH:
		return "Legacy (P2PKH)"
	case P2WSH:
		return "Segwit (P2WSH)"
	case P2WPKH:
		return "Segwit (P2WPKH)"
	case P2TR:
		return "Taproot (P2TR)"
	default:
		return "Unknown"
	}
}

type MultisigType int

const (
	Singlesig MultisigType = iota
	SortedMulti
)

func (k Key) ExtendedKey() *hdkeychain.ExtendedKey {
	var fp [4]byte
	binary.BigEndian.PutUint32(fp[:], k.ParentFingerprint)
	childNum := uint32(0)
	if len(k.DerivationPath) > 0 {
		childNum = k.DerivationPath[len(k.DerivationPath)-1]
	}
	return hdkeychain.NewExtendedKey(
		k.Network.HDPublicKeyID[:],
		k.KeyData, k.ChainCode, fp[:], uint8(len(k.DerivationPath)),
		childNum, false,
	)
}

func (k Key) String() string {
	return k.ExtendedKey().String()
}

// Singlesig reports whether the script is for single-sig.
func (s Script) Singlesig() bool {
	for _, s2 := range []Script{P2PKH, P2WPKH, P2SH_P2WPKH, P2TR} {
		if s == s2 {
			return true
		}
	}
	return false
}

// DerivationPath returns the standard derivation path
// for the script. It panics if the script is unknown.
func (s Script) DerivationPath() bip32.Path {
	switch s {
	case P2WPKH:
		return bip32.Path{
			hdkeychain.HardenedKeyStart + 84,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 0,
		}
	case P2PKH:
		return bip32.Path{
			hdkeychain.HardenedKeyStart + 44,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 0,
		}
	case P2SH_P2WPKH:
		return bip32.Path{
			hdkeychain.HardenedKeyStart + 49,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 0,
		}
	case P2TR:
		return bip32.Path{
			hdkeychain.HardenedKeyStart + 86,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 0,
		}
	case P2SH:
		return bip32.Path{
			hdkeychain.HardenedKeyStart + 45,
		}
	case P2SH_P2WSH:
		return bip32.Path{
			hdkeychain.HardenedKeyStart + 48,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 1,
		}
	case P2WSH:
		return bip32.Path{
			hdkeychain.HardenedKeyStart + 48,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 2,
		}
	}
	panic("unknown script")
}

// Encode the descriptor into bip380 format.
func (d *Descriptor) Encode() string {
	return d.encode(false)
}

// EncodeCompact is like Encode, but with key origins and
// the checksum omitted.
func (d *Descriptor) EncodeCompact() string {
	return d.encode(true)
}

// encode the descriptor into bip380 format. Key origins
// and the checksum are omitted if compact is true.
func (d *Descriptor) encode(compact bool) string {
	res := new(strings.Builder)
	parens := 1
	switch d.Script {
	case P2SH_P2WSH, P2SH_P2WPKH:
		res.WriteString("sh(")
		parens++
	}
	var script string
	switch d.Script {
	case P2SH:
		script = "sh"
	case P2PKH:
		script = "pkh"
	case P2WSH, P2SH_P2WSH:
		script = "wsh"
	case P2WPKH, P2SH_P2WPKH:
		script = "wpkh"
	case P2TR:
		script = "tr"
	default:
		panic("unknown script")
	}
	res.WriteString(script)
	res.WriteByte('(')
	switch d.Type {
	case SortedMulti:
		res.WriteString("sortedmulti(")
		res.WriteString(strconv.Itoa(d.Threshold))
		res.WriteString(",")
		parens++
	}
	for i, k := range d.Keys {
		if mfp := k.MasterFingerprint; !compact && mfp != 0 {
			res.WriteByte('[')
			fmt.Fprintf(res, "%.8x", mfp)
			res.WriteString(k.DerivationPath.Encode())
			res.WriteByte(']')
		}
		res.WriteString(k.String())
		for _, d := range k.Children {
			res.WriteString(d.Encode())
		}
		if i < len(d.Keys)-1 {
			res.WriteByte(',')
		}
	}
	for range parens {
		res.WriteByte(')')
	}
	s := res.String()
	if !compact {
		sum, ok := checksum(s)
		if !ok {
			panic("impossible by construction")
		}
		s = s + "#" + sum
	}
	return s
}

func (d Derivation) Encode() string {
	res := new(strings.Builder)
	res.WriteByte('/')
	switch d.Type {
	case ChildDerivation:
		res.WriteString(strconv.Itoa(int(d.Index)))
	case WildcardDerivation:
		res.WriteByte('*')
	case RangeDerivation:
		res.WriteByte('<')
		res.WriteString(strconv.Itoa(int(d.Index)))
		res.WriteByte(';')
		res.WriteString(strconv.Itoa(int(d.End)))
		res.WriteByte('>')
	default:
		panic("invalid derivation")
	}
	if d.Hardened {
		res.WriteByte('h')
	}
	return res.String()
}

// Parse a descriptor in textual form, as described in
// https://github.com/bitcoin/bitcoin/blob/master/doc/descriptors.md.
func Parse(desc string) (*Descriptor, error) {
	desc, checksum, ok := strings.Cut(desc, "#")
	if ok && !validChecksum(desc, checksum) {
		return nil, errors.New("bip380: invalid checksum")
	}
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
		return nil, fmt.Errorf("bip380: script: %w", err)
	}
	r := &Descriptor{
		Threshold: 1,
	}
	switch script {
	case "wsh":
		r.Script = P2WSH
	case "pkh":
		r.Script = P2PKH
	case "sh":
		r.Script = P2SH
	case "wpkh":
		r.Script = P2WPKH
	case "tr":
		r.Script = P2TR
	default:
		return nil, fmt.Errorf("bip380: unknown script type: %q", script)
	}
	if script2, err := parseFunc(); err == nil {
		switch script2 {
		case "wpkh", "wsh":
			if r.Script != P2SH {
				return nil, fmt.Errorf("bip380: invalid wrapped script type: %q", script2)
			}
			if r.Script != P2SH {
				return nil, fmt.Errorf("bip380: invalid wrapped script type: %q", script2)
			}
			switch script2 {
			case "wpkh":
				r.Script = P2SH_P2WPKH
			case "wsh":
				r.Script = P2SH_P2WSH
			default:
				return nil, fmt.Errorf("bip380: unknown script type: %q", script2)
			}
			script2, err = parseFunc()
		}
		if err == nil {
			switch script2 {
			case "sortedmulti":
				r.Type = SortedMulti
			default:
				return nil, fmt.Errorf("bip380: unknown script type: %q", script2)
			}
		}
	}
	var keys []string
	switch r.Type {
	case Singlesig:
		keys = []string{desc}
	case SortedMulti:
		args := strings.Split(desc, ",")
		threshold, err := strconv.Atoi(args[0])
		if err != nil {
			return nil, fmt.Errorf("bip380: invalid multikey threshold: %q", desc)
		}
		r.Threshold = threshold
		keys = args[1:]
	}
	for _, k := range keys {
		key, err := ParseKey(r.Script.DerivationPath(), []byte(k))
		if err != nil {
			return nil, err
		}
		r.Keys = append(r.Keys, key)
	}
	return r, nil
}

// ParseKey parses an extended key on the form [mfp/path]key.
func ParseKey(impliedPath bip32.Path, enc []byte) (Key, error) {
	k := string(enc)
	key := Key{
		DerivationPath: impliedPath,
	}
	if len(k) > 0 && k[0] == '[' {
		end := strings.Index(k, "]")
		if end == -1 {
			return Key{}, fmt.Errorf("hdkey: missing ']': %q", k)
		}
		originAndPath := k[1:end]
		k = k[end+1:]
		if len(originAndPath) < 9 || originAndPath[8] != '/' {
			return Key{}, fmt.Errorf("hdkey: missing or invalid fingerprint: %q", k)
		}
		fp, err := hex.DecodeString(originAndPath[:8])
		if err != nil {
			return Key{}, fmt.Errorf("hdkey: invalid fingerprint: %q", k)
		}
		key.MasterFingerprint = binary.BigEndian.Uint32(fp)
		path, err := bip32.ParsePath(originAndPath[9:])
		if err != nil {
			return Key{}, fmt.Errorf("hdkey: invalid derivation path: %q", k)
		}
		key.DerivationPath = path
	}
	if xpubEnd := strings.Index(k, "/"); xpubEnd != -1 {
		children := k[xpubEnd+1:]
		k = k[:xpubEnd]
		childPath, err := parsePath(children)
		if err != nil {
			return Key{}, fmt.Errorf("hdkey: invalid children path: %q", children)
		}
		key.Children = childPath
	}
	script, xpub, err := ParseExtendedKey(k)
	if err != nil {
		return Key{}, err
	}
	if key.DerivationPath == nil {
		// This is a key with no implicit or explicit derivation path, fall back
		// to deriving the path from the SLIP-132 version. We support only the
		// common ones, because ideally the derivation path should always be provided.
		key.DerivationPath = script.DerivationPath()
	}
	pub, err := xpub.ECPubKey()
	if err != nil {
		return Key{}, fmt.Errorf("hdkey: invalid public key: %q", k)
	}
	network, err := bip32.NetworkFor(xpub)
	if err != nil {
		return Key{}, fmt.Errorf("hdkey: invalid network: %q", k)
	}
	key.Network = network
	key.ChainCode = xpub.ChainCode()
	key.KeyData = pub.SerializeCompressed()
	key.ParentFingerprint = xpub.ParentFingerprint()
	return key, nil
}

// ParseExtendedKey parses an extended key, along with its implied script type. It returns
// normalized xpubs where the version bytes matches a network.
func ParseExtendedKey(k string) (Script, *hdkeychain.ExtendedKey, error) {
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
	var script Script
	switch version {
	case xpubVer, tpubVer:
		script = P2PKH
	case zpubVer:
		script = P2WPKH
	case YpubVer:
		script = P2SH_P2WSH
	case ZpubVer:
		script = P2WSH
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

func parsePath(path string) ([]Derivation, error) {
	var res []Derivation
	for _, p := range strings.Split(path, "/") {
		var d Derivation
		switch {
		case p == "*":
			d = Derivation{Type: WildcardDerivation}
		case p == "*'" || p == "*h":
			d = Derivation{Type: WildcardDerivation, Hardened: true}
		case len(p) > 2 && p[0] == '<' && p[len(p)-1] == '>':
			starts, ends, ok := strings.Cut(p[1:len(p)-1], ";")
			if !ok {
				return nil, fmt.Errorf("invalid range path element: %q", p)
			}
			start, err := bip32.ParsePathElement(starts)
			if err != nil {
				return nil, err
			}
			end, err := bip32.ParsePathElement(ends)
			if err != nil {
				return nil, err
			}
			// Assume for now that ranges can't be hardened.
			if start > end || start >= hdkeychain.HardenedKeyStart || end >= hdkeychain.HardenedKeyStart {
				return nil, fmt.Errorf("invalid range path element: %q", p)
			}
			d = Derivation{
				Type:  RangeDerivation,
				Index: start,
				End:   end,
			}
		default:
			e, err := bip32.ParsePathElement(p)
			if err != nil {
				return nil, err
			}
			d = Derivation{
				Type:  ChildDerivation,
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
