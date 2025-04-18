// command controller is the user interface for engraving SeedHammer plates.
package main

import (
	"device/rp"
	"errors"
	"fmt"
	"io"
	"machine"
	"os"

	"seedhammer.com/driver/ap33772s"
	"seedhammer.com/driver/st25r3916"
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
	// for {
	// 	if err := nfc.DetectCard(); err != nil {
	// 		return err
	// 	}
	// 	fmt.Println("card detected")
	// 	time.Sleep(500 * time.Millisecond)
	// }
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
