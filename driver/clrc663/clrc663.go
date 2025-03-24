//go:build tinygo

// Package clrc663 implements a TinyGo driver for the CLRC663 NFC writer.
//
// Datasheet: https://www.nxp.com/docs/en/data-sheet/CLRC663.pdf
package clrc663

import (
	"fmt"
	"io"
	"machine"
	"time"
)

// FIFOSize is the number of bytes that can be
// read without risking overflow.
const FIFOSize = 256

type Device struct {
	bus *machine.I2C

	// CRC presets for the active protocol.
	rxCRCPreset, txCRCPreset uint8
	// txDataNum is the TxBits setting.
	txDataNum uint8
	// rxBitCtrl is the RxBitCtrl setting.
	rxBitCtrl uint8
	readDone  bool
	rxLen     int
	scratch   [14]byte
}

func New(bus *machine.I2C) *Device {
	return &Device{
		bus: bus,
	}
}

type Protocol int

const (
	ISO15693 Protocol = iota
	ISO14443a
)

func (d *Device) Configure() error {
	if err := d.writeRegs(
		// Cancel any running command.
		regCommand, cmdIdle,
		// Soft reset.
		regCommand, cmdSoftReset,
	); err != nil {
		return fmt.Errorf("clrc663: soft reset: %w", err)
	}
	// Wait for reset to complete.
	if err := d.waitForIdle(); err != nil {
		return fmt.Errorf("clrc663: soft reset: %w", err)
	}
	return nil
}

func (d *Device) SetPadEnable(padEn uint8) error {
	return d.writeRegs(regPadEn, padEn)
}

func (d *Device) SetPadOutput(padOut uint8) error {
	return d.writeRegs(regPadOut, padOut)
}

func (d *Device) SetTxBits(bits int) {
	d.txDataNum = txDataNumDataEn | uint8(bits)
}

func (d *Device) SetRxBitCtrl(v uint8) {
	d.rxBitCtrl = v
}

func (d *Device) SetCRC(tx, rx bool) {
	if tx {
		d.txCRCPreset |= 0b1
	} else {
		d.txCRCPreset &^= 0b1
	}
	if rx {
		d.rxCRCPreset |= 0b1
	} else {
		d.rxCRCPreset &^= 0b1
	}
}

func (d *Device) Transceive(tx []byte) error {
	d.readDone = false
	if err := d.writeRegs(
		// Cancel any running command.
		regCommand, cmdIdle,
		// Clear FIFO.
		regFIFOControl, 1<<4,
		// Clear interrupts.
		regIRQ0, 0x7F,
		regIRQ1, 0x7F,
		// Settings.
		regTxCrcPreset, d.txCRCPreset,
		regRxCrcPreset, d.rxCRCPreset,
		regTxDataNum, d.txDataNum,
		regRxBitCtrl, d.rxBitCtrl,
	); err != nil {
		return fmt.Errorf("clrc663: transceive: %w", err)
	}
	if err := d.writeFIFO(tx...); err != nil {
		return fmt.Errorf("clrc663: %w", err)
	}
	if err := d.writeRegs(regCommand, cmdTransceive); err != nil {
		return fmt.Errorf("clrc663: transceive: %w", err)
	}
	return nil
}

// Read the data received from a Transceive.
func (d *Device) Read(buf []byte) (int, error) {
	if !d.readDone {
		start := time.Now()
		const timeout = 1 * time.Second
		for {
			irq0, err := d.readStatus()
			if err != nil {
				return 0, fmt.Errorf("clrc663: read: %w", err)
			}
			if irq0&irqRx == 0 {
				// Read not completed yet.
				if time.Since(start) > timeout {
					return 0, fmt.Errorf("clrc663: read timeout")
				}
				continue
			}
			// Read complete; read message length.
			tx, rx := d.scratch[:1], d.scratch[1:2]
			tx[0] = regFIFOLength
			if err := d.bus.Tx(i2cAddr, tx, rx); err != nil {
				return 0, fmt.Errorf("clrc663: read: %w", err)
			}
			d.rxLen = int(rx[0])
			d.readDone = true
			break
		}
	}
	if d.rxLen == 0 {
		return 0, io.EOF
	}
	// Read data.
	tx := d.scratch[:1]
	tx[0] = regFIFOData
	buf = buf[:min(len(buf), d.rxLen)]
	if err := d.bus.Tx(i2cAddr, tx, buf); err != nil {
		return 0, fmt.Errorf("clrc663: read: %w", err)
	}
	d.rxLen -= len(buf)
	return len(buf), nil
}

