//go:build tinygo

// package st25r3916 implements a driver for the [ST25R3916] NFC reader device.
//
// [ST25R3916]: https://www.st.com/resource/en/datasheet/st25r3916.pdf
package st25r3916

import (
	"errors"
	"fmt"
	"io"
	"machine"
	"time"
)

type Device struct {
	Bus    *machine.I2C
	Int    machine.Pin
	txBits uint8
	txCRC  bool
	rxCRC  bool

	scratch [100]byte
}

// FIFOSize is the number of bytes that can be
// read without risking overflow
const FIFOSize = 512 - 2 // Make room for the CRC bytes.

type Protocol int

const (
	ISO15693 Protocol = iota
	ISO14443a
)

func (d *Device) Configure() error {
	// Reset.
	if err := d.command(cmdSetDefault); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Initialize according to the datasheet, section 4.1 "Power-on sequence".
	datasheetSetup := d.scratch[:3]
	datasheetSetup[0] = cmdTestAccess
	datasheetSetup[1] = 0x04
	datasheetSetup[2] = 0x10
	if err := d.Bus.Tx(i2cAddr, datasheetSetup, nil); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Disable the MCU_CLK pin.
	if err := d.writeReg(regIOConf1, 0b11<<out_cl); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Increase IO drive strength, as recommended in table 20.
	if err := d.writeReg(regIOConf2, 0b1<<io_drv_lvl); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Calibrate capacitive sensor.
	if err := d.writeReg(regCapSensorCtrl, 0); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	if err := d.command(cmdCalibrateCapSensor); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Calibration takes up to 3 ms.
	time.Sleep(3 * time.Millisecond)
	// n, err := d.readReg(regCapSensor)
	// if err != nil {
	// 	panic(err)
	// }
	// fmt.Println("cal", n)
	// for {
	// 	if err := d.command(cmdMeasureCap); err != nil {
	// 		return fmt.Errorf("st25r3916: %w", err)
	// 	}
	// 	time.Sleep(1 * time.Millisecond)
	// 	n, err := d.readReg(regADConvOut)
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// 	fmt.Println("n", n)
	// 	time.Sleep(300 * time.Millisecond)
	// }
	// Start oscillator.
	if err := d.writeReg(regOpCtrl, 0b1<<en); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Wait for ready.
	for {
		if d.Int.Get() {
			continue
		}
		intr, err := d.readReg(regMainIntr)
		if err != nil {
			return fmt.Errorf("st25r3916: %w", err)
		}
		if intr&(0b1<<i_osc) != 0 {
			break
		}
	}
	// Wait for interrupt line release.
	for !d.Int.Get() {
	}
	// // Measure supply
	// for i := uint8(0); i <= 4; i++ {
	// 	if err := d.writeReg(regRegulatorCtrl, i<<mpsv); err != nil {
	// 		return fmt.Errorf("st25r3916: %w", err)
	// 	}
	// 	regctrl := mustr(d.readReg(regRegulatorCtrl))
	// 	fmt.Println("measuring, existing regctrl", regctrl)
	// 	if err := d.command(cmdMeasureSupply); err != nil {
	// 		return fmt.Errorf("st25r3916: %w", err)
	// 	}
	// 	{
	// 		// Wait for ready.
	// 		for {
	// 			// if d.Int.Get() {
	// 			// 	continue
	// 			// }
	// 			intr, err := d.readReg(regTimerNFCIntr)
	// 			if err != nil {
	// 				return fmt.Errorf("st25r3916: %w", err)
	// 			}
	// 			fmt.Println("measure intr", intr)
	// 			if intr&(0b1<<i_dct) != 0 {
	// 				break
	// 			}
	// 			time.Sleep(time.Second)
	// 		}
	// 		// Wait for interrupt line release.
	// 		for !d.Int.Get() {
	// 		}
	// 	}
	// 	meas, err := d.readReg(regADConvOut)
	// 	if err != nil {
	// 		return fmt.Errorf("st25r3916: %w", err)
	// 	}
	// 	fmt.Println(meas)
	// }
	//
	// Adjust regulators.
	// First, the reg_s bit must be cycled.
	if err := d.writeReg(regRegulatorCtrl, 0b1<<reg_s); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	if err := d.writeReg(regRegulatorCtrl, 0b0<<reg_s); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Then issue the adjust regulator command.
	if err := d.command(cmdAdjustRegulator); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Adjustment takes up to 5 milliseconds.
	time.Sleep(5 * time.Millisecond)

	return nil
}

