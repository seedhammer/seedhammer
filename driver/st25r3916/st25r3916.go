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
	"machine"
	"time"
)

type Device struct {
	bus    Bus
	intPin machine.Pin
	cancel chan struct{}

	prot       Protocol
	extField   fieldState
	interrupts chan struct{}
	timer      *time.Timer
	excludeCRC bool

	scratch [256]byte
}

type Bus interface {
	Tx(addr uint16, w, r []byte) error
}

// FIFOSize is the number of bytes that can be
// read without risking overflow.
const FIFOSize = 512 - 2 // Make room for the CRC bytes.

type Protocol int

const (
	ISO15693 Protocol = iota
	ISO14443a
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
	// General timeout to guard against hangs, excessive
	// receive times etc.
	defTimeout = 1 * time.Second

	// Once a field is on, we wait until it is off again.
	// However, leave a long timeout in case the off detection
	// somehow fails.
	fieldOnTimeout = 10 * time.Second

	// fieldOffTimeouit is the timeout in listen mode while
	// no tag has been seen.
	fieldOffTimeout = 1 * time.Second

	// fieldDetectionTimeout is the time spent waiting for
	// an external field.
	fieldDetectionTimeout = 700 * time.Millisecond

	// Card detection thresholds.
	ampSens   = 2
	phaseSens = 2
)

type fieldState int

const (
	fieldOff fieldState = iota
	fieldOn
	fieldActive
)

var errTimeout = errors.New("timeout")

func New(b Bus, intPin machine.Pin) *Device {
	return &Device{
		bus:        b,
		intPin:     intPin,
		timer:      time.NewTimer(0),
		interrupts: make(chan struct{}, 1),
		cancel:     make(chan struct{}, 1),
	}
}

func (d *Device) reset() error {
	d.intPin.Configure(machine.PinConfig{Mode: machine.PinInput})
	d.intPin.SetInterrupt(machine.PinRising, d.handleInterrupt)
	// Reset.
	if err := d.command(cmdSetDefault); err != nil {
		return err
	}
	// Initialize according to the datasheet, section 4.1 "Power-on sequence".
	datasheetSetup := d.scratch[:3]
	datasheetSetup[0] = cmdTestAccess
	datasheetSetup[1] = 0x04
	datasheetSetup[2] = 0x10
	if err := d.bus.Tx(i2cAddr, datasheetSetup, nil); err != nil {
		return err
	}
	if err := d.writeRegs(
		regIOConf1, 0b11<<out_cl|0b1<<lf_clk_off|0b01<<i2c_thd, // Disable the MCU_CLK pin, 400 kHz i2c.
		regIOConf2, 0b1<<io_drv_lvl, // Increase IO drive strength, as recommended in table 20.
		regResAMMod, 0b1<<fa3_f|0<<md_res, // Minimum non-overlap.
		// Adjust fdel to 5 like the ST example code. Datasheet table 32 says minimum 2.
		regNFCIP1PassiveTarg, 5<<fdel|
			// Disable unused passive detection modes.
			0b1<<d_212_424_1r|0b1<<d_ac_ap2p,
		regExtFieldAct, 0b001<<trg_l|0b0001<<rfe_t, // Lower activation threshold.
		regExtFieldDeact, 0b000<<trg_ld|0b000<<rfe_td, // Lower deactivation threshold.
		regPassiveTargetMod, 0x5f, // Reduce RFO resistance in modulated state.
		regEMDSupConf, 0b1<<rx_start_emv, // Enable start on first 4 bits.
		regStreamModeDef, modeISO15693, // Setup streaming mode for iso15693.
		regTimerEMVCtrl, 0b001<<gptc, // Start timer at end of rx.
		regWakeupCtrl, 0b010<<wut|0b1<<wur|0b1<<wam|0b1<<wph, // Enable card detection methods, set measure period.
		regAmplitudeMeasCtrl, ampSens<<am_d|0b1<<am_ae|0b1<<am_aam|0b10<<am_aew, // Set amplitude measurement ∆am, auto-averaging reference.
		regPhaseMeasCtrl, phaseSens<<pm_d|0b1<<pm_ae|0b1<<pm_aam|0b10<<pm_aew, // Set phase measurement ∆pm, auto-averaging reference.
	); err != nil {
		return err
	}
	if err := d.enable(); err != nil {
		return err
	}
	if err := d.command(cmdGotoSense); err != nil {
		return err
	}
	// Adjust regulators.
	if err := d.writeRegs(
		// First, the reg_s bit must be cycled.
		regRegulatorCtrl, 0b1<<reg_s,
		regRegulatorCtrl, 0b0<<reg_s,
	); err != nil {
		return err
	}
	// Then issue the adjust regulator command.
	if _, err := d.commandAndWait(cmdAdjustRegulator, interrupts{Timer: 0b1 << i_dct}, defTimeout); err != nil {
		return err
	}
	// Generate random 4-byte UID, starting with 0x08 to indicate
	// it is dynamically generated.
	uid := make([]byte, 4)
	rng, err := machine.GetRNG()
	if err != nil {
		// Should never happen.
		panic(err)
	}
	binary.BigEndian.PutUint32(uid, rng)
	// SAK for NFC Type 4A (4.8.2, table 17).
	const sakT4A = 0b0_01_00_0_00
	// Load PT memory with NFC-A card emulation responses.
	req := append(d.scratch[:0], []byte{
		modeFIFO | loadPTMemory,
		0x08, uid[0], uid[1], uid[2], // UID.
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // Unused UID storage.
		// SENS_RES byte 1.
		0b00<<6 | // 4-byte UID.
			0b1<<0, // Bit frame SDD at position 0.
		0x00, // SENS_RES byte 2.
		// SAK1, SAK2, SAK3.
		sakT4A, sakT4A, sakT4A,
	}...)
	if err := d.bus.Tx(i2cAddr, req, nil); err != nil {
		return err
	}
	return d.Close()
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
	if err := d.resetInterruptMask(mask); err != nil {
		return err
	}
	// Start oscillator.
	if err := d.writeReg(regOpCtrl, 0b1<<en); err != nil {
		return err
	}
	// Wait for oscillator stable.
	_, err = d.waitForInterrupt(defTimeout)
	return err
}