func (d *Device) readStatus() (irq0 uint8, err error) {
	scratch := d.scratch[:]
	regStart := scratch[:1]
	scratch = scratch[1:]
	vals := scratch[:5]
	regStart[0] = regIRQ0
	if err := d.bus.Tx(i2cAddr, regStart, vals); err != nil {
		return 0, err
	}
	irq0, errCode := vals[0], vals[4]
	if err != nil {
		return 0, err
	}
	if irq0&irqErr != 0 {
		return 0, fmt.Errorf("command error (code %#.2x)", errCode)
	}
	return
}

// writeRegs write a list of (register, value) pairs.
func (d *Device) writeRegs(regVals ...uint8) error {
	if len(regVals)%2 != 0 {
		panic("register values not paired")
	}
	for i := 0; i < len(regVals); i += 2 {
		if err := d.bus.Tx(i2cAddr, regVals[i:i+2], nil); err != nil {
			return fmt.Errorf("clrc663: %w", err)
		}
	}
	return nil
}

func (d *Device) writeFIFO(data ...uint8) error {
	tx := d.scratch[:]
	tx[0] = regFIFOData
	for len(data) > 0 {
		n := copy(tx[1:], data)
		data = data[n:]
		if err := d.bus.Tx(i2cAddr, tx[:n+1], nil); err != nil {
			return fmt.Errorf("write fifo: %w", err)
		}
	}
	return nil
}

func (d *Device) runCommand(cmd uint8, args ...uint8) error {
	if err := d.writeFIFO(args...); err != nil {
		return err
	}
	if err := d.writeRegs(regCommand, cmd); err != nil {
		return fmt.Errorf("command %#x: %w", cmd, err)
	}
	return nil
}

func (d *Device) waitForIdle() error {
	for {
		irq0, err := d.readStatus()
		if err != nil {
			return err
		}
		if irq0&irqIdle != 0 {
			return nil
		}
	}
}

func (d *Device) measureLPCD() error {
	// Part-1, configurate LPCD Mode
	// Please remove any PICC from the HF of the reader.
	// "I" and the "Q" values read from reg 0x42 and 0x43
	// shall be used in part-2 "Detect PICC"
	d.writeRegs(
		regCommand, 0,
		regFIFOControl, 0xB0, // Flush FIFO
		// LPCD_config
		regLPCD_QMin, 0xC0, // Set Qmin register
		regLPCD_QMax, 0xFF, // Set Qmax register
		regLPCD_IMin, 0xC0, // Set Imin register
		regDrvMode, 0x89, // set DrvMode register
		regLPCD_Options, lpcdTxHigh|lpcdFilter, // Stronger detection field, filter measurements.
		// Execute trimming procedure
		regT3ReloadHi, 0x00, // Write default. T3 reload value Hi
		regT3ReloadLo, 0x10, // Write default. T3 reload value Lo
		regT4ReloadHi, 0x00, // Write min. T4 reload value Hi
		regT4ReloadLo, 0x05, // Write min. T4 reload value Lo
		regT4Control, 0b1111_1000, // Config T4 for AutoLPCD&AutoRestart.Set AutoTrimm bit.Start T4.
		regRcv, 0x52, // Set Rx_ADCmode bit
		regRxAna, 0x03, // Raise receiver gain to maximum
		regCommand, cmdLPCD, // Execute Rc663 command "Auto_T4" (Low power card detection and/or Auto trimming)
	)

	//> ------------ I and Q Value for LPCD ----------------
	req, I_Q := d.scratch[:1], d.scratch[1:3]
	req[0] = regLPCD_I_Result
	for {
		// Measure.
		time.Sleep(100 * time.Millisecond)

		if err := d.bus.Tx(i2cAddr, req, I_Q); err != nil {
			return fmt.Errorf("clrc663: lpcd calibration: %w", err)
		}

		I := I_Q[0] & 0x3F
		Q := I_Q[1] & 0x3F
		fmt.Println(I, Q)
	}
	d.writeRegs(
		regCommand, 0x00,
		regRcv, 0x12, // Clear Rx_ADCmode bit
	)
	return nil
}

