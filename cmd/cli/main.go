// command cli is the internal tool for testing the SeedHammer engraver.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/backup"
	"seedhammer.com/bc/urtypes"
	"seedhammer.com/bip32"
	"seedhammer.com/bip39"
	"seedhammer.com/engrave"
	"seedhammer.com/font/constant"
	"seedhammer.com/mjolnir"
	"seedhammer.com/nonstandard"
)

var (
	serialDev  = flag.String("device", "", "serial device")
	dryrun     = flag.Bool("n", false, "dry run")
	output     = flag.String("o", "plates", "output plates to directory")
	side       = flag.String("side", "front", "plate side, front or back")
	descriptor = flag.String("descriptor", "wpkh([97a6d3c2/84h/1h/0h]tpubDD5cTgxiP4qYJgBgkS6arjQH3GsJEHExFZWvumhNGGe4gBShn9u3b4TdpG2DvRg3knNXV7fBdmaw6cH2kKYdk2aXjQZYsnTchA4aFsZWehG)", "output descriptor")
	mnemonic   = flag.String("mnemonic", "vocal tray giggle tool duck letter category pattern train magnet excite swamp", "seed phrase")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if *mnemonic == "" {
		return errors.New("specify a seed")
	}
	m, err := bip39.ParseMnemonic(*mnemonic)
	if err != nil {
		return fmt.Errorf("invalid mnemonic: %w", err)
	}
	seed := bip39.MnemonicSeed(m, "")
	var desc urtypes.OutputDescriptor
	if *descriptor != "" {
		desc, err = nonstandard.OutputDescriptor([]byte(*descriptor))
		if err != nil {
			return err
		}
	}
	network := &chaincfg.MainNetParams
	if len(desc.Keys) > 0 {
		network = desc.Keys[0].Network
	}
	mk, err := hdkeychain.NewMaster(seed, network)
	if err != nil {
		return err
	}
	if *descriptor == "" {
		path := urtypes.Path{0}
		mfp, xpub, err := bip32.Derive(mk, path)
		if err != nil {
			return fmt.Errorf("failed to derive key: %w", err)
		}
		pub, err := xpub.ECPubKey()
		if err != nil {
			return fmt.Errorf("failed to derive public key: %w", err)
		}
		desc = urtypes.OutputDescriptor{
			Threshold: 1,
			Script:    urtypes.UnknownScript,
			Type:      urtypes.Singlesig,
			Keys: []urtypes.KeyDescriptor{
				{
					Network:           network,
					DerivationPath:    path,
					MasterFingerprint: mfp,
					KeyData:           pub.SerializeCompressed(),
					ChainCode:         xpub.ChainCode(),
					ParentFingerprint: xpub.ParentFingerprint(),
				},
			},
		}
	}
	if len(desc.Keys) == 0 {
		return errors.New("descriptor contains no keys")
	}
	keyIdx := -1
	for i, k := range desc.Keys {
		_, xpub, err := bip32.Derive(mk, k.DerivationPath)
		if err != nil {
			// A derivation that generates an invalid key is by itself very unlikely,
			// but also means that the seed doesn't match this xpub.
			continue
		}
		if k.String() == xpub.String() {
			keyIdx = i
			break
		}
	}
	if keyIdx == -1 {
		return errors.New("seed is not among the descriptor keys")
	}
	plate := backup.PlateDesc{
		Font:       &constant.Font,
		Descriptor: desc,
		KeyIdx:     keyIdx,
		Mnemonic:   m,
	}
	if *serialDev != "" {
		var s int
		switch *side {
		case "back":
			s = 0
		case "front":
			s = 1
		default:
			return fmt.Errorf("-side must be 'front' or 'back'")
		}
		err = hammer(plate, s, *serialDev)
	} else {
		if err := os.MkdirAll(*output, 0o755); err != nil {
			return err
		}
		err = dump(plate, *output)
	}
	return err
}

func dump(desc backup.PlateDesc, output string) error {
	const ppmm = 24
	plate, err := backup.Engrave(mjolnir.Millimeter, mjolnir.StrokeWidth, desc)
	if err != nil {
		return err
	}
	bounds := plate.Size.Bounds()
	bounds = image.Rectangle{
		Min: bounds.Min.Mul(ppmm),
		Max: bounds.Max.Mul(ppmm),
	}
	for s := range plate.Sides {
		img := image.NewNRGBA(bounds)
		r := engrave.NewRasterizer(img, img.Bounds(), ppmm/mjolnir.Millimeter, mjolnir.StrokeWidth*ppmm)
		se := plate.Sides[s]
		se.Engrave(r)
		r.Rasterize()
		buf := new(bytes.Buffer)
		if err := png.Encode(buf, img); err != nil {
			return err
		}
		file := filepath.Join(output, fmt.Sprintf("plate-%d-side-%d.png", desc.KeyIdx, s))
		if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func hammer(plateDesc backup.PlateDesc, side int, dev string) error {
	plate, err := backup.Engrave(mjolnir.Millimeter, mjolnir.StrokeWidth, plateDesc)
	if err != nil {
		return err
	}
	if side >= len(plate.Sides) {
		return fmt.Errorf("no such side: %d", side)
	}
	s, err := mjolnir.Open(dev)
	if err != nil {
		return err
	}
	defer s.Close()

	prog := &mjolnir.Program{
		DryRun: *dryrun,
	}
	plate.Sides[side].Engrave(prog)
	prog.Prepare()
	quit := make(chan os.Signal, 1)
	cancel := make(chan struct{})
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	engraveErr := make(chan error)
	go func() {
		<-quit
		signal.Reset(os.Interrupt)
		close(cancel)
		<-engraveErr
		os.Exit(1)
	}()
	go func() {
		engraveErr <- mjolnir.Engrave(s, prog, nil, cancel)
	}()
	plate.Sides[side].Engrave(prog)
	return <-engraveErr
}
