//go:build tinygo

// package st25r3916 implements a driver for the [ST25R3916] NFC reader device.
//
// [ST25R3916]: https://www.st.com/resource/en/datasheet/st25r3916.pdf
package st25r3916

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"machine"
	"time"
)

type Device struct {
	Bus        Bus
	Int        machine.Pin
	prot       Protocol
	interrupts chan struct{}
	eof        bool
	timer      *time.Timer
	excludeCRC bool

	scratch [100]byte
}

type Bus interface {
	Tx(addr uint16, w, r []byte) error
}

// FIFOSize is the number of bytes that can be
// read without risking overflow
const FIFOSize = 512 - 2 // Make room for the CRC bytes.

type Protocol int

const (
	ISO15693 Protocol = iota
	ISO14443a
	Detect
	Listen
)

// interrupts represent a set of interrupt statuses or
// masks.
type interrupts struct {
	Main    byte
	Timer   byte
	Passive byte
	Error   byte
}

const (
	// General timeout to guard for hangs, excessive
	// receive times etc.
	timeout = 500 * time.Millisecond

	// Card detection thresholds.
	ampSens   = 2
	phaseSens = 2
)

func (d *Device) Configure() error {
	if d.timer == nil {
		d.timer = time.NewTimer(0)
	}
	if d.interrupts == nil {
		d.interrupts = make(chan struct{}, 1)
	}
	d.Int.Configure(machine.PinConfig{Mode: machine.PinInput})
	d.Int.SetInterrupt(machine.PinRising, d.handleInterrupt)
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
		return fmt.Errorf("st25r3916: configure: %w", err)
	}
	if err := d.writeRegs(
		regIOConf1, 0b11<<out_cl|0b1<<lf_clk_off|0b01<<i2c_thd, // Disable the MCU_CLK pin, 400 kHz i2c.
		regIOConf2, 0b1<<io_drv_lvl, // Increase IO drive strength, as recommended in table 20.
		regResAMMod, 0b1<<fa3_f|0<<md_res, // Minimum non-overlap.
		regExtFieldAct, 0b001<<trg_l|0b0001<<rfe_t, // Lower activation threshold.
		regExtFieldDeact, 0b000<<trg_ld|0b000<<rfe_td, // Lower deactivation threshold.
		regNFCIP1PassiveTarg, 5<<fdel| // Adjust fdel to 5 like the ST example code. Datasheet table 32 says minimum 2.
			0b1<<d_212_424_1r|0b1<<d_ac_ap2p, // Disable unused passive detection modes.
		regPassiveTargetMod, 0x5f, // Reduce RFO resistance in modulated state.
		regEMDSupConf, 0b1<<rx_start_emv, // Enable start on first 4 bits.
		regStreamModeDef, modeISO15693, // Setup streaming mode for iso15693.
		regTimerEMVCtrl, 0b001<<gptc, // Start timer at end of rx.
		regWakeupCtrl, 0b010<<wut|0b1<<wur|0b1<<wam|0b1<<wph, // Enable all card detection methods, set measure period.
		regAmplitudeMeasCtrl, ampSens<<am_d|0b1<<am_ae|0b1<<am_aam|0b10<<am_aew, // Set amplitude measurement ∆am, auto-averaging reference.
		regPhaseMeasCtrl, phaseSens<<pm_d|0b1<<pm_ae|0b1<<pm_aam|0b10<<pm_aew, // Set phase measurement ∆pm, auto-averaging reference.
	); err != nil {
		return fmt.Errorf("st25r3916: configure: %w", err)
	}
	if err := d.enable(); err != nil {
		return fmt.Errorf("st25r3916: configure: %w", err)
	}
	// Adjust regulators.
	if err := d.writeRegs(
		// First, the reg_s bit must be cycled.
		regRegulatorCtrl, 0b1<<reg_s,
		regRegulatorCtrl, 0b0<<reg_s,
	); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	// Then issue the adjust regulator command.
	if _, err := d.commandAndWait(cmdAdjustRegulator, interrupts{Timer: 0b1 << i_dct}); err != nil {
		return fmt.Errorf("st25r3916: configure: %w", err)
	}
	if err := d.RadioOff(); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	return nil
}