func (d *Device) Listen() error {
	if err := d.stopAll(); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	// Load PT memory with NFC-A card emulation responses.
	req := []byte{
		modeFIFO | loadPTMemory,
		// UID.
		0x04, 0xf7, 0x73, 0x7c, 0x8f, 0x61, 0x81, 0x00, 0x00, 0x00,
		// SENS_REQ.
		0x44, 0x00,
		// SEL1, SEL2, SEL3.
		0x00, 0x00, 0x00,
		// SEL1.
		// 0x88, 0x04, 0xf7, 0x73, 0x08,
		// // SEL2.
		// 0x7c, 0x8f, 0x61, 0x81, 0x13,
	}
	if err := d.Bus.Tx(i2cAddr, req, nil); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	// Set listen, iso-14443a mode.
	if err := d.writeReg(regModeDef, 0b1<<targ|omISO14443A); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	// Adjust fdel to 2, as suggested by the datasheet in table 32.
	if err := d.writeReg(regNFCIP1PassiveTarg, 2<<fdel); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	// Enable receiver and external field detector.
	if err := d.writeReg(regOpCtrl, 0b1<<en|0b1<<rx_en|0b11<<en_fd_c); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	// Start sensing.
	if err := d.command(cmdGotoSense); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	fmt.Println("waiting anxiously")
	// oldstate := uint8(0)
	// Wait for ready.
	act := false
	buf := make([]byte, 100)
	buf2 := make([]byte, 100)
	buf3 := make([]byte, 100)
	page0 := []byte{0x04, 0xf7, 0x73, 0x08, 0x7c, 0x8f, 0x61, 0x81, 0x13, 0x48, 0x00, 0x00, 0xe1, 0x10, 0x3e, 0x00}
	// page4 := []byte{0x03, 0x14, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x62, 0x69, 0x74, 0x63, 0x6f, 0x69, 0x6e, 0x2e, 0x6f}
	// page8 := []byte{0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f, 0xfe, 0x00, 0x63, 0x65, 0x2f, 0x70, 0x6f, 0x64, 0x63, 0x61}
	page4 := []byte{0x03, 0x14, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x48, 0x69, 0x20, 0x4e, 0x69, 0x63, 0x6b, 0x21, 0x20}
	page8 := []byte{0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f, 0xfe, 0x00, 0x63, 0x65, 0x2f, 0x70, 0x6f, 0x64, 0x63, 0x61}
	for {
		// if err != nil {
		// 	return fmt.Errorf("st25r3916: %w", err)
		// }
		// if oldstate != state {
		// 	oldstate = state
		// 	if state != 0 {
		// 		fmt.Printf("state %.8b\n", state)
		// 	}
		// }
		if d.Int.Get() {
			continue
		}
		// state, err := d.readReg(regPassiveTarg)
		req, intrs := d.scratch[:1], d.scratch[1:4]
		req[0] = modeReadReg | regTimerNFCIntr
		if err := d.Bus.Tx(i2cAddr, req, intrs); err != nil {
			return fmt.Errorf("st25r3916: listen: %w", err)
		}
		timInt, errInt, passInt := intrs[0], intrs[1], intrs[2]
		intr, err := d.readReg(regMainIntr)
		if err != nil {
			return fmt.Errorf("st25r3916: listen: %w", err)
		}
		act = act || passInt&(0b1<<i_wu_a) != 0
		if act {
			println("act!")
			if intr&(0b1<<i_rxe) != 0 {
				n, err := d.Read(buf)
				buf = buf[:n]
				if err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				if err := d.Transceive(page0); err != nil {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				n, err = d.Read(buf2)
				buf2 = buf2[:n]
				if err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				if err := d.Transceive(page4); err != nil {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				n, err = d.Read(buf3)
				buf3 = buf3[:n]
				if err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				if err := d.Transceive(page8); err != nil {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				fmt.Printf("buf1 %x\nbuf2 %x\nbuf3 %x\n", buf, buf2, buf3)
				fmt.Println("passIntr", passInt, "intr", intr, "errInt", errInt, "timInt", timInt /*"state", state*/)
			}
		}
	}
	return nil
}

func (d *Device) RadioOff() error {
	if err := d.command(cmdStopAll); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Disable transmitter and receiver.
	if err := d.writeReg(regOpCtrl, 0b1<<en); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	return nil
}

func (d *Device) RadioOn(prot Protocol) error {
	if err := d.stopAll(); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Enable transmitter and receiver.
	if err := d.writeReg(regOpCtrl, 0b1<<en|0b1<<tx_en|0b1<<rx_en); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Set mode.
	switch prot {
	case ISO14443a:
		if err := d.writeReg(regModeDef, omISO14443A); err != nil {
			return fmt.Errorf("st25r3916: %w", err)
		}
	case ISO15693:
		if err := d.writeReg(regModeDef, omISO15693); err != nil {
			return fmt.Errorf("st25r3916: %w", err)
		}
		if err := d.writeReg(regStreamModeDef, modeISO15693); err != nil {
			return fmt.Errorf("st25r3916: %w", err)
		}
		panic("TODO")
	}

	// Wait for the receiver to power up.
	time.Sleep(5 * time.Millisecond)
	return nil
}

func (d *Device) Transceive(tx []byte) error {
	const reqa = 0x26
	var err error
	// Special case REQA.
	if len(tx) == 1 && tx[0] == reqa {
		// Transmit REQA.
		err = d.command(cmdTransmitREQA)
	} else {
		if err := d.command(cmdClearFIFO); err != nil {
			return fmt.Errorf("st25r3916: %w", err)
		}
		if err := d.writeFIFO(tx, d.txBits); err != nil {
			return fmt.Errorf("st25r3916: %w", err)
		}
		aux := uint8(0)
		if !d.rxCRC {
			aux = 0b1 << no_crc_rx
		}
		if err := d.writeReg(regAuxDef, aux); err != nil {
			return fmt.Errorf("st25r3916: %w", err)
		}
		cmd := uint8(cmdTransmitWithCRC)
		if !d.txCRC {
			cmd = cmdTransmitWithoutCRC
		}
		err = d.command(cmd)
	}
	if err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	for {
		for d.Int.Get() {
		}
		if err := d.readError(); err != nil {
			return fmt.Errorf("st25r3916: transceive: %w", err)
		}
		intr, err := d.readReg(regMainIntr)
		if err != nil {
			return fmt.Errorf("st25r3916: %w", err)
		}
		if intr&(0b1<<i_rxe) != 0 {
			break
		}
	}
	return nil
}

func (d *Device) readError() error {
	errIntr, err := d.readReg(regErrorWakeupIntr)
	if err != nil {
		return err
	}
	switch {
	case errIntr&(0b1<<i_crc) != 0:
		return errors.New("CRC error")
	case errIntr&(0b1<<i_par) != 0:
		return errors.New("parity error")
	case errIntr&(0b1<<i_err2) != 0:
		return errors.New("soft framing error")
	case errIntr&(0b1<<i_err1) != 0:
		return errors.New("hard framing error")
	}
	return nil
}

func (d *Device) SetCRC(tx, rx bool) {
	d.txCRC = tx
	d.rxCRC = rx
}

func (d *Device) SetTxBits(bits int) {
	if bits < 0 || 7 < bits {
		panic("invalid tx bits")
	}
	d.txBits = uint8(bits)
}

func (d *Device) Read(buf []byte) (int, error) {
	req, fifoStatus := d.scratch[:1], d.scratch[1:3]
	req[0] = modeReadReg | regFIFOStatus1
	if err := d.Bus.Tx(i2cAddr, req, fifoStatus); err != nil {
		return 0, err
	}
	fifoLen := int(fifoStatus[1]&0b1100_0000)<<2 | int(fifoStatus[0])
	// Exclude the CRC bytes.
	if d.rxCRC {
		fifoLen -= 2
	}
	n := min(fifoLen, len(buf))
	req = d.scratch[:1]
	req[0] = modeFIFO | readFIFO
	if err := d.Bus.Tx(i2cAddr, req, buf[:n]); err != nil {
		return 0, fmt.Errorf("st25r3916: read: %w", err)
	}
	if n == fifoLen {
		return n, io.EOF
	}
	return n, nil
}

func (d *Device) writeFIFO(tx []byte, txBits uint8) error {
	// First write number
	const maxTxSize = 0b1<<13 - 1
	// We don't support streaming transmissions.
	if len(tx) > FIFOSize || len(tx) > maxTxSize {
		return fmt.Errorf("write FIFO: buffer too large: %d", len(tx))
	}
	if len(tx) > maxTxSize {
		return fmt.Errorf("write FIFO: buffer too large: %d bytes", len(tx))
	}
	// Set transmit size.
	req := d.scratch[:3]
	req[0] = modeWriteReg | regNumTX1
	// Most significant bits.
	req[1] = uint8(len(tx) >> 5)
	// Least significant bits and the partial bits.
	req[2] = uint8((len(tx)&0b11111)<<3) | txBits
	if err := d.Bus.Tx(i2cAddr, req, nil); err != nil {
		return err
	}
	req = d.scratch[:]
	req[0] = modeFIFO | loadFIFO
	for len(tx) > 0 {
		n := copy(req[1:], tx)
		tx = tx[n:]
		if err := d.Bus.Tx(i2cAddr, req[:n+1], nil); err != nil {
			return fmt.Errorf("load FIFO: %w", err)
		}
	}
	return nil
}

func (d *Device) stopAll() error {
	// Stop all activities.
	if err := d.command(cmdStopAll); err != nil {
		return err
	}
	// Reset RX gain.
	return d.command(cmdResetRXGain)
}

func (d *Device) readReg(reg uint8) (uint8, error) {
	req, res := d.scratch[:1], d.scratch[1:2]
	req[0] = modeReadReg | reg
	err := d.Bus.Tx(i2cAddr, req, res)
	return res[0], err
}

func (d *Device) writeReg(reg, val uint8) error {
	req := d.scratch[:2]
	req[0] = modeWriteReg | reg
	req[1] = val
	return d.Bus.Tx(i2cAddr, req, nil)
}

func (d *Device) command(cmd uint8) error {
	req := d.scratch[:1]
	req[0] = modeCommand | cmd
	return d.Bus.Tx(i2cAddr, req, nil)
}

const (
	i2cAddr = 0x50

	// Modes, see table 11 in the datasheet.
	modeWriteReg = 0b00 << 6
	modeReadReg  = 0b01 << 6
	modeCommand  = 0b11 << 6
	modeFIFO     = 0b10 << 6

	loadFIFO     = 0b000000
	readFIFO     = 0b011111
	loadPTMemory = 0b100000

	// Register addresses, space A. See table 17
	// in the datasheet.
	regIOConf1           = 0x00
	regIOConf2           = 0x01
	regOpCtrl            = 0x02
	regModeDef           = 0x03
	regBitRate           = 0x04
	regISO14443AConf     = 0x05
	regNFCIP1PassiveTarg = 0x08
	regStreamModeDef     = 0x09
	regAuxDef            = 0x0a
	regMainIntr          = 0x1a
	regTimerNFCIntr      = 0x1b
	regErrorWakeupIntr   = 0x1c
	regPassiveTargIntr   = 0x1d
	regFIFOStatus1       = 0x1e
	regFIFOStatus2       = 0x1f
	regPassiveTarg       = 0x21
	regNumTX1            = 0x22
	regNumTX2            = 0x23
	regADConvOut         = 0x25
	regRegulatorCtrl     = 0x2C
	regCapSensorCtrl     = 0x2f
	regCapSensor         = 0x30
	regICID              = 0x3f
	// Register addresses, space B. See table 28.
	spaceB     = 0b1 << 7
	regReAMMod = spaceB | 0x2a

	// Commands, see table table 13.
	// Note that the constant include the command mode prefix 0b11. For example,
	// the set default command is really command 0 (0b11_000000).
	cmdSetDefault         = 0xc0
	cmdStopAll            = 0xc2
	cmdTransmitWithCRC    = 0xc4
	cmdTransmitWithoutCRC = 0xc5
	cmdTransmitREQA       = 0xc6
	cmdGotoSense          = 0xcd
	cmdGotoSleep          = 0xce
	cmdResetRXGain        = 0xd5
	cmdAdjustRegulator    = 0xd6
	cmdClearFIFO          = 0xdb
	cmdCalibrateCapSensor = 0xdd
	cmdMeasureCap         = 0xde
	cmdMeasureSupply      = 0xdf
	cmdSpaceBAccess       = 0xfb
	cmdTestAccess         = 0xfc

	// IO configuration register 1 bits.
	out_cl = 1

	// IO Configuration register 2 bits.
	io_drv_lvl = 2

	// Mode definition bits.
	omISO14443A = 0b0001 << 3
	omISO15693  = 0b1110 << 3 // Sub-carrier stream mode.
	targ        = 7

	// Stream mode definition bits.
	stx          = 0
	scp          = 3
	scf          = 5
	modeISO15693 = 0b01<<scf | // fc/32
		0b000<<stx | // fc/120
		0b11<<scp // 8 pulses

	// Operation control bits.
	en_fd_c = 1
	wu      = 2
	tx_en   = 3
	rx_en   = 6
	en      = 7

	// Main interrupt bits.
	i_rx_rest = 1
	i_col     = 2
	i_txe     = 3
	i_rxe     = 4
	i_rxs     = 5
	i_wl      = 6
	i_osc     = 7

	// Timer/NFC interrupt bits.
	i_dct = 7

	// Error and wake-up interrupt bits.
	i_wcap = 0
	i_wph  = 1
	i_wam  = 2
	i_wt   = 3
	i_err1 = 4
	i_err2 = 5
	i_par  = 6
	i_crc  = 7

	// Passive target interrupt bits.
	i_wu_a    = 0
	I_wu_f    = 3
	I_rxe_pta = 4

	// Regulator control bits.
	reg_s = 7
	mpsv  = 0

	// ISO14443A configuration bits
	antcl     = 0
	no_rx_par = 6
	no_tx_par = 7

	// Auxiliary definition bits.
	dis_corr  = 2
	no_crc_rx = 7

	// NFCIP-1 passive target definition bits (table 32).
	fdel = 4
)