func (d *Device) handleInterrupt(machine.Pin) {
	select {
	case d.interrupts <- struct{}{}:
	default:
	}
}

// Wait for detection of a tag or external field, and
// attempt to turn on the field. Return false if an external
// field is on.
func (d *Device) Detect() (bool, error) {
	if d.extField > fieldOff {
		// External field still on.
		return false, nil
	}

	// According to AN5320, "Wake-up mode for ST25R3916", the
	// only way to reset the sensor calibration is to reset
	// the device.
	if err := d.reset(); err != nil {
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}
	if err := d.configureProtocol(ISO14443a); err != nil {
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}

	// Wait for field or tag detection.
	mask := interrupts{
		Error: 0b1<<i_wt | 0b1<<i_wam | 0b1<<i_wph,
	}
	if err := d.resetInterruptMask(mask); err != nil {
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}
	if err := d.writeRegs(regOpCtrl, 0b1<<wu); err != nil {
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}
	if _, err := d.waitForInterrupt(0); err != nil {
		if err == io.EOF {
			return false, err
		}
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}

	// Setup up listening (target) mode.
	if err := d.writeReg(regModeDef, 0b1<<targ|omISO14443A); err != nil {
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}

	// Listen for external fields.
	mask = interrupts{
		Main:    0b1 << i_rxe,
		Timer:   0b1<<i_nre | 0b1<<i_eof | 0b1<<i_eon | 0b1<<i_cac | 0b1<<i_cat,
		Error:   0b1<<i_crc | 0b1<<i_par | 0b1<<i_err1 | 0b1<<i_err2,
		Passive: 0b1<<i_wu_a_x | 0b1<<i_wu_a,
	}
	if err := d.resetInterruptMask(mask); err != nil {
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}
	if err := d.writeReg(regOpCtrl, 0b11<<en_fd_c); err != nil {
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}
	if _, err := d.waitForInterrupt(fieldDetectionTimeout); err != nil && err != errTimeout {
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}

	flags := byte(0b1<<en | 0b1<<rx_en)
	if d.extField == fieldOff {
		// No external field detected. Attempt to turn on our field to
		// prepare for communication with a tag.
		if err := d.enable(); err != nil {
			return false, fmt.Errorf("st25r3916: detect: %w", err)
		}
		_, err := d.commandAndWait(
			cmdInitialFieldOn,
			mask,
			defTimeout,
		)
		if err != nil {
			return false, fmt.Errorf("st25r3916: detect: %w", err)
		}
	}
	if d.extField == fieldOff {
		// There was no collision with external fields and the field is on (tx_en).
		flags |= 0b1 << tx_en
	} else {
		// External field detected. Acknowledge the wakeup by setting en and rx_en.
		// See "Low power field detection" in the datasheet.
		flags |= 0b11 << en_fd_c
	}
	if err := d.writeReg(regOpCtrl, flags); err != nil {
		return false, fmt.Errorf("st25r3916: detect: %w", err)
	}
	return d.extField == fieldOff, nil
}