func (d *Device) enable() error {
	aux, err := d.readReg(regAuxDisp)
	if err != nil {
		return err
	}
	if aux&(0b1<<osc_ok) != 0 {
		// Already enabled.
		return nil
	}
	mask := interrupts{Main: 0b1 << i_osc}
	if err := d.setInterruptMask(mask); err != nil {
		return err
	}
	// Start oscillator.
	if err := d.writeReg(regOpCtrl, 0b1<<en); err != nil {
		return err
	}
	// Wait for oscillator stable.
	_, err = d.waitForInterrupt(timeout, nil)
	return err
}

func (d *Device) handleInterrupt(machine.Pin) {
	select {
	case d.interrupts <- struct{}{}:
	default:
	}
}

func (d *Device) Listen(timeout time.Duration) error {
	// Notes:
	// RATS/ATS response: search for RFAL_ISODEP_CMD_RATS
	//   - check DID == 0?
	//   - Check FSDI (maximum data size)
	//   - Synthesize ATS response.

	/* Compute ATS                                                                 */

	// Generate random 4-byte UID, starting with 0x08 to indicate
	// it is dynamically generated.
	uid := make([]byte, 4)
	rng, err := machine.GetRNG()
	if err != nil {
		// Should never happen.
		panic(err)
	}
	binary.BigEndian.PutUint32(uid, rng)
	// Load PT memory with NFC-A card emulation responses.
	req := []byte{
		modeFIFO | loadPTMemory,
		// UID.
		0x08, uid[0], uid[1], uid[2], 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		// ATQA.
		0x04, 0x00,
		// SAK1, SAK2, SAK3.
		// 0b0_01_00_0_00, 0b0_01_00_0_00, 0b0_01_00_0_00,
		0x00, 0x00, 0x00,
	}
	if err := d.Bus.Tx(i2cAddr, req, nil); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	// // Set 7-byte UID mode.
	// if err := d.writeReg(regAuxDef, 0b01<<nfc_id); err != nil {
	// 	return fmt.Errorf("st25r3916: listen: %w", err)
	// }
	// Set listen, iso-14443a mode.
	if err := d.writeReg(regModeDef, 0b1<<targ|omISO14443A); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	d.prot = ISO14443a
	// Start sensing.
	// if err := d.command(cmdGotoSense); err != nil {
	// 	return fmt.Errorf("st25r3916: listen: %w", err)
	// }
	defer func() {
		err := (func() error {
			if err := d.command(cmdStopAll); err != nil {
				return fmt.Errorf("st25r3916: radio: %w", err)
			}
			if err := d.command(cmdGotoSleep); err != nil {
				return fmt.Errorf("st25r3916: listen: %w", err)
			}
			return nil
		})()
		if err != nil {
			panic(err)
		}
	}()
	// oldstate := byte(0)
	buf := make([]byte, 100)
	// buf2 := make([]byte, 100)
	// buf3 := make([]byte, 100)
	// page0 := []byte{0x04, 0xf7, 0x73, 0x08, 0x7c, 0x8f, 0x61, 0x81, 0x13, 0x48, 0x00, 0x00, 0xe1, 0x10, 0x3e, 0x00}
	// // page4 := []byte{0x03, 0x14, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x62, 0x69, 0x74, 0x63, 0x6f, 0x69, 0x6e, 0x2e, 0x6f}
	// // page8 := []byte{0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f, 0xfe, 0x00, 0x63, 0x65, 0x2f, 0x70, 0x6f, 0x64, 0x63, 0x61}
	// page4 := []byte{0x03, 0x14, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x48, 0x69, 0x20, 0x4e, 0x69, 0x63, 0x6b, 0x21, 0x20}
	// page8 := []byte{0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f, 0xfe, 0x00, 0x63, 0x65, 0x2f, 0x70, 0x6f, 0x64, 0x63, 0x61}
	// ATS := []byte{0x05, 0x78, 0x00, 0x80, 0x00}
	// Perform dummy write to set up chip for reading.
	if _, err := d.Write(nil); err != nil {
		return fmt.Errorf("st25r3916: listen: %w", err)
	}
	mem := []byte{
		0x04, 0xf7, 0x73, 0x08, 0x7c, 0x8f, 0x61, 0x81, 0x13, 0x48, 0x00, 0x00, 0xe1, 0x10, 0x3e, 0x00,
		// page4 := []byte{0x03, 0x14, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x62, 0x69, 0x74, 0x63, 0x6f, 0x69, 0x6e, 0x2e, 0x6f}
		// page8 := []byte{0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f, 0xfe, 0x00, 0x63, 0x65, 0x2f, 0x70, 0x6f, 0x64, 0x63, 0x61}
		0x03, 0x14, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x48, 0x69, 0x20, 0x4e, 0x69, 0x63, 0x6b, 0x21, 0x20,
		0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f, 0xfe, 0x00, 0x63, 0x65, 0x2f, 0x70, 0x6f, 0x64, 0x63, 0x61,
	}
	writeMem := make([]byte, 492)
	copy(writeMem, mem)
	const (
		SLP_REQ   = 0x50
		T2T_READ  = 0x30
		T2T_WRITE = 0xa2
		WRITE_ACK = 0x0a

		blockSize = 4
		readSize  = 16
	)
	ack := []byte{WRITE_ACK}
	nwrites, nreads := 0, 0
	writtenBlocks := make([]int, 0, 100)
	defer func() {
		fmt.Println("stats", nwrites, nreads, writtenBlocks)
	}()
	for {
		n, err := d.read(buf, timeout)
		buf := buf[:n]
		if err != nil && err != io.EOF {
			return fmt.Errorf("st25r3916: listen: %w", err)
		}
		if len(buf) < 1 {
			return io.ErrUnexpectedEOF
		}
		cmd := buf[0]
		buf = buf[1:]
		switch cmd {
		// case bytes.Equal(buf, []byte{0xe0, 0x80}):
		// 	// RATS
		// 	if _, err := d.Write(ATS); err != nil {
		// 		return fmt.Errorf("st25r3916: listen: %w", err)
		// 	}
		case SLP_REQ:
			if len(buf) < 1 || buf[0] != 0 {
				return fmt.Errorf("st25r3916: listen: unknown SLP_REQ argument: %x", buf[0])
			}
			fmt.Printf("HLTA %x\n", buf)
			return nil
		case T2T_READ:
			if len(buf) == 0 {
				return io.ErrUnexpectedEOF
			}
			start := int(buf[0]) * blockSize
			buf = buf[1:]
			end := start + readSize
			if end > len(writeMem) {
				return fmt.Errorf("st25r3916: listen: read out of bounds")
			}
			data := writeMem[start:end]
			if _, err := d.Write(data); err != nil {
				return fmt.Errorf("st25r3916: listen: %w", err)
			}
			if nwrites > 0 {
				nreads++
			}
		case T2T_WRITE:
			if len(buf) == 0 {
				return io.ErrUnexpectedEOF
			}
			start := int(buf[0]) * blockSize
			buf = buf[1:]
			if start > len(writeMem) {
				return fmt.Errorf("st25r3916: listen: write out of bounds")
			}
			copy(writeMem[start:], buf)
			if _, err := d.Write(ack); err != nil {
				return fmt.Errorf("st25r3916: listen: %w", err)
			}
			writtenBlocks = append(writtenBlocks, start, len(buf))
			// fmt.Println(start, buf)
			nwrites++
		default:
			return fmt.Errorf("st25r3916: listen: unknown Type 2 command: %x", cmd)
		}
	}
	return nil
}

