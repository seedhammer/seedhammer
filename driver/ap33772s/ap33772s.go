//go:build tinygo

// package ap33772s implements a driver for the Diodes AP33772S
// USB PD 3.1 sink controller.
package ap33772s

import (
	"fmt"
	"machine"
)

type Device struct {
	bus     *machine.I2C
	scratch [2]byte
}

func New(bus *machine.I2C) *Device {
	return &Device{
		bus: bus,
	}
}

func (d *Device) Configure() error {
	// Setup interrupt sources.
	if err := d.writeReg(regMASK, maskREADY|maskSTARTED|maskNEWPDO); err != nil {
		return fmt.Errorf("ap3372s: %w", err)
	}
	return nil
}

// ReadTemperature reads the temperature in degrees celcius (C)
// off the connected thermistor.
func (d *Device) ReadTemperature() (int, error) {
	v, err := d.readReg(regTEMP)
	if err != nil {
		return 0, fmt.Errorf("ap3372s: %w", err)
	}
	return int(v), nil
}

// ReadCurrent reads the current in milliamperes (mA).
func (d *Device) ReadCurrent() (int, error) {
	v, err := d.readReg(regCURRENT)
	if err != nil {
		return 0, fmt.Errorf("ap3372s: %w", err)
	}
	const milliAmperesPerUnit = 24
	return int(v) * milliAmperesPerUnit, nil
}

// ReadCurrent reads the voltage in millivolts (mV).
func (d *Device) ReadVoltage() (int, error) {
	v, err := d.readReg(regVOLTAGE)
	if err != nil {
		return 0, fmt.Errorf("ap3372s: %w", err)
	}
	const milliVoltsPerUnit = 80
	return int(v) * milliVoltsPerUnit, nil
}

// ReadState reads the interrupt status.
func (d *Device) ReadStatus() (int, error) {
	v, err := d.readReg(regSTATUS)
	if err != nil {
		return 0, fmt.Errorf("ap3372s: %w", err)
	}
	return int(v), nil
}

// AdjustVoltage negotiates the highest voltage that doesn't
// exceed the maximum. It returns the voltage negotiated.
func (d *Device) AdjustVoltage(maxVoltagemV int) error {
	pdos := make([]byte, (nSPRs+nEPRs)*2)
	if err := d.bus.Tx(ap33772sAddr, []byte{regSRCPDO}, pdos); err != nil {
		return fmt.Errorf("ap3372s: %w", err)
	}
	bestPDO := -1
	bestVoltage := uint16(0)
	bestVoltagemV := uint16(0)
	bestCurrent := uint16(0)
	for i := 0; i < nSPRs+nEPRs; i++ {
		// PDOs are 16 bit, big endian.
		pdo := uint16(pdos[i*2+1])<<8 | uint16(pdos[i*2+0])
		if detect := pdo>>15 == 0b1; !detect {
			continue
		}
		current := (pdo >> 10) & 0b1111
		voltage := pdo & 0xff
		voltagemV := voltage
		if i < nSPRs {
			voltagemV *= 100
		} else {
			voltagemV *= 200
		}
		if int(voltagemV) <= maxVoltagemV {
			if voltagemV > bestVoltagemV || (voltagemV == bestVoltagemV && current > bestCurrent) {
				bestVoltage = voltage
				bestVoltagemV = voltagemV
				bestPDO = i
				bestCurrent = current
			}
		}
	}
	if bestPDO != -1 {
		req := uint16(
			uint16(bestPDO+1)<<12 | // PDOs are 1-indexed
				bestCurrent<<8 |
				bestVoltage,
		)
		if err := d.bus.Tx(ap33772sAddr, []byte{regPD_REQMSG, uint8(req), uint8(req >> 8)}, nil); err != nil {
			return fmt.Errorf("ap3372s: %w", err)
		}
	}
	return nil
}

func (d *Device) writeReg(reg, val uint8) error {
	req := d.scratch[:2]
	req[0], req[1] = reg, val
	return d.bus.Tx(ap33772sAddr, req, nil)
}

func (d *Device) readReg(reg uint8) (uint8, error) {
	req, resp := d.scratch[:1], d.scratch[1:2]
	req[0] = reg
	err := d.bus.Tx(ap33772sAddr, req, resp)
	return resp[0], err
}

const (
	ap33772sAddr = 0x52
	nSPRs        = 7
	nEPRs        = 6

	regSTATUS        = 0x01
	regMASK          = 0x02
	regOPMODE        = 0x03
	regCONFIG        = 0x04
	regPDCONFIG      = 0x05
	regSYSTEM        = 0x06
	regTR25          = 0x0C
	regTR50          = 0x0D
	regTR75          = 0x0E
	regTR100         = 0x0F
	regVOLTAGE       = 0x11
	regCURRENT       = 0x12
	regTEMP          = 0x13
	regVREQ          = 0x14
	regIREQ          = 0x15
	regVSELMIN       = 0x16
	regUVPTHR        = 0x17
	regOVPTHR        = 0x18
	regOCPTHR        = 0x19
	regOTPTHR        = 0x1A
	regDRTHR         = 0x1B
	regSRCPDO        = 0x20
	regSRC_SPR_PDO1  = 0x21
	regSRC_SPR_PDO2  = 0x22
	regSRC_SPR_PDO3  = 0x23
	regSRC_SPR_PDO4  = 0x24
	regSRC_SPR_PDO5  = 0x25
	regSRC_SPR_PDO6  = 0x26
	regSRC_SPR_PDO7  = 0x27
	regSRC_EPR_PDO8  = 0x28
	regSRC_EPR_PDO9  = 0x29
	regSRC_EPR_PDO10 = 0x2A
	regSRC_EPR_PDO11 = 0x2B
	regSRC_EPR_PDO12 = 0x2C
	regSRC_EPR_PDO13 = 0x2D
	regPD_REQMSG     = 0x31
	regPD_CMDMSG     = 0x32
	regPD_MSGRLT     = 0x33

	// Masks of the MASK register.
	maskSTARTED = 0b1 << 0
	maskREADY   = 0b1 << 1
	maskNEWPDO  = 0b1 << 2
)