func (d *Device) Close() error {
	if err := d.writeReg(regOpCtrl, 0); err != nil {
		return fmt.Errorf("st25r3916: %w", err)
	}
	return nil
}

func (d *Device) Interrupt() {
	select {
	case d.cancel <- struct{}{}:
	default:
	}
}

func (d *Device) SetProtocol(prot Protocol) error {
	if err := d.configureProtocol(prot); err != nil {
		return fmt.Errorf("st25r3916: protocol: %w", err)
	}
	return nil
}

func (d *Device) Sleep() error {
	if err := d.command(cmdGotoSleep); err != nil {
		return fmt.Errorf("st25r3916: sleep: %w", err)
	}
	return nil
}

func (i interrupts) Union(i2 interrupts) interrupts {
	return interrupts{
		Main:    i.Main | i2.Main,
		Timer:   i.Timer | i2.Timer,
		Error:   i.Error | i2.Error,
		Passive: i.Passive | i2.Passive,
	}
}

func (d *Device) Write(tx []byte) (int, error) {
	n, err := d.write(tx)
	if err != nil {
		err = fmt.Errorf("st25r3916: write: %w", err)
	}
	return n, err
}

func (d *Device) write(tx []byte) (int, error) {
	d.excludeCRC = true
	if err := d.command(cmdStopAll); err != nil {
		return 0, err
	}
	if err := d.command(cmdResetRXGain); err != nil {
		return 0, err
	}
	var (
		transmitCmd byte
		bits        byte
	)
	switch d.prot {
	case ISO14443a:
		const SENS_REQ = 0x26
		var conf byte
		transmitCmd = byte(cmdTransmitWithCRC)
		// Simple detection of anti-collision frame.
		anticol := len(tx) == 2 && tx[1] == 0x20 &&
			(tx[0] == casLevel1 || tx[0] == casLevel2 || tx[0] == casLevel3)
		reqa := len(tx) == 1 && tx[0] == SENS_REQ
		switch {
		case anticol, reqa:
			d.excludeCRC = false
		}
		switch {
		case anticol:
			transmitCmd = cmdTransmitWithoutCRC
		case reqa:
			transmitCmd = cmdTransmitREQA
		}
		if anticol {
			conf = 0b1 << antcl
		}
		if err := d.writeReg(regISO14443AConf, conf); err != nil {
			return 0, err
		}
	case ISO15693:
		d.excludeCRC = false
		transmitCmd = cmdTransmitWithoutCRC
	}
	if transmitCmd != cmdTransmitREQA {
		if err := d.writeFIFO(tx, bits); err != nil {
			return 0, err
		}
	}
	if err := d.command(transmitCmd); err != nil {
		return 0, err
	}
	return len(tx), nil
}

