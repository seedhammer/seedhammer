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
	txBits byte
	txCRC  bool
	rxCRC  bool
	prot   Protocol

	scratch [100]byte
	enc     [100]byte
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
	if err := d.writeRegs(
		regIOConf1, 0b11<<out_cl|0b1<<lf_clk_off, // Disable the MCU_CLK pin.
		regIOConf2, 0b1<<io_drv_lvl, // Increase IO drive strength, as recommended in table 20.
		regResAMMod, 0b1<<fa3_f|0<<md_res, // Minimum non-overlap.
		regExtFieldAct, 0b001<<trg_l|0b0001<<rfe_t, // Lower activation threshold.
		regExtFieldDeact, 0b000<<trg_ld|0b000<<rfe_td, // Lower deactivation threshold.
		regNFCIP1PassiveTarg, 5<<fdel, // Adjust fdel to 2 as suggested by the datasheet table 32.
		regPassiveTargetMod, 0x5f, // Reduce RFO resistance in modulated state.
		regEMDSupConf, 0b1<<rx_start_emv, // Enable start on first 4 bits.
		regCapSensorCtrl, 0, // Reset capacitive sensor calibration.
		regStreamModeDef, modeISO15693, // Setup streaming mode for iso15693.
	); err != nil {
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
	// for i := byte(0); i <= 4; i++ {
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
	if err := d.writeRegs(
		// First, the reg_s bit must be cycled.
		regRegulatorCtrl, 0b1<<reg_s,
		regRegulatorCtrl, 0b0<<reg_s,
	); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Then the adjust regulator command issues.
	if err := d.command(cmdAdjustRegulator); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Adjustment takes up to 5 milliseconds.
	time.Sleep(5 * time.Millisecond)

	return nil
}

