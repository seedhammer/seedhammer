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
	"seedhammer.com/nfc/poller"
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

	nfc := st25r3916.New(dataI2C, DATA_INT)
	dev := newNFCDevice(nfc)
	go func() {
		for {
			time.Sleep(5 * time.Second)
			dev.Interrupt()
		}
	}()
	defer dev.Close()
	p := poller.New(dev)
	for {
		got, err := io.ReadAll(p)
		if len(got) > 0 {
			log.Println(string(got), err)
		} else {
			log.Println("(empty)", err)
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("read err: %v", err)
			}
		}
	}
}

type nfcDev struct {
	*st25r3916.Device
	trans    *type5.Transceiver
	iso15693 bool
}

func newNFCDevice(d *st25r3916.Device) *nfcDev {
	return &nfcDev{
		Device: d,
		trans:  type5.NewTransceiver(d, st25r3916.FIFOSize),
	}
}

func (d *nfcDev) SetProtocol(mode poller.Protocol) error {
	d.iso15693 = false
	var prot st25r3916.Protocol
	switch mode {
	case poller.ISO14443a:
		prot = st25r3916.ISO14443a
	case poller.ISO15693:
		d.iso15693 = true
		prot = st25r3916.ISO15693
	default:
		panic("unsupported mode")
	}
	return d.Device.SetProtocol(prot)
}

func (d *nfcDev) Write(buf []byte) (int, error) {
	if d.iso15693 {
		return d.trans.Write(buf)
	}
	return d.Device.Write(buf)
}

func (d *nfcDev) Read(buf []byte) (int, error) {
	if d.iso15693 {
		return d.trans.Read(buf)
	}
	return d.Device.Read(buf)
}

func (d nfcDev) ReadCapacity() int {
	if d.iso15693 {
		return d.trans.ReadCapacity()
	}
	return st25r3916.FIFOSize
}
