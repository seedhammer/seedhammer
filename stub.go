// command controller is the user interface for engraving SeedHammer plates.
package main

import (
	"fmt"
	"io"
	"log"
	"machine"
	"os"
	"time"

	"seedhammer.com/bip39"
	"seedhammer.com/driver/st25r3916"
	"seedhammer.com/nfc/iso14443a"
	"seedhammer.com/nfc/iso15693"
	"seedhammer.com/nfc/ndef"
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
	// time.Sleep(500 * time.Millisecond)
	defer log.Println("**** all done! **** ")

	nfc := &st25r3916.Device{
		Bus: dataI2C,
		Int: DATA_INT,
	}
	// if err := nfc.Configure(); err != nil {
	// 	return err
	// }
	var lastPoll time.Time
	const pollFrequency = 0 * 500 * time.Millisecond
	trans := iso15693.NewTransceiver(nfc, st25r3916.FIFOSize)
	contents := make([]byte, 8*1024)
	defer nfc.RadioOff()
	for {
		if err := nfc.Configure(); err != nil {
			return err
		}
		if err := nfc.RadioOn(st25r3916.Detect); err != nil {
			log.Printf("RadioOn: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		// nfc.DumpMeasurements()
		for {
			// log.Println("detecting...")
			if err := nfc.Detect(nil); err != nil {
				log.Printf("Detect: %v", err)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			// nfc.DumpMeasurements()
			// log.Println("detect wakeup")
			now := time.Now()
			// Don't poll too often.
			if now.Sub(lastPoll) < pollFrequency {
				// But keep the detection loop running on the
				// device.
				continue
			}
			lastPoll = now
			break
		}
		if false {
			r, err := poll(nfc, trans)
			if err != nil {
				log.Printf("Poll: %v", err)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			if r != nil {
				// buf := make([]byte, 512)
				// for {
				// 	n, err := r.Read(buf)
				// 	log.Printf("%s\n", buf[:n])
				// 	if err != nil {
				// 		if err != io.EOF {
				// 			return err
				// 		}
				// 		break
				// 	}
				// }
				nr := ndef.NewReader(r)
				n, err := nr.Read(contents)
				if err == nil || err == io.EOF {
					log.Printf("Succes! %q", string(contents[:n]))
					m, err := bip39.Parse(contents[:n])
					log.Println("message", m, err)
					continue
				}
				// Ignore read errors.
				log.Printf("ndef: %v", err)
			}
		}
		nfc.RadioOn(st25r3916.Listen)
		if err := nfc.Listen(1500*time.Millisecond, nil); err != nil {
			log.Println("nfc.Listen:", err)
		}
		lastPoll = time.Now()
	}
	return nil
}

func poll(d *st25r3916.Device, trans *iso15693.Transceiver) (io.Reader, error) {
	if err := d.RadioOn(st25r3916.ISO15693); err != nil {
		return nil, err
	}
	tag15693, err := iso15693.Open(trans, trans.DecodedSize())
	if err == nil {
		return tag15693, nil
	}
	log.Printf("iso15693: %v", err)
	if err := d.RadioOn(st25r3916.ISO14443a); err != nil {
		return nil, err
	}
	tag14443, err := iso14443a.Open(d)
	if err != nil {
		log.Printf("iso14443: %v", err)
		// Ignore read errors.
		return nil, nil
	}
	return tag14443, nil
}