func (d *Device) Listen() error {
	// 	//    /****** Default Analog Configuration for Chip-Specific Poll Common ******/
	// 	//    , MODE_ENTRY_9_REG( (RFAL_ANALOG_CONFIG_TECH_CHIP | RFAL_ANALOG_CONFIG_CHIP_POLL_COMMON)
	// 	//                        , ST25R3916_REG_MODE, ST25R3916_REG_MODE_tr_am  , ST25R3916_REG_MODE_tr_am_am                                           /* Use AM modulation */
	// 	//                        , ST25R3916_REG_TX_DRIVER, ST25R3916_REG_TX_DRIVER_am_mod_mask, ST25R3916_REG_TX_DRIVER_am_mod_12percent                /* Set Modulation index */
	// 	//                        , ST25R3916_REG_AUX_MOD, (ST25R3916_REG_AUX_MOD_dis_reg_am | ST25R3916_REG_AUX_MOD_res_am), 0x00                           /* Use AM via regulator */
	// 	//                        , ST25R3916_REG_ANT_TUNE_A, 0xFF, 0x82                                                                                  /* Set Antenna Tuning (Poller): ANTL */
	// 	//                        , ST25R3916_REG_ANT_TUNE_B, 0xFF, 0x82                                                                                  /* Set Antenna Tuning (Poller): ANTL */
	// 	//                        , ST25R3916_REG_OVERSHOOT_CONF1,  0xFF, 0x00                                                                            /* Disable Overshoot Protection  */
	// 	//                        , ST25R3916_REG_OVERSHOOT_CONF2,  0xFF, 0x00                                                                            /* Disable Overshoot Protection  */
	// 	//                        , ST25R3916_REG_UNDERSHOOT_CONF1, 0xFF, 0x00                                                                            /* Disable Undershoot Protection */
	// 	//                        , ST25R3916_REG_UNDERSHOOT_CONF2, 0xFF, 0x00                                                                            /* Disable Undershoot Protection */
	// 	//                        )
	// }
	if err := d.command(cmdStopAll); err != nil {
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
	// Enable receiver and external field detector.
	if err := d.writeReg(regOpCtrl, 0b1<<en|0b1<<rx_en|0b11<<en_fd_c); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	if err := d.command(cmdResetRXGain); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	// Start sensing.
	if err := d.command(cmdGotoSense); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	fmt.Println("waiting anxiously")
	// oldstate := byte(0)
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
				if _, err := d.Write(page0); err != nil {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				n, err = d.Read(buf2)
				buf2 = buf2[:n]
				if err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				if _, err := d.Write(page4); err != nil {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				n, err = d.Read(buf3)
				buf3 = buf3[:n]
				if err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("st25r3916: listen: %w", err)
				}
				if _, err := d.Write(page8); err != nil {
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
	if err := d.command(cmdStopAll); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	if err := d.configureProtocol(prot); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Reset RX gain.
	if err := d.command(cmdResetRXGain); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Enable transmitter and receiver.
	if err := d.writeReg(regOpCtrl, 0b1<<en|0b1<<tx_en|0b1<<rx_en); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}

	// Wait for the receiver to power up.
	time.Sleep(5 * time.Millisecond)
	return nil
}

func (d *Device) Write(tx []byte) (int, error) {
	if err := d.command(cmdClearFIFO); err != nil {
		return 0, fmt.Errorf("st25r3916: transceive: %w", err)
	}
	switch d.prot {
	case ISO14443a:
		const reqa = 0x26
		// Special case REQA.
		if len(tx) == 1 && tx[0] == reqa {
			// Transmit REQA.
			if err := d.command(cmdTransmitREQA); err != nil {
				return 0, fmt.Errorf("st25r3916: transceive: %w", err)
			}
		} else {
			aux := byte(0)
			if !d.rxCRC {
				aux = 0b1 << no_crc_rx
			}
			if err := d.writeReg(regAuxDef, aux); err != nil {
				return 0, fmt.Errorf("st25r3916: transceive: %w", err)
			}
			if err := d.writeTXLen(len(tx), d.txBits); err != nil {
				return 0, fmt.Errorf("st25r3916: transceive: %w", err)
			}
			if err := d.writeFIFO(tx); err != nil {
				return 0, fmt.Errorf("st25r3916: transceive: %w", err)
			}
			cmd := byte(cmdTransmitWithCRC)
			if !d.txCRC {
				cmd = cmdTransmitWithoutCRC
			}
			if err := d.command(cmd); err != nil {
				return 0, fmt.Errorf("st25r3916: transceive: %w", err)
			}
		}
	case ISO15693:
		const (
			sof1of4 = 0x21
			eof1of4 = 0x04
		)
		txlen := 1 + (len(tx)+2)*4 + 1 // SOF, data, CRC, EOF.
		if err := d.writeTXLen(txlen, 0); err != nil {
			return 0, fmt.Errorf("st25r3916: transceive: %w", err)
		}
		// dummy := []byte{0x21, 0x20, 0x08, 0x20, 0x02, 0x08, 0x02, 0x02, 0x02, 0x02, 0x02, 0x02, 0x02, 0x20, 0x08, 0x80, 0x80, 0x20, 0x20, 0x02, 0x02, 0x04}
		// if err := d.writeTXLen(len(dummy), 0); err != nil {
		// 	return fmt.Errorf("st25r3916: transceive: %w", err)
		// }
		// if err := d.writeFIFO(dummy); err != nil {
		// 	return fmt.Errorf("st25r3916: transceive: %w", err)
		// }
		crc := crcCITT(tx)
		fmt.Printf("transceive len: %d crc %.4x %x %.8b\n", txlen, crc, tx, tx)
		e := encoder1of4{Dev: d, Buf: d.enc[:0]}
		e.Write(sof1of4)
		e.Encode(tx...)
		e.Encode(byte(crc), byte(crc>>8))
		e.Write(eof1of4)
		if err := e.Flush(); err != nil {
			return 0, fmt.Errorf("st25r3916: transceive: %w", err)
		}

		// buf := make([]byte, 100)
		// n, err := d.Read(buf)
		// if err != nil && !errors.Is(err, io.EOF) {
		// 	return err
		// }
		// fmt.Printf("FIFO     : (%d) %x\n", n, buf[:n])

		// // Disable built-in CRC calculation.
		// if err := d.writeReg(regAuxDef, 0b1<<no_crc_rx); err != nil {
		// 	return fmt.Errorf("st25r3916: transceive: %w", err)
		// }
		// for i := range byte(0x3F) {
		// 	v, err := d.readReg(i)
		// 	if err != nil {
		// 		return fmt.Errorf("st25r3916: transceive: %w", err)
		// 	}
		// 	if v != 0 {
		// 		fmt.Printf("A%#.2x: %#.2x\n", i, v)
		// 	}
		// }
		// for i := range byte(0x3F) {
		// 	v, err := d.readReg(spaceB | i)
		// 	if err != nil {
		// 		return fmt.Errorf("st25r3916: transceive: %w", err)
		// 	}
		// 	if v != 0 {
		// 		fmt.Printf("B%#.2x: %#.2x\n", i, v)
		// 	}
		// }
		if err := d.command(cmdTransmitWithoutCRC); err != nil {
			return 0, fmt.Errorf("st25r3916: transceive: %w", err)
		}
	}
	for {
		for d.Int.Get() {
		}
		if err := d.readError(); err != nil {
			return 0, fmt.Errorf("st25r3916: transceive: %w", err)
		}
		req, intrs := d.scratch[:1], d.scratch[1:4]
		req[0] = modeReadReg | regTimerNFCIntr
		if err := d.Bus.Tx(i2cAddr, req, intrs); err != nil {
			return 0, fmt.Errorf("st25r3916: transceive: %w", err)
		}
		timInt, errInt, passInt := intrs[0], intrs[1], intrs[2]
		intr, err := d.readReg(regMainIntr)
		if err != nil {
			return 0, fmt.Errorf("st25r3916: transceive: %w", err)
		}
		opctrl, err := d.readReg(regOpCtrl)
		if err != nil {
			return 0, fmt.Errorf("st25r3916: transceive: %w", err)
		}
		fmt.Println(intr, timInt, errInt, passInt, opctrl)
		if intr&(0b1<<i_txe) != 0 {
			fmt.Println("transceive done")
		}
		if intr&(0b1<<i_rxe) != 0 {
			break
		}
	}
	return len(tx), nil
}

type encoder1of4 struct {
	Dev *Device
	Buf []byte

	err error
}

func (e *encoder1of4) Write(data ...byte) {
	for len(data) > 0 {
		if len(e.Buf) == cap(e.Buf) {
			e.Flush()
		}
		n := min(len(data), cap(e.Buf)-len(e.Buf))
		e.Buf = append(e.Buf, data[:n]...)
		data = data[n:]
	}
}

func (e *encoder1of4) Flush() error {
	if e.err == nil {
		e.err = e.Dev.writeFIFO(e.Buf)
	}
	e.Buf = e.Buf[:0]
	return e.err
}

func (e *encoder1of4) Encode(data ...byte) {
	if len(e.Buf)+4 > cap(e.Buf) {
		e.Flush()
	}
	const (
		data_00_1_4 = 0x02
		data_01_1_4 = 0x08
		data_10_1_4 = 0x20
		data_11_1_4 = 0x80
	)
	encMap := [...]byte{
		data_00_1_4,
		data_01_1_4,
		data_10_1_4,
		data_11_1_4,
	}
	for _, b := range data {
		for range 4 {
			e.Buf = append(e.Buf, encMap[b&0b11])
			b >>= 2
		}
	}
}

func (d *Device) configureProtocol(prot Protocol) error {
	type config struct {
		opMode     byte
		rxConf     [4]byte
		corrConf   [2]byte
		overshoot  [2]byte
		undershoot [2]byte
		iso14443a  byte
	}
	var conf config
	switch prot {
	case ISO14443a:
		conf = config{
			opMode:     omISO14443A,
			rxConf:     [...]byte{0x08, 0x2d, 0x00, 0x00},
			corrConf:   [...]byte{0x51, 0x00},
			overshoot:  [...]byte{0x40, 0x03},
			undershoot: [...]byte{0x40, 0x03},
			iso14443a:  0,
		}
	case ISO15693:
		conf = config{
			opMode: omISO15693,
			// rxConf:    [...]byte{0x13, 0x2d, 0x00, 0x00}, // TODO: why?
			rxConf:    [...]byte{0x13, 0x25, 0x00, 0x00},
			corrConf:  [...]byte{0x13, 0x01},
			iso14443a: 0b1<<no_tx_par | 0b1<<no_rx_par,
		}
	}
	if err := d.writeRegs(
		regModeDef, conf.opMode,
		regRXConf1, conf.rxConf[0],
		regRXConf2, conf.rxConf[1],
		regRXConf3, conf.rxConf[2],
		regRXConf4, conf.rxConf[3],
		regCorrConf1, conf.corrConf[0],
		regCorrConf2, conf.corrConf[1],
		regOvershootConf1, conf.overshoot[0],
		regOvershootConf2, conf.overshoot[1],
		regUndershootConf1, conf.undershoot[0],
		regUndershootConf2, conf.undershoot[1],
		regISO14443AConf, conf.iso14443a,

		0x0f, 0x41, // TODO
		// 0x11, 0x52,
		0x12, 0x20,
		0x13, 0x01,
		0x14, 0x84,
		0x16, 0x87,
		0x17, 0xbf,
		0x18, 0x0f,
		0x19, 0xff,
		0x23, 0xb0,
		0x26, 0x82,
		0x27, 0x82,
		0x28, 0x70,
		0x29, 0x5f,
		0x2a, 0x11,
	); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	d.prot = prot
	return nil
}

func (d *Device) readError() error {
	errIntr, err := d.readReg(regErrorWakeupIntr)
	if err != nil {
		return err
	}
	if errIntr != 0 {
		fmt.Println("errIntr", errIntr)
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
	d.txBits = byte(bits)
}

func (d *Device) Read(buf []byte) (int, error) {
	req, fifoStatus := d.scratch[:1], d.scratch[1:3]
	req[0] = modeReadReg | regFIFOStatus1
	if err := d.Bus.Tx(i2cAddr, req, fifoStatus); err != nil {
		return 0, err
	}
	fifoLen := int(fifoStatus[1]&0b1100_0000)<<2 | int(fifoStatus[0])
	// Exclude the CRC bytes.
	if d.prot == ISO14443a && d.rxCRC {
		fifoLen -= 2
	}
	buf2 := buf
	buf = make([]byte, 100)
	n := min(fifoLen, len(buf))
	req = d.scratch[:1]
	req[0] = modeFIFO | readFIFO
	if err := d.Bus.Tx(i2cAddr, req, buf[:n]); err != nil {
		return 0, fmt.Errorf("st25r3916: read: %w", err)
	}
	fmt.Printf("READ! %d %x (fifostatus2: %b)\n", n, buf[:n], fifoStatus[1])
	n = copy(buf2, []byte{0x00, 0x00, 0x95, 0x83, 0xcb, 0x89, 0x00, 0x01, 0x04, 0xe0 /*0x25, 0x61*/})
	if n == fifoLen {
		return n, io.EOF
	}
	return n, nil
}

func (d *Device) writeTXLen(bytes int, bits byte) error {
	// First write FIFO size.
	const maxTxSize = 0b1<<13 - 1
	// We don't support streaming transmissions.
	if bytes > FIFOSize || bytes > maxTxSize {
		return fmt.Errorf("write FIFO: buffer too large: %d", bytes)
	}
	if bytes > maxTxSize {
		return fmt.Errorf("write FIFO: buffer too large: %d bytes", bytes)
	}
	// Set transmit size.
	req := d.scratch[:3]
	req[0] = modeWriteReg | regNumTX1
	// Most significant bits.
	req[1] = byte(bytes >> 5)
	// Least significant bits and the partial bits.
	req[2] = byte((bytes&0b11111)<<3) | bits
	return d.Bus.Tx(i2cAddr, req, nil)
}

func (d *Device) writeFIFO(tx []byte) error {
	fmt.Printf("writeFIFO: (%d) %x\n", len(tx), tx)
	req := d.scratch[:]
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

func (d *Device) readReg(reg byte) (byte, error) {
	isSpaceB := reg&spaceB != 0
	reg &^= spaceB
	req, res := d.scratch[:2], d.scratch[2:3]
	req[0] = cmdSpaceBAccess
	req[1] = modeReadReg | reg
	if !isSpaceB {
		req = req[1:]
	}
	err := d.Bus.Tx(i2cAddr, req, res)
	return res[0], err
}

// writeRegs in pairs of (register, value).
func (d *Device) writeRegs(values ...byte) error {
	for i := 0; i < len(values); i += 2 {
		reg, val := values[i], values[i+1]
		if err := d.writeReg(reg, val); err != nil {
			return err
		}
	}
	return nil
}

func (d *Device) writeReg(reg, val byte) error {
	var req []byte
	isSpaceB := reg&spaceB != 0
	reg &^= spaceB
	req = d.scratch[:3]
	req[0] = cmdSpaceBAccess
	req[1] = modeWriteReg | reg
	req[2] = val
	if !isSpaceB {
		req = req[1:]
	}
	return d.Bus.Tx(i2cAddr, req, nil)
}

func (d *Device) command(cmd byte) error {
	req := d.scratch[:1]
	req[0] = modeCommand | cmd
	return d.Bus.Tx(i2cAddr, req, nil)
}

func crcCITT(data []byte) uint16 {
	crc := uint16(0xffff)
	for _, b := range data {
		b ^= byte(crc & 0xFF)
		b ^= b << 4

		b16 := uint16(b)
		crc = (crc >> 8) ^ (b16 << 8) ^ (b16 << 3) ^ (b16 >> 4)
	}
	return ^crc
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
	regRXConf1           = 0x0b
	regRXConf2           = 0x0c
	regRXConf3           = 0x0d
	regRXConf4           = 0x0e
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
	regPassiveTargetMod  = 0x29
	regExtFieldAct       = 0x2a
	regExtFieldDeact     = 0x2b
	regRegulatorCtrl     = 0x2c
	regCapSensorCtrl     = 0x2f
	regCapSensor         = 0x30
	regICID              = 0x3f
	// Register addresses, space B. See table 28.
	spaceB             = 0b1 << 7
	regEMDSupConf      = spaceB | 0x05
	regCorrConf1       = spaceB | 0x0c
	regCorrConf2       = spaceB | 0x0d
	regResAMMod        = spaceB | 0x2a
	regOvershootConf1  = spaceB | 0x30
	regOvershootConf2  = spaceB | 0x31
	regUndershootConf1 = spaceB | 0x32
	regUndershootConf2 = spaceB | 0x33

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
	lf_clk_off = 0
	out_cl     = 1

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

	// Resistive AM modulation bits.
	md_res = 0
	fa3_f  = 7

	// External field detector activation bits (table 83).
	rfe_t = 0
	trg_l = 4

	// External field detector deactivation bits (table 86).
	rfe_td = 0
	trg_ld = 4

	// EMD suppression configuration bits (table 38).
	rx_start_emv = 6
)
