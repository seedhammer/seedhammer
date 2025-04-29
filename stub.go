// command controller is the user interface for engraving SeedHammer plates.
package main

import (
	"device/rp"
	"errors"
	"fmt"
	"io"
	"machine"
	"os"
	"time"

	"seedhammer.com/bip39"
	"seedhammer.com/driver/ap33772s"
	"seedhammer.com/driver/st25r3916"
	"seedhammer.com/nfc/iso14443a"
	"seedhammer.com/nfc/iso15693"
	"seedhammer.com/nfc/ndef"
)

func main() {
	uart := machine.UART0
	for uart.Bus.UARTFR.HasBits(rp.UART0_UARTFR_TXFF) {
	}
	boot0 := rp.POWMAN.BOOT0.Get()
	rp.POWMAN.BOOT0.Set(boot0 + 1)
	uart.Bus.UARTDR.Set(uint32('a' + boot0))
	for range 1 {
		for _, c := range "qoot\n" {
			for uart.Bus.UARTFR.HasBits(rp.UART0_UARTFR_TXFF) {
			}
			uart.Bus.UARTDR.Set(uint32(c))
		}
	}
	for _, c := range "done\n" {
		for uart.Bus.UARTFR.HasBits(rp.UART0_UARTFR_TXFF) {
		}
		uart.Bus.UARTDR.Set(uint32(c))
	}
	// for {
	// c := 'a'
	// for {
	// 	for uart.Bus.UARTFR.HasBits(rp.UART0_UARTFR_TXFF) {
	// 	}
	// 	uart.Bus.UARTDR.Set(uint32(c))
	// 	for uart.Bus.UARTFR.HasBits(rp.UART0_UARTFR_TXFF) {
	// 	}
	// 	uart.Bus.UARTDR.Set(uint32('\n'))
	// 	c++
	// 	if c > 'z' {
	// 		c = 'a'
	// 	}
	// }
	// }
	cr := rp.POWMAN.CHIP_RESET.Get()
	fmt.Printf("CHIP_RESET: %.32b POR: %v BOR: %v RUN_LOW: %v\n", cr, cr&(0b1<<16) != 0, cr&(0b1<<17) != 0, cr&(0b1<<18) != 0)
	// wd := rp.WATCHDOG.CTRL.Get()
	// fmt.Printf("WATCHDOG: %.32b\n", wd)
	// rp.POWMAN.WDSEL.Set(0xffffffff)
	// rp.WATCHDOG.CTRL.Set(0b1<<30 | 0xffff)
	if err := run(); err != nil {
		fmt.Printf("main: %v\n", err)
	}
	os.Exit(2)
}

func run() error {
	const (
		DATA_SDA  = machine.GPIO28
		DATA_SCL  = machine.GPIO29
		DATA_INT  = machine.GPIO26
		USBPD_INT = machine.GPIO27
	)
	dataI2C := machine.I2C0
	if err := dataI2C.Configure(machine.I2CConfig{Frequency: 400_000, SDA: DATA_SDA, SCL: DATA_SCL}); err != nil {
		return fmt.Errorf("data I2C: %w", err)
	}

	go func() {
		for {
			before := time.Now()
			for time.Since(before) < 100*time.Millisecond {
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	usbpd := ap33772s.New(dataI2C, USBPD_INT)
	if err := usbpd.Configure(); err != nil {
		return err
	}
	// if err := usbpd.AdjustVoltage(9*1000, 20*1000); err != nil {
	// 	return err
	// }
	fmt.Println("**** here we go! **** ")
	// time.Sleep(500 * time.Millisecond)
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
	var lastPoll time.Time
	const pollFrequency = 500 * time.Millisecond
	trans := iso15693.NewTransceiver(nfc, st25r3916.FIFOSize)
	defer nfc.RadioOff()
	contents := make([]byte, 8*1024)
	for {
		if err := nfc.RadioOn(st25r3916.Detect); err != nil {
			return err
		}
		for {
			if err := nfc.Detect(nil); err != nil {
				return err
			}
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
		fmt.Println("card detected, reading...")
		r, err := poll(nfc, trans)
		if err != nil {
			return err
		}
		if r == nil {
			continue
		}
		// buf := make([]byte, 512)
		// for {
		// 	n, err := r.Read(buf)
		// 	fmt.Printf("%s\n", buf[:n])
		// 	if err != nil {
		// 		if !errors.Is(err, io.EOF) {
		// 			return err
		// 		}
		// 		break
		// 	}
		// }
		nr := ndef.NewReader(r)
		n, err := nr.Read(contents)
		if err != nil && !errors.Is(err, io.EOF) {
			// Ignore read errors.
			fmt.Println("ndef", err)
			continue
		}
		fmt.Println("Succes!", string(contents[:n]))
		m, err := bip39.Parse(contents[:n])
		fmt.Println(m)
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
	fmt.Println("iso15693", err)
	if err := d.RadioOn(st25r3916.ISO14443a); err != nil {
		return nil, err
	}
	tag14443, err := iso14443a.Open(d)
	if err != nil {
		fmt.Println("iso14443", err)
		// Ignore read errors.
		return nil, nil
	}
	return tag14443, nil
}
