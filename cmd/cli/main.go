// command cli is the internal tool for testing the SeedHammer engraver.
package main

import (
	"bytes"
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
)

var (
	serialDev = flag.String("device", "", "serial device")
	dryrun    = flag.Bool("n", false, "dry run")
	output    = flag.String("o", "plates", "output plates to directory")
	side      = flag.String("side", "front", "plate side, front or back")
	threshold = flag.Int("threshold", 2, "threshold")
	shares    = flag.Int("shares", 3, "number of shares in total")
	seedonly  = flag.Bool("seedonly", false, "seed-only mode")
	mnemonic  = flag.String("mnemonic", "flip begin artist fringe online release swift genre wool general transfer arm", "mnemonic")
)

func main() {
	flag.Parse()
	m, err := bip39.ParseMnemonic(*mnemonic)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid mnemonic: %v\n", err)
		os.Exit(1)
	}
	plateDesc := genPlate(m)
	if *serialDev != "" {
		var s int
		switch *side {
		case "back":
			s = 0
		case "front":
			s = 1
		default:
			fmt.Fprintf(os.Stderr, "-side must be 'front' or 'back'\n")
			os.Exit(1)
		}
		err = hammer(plateDesc, s, *serialDev)
	} else {
		if err := os.MkdirAll(*output, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		err = dump(plateDesc, *output)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func dump(plateDesc backup.PlateDesc, output string) error {
	const ppmm = 10
	for i := range plateDesc.Descriptor.Keys {
		desc := plateDesc
		desc.KeyIdx = i
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
			file := filepath.Join(output, fmt.Sprintf("plate-%d-side-%d.png", i, s))
			if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func genPlate(m0 bip39.Mnemonic) backup.PlateDesc {
	plate := backup.PlateDesc{
		Title:  "Satoshis stash",
		Font:   &constant.Font,
		KeyIdx: 0,
		Descriptor: urtypes.OutputDescriptor{
			Threshold: *threshold,
			Script:    urtypes.P2WSH,
		},
	}
	if *seedonly {
		plate.Descriptor.Script = urtypes.UnknownScript
	}
	for i := 0; i < *shares; i++ {
		var m bip39.Mnemonic
		if i == plate.KeyIdx {
			m = m0
			plate.Mnemonic = m
		} else {
			m = make(bip39.Mnemonic, len(m0))
			for j := range m {
				m[j] = bip39.RandomWord()
			}
			m = m.FixChecksum()
		}
		seed := bip39.MnemonicSeed(m, "")
		path := urtypes.Path{
			hdkeychain.HardenedKeyStart + 48,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 0,
			hdkeychain.HardenedKeyStart + 2,
		}
		mk, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
		if err != nil {
			panic(err)
		}
		mfp, xpub, err := bip32.Derive(mk, path)
		if err != nil {
			panic(err)
		}
		pub, err := xpub.ECPubKey()
		if err != nil {
			panic(err)
		}
		plate.Descriptor.Keys = append(plate.Descriptor.Keys, urtypes.KeyDescriptor{
			MasterFingerprint: mfp,
			DerivationPath:    path,
			KeyData:           pub.SerializeCompressed(),
			ChainCode:         xpub.ChainCode(),
			ParentFingerprint: xpub.ParentFingerprint(),
		})
	}
	return plate
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