func (d *Device) waitForInterrupt(timeout time.Duration) (interrupts, error) {
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
		case <-d.cancel:
			return interrupts{}, io.EOF
		case <-tim:
			return interrupts{}, errTimeout
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

func (d *Device) resetInterruptMask(mask interrupts) error {
	req := d.scratch[:5]
	req[0] = regMaskMainIntr
	req[regMaskMainIntr-regMaskMainIntr+1] = ^mask.Main
	req[regMaskTimerNFCIntr-regMaskMainIntr+1] = ^mask.Timer
	req[regMaskErrorWakeupIntr-regMaskMainIntr+1] = ^mask.Error
	req[regMaskPassiveTargIntr-regMaskMainIntr+1] = ^mask.Passive
	if err := d.bus.Tx(i2cAddr, req, nil); err != nil {
		return err
	}
	// Clear interrupt status.
	select {
	case <-d.interrupts:
	default:
	}
	_, _, err := d.interruptStatus()
	return err
}

func (d *Device) interruptStatus() (intrs interrupts, mask interrupts, err error) {
	req, resp := d.scratch[:1], d.scratch[1:4]
	req[0] = modeReadReg | regTimerNFCIntr
	if err := d.bus.Tx(i2cAddr, req, resp); err != nil {
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
	if err := d.bus.Tx(i2cAddr, req, resp); err != nil {
		return interrupts{}, interrupts{}, err
	}
	intrs.Main = resp[4]
	mask = interrupts{
		Main:    ^resp[0],
		Timer:   ^resp[1],
		Error:   ^resp[2],
		Passive: ^resp[3],
	}
	if intrs.Timer&(0b1<<i_eon|0b1<<i_cac) != 0 {
		d.extField = max(fieldOn, d.extField)
	}
	if intrs.Passive&(0b1<<i_wu_a_x|0b1<<i_wu_a) != 0 {
		d.extField = fieldActive
	}
	if intrs.Timer&(0b1<<i_cat|0b1<<i_eof) != 0 {
		d.extField = fieldOff
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
	case ISO14443a:
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
	for {
		timeout := defTimeout
		if d.extField > fieldOff {
			timeout = fieldOffTimeout
			if d.extField >= fieldOn {
				timeout = fieldOnTimeout
			}
		}
		wasAct := d.extField == fieldActive
		intrs, err := d.waitForInterrupt(timeout)
		if err != nil {
			// In case of timeout, retry field collision
			// detection.
			d.extField = fieldOff
			if err == io.EOF {
				return 0, err
			}
			return 0, fmt.Errorf("st25r3916: read: %w", err)
		}
		var n int
		if intrs.Main&(0b1<<i_rxe) != 0 {
			// Data available.
			n, err = d.read(buf)
			if err != nil && err != io.EOF {
				err = fmt.Errorf("st25r3916: read: %w", err)
			}
		}
		if err == nil && wasAct && d.extField == fieldOff {
			// Treat the turning off of a previously active field
			// as EOF.
			err = io.EOF
		}
		if n > 0 || err != nil {
			return n, err
		}
	}
}

func (d *Device) read(buf []byte) (int, error) {
	req, fifoStatus := d.scratch[:1], d.scratch[1:3]
	req[0] = modeReadReg | regFIFOStatus1
	if err := d.bus.Tx(i2cAddr, req, fifoStatus); err != nil {
		return 0, err
	}
	fifoLen := int(fifoStatus[1]&0b1100_0000)<<2 | int(fifoStatus[0])
	// Exclude the CRC bytes left in the FIFO.
	// Messages without CRC are not supported in listen mode.
	if d.excludeCRC || d.extField >= fieldOn {
		fifoLen = max(fifoLen-2, 0)
	}
	overflow := fifoStatus[1]&(0b1<<fifo_ovr) != 0
	n := min(fifoLen, len(buf))
	req = d.scratch[:1]
	req[0] = modeFIFO | readFIFO
	if err := d.bus.Tx(i2cAddr, req, buf[:n]); err != nil {
		return 0, err
	}
	var err error
	switch {
	case overflow:
		err = errors.New("FIFO overflow")
	case len(buf) < fifoLen:
		// We don't support reading the FIFO contents in multiple reads.
		err = io.ErrShortBuffer
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
	return d.bus.Tx(i2cAddr, req, nil)
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
		if err := d.bus.Tx(i2cAddr, req[:n+1], nil); err != nil {
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
	err := d.bus.Tx(i2cAddr, req, res)
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
	return d.bus.Tx(i2cAddr, req, nil)
}

func (d *Device) commandAndWait(cmd byte, mask interrupts, timeout time.Duration) (interrupts, error) {
	if err := d.resetInterruptMask(mask); err != nil {
		return interrupts{}, err
	}
	if err := d.command(cmd); err != nil {
		return interrupts{}, err
	}
	return d.waitForInterrupt(timeout)
}

func (d *Device) command(cmd byte) error {
	req := d.scratch[:1]
	req[0] = modeCommand | cmd
	return d.bus.Tx(i2cAddr, req, nil)
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

	// Mode definition bits, table 22, 23, 24.
	om0         = 3
	om1         = 4
	om2         = 5
	om3         = 6
	omISO14443A = 0b1 << om0
	omISO15693  = 0b1<<om1 | 0b1<<om2 | 0b1<<om3 // Sub-carrier stream mode.
	targ        = 7

	// Stream mode definition bits.
	stx          = 0
	scp          = 3
	scf          = 5
	modeISO15693 = 0b01<<scf | // fc/32
		0b000<<stx | // fc/128
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