func (d *Device) RadioOn(prot Protocol) error {
	var (
		rxProtocol, txProtocol uint8
		regEEPROMAddr          uint16
	)
	switch prot {
	case ISO15693:
		rxProtocol, txProtocol = protocol_ISO15693_26_SSC_26_1_4, protocol_ISO15693_26_SSC_26_1_4
		regEEPROMAddr = eepromAddrISO15693_SLI_1_4_SSC_26
	case ISO14443a:
		rxProtocol, txProtocol = protocol_ISO14443A_106_MILLER_MANCHESTER, protocol_ISO14443A_106_MILLER_MANCHESTER
		regEEPROMAddr = eepromAddrISO14443A_106
	default:
		panic("invalid protocol")
	}
	// Load preset protocol registers.
	if err := d.runCommand(
		cmdLoadProtocol,
		rxProtocol, txProtocol,
	); err != nil {
		return fmt.Errorf("clrc663: load protocol: %w", err)
	}
	if err := d.waitForIdle(); err != nil {
		return err
	}

	// Load preset antenna registers.
	const eepromLength = regRxAna - regDrvMode + 1
	if err := d.runCommand(
		cmdLoadReg,
		// Source EEPROM address.
		uint8(regEEPROMAddr>>8), uint8(regEEPROMAddr&0xff),
		// Destination register
		regDrvMode,
		// Length
		eepromLength,
	); err != nil {
		return fmt.Errorf("clrc663: load reg:: %w", err)
	}
	if err := d.waitForIdle(); err != nil {
		return err
	}
	req, presets := d.scratch[:1], d.scratch[1:4]
	req[0] = regTxCrcPreset
	if err := d.bus.Tx(i2cAddr, req, presets); err != nil {
		return fmt.Errorf("clrc663: read crc presets: %w", err)
	}
	d.txCRCPreset, d.rxCRCPreset, d.txDataNum = presets[0], presets[1], presets[2]
	req, reg := d.scratch[:1], d.scratch[1:2]
	req[0] = regRxBitCtrl
	if err := d.bus.Tx(i2cAddr, req, reg); err != nil {
		return fmt.Errorf("clrc663: read rxbitctrl: %w", err)
	}
	d.rxBitCtrl = reg[0]
	return nil
}

func (d *Device) RadioOff() error {
	// Cancel any running command and shut off radio.
	if err := d.writeRegs(regCommand, cmdIdle|commandModemOff); err != nil {
		return fmt.Errorf("clrc663: modem off: %w", err)
	}
	return nil
}

