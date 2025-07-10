// command controller is the user interface for engraving SeedHammer plates.
package main

import (
	"fmt"
	"io"
	"log"
	"machine"
	"os"
	"time"

	"seedhammer.com/driver/st25r3916"
	"seedhammer.com/nfc/ndef"
	"seedhammer.com/nfc/type2"
	"seedhammer.com/nfc/type4"
	"seedhammer.com/nfc/type5"
)

func main() {
	if err := run(); err != nil {
		log.Printf("main: %v\n", err)
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

	log.Println("**** here we go! **** ")
	defer log.Println("**** all done! **** ")

	nfc := &st25r3916.Device{
		Bus: dataI2C,
		Int: DATA_INT,
	}
	trans := type5.NewTransceiver(nfc, st25r3916.FIFOSize)
	t4temu := type4.NewTag(nfc)
	contents := make([]byte, 8*1024)
	defer nfc.RadioOff()
	for {
		active, err := nfc.Detect()
		if err != nil {
			log.Printf("Detect: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var r io.Reader
		if active {
			// Reset the tag emulator when the
			// external field is off.
			t4temu.Reset()

			r, err = poll(nfc, trans)
			if err != nil {
				log.Printf("Poll: %v", err)
				continue
			}
			if r == nil {
				continue
			}
			r = ndef.NewReader(r)
		} else {
			r = t4temu
		}
		for {
			n, err := r.Read(contents)
			log.Printf("%s (%v)\n", contents[:n], err)
			if err != nil {
				break
			}
		}
	}
}

func poll(d *st25r3916.Device, trans *type5.Transceiver) (io.Reader, error) {
	if err := d.SetProtocol(st25r3916.ISO15693); err != nil {
		return nil, err
	}
	tag15693, err := type5.NewReader(trans, trans.ReadCapacity())
	if err == nil {
		return tag15693, nil
	}
	log.Printf("iso15693: %v", err)
	if err := d.SetProtocol(st25r3916.ISO14443a); err != nil {
		return nil, err
	}
	tag14443, err := type2.NewReader(d)
	if err != nil {
		log.Printf("iso14443: %v", err)
		// Ignore read errors.
		return nil, nil
	}
	return tag14443, nil
}