func (d *Device) dumpMeasurements() {
	aad, err1 := d.readReg(regAmplitudeMeasAutoDisp)
	amd, err2 := d.readReg(regAmplitudeMeasDisp)
	pad, err3 := d.readReg(regPhaseMeasAutoDisp)
	pmd, err4 := d.readReg(regPhaseMeasDisp)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		panic("measurements failed")
	}
	log.Println("aad", aad, "amd", amd, "pad", pad, "pmd", pmd)
}

func (d *Device) Detect(quit <-chan struct{}) error {
	intrs, err := d.waitForInterrupt(0, quit)
	if err != nil {
		return fmt.Errorf("st25r3916: detect: %w", err)
	}
	_ = intrs
	// fmt.Printf("intrs: %+v\n", intrs)
	return nil
}

func (d *Device) RadioOff() error {
	if err := d.writeReg(regOpCtrl, 0); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	return nil
}

func (d *Device) RadioOn(prot Protocol) error {
	d.prot = prot
	if err := d.configureProtocol(prot); err != nil {
		return fmt.Errorf("st25r3916: radio: %w", err)
	}
	switch d.prot {
	case Detect:
		mask := interrupts{
			Error: 0b1<<i_wt | 0b1<<i_wam | 0b1<<i_wph,
		}
		if err := d.setInterruptMask(mask); err != nil {
			return fmt.Errorf("st25r3916: radio: %w", err)
		}
		if err := d.writeRegs(regOpCtrl, 0b1<<wu); err != nil {
			return fmt.Errorf("st25r3916: radio: %w", err)
		}
	default:
		if err := d.enable(); err != nil {
			return fmt.Errorf("st25r3916: radio: %w", err)
		}
		if err := d.command(cmdStopAll); err != nil {
			return fmt.Errorf("st25r3916: radio: %w", err)
		}
		flags := byte(0b1<<en | 0b11<<en_fd_c)
		if err := d.writeReg(regOpCtrl, flags); err != nil {
			return fmt.Errorf("st25r3916: radio: %w", err)
		}
		if d.prot != Listen {
			intrs, err := d.commandAndWait(cmdInitialFieldOn,
				interrupts{Timer: 0b1<<i_cac | 0b1<<i_cat})
			if err != nil {
				return fmt.Errorf("st25r3916: radio: %w", err)
			}
			if intrs.Timer&(0b1<<i_cat) == 0 {
				return fmt.Errorf("st25r3916: radio: field conflict")
			}
			flags |= 0b1 << tx_en
		}
		// Enable receiver.
		flags |= 0b1 << rx_en
		if err := d.writeReg(regOpCtrl, flags); err != nil {
			return fmt.Errorf("st25r3916: radio: %w", err)
		}
	}
	return nil
}

