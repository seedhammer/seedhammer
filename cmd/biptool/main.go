// Command biptool manipulates Bitcoin seeds, keys and
// mnemonics. It reads codex32 shares, BIP39 mnemonics,
// extended keys (xprv, xpub), or raw entropy from standard
// in.
//
// Do not use for real funds or important secrets!
package main

import (
	"bytes"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/bip32"
	"seedhammer.com/bip39"
	"seedhammer.com/bip85"
	"seedhammer.com/codex32"
)

var (
	seedFlags = flag.NewFlagSet("seed", flag.ExitOnError)
	threshold = seedFlags.Int("threshold", 1, "threshold number of shares")
	id        = seedFlags.String("id", "", "4-character bech32 id")
	seedLen   = seedFlags.Int("seedlen", 32, "seed length in bytes (16-64)")
	seedIdx   = seedFlags.String("idx", "S", "codex32 share index ('A', 'C', 'D', ...) or 'S'")
	hrp       = seedFlags.String("hrp", "ms", "codex32 HRP")

	randFlags = flag.NewFlagSet("rand", flag.ExitOnError)
	randLen   = randFlags.Int("n", 32, "number of bytes to generate (min 16)")

	interpolateFlags = flag.NewFlagSet("interpolate", flag.ExitOnError)
	interpolateIdx   = interpolateFlags.String("idx", "S", "codex32 share index ('A', 'C', 'D', ...) or 'S'")

	deriveFlags = flag.NewFlagSet("derive", flag.ExitOnError)
	derivePath  = deriveFlags.String("path", "m", "the derivation path (e.g. 'm/1234h/567h/8')")

	bip39Flags = flag.NewFlagSet("bip39", flag.ExitOnError)
	words      = bip39Flags.Int("words", 24, "number of seed words")

	deriveSeedFlags = flag.NewFlagSet("seed", flag.ExitOnError)
	bytesLength     = deriveSeedFlags.Int("n", 0, "seed length in bytes (zero means all available)")
)

var chain = &chaincfg.MainNetParams

type codex32Conf struct {
	Threshold int
	ID        string
	SeedLen   int
	Index     rune
}

func main() {
	if err := run(os.Stdout, os.Stdin, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "biptool: %v\n", err)
		os.Exit(2)
	}
}

func run(stdout io.Writer, stdin io.Reader, args []string) error {
	if len(args) == 0 {
		return errors.New("missing command (derive, interpolate, rand, seed)")
	}
	cmd := args[0]
	args = args[1:]
	switch cmd {
	case "rand":
		if err := randFlags.Parse(args); err != nil {
			randFlags.Usage()
		}
		return genRand(stdout)
	case "seed":
		if err := seedFlags.Parse(args); err != nil {
			seedFlags.Usage()
		}
		return genSeed(stdout, stdin)
	case "interpolate":
		if err := interpolateFlags.Parse(args); err != nil {
			interpolateFlags.Usage()
		}
		return interpolate(stdout, stdin)
	case "derive":
		if err := deriveFlags.Parse(args); err != nil {
			deriveFlags.Usage()
		}
		return derive(stdout, stdin)
	default:
		return fmt.Errorf("unknown command: %q", cmd)
	}
}

func derive(stdout io.Writer, stdin io.Reader) error {
	args := deriveFlags.Args()
	if len(args) < 1 {
		return errors.New("derive: specify format (pubkey, privkey, xpub, xprv, bip39, seed)")
	}
	dpath := *derivePath
	var path []uint32
	if dpath != "" {
		p, err := bip32.ParsePath(dpath)
		if err != nil {
			return err
		}
		path = p
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("derive: %w", err)
	}
	s := string(bytes.TrimSpace(b))
	var seed []byte
	// Attempt to decode input as an extended key.
	xkey, err := hdkeychain.NewKeyFromString(s)
	if err != nil {
		// And then as (a set of) codex32 shares.
		shares, err := parseShares(s)
		if err != nil {
			return err
		}
		k, err := codex32.Interpolate(shares, 'S')
		if err != nil {
			return err
		}
		seed = k.Seed()
		xkey, err = hdkeychain.NewMaster(seed, chain)
		if err != nil {
			return err
		}
	}
	if len(path) > 0 {
		if path[0] != bip85.PathRoot {
			return fmt.Errorf("derive: derivation path %q must start with m/%dh/...", dpath,
				bip85.PathRoot-hdkeychain.HardenedKeyStart)
		}
		for _, p := range path {
			if p < hdkeychain.HardenedKeyStart {
				return fmt.Errorf("derive: derivation path contains unhardened elements: %s", dpath)
			}
			seed = nil
			xkey, err = xkey.Derive(p)
			if err != nil {
				return err
			}
		}
		// Derive BIP85 seed.
		pkey, err := xkey.ECPrivKey()
		if err != nil {
			return err
		}
		s := bip85.Entropy(pkey.Serialize())
		if err != nil {
			return err
		}
		seed = s
		// Derive BIP85 extended private key.
		chaincode := seed[:32]
		key := seed[32:64]
		parent := []byte{0, 0, 0, 0}
		xkey = hdkeychain.NewExtendedKey(chain.HDPrivateKeyID[:], key, chaincode, parent, 0, 0, true)
	}

	const h = hdkeychain.HardenedKeyStart
	isXprvDeriv := len(path) == 0 || (len(path) == 3 && path[1] == 32+h)
	f := args[0]
	args = args[1:]
	switch f {
	case "bip39":
		if err := bip39Flags.Parse(args); err != nil {
			bip39Flags.Usage()
		}
		n := *words
		if n < 12 || 24 < n || n%3 != 0 {
			return fmt.Errorf("derive: invalid bip39 seed length: %d", n)
		}
		if len(path) > 0 {
			if len(path) != 5 || path[1] != 39+h || path[2] != 0+h ||
				path[3] != uint32(n)+h {
				return fmt.Errorf("derive: bip39 derivation path %q does not begin with m/83696968h/39h/0h/", dpath)
			}
		}
		entLen := (n*11 - n/3) / 8
		m := bip39.New(seed[:entLen])
		fmt.Fprintln(stdout, m)
	case "seed":
		if err := deriveSeedFlags.Parse(args); err != nil {
			deriveSeedFlags.Usage()
		}
		n := *bytesLength
		if n == 0 {
			n = len(seed)
		}
		if n > len(seed) {
			return fmt.Errorf("derive: only %d bytes of seed available", len(seed))
		}
		if len(seed) == 0 {
			return fmt.Errorf("derive: no seed provided")
		}
		stdout.Write(seed[:n])
	case "xpub":
		if !isXprvDeriv {
			return fmt.Errorf("derive: xpub derivation path %q does not begin with m/83696968h/32h/", dpath)
		}
		xpub, err := xkey.Neuter()
		if err != nil {
			return fmt.Errorf("derive: %w", err)
		}
		fmt.Fprintln(stdout, xpub)
	case "xprv":
		if !isXprvDeriv {
			return fmt.Errorf("derive: xprv derivation path %q does not begin with m/83696968h/32h/", dpath)
		}
		if !xkey.IsPrivate() {
			return errors.New("derive: cannot derive a private key from a public key")
		}
		fmt.Fprintln(stdout, xkey)
	case "pubkey":
		if !isXprvDeriv {
			return fmt.Errorf("derive: pubkey derivation path %q does not begin with m/83696968h/32h/", dpath)
		}
		pkey, err := xkey.ECPubKey()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%x\n", pkey.SerializeCompressed())
	case "privkey":
		if !isXprvDeriv {
			return fmt.Errorf("derive: privkey derivation path %q does not begin with m/83696968h/32h/", dpath)
		}
		pkey, err := xkey.ECPrivKey()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%x\n", pkey.Serialize())
	default:
		return fmt.Errorf("derive: unknown format %q", f)
	}
	return nil
}

