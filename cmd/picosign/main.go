// Command picosign replaces the signature of UF2 firmware images
// for the rp2350 microcontroller from Raspberry Pi. The purpose
// is to support external signing, in contrast with the official
// picotool that require the signing key in clear text.
//
// Subcommand hash extracts the data to be signed, and subcommand
// sign replaces the signature.
package main

import (
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"seedhammer.com/picobin"
	"seedhammer.com/uf2"
)

var (
	hashCmd   = flag.NewFlagSet("hash", flag.ExitOnError)
	signCmd   = flag.NewFlagSet("sign", flag.ExitOnError)
	clear     = signCmd.Bool("clear", false, "zero public key and signature")
	pubKey    = signCmd.String("pubkey", "", "public key in hex-encoded 33-byte compressed form")
	sig       = signCmd.String("sig", "", "64 byte signature")
	sigFormat = signCmd.String("sigfmt", "raw", "signature format ('der', 'raw')")
)

func main() {
	if len(os.Args) <= 1 {
		fmt.Fprintf(os.Stderr, "picosign: specify 'hash' or 'sign' command\n")
		os.Exit(2)
	}
	args := os.Args[2:]
	var err error
	switch cmd := os.Args[1]; cmd {
	case "hash":
		if err := hashCmd.Parse(args); err != nil {
			hashCmd.Usage()
		}
		err = hash()
	case "sign":
		if err := signCmd.Parse(args); err != nil {
			signCmd.Usage()
		}
		err = sign()
	default:
		fmt.Fprintf(os.Stderr, "picosign: unknown command: %q\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "picosign: %v\n", err)
		os.Exit(2)
	}
}

// hash dumps the data covered by a firmware image to standard out.
func hash() error {
	path := hashCmd.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := uf2.NewReader(f, uf2.FamilyRP2350ARMSigned)
	firmware, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	finfo, err := picobin.Read(firmware)
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	hashData, err := finfo.HashData(firmware, r.StartAddr)
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	hash := sha256.Sum256(hashData)
	os.Stdout.Write(hash[:])
	return nil
}

// sign replaces the public key and signature of a signed
// firmware image.
func sign() (cerr error) {
	keyAndSig := make([]byte, 128)
	if !*clear {
		if *pubKey == "" {
			return errors.New("sign: specify a public key (-pubkey <key>)")
		}
		pkeyEnc, err := hex.DecodeString(*pubKey)
		if err != nil {
			return fmt.Errorf("sign: invalid publi key %q", *pubKey)
		}
		pkey, err := secp256k1.ParsePubKey(pkeyEnc)
		if err != nil {
			return fmt.Errorf("sign: invalid public key: %w", err)
		}
		if *sig == "" {
			return fmt.Errorf("sign: specify a signature (-sig <signature>)")
		}
		sigEnc, err := hex.DecodeString(*sig)
		if err != nil {
			return fmt.Errorf("sign: invalid signature: %q", *sig)
		}
		pkey.X().FillBytes(keyAndSig[:32])
		pkey.Y().FillBytes(keyAndSig[32:64])
		sig := keyAndSig[64:]
		switch f := *sigFormat; f {
		case "der":
			var sigDer struct {
				B1, B2 *big.Int
			}
			rest, err := asn1.Unmarshal(sigEnc, &sigDer)
			if err != nil {
				return fmt.Errorf("sign: invalid signature: %w", err)
			}
			if len(rest) > 0 {
				return fmt.Errorf("sign: trailing data after signature")
			}
			sigDer.B1.FillBytes(sig[:32])
			sigDer.B2.FillBytes(sig[32:])
		case "raw":
			if len(sigEnc) != len(sig) {
				return fmt.Errorf("sign: got %d signature bytes, expected %d", len(sigEnc), len(sig))
			}
			copy(sig, sigEnc)
		default:
			return fmt.Errorf("sign: invalid signature format: %q", f)
		}
	}
	path := signCmd.Arg(0)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); cerr == nil {
			cerr = err
		}
	}()
	r := uf2.NewReader(f, uf2.FamilyRP2350ARMSigned)
	firmware, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("sign: %s: %v", path, err)
	}
	finfo, err := picobin.Read(firmware)
	if err != nil {
		return fmt.Errorf("sign: %s: %v", path, err)
	}
	// Rewind the file and seek to the signature.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rw := uf2.NewReader(f, uf2.FamilyRP2350ARMSigned)
	skip := make([]byte, finfo.SignatureOffset)
	if _, err := io.ReadFull(rw, skip); err != nil {
		return err
	}
	// Rewrite the signature in place.
	_, err = rw.Write(keyAndSig)
	return err
}