func (d *Device) Write(tx []byte) (int, error) {
	if err := d.command(cmdStopAll); err != nil {
		return 0, fmt.Errorf("st25r3916: transceive: %w", err)
	}
	if err := d.command(cmdResetRXGain); err != nil {
		return 0, fmt.Errorf("st25r3916: transceive: %w", err)
	}
	d.excludeCRC = true
	var transmitCmd byte
	var bits byte
	switch d.prot {
	case ISO14443a:
		const (
			REQA = 0x26
			ACK  = 0x0a
		)
		var conf byte
		transmitCmd = byte(cmdTransmitWithCRC)
		// Simple detection of anti-collision frame.
		anticol := len(tx) == 2 && tx[1] == 0x20 &&
			(tx[0] == casLevel1 || tx[0] == casLevel2 || tx[0] == casLevel3)
		reqa := len(tx) == 1 && tx[0] == REQA
		ack := len(tx) == 1 && tx[0] == ACK
		switch {
		case anticol, reqa:
			d.excludeCRC = false
		}
		if ack {
			bits = 4
		}
		switch {
		case anticol || ack:
			transmitCmd = cmdTransmitWithoutCRC
		case reqa:
			transmitCmd = cmdTransmitREQA
		}
		if anticol {
			conf = 0b1 << antcl
		}
		if err := d.writeReg(regISO14443AConf, conf); err != nil {
			return 0, fmt.Errorf("st25r3916: transceive: %w", err)
		}
	case ISO15693:
		d.excludeCRC = false
		transmitCmd = cmdTransmitWithoutCRC
	}
	mask := interrupts{
		Main:  0b1 << i_rxe,
		Timer: 0b1 << i_nre,
		Error: 0b1<<i_crc | 0b1<<i_par | 0b1<<i_err1 | 0b1<<i_err2,
	}
	if err := d.setInterruptMask(mask); err != nil {
		return 0, err
	}
	d.eof = false
	if len(tx) == 0 {
		return 0, nil
	}
	if transmitCmd != cmdTransmitREQA {
		if err := d.writeFIFO(tx, bits); err != nil {
			return 0, fmt.Errorf("st25r3916: transceive: %w", err)
		}
	}
	if err := d.command(transmitCmd); err != nil {
		return 0, fmt.Errorf("st25r3916: transceive: %w", err)
	}
	return len(tx), nil
}