func parseCodex32Conf() (codex32Conf, error) {
	shareIdx, err := parseShareIndex(*seedIdx)
	if err != nil {
		return codex32Conf{}, err
	}
	alph := strings.ToLower(codex32.Alphabet)
	c := codex32Conf{
		Threshold: *threshold,
		ID:        strings.ToLower(*id),
		SeedLen:   *seedLen,
		Index:     shareIdx,
	}
	if len(c.ID) != 4 {
		return codex32Conf{}, fmt.Errorf("codex32: id too short: %q", c.ID)
	}
	for _, r := range c.ID {
		if !strings.ContainsRune(alph, r) {
			return codex32Conf{}, fmt.Errorf("codex32: invalid bech32 character %q in %q", r, c.ID)
		}
	}
	if c.Threshold <= 0 || len(codex32.Alphabet) <= c.Threshold {
		return codex32Conf{}, fmt.Errorf("codex32: invalid threshold: %d", c.Threshold)
	}
	if c.Threshold == 1 {
		// Zero is the encoding of a single share.
		c.Threshold = 0
	}
	if c.SeedLen < 16 || 64 < c.SeedLen {
		return codex32Conf{}, fmt.Errorf("codex32: seed length %d not in range [16,64]", c.SeedLen)
	}
	return c, nil
}

func parseShareIndex(s string) (rune, error) {
	sl := strings.ToLower(s)
	alph := strings.ToLower(codex32.Alphabet)
	if len(sl) != 1 || !strings.ContainsRune(alph, rune(sl[0])) {
		return 0, fmt.Errorf("codex32: invalid share index: %q", s)
	}
	return rune(sl[0]), nil
}

func genRand(stdout io.Writer) error {
	n := *randLen
	if n < 16 {
		return errors.New("rand: length must be at least 16")
	}
	r := io.LimitReader(rand.Reader, int64(n))
	_, err := io.Copy(stdout, r)
	return err
}

// genSeed encodes a secret in codex32 form.
func genSeed(stdout io.Writer, stdin io.Reader) error {
	conf, err := parseCodex32Conf()
	if err != nil {
		return err
	}
	seed, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	if got, want := len(seed), conf.SeedLen; got != want {
		return fmt.Errorf("codex32: read %d bytes of entropy, expected %d", got, want)
	}
	key, err := codex32.NewSeed(*hrp, conf.Threshold, conf.ID, conf.Index, seed)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, key)
	return nil
}

// interpolate a set of codex32 shares.
func interpolate(stdout io.Writer, stdin io.Reader) error {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	shares, err := parseShares(string(input))
	if err != nil {
		return err
	}
	shareIdx, err := parseShareIndex(*interpolateIdx)
	if err != nil {
		return err
	}
	k, err := codex32.Interpolate(shares, shareIdx)
	if err != nil {
		return err
	}
	shareIdx++
	if shareIdx == 'S' {
		shareIdx++
	}
	fmt.Fprintln(stdout, k)
	return nil
}

func parseShares(s string) ([]codex32.String, error) {
	var shares []codex32.String
	for l := range strings.Lines(s) {
		l = strings.TrimSpace(l)
		k, err := codex32.New(string(l))
		if err != nil {
			return nil, err
		}
		shares = append(shares, k)
	}
	return shares, nil
}
