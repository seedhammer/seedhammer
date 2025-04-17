// command controller is the user interface for engraving SeedHammer plates.
package main

import (
	"errors"
	"fmt"
	"io"
	"machine"
	"os"
	"time"

	"seedhammer.com/driver/st25r3916"
	"seedhammer.com/nfc/iso15693"
	"seedhammer.com/nfc/ndef"
)

func main() {
	if err := run(); err != nil {
		fmt.Printf("main: %v\n", err)
	}
	os.Exit(2)
}

func run() error {
	const (
		DATA_SDA = machine.GPIO28
		DATA_SCL = machine.GPIO29
		DATA_INT = machine.GPIO26
	)
	dataI2C := machine.I2C0
	if err := dataI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: DATA_SDA, SCL: DATA_SCL}); err != nil {
		return fmt.Errorf("data I2C: %w", err)
	}
	DATA_INT.Configure(machine.PinConfig{Mode: machine.PinInput})

	// usbpd := ap33772s.New(dataI2C, )
	// for {
	// 	st, err := usbpd.ReadStatus()
	// 	if err != nil {
	// 		return fmt.Errorf("data I2C: %w", err)
	// 	}
	// 	if st == 0 {
	// 		break
	// 	}
	// }
	fmt.Println("**** here we go! **** ")
	time.Sleep(500 * time.Millisecond)
	defer fmt.Println("**** all done! **** ")

	nfc := &st25r3916.Device{
		Bus: dataI2C,
		Int: DATA_INT,
	}
	if err := nfc.Configure(); err != nil {
		return err
	}
	// nfc.SetCRC(true, true)
	// return nfc.Listen()
	// prot := st25r3916.ISO14443a
	prot := st25r3916.ISO15693
	if err := nfc.RadioOn(prot); err != nil {
		return err
	}
	defer nfc.RadioOff()
	fmt.Println("**** opening tag")
	// tag, err := iso14443a.Open(nfc)
	trans := iso15693.NewTransceiver(nfc, st25r3916.FIFOSize)
	tag, err := iso15693.Open(trans, trans.DecodedSize())
	fmt.Println("***** tag opened", err)
	if err != nil {
		return err
	}
	buf := make([]byte, 512)
	for {
		n, err := tag.Read(buf)
		fmt.Printf("%s\n", buf[:n])
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return err
			}
			break
		}
	}
	contents := ndef.NewReader(tag)
	if err := contents.Next(); err != nil {
		return err
	}
	return nil
}