func (d *Device) waitForInterrupt(timeout time.Duration, quit <-chan struct{}) (interrupts, error) {
	if !d.timer.Stop() {
		select {
		case <-d.timer.C:
		default:
		}
	}
	tim := d.timer.C
	if timeout > 0 {
		d.timer.Reset(timeout)
	} else {
		tim = nil
	}
	for {
		select {
		case <-d.interrupts:
		case <-quit:
			return interrupts{}, errors.New("cancelled")
		case <-tim:
			return interrupts{}, errors.New("timeout")
		}
		intrs, mask, err := d.interruptStatus()
		if err != nil {
			return interrupts{}, err
		}
		intrs.Main &= mask.Main
		intrs.Timer &= mask.Timer
		intrs.Passive &= mask.Passive
		intrs.Error &= mask.Error
		switch {
		case intrs.Error&(0b1<<i_crc) != 0:
			err = errors.New("CRC error")
		case intrs.Error&(0b1<<i_par) != 0:
			err = errors.New("parity error")
		case intrs.Error&(0b1<<i_err2) != 0:
			err = errors.New("soft framing error")
		case intrs.Error&(0b1<<i_err1) != 0:
			err = errors.New("hard framing error")
		case intrs.Main&(0b1<<i_col) != 0:
			// We don't implement anti-collision loops yet,
			// so for now treat collisions as errors.
			err = errors.New("collision")
		case intrs.Timer&(0b1<<i_nre) != 0:
			err = errors.New("response timeout")
		}
		if err != nil || intrs != (interrupts{}) {
			return intrs, err
		}
	}
}

func (d *Device) setInterruptMask(mask interrupts) error {
	// Clear interrupt status.
	if _, _, err := d.interruptStatus(); err != nil {
		return err
	}
	select {
	case <-d.interrupts:
	default:
	}
	req := d.scratch[:5]
	req[0] = regMaskMainIntr
	req[regMaskMainIntr-regMaskMainIntr+1] = ^mask.Main
	req[regMaskTimerNFCIntr-regMaskMainIntr+1] = ^mask.Timer
	req[regMaskErrorWakeupIntr-regMaskMainIntr+1] = ^mask.Error
	req[regMaskPassiveTargIntr-regMaskMainIntr+1] = ^mask.Passive
	return d.Bus.Tx(i2cAddr, req, nil)
}

func (d *Device) interruptStatus() (intrs interrupts, mask interrupts, err error) {
	req, resp := d.scratch[:1], d.scratch[1:4]
	req[0] = modeReadReg | regTimerNFCIntr
	if err := d.Bus.Tx(i2cAddr, req, resp); err != nil {
		return interrupts{}, interrupts{}, err
	}
	intrs = interrupts{
		Timer:   resp[0],
		Error:   resp[1],
		Passive: resp[2],
	}
	// The main interrupt register is read last, because
	// reading it also clears the error interrupt register.
	req, resp = d.scratch[:1], d.scratch[1:6]
	req[0] = modeReadReg | regMaskMainIntr
	if err := d.Bus.Tx(i2cAddr, req, resp); err != nil {
		return interrupts{}, interrupts{}, err
	}
	intrs.Main = resp[4]
	mask = interrupts{
		Main:    ^resp[0],
		Timer:   ^resp[1],
		Error:   ^resp[2],
		Passive: ^resp[3],
	}
	return intrs, mask, nil
}

func (d *Device) configureProtocol(prot Protocol) error {
	type config struct {
		opMode      byte
		rxConf      [4]byte
		corrConf    [2]byte
		overshoot   [2]byte
		undershoot  [2]byte
		maskReceive byte
		nrt         uint16
		iso14443a   byte
	}
	var conf config
	switch prot {
	case ISO14443a, Listen, Detect:
		conf = config{
			opMode:      omISO14443A,
			rxConf:      [...]byte{0x08, 0x2d, 0x00, 0x00},
			corrConf:    [...]byte{0x51, 0x00},
			overshoot:   [...]byte{0x40, 0x03},
			undershoot:  [...]byte{0x40, 0x03},
			maskReceive: 0x0e,
			nrt:         0x23,
			iso14443a:   0x00,
		}
	case ISO15693:
		conf = config{
			opMode:      omISO15693,
			rxConf:      [...]byte{0x13, 0x25, 0x00, 0x00},
			corrConf:    [...]byte{0x13, 0x01},
			overshoot:   [...]byte{0x00, 0x00},
			undershoot:  [...]byte{0x00, 0x00},
			maskReceive: 0x41,
			nrt:         0x52,
			iso14443a:   0b1<<no_tx_par | 0b1<<no_rx_par,
		}
	default:
		panic("invalid protocol")
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
		regMaskRecieveTimer, conf.maskReceive,
		regNoResponseTimer1, byte(conf.nrt>>8),
		regNoResponseTimer2, byte(conf.nrt),
		regISO14443AConf, conf.iso14443a,
	); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	d.prot = prot
	return nil
}