const (
	i2cAddr = 0b01010_00 // Last two bits depend on pin settings.

	regCommand          = 0x00 //  Starts and stops command execution
	regHostCtrl         = 0x01 //  Host control register
	regFIFOControl      = 0x02 //  Control register of the FIFO
	regWaterLevel       = 0x03 //  Level of the FIFO underflow and overflow warning
	regFIFOLength       = 0x04 //  Length of the FIFO
	regFIFOData         = 0x05 //  Data In/Out exchange register of FIFO buffer
	regIRQ0             = 0x06 //  Interrupt register 0
	regIRQ1             = 0x07 //  Interrupt register 1
	regIRQ0En           = 0x08 //  Interrupt enable register 0
	regIRQ1En           = 0x09 //  Interrupt enable register 1
	regError            = 0x0A //  Error bits showing the error status of the last command execution
	regStatus           = 0x0B //  Contains status of the communication
	regRxBitCtrl        = 0x0C //  Control register for anticollision adjustments for bit oriented protocols
	regRxColl           = 0x0D //  Collision position register
	regTControl         = 0x0E //  Control of Timer 0..3
	regT0Control        = 0x0F //  Control of Timer0
	regT0ReloadHi       = 0x10 //  High register of the reload value of Timer0
	regT0ReloadLo       = 0x11 //  Low register of the reload value of Timer0
	regT0CounterValHi   = 0x12 //  Counter value high register of Timer0
	regT0CounterValLo   = 0x13 //  Counter value low register of Timer0
	regT1Control        = 0x14 //  Control of Timer1
	regT1ReloadHi       = 0x15 //  High register of the reload value of Timer1
	regT1ReloadLo       = 0x16 //  Low register of the reload value of Timer1
	regT1CounterValHi   = 0x17 //  Counter value high register of Timer1
	regT1CounterValLo   = 0x18 //  Counter value low register of Timer1
	regT2Control        = 0x19 //  Control of Timer2
	regT2ReloadHi       = 0x1A //  High byte of the reload value of Timer2
	regT2ReloadLo       = 0x1B //  Low byte of the reload value of Timer2
	regT2CounterValHi   = 0x1C //  Counter value high byte of Timer2
	regT2CounterValLo   = 0x1D //  Counter value low byte of Timer2
	regT3Control        = 0x1E //  Control of Timer3
	regT3ReloadHi       = 0x1F //  High byte of the reload value of Timer3
	regT3ReloadLo       = 0x20 //  Low byte of the reload value of Timer3
	regT3CounterValHi   = 0x21 //  Counter value high byte of Timer3
	regT3CounterValLo   = 0x22 //  Counter value low byte of Timer3
	regT4Control        = 0x23 //  Control of Timer4
	regT4ReloadHi       = 0x24 //  High byte of the reload value of Timer4
	regT4ReloadLo       = 0x25 //  Low byte of the reload value of Timer4
	regT4CounterValHi   = 0x26 //  Counter value high byte of Timer4
	regT4CounterValLo   = 0x27 //  Counter value low byte of Timer4
	regDrvMode          = 0x28 //  Driver mode register
	regTxAmp            = 0x29 //  Transmitter amplifier register
	regDrvCon           = 0x2A //  Driver configuration register
	regTxl              = 0x2B //  Transmitter register
	regTxCrcPreset      = 0x2C //  Transmitter CRC control register, preset value
	regRxCrcPreset      = 0x2D //  Receiver CRC control register, preset value
	regTxDataNum        = 0x2E //  Transmitter data number register
	regTxModWidth       = 0x2F //  Transmitter modulation width register
	regTxSym10BurstLen  = 0x30 //  Transmitter symbol 1 + symbol 0 burst length register
	regTXWaitCtrl       = 0x31 //  Transmitter wait control
	regTxWaitLo         = 0x32 //  Transmitter wait low
	regFrameCon         = 0x33 //  Transmitter frame control
	regRxSofD           = 0x34 //  Receiver start of frame detection
	regRxCtrl           = 0x35 //  Receiver control register
	regRxWait           = 0x36 //  Receiver wait register
	regRxThreshold      = 0x37 //  Receiver threshold register
	regRcv              = 0x38 //  Receiver register
	regRxAna            = 0x39 //  Receiver analog register
	regLPCD_Options     = 0x3A //  LPCD options (CLRC66303 only)
	regSerialSpeed      = 0x3B //  Serial speed register
	regLFO_Trimm        = 0x3C //  Low-power oscillator trimming register
	regPLL_Ctrl         = 0x3D //  IntegerN PLL control register, for microcontroller clock output adjustment
	regPLL_DivOut       = 0x3E //  IntegerN PLL control register, for microcontroller clock output adjustment
	regLPCD_QMin        = 0x3F //  Low-power card detection Q channel minimum threshold
	regLPCD_QMax        = 0x40 //  Low-power card detection Q channel maximum threshold
	regLPCD_IMin        = 0x41 //  Low-power card detection I channel minimum threshold
	regLPCD_I_Result    = 0x42 //  Low-power card detection I channel result register
	regLPCD_Q_Result    = 0x43 //  Low-power card detection Q channel result register
	regPadEn            = 0x44 //  PIN enable register
	regPadOut           = 0x45 //  PIN out register
	regPadIn            = 0x46 //  PIN in register
	regSigOut           = 0x47 //  Enables and controls the SIGOUT Pin
	regTxBitMod         = 0x48 //  Transmitter bit mode register
	regTxDataCon        = 0x4A //  Transmitter data configuration register
	regTxDataMod        = 0x4B //  Transmitter data modulation register
	regTxSymFreq        = 0x4C //  Transmitter symbol frequency
	regTxSym0H          = 0x4D //  Transmitter symbol 0 high register
	regTxSym0L          = 0x4E //  Transmitter symbol 0 low register
	regTxSym1H          = 0x4F //  Transmitter symbol 1 high register
	regTxSym1L          = 0x50 //  Transmitter symbol 1 low register
	regTxSym2           = 0x51 //  Transmitter symbol 2 register
	regTxSym3           = 0x52 //  Transmitter symbol 3 register
	regTxSym10Len       = 0x53 //  Transmitter symbol 1 + symbol 0 length register
	regTxSym32Len       = 0x54 //  Transmitter symbol 3 + symbol 2 length register
	regTxSym10BurstCtrl = 0x55 //  Transmitter symbol 1 + symbol 0 burst control register
	regTxSym10Mod       = 0x56 //  Transmitter symbol 1 + symbol 0 modulation register
	regTxSym32Mod       = 0x57 //  Transmitter symbol 3 + symbol 2 modulation register
	regRxBitMod         = 0x58 //  Receiver bit modulation register
	regRxEofSym         = 0x59 //  Receiver end of frame symbol register
	regRxSyncValH       = 0x5A //  Receiver synchronisation value high register
	regRxSyncValL       = 0x5B //  Receiver synchronisation value low register
	regRxSyncMod        = 0x5C //  Receiver synchronisation mode register
	regRxMod            = 0x5D //  Receiver modulation register
	regRxCorr           = 0x5E //  Receiver correlation register
	regFabCal           = 0x5F //  Calibration register of the receiver, calibration performed at production
	regVersion          = 0x7F //  Version and subversion register

	irqErr    = 0b1 << 1
	irqRx     = 0b1 << 2
	irqTx     = 0b1 << 3
	irqIdle   = 0b1 << 4
	irqGlobal = 0b1 << 6

	cmdIdle         = 0x00
	cmdLPCD         = 0x01
	cmdReceive      = 0x05
	cmdTransceive   = 0x07
	cmdLoadReg      = 0x0c
	cmdLoadProtocol = 0x0d
	cmdSoftReset    = 0x1f

	drvModeTx2Inv = 0b1 << 7
	drvModeTxEn   = 0b1 << 3

	lpcdFilter = 0b1 << 2
	lpcdTxHigh = 0b1 << 3

	lpcdIRQClr = 0b1 << 6

	irq0ErrEn     = 0b1 << 1
	irq0RxEn      = 0b1 << 2
	irq0IdleEn    = 0b1 << 4
	irq0LoAlertEn = 0b1 << 5
	irq0Inv       = 0b1 << 7

	irq1LPCDEn = 0b1 << 5
	irq1PinEn  = 0b1 << 6

	txDataNumDataEn = 0b1 << 3

	commandModemOff = 0b1 << 6

	errorCollDet = 0b1 << 2
)

// Protocol numbers for the LoadProtocol command.
const (
	protocol_ISO14443A_106_MILLER_MANCHESTER = 0
	protocol_ISO15693_26_SSC_26_1_4          = 10
)

// Antenna configuration EEPROM addresses.
const (
	eepromAddrISO14443A_106           = 0xc0
	eepromAddrISO15693_SLI_1_4_SSC_26 = 0x194
)