func (d *Device) Read(buf []byte) (int, error) {
	return d.read(buf, timeout)
}

func (d *Device) read(buf []byte, timeout time.Duration) (int, error) {
	if !d.eof {
		if _, err := d.waitForInterrupt(timeout, nil); err != nil {
			return 0, fmt.Errorf("st25r3916: read: %w", err)
		}
		d.eof = true
	}
	req, fifoStatus := d.scratch[:1], d.scratch[1:3]
	req[0] = modeReadReg | regFIFOStatus1
	if err := d.Bus.Tx(i2cAddr, req, fifoStatus); err != nil {
		return 0, err
	}
	fifoLen := int(fifoStatus[1]&0b1100_0000)<<2 | int(fifoStatus[0])
	overflow := fifoStatus[1]&(0b1<<fifo_ovr) != 0
	// Exclude the CRC bytes left in the FIFO.
	if d.excludeCRC {
		fifoLen = max(fifoLen-2, 0)
	}
	n := min(fifoLen, len(buf))
	req = d.scratch[:1]
	req[0] = modeFIFO | readFIFO
	if err := d.Bus.Tx(i2cAddr, req, buf[:n]); err != nil {
		return 0, fmt.Errorf("st25r3916: read: %w", err)
	}
	var err error
	switch {
	case overflow:
		err = errors.New("st25r3916: read: FIFO overflow")
	case n == fifoLen:
		err = io.EOF
	}
	return n, err
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
	if bits > 0 {
		bytes--
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

func (d *Device) writeFIFO(tx []byte, txBits byte) error {
	if err := d.writeTXLen(len(tx), txBits); err != nil {
		return err
	}
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

func (d *Device) commandAndWait(cmd byte, mask interrupts) (interrupts, error) {
	if err := d.setInterruptMask(mask); err != nil {
		return interrupts{}, err
	}
	if err := d.command(cmd); err != nil {
		return interrupts{}, err
	}
	return d.waitForInterrupt(timeout, nil)
}

func (d *Device) command(cmd byte) error {
	req := d.scratch[:1]
	req[0] = modeCommand | cmd
	return d.Bus.Tx(i2cAddr, req, nil)
}

const (
	i2cAddr = 0x50

	txWaterLevel = 200
	rxWaterLevel = 300

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
	regIOConf1               = 0x00
	regIOConf2               = 0x01
	regOpCtrl                = 0x02
	regModeDef               = 0x03
	regBitRate               = 0x04
	regISO14443AConf         = 0x05
	regNFCIP1PassiveTarg     = 0x08
	regStreamModeDef         = 0x09
	regAuxDef                = 0x0a
	regRXConf1               = 0x0b
	regRXConf2               = 0x0c
	regRXConf3               = 0x0d
	regRXConf4               = 0x0e
	regMaskRecieveTimer      = 0x0f
	regNoResponseTimer1      = 0x10
	regNoResponseTimer2      = 0x11
	regTimerEMVCtrl          = 0x12
	regMaskMainIntr          = 0x16
	regMaskTimerNFCIntr      = 0x17
	regMaskErrorWakeupIntr   = 0x18
	regMaskPassiveTargIntr   = 0x19
	regMainIntr              = 0x1a
	regTimerNFCIntr          = 0x1b
	regErrorWakeupIntr       = 0x1c
	regPassiveTargIntr       = 0x1d
	regFIFOStatus1           = 0x1e
	regFIFOStatus2           = 0x1f
	regPassiveTarg           = 0x21
	regNumTX1                = 0x22
	regNumTX2                = 0x23
	regADConvOut             = 0x25
	regPassiveTargetMod      = 0x29
	regExtFieldAct           = 0x2a
	regExtFieldDeact         = 0x2b
	regRegulatorCtrl         = 0x2c
	regCapSensorCtrl         = 0x2f
	regCapSensor             = 0x30
	regAuxDisp               = 0x31
	regWakeupCtrl            = 0x32
	regAmplitudeMeasCtrl     = 0x33
	regAmplitudeMeasAutoDisp = 0x35
	regAmplitudeMeasDisp     = 0x36
	regPhaseMeasCtrl         = 0x37
	regPhaseMeasAutoDisp     = 0x39
	regPhaseMeasDisp         = 0x3a
	regCapMeasCtrl           = 0x3b
	regICID                  = 0x3f
	// Register addresses, space B. See table 28.
	spaceB              = 0b1 << 7
	regEMDSupConf       = spaceB | 0x05
	regCorrConf1        = spaceB | 0x0c
	regCorrConf2        = spaceB | 0x0d
	regFieldOnGuardTime = spaceB | 0x15
	regAuxMod           = spaceB | 0x28
	regResAMMod         = spaceB | 0x2a
	regRegulatorDisp    = spaceB | 0x2c
	regOvershootConf1   = spaceB | 0x30
	regOvershootConf2   = spaceB | 0x31
	regUndershootConf1  = spaceB | 0x32
	regUndershootConf2  = spaceB | 0x33

	// Commands, see table table 13.
	// Note that the constant include the command mode prefix 0b11. For example,
	// the set default command is really command 0 (0b11_000000).
	cmdSetDefault         = 0xc0
	cmdStopAll            = 0xc2
	cmdTransmitWithCRC    = 0xc4
	cmdTransmitWithoutCRC = 0xc5
	cmdTransmitREQA       = 0xc6
	cmdInitialFieldOn     = 0xc8
	cmdGotoSense          = 0xcd
	cmdGotoSleep          = 0xce
	cmdResetRXGain        = 0xd5
	cmdAdjustRegulator    = 0xd6
	cmdClearFIFO          = 0xdb
	cmdCalibrateCapSensor = 0xdd
	cmdMeasureCap         = 0xde
	cmdMeasureSupply      = 0xdf
	cmdStartWakeupTimer   = 0xe1
	cmdSpaceBAccess       = 0xfb
	cmdTestAccess         = 0xfc

	// IO configuration register 1 bits.
	lf_clk_off = 0
	out_cl     = 1
	i2c_thd    = 4

	// IO Configuration register 2 bits.
	slow_up    = 0
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

	// Timer and NFC interrupt bits.
	i_nfct = 0
	i_cat  = 1
	i_cac  = 2
	i_eof  = 3
	i_eon  = 4
	i_gpe  = 5
	i_nre  = 6
	i_dct  = 7

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
	i_wu_a_x  = 1
	i_wu_f    = 3
	i_rxe_pta = 4

	// Regulator control bits.
	reg_s = 7
	mpsv  = 0

	// ISO14443A configuration bits
	antcl     = 0
	no_rx_par = 6
	no_tx_par = 7

	// Auxiliary definition bits.
	dis_corr  = 2
	nfc_id    = 4
	no_crc_rx = 7

	// NFCIP-1 passive target definition bits (table 32).
	d_106_ac_a   = 0
	d_212_424_1r = 2
	d_ac_ap2p    = 3
	fdel         = 4

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

	// FIFO status 1 bits.
	fifo_ovr = 4
	fifo_unf = 5

	// Timer and EMV control bits.
	gptc = 5

	// Wakeup timer control bits.
	wcap = 0
	wph  = 1
	wam  = 2
	wto  = 3
	wut  = 4
	wur  = 7

	// Amplitude measurement configuration bits (table 105).
	am_ae  = 0
	am_aew = 1
	am_aam = 3
	am_d   = 4

	// Phase measurement configuration bits (table 109).
	pm_ae  = 0
	pm_aew = 1
	pm_aam = 3
	pm_d   = 4

	// Capacitance measurement configuration bits (table 113).
	cm_ae  = 0
	cm_aew = 1
	cm_aam = 3
	cm_d   = 4

	// Auxillary display bits (table 98).
	osc_ok = 4

	// iso14443a collision avoidance loop commands.
	casLevel1 = 0x93
	casLevel2 = 0x95
	casLevel3 = 0x97
)
