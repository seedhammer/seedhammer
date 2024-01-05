//go:build tinygo

// package ap33772s implements a driver for the Diodes AP33772S
// USB PD 3.1 sink controller.
package ap33772s

import (
	"encoding/binary"
	"errors"
	"fmt"
	"machine"
)

type Device struct {
	bus        Bus
	intr       machine.Pin
	interrupts chan struct{}
	scratch    [1 + (nSPRs+nEPRs)*2]byte
}

type Bus interface {
	Tx(addr uint16, w, r []byte) error
}

func New(bus Bus, intr machine.Pin) *Device {
	return &Device{
		bus:  bus,
		intr: intr,
	}
}

func (d *Device) Configure() error {
	d.interrupts = make(chan struct{}, 1)
	d.intr.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	d.intr.SetInterrupt(machine.PinFalling, d.handleInterrupt)
	// Set interrupt mask.
	if err := d.writeReg(regMASK, intREADY|intSTARTED|intNEWPDO); err != nil {
		return fmt.Errorf("ap33772s: %w", err)
	}
	return nil
}

func (d *Device) handleInterrupt(machine.Pin) {
	select {
	case d.interrupts <- struct{}{}:
	default:
	}
}

// MeasureTemperature reads the temperature in degrees celcius (C)
// off the connected thermistor.
func (d *Device) MeasureTemperature() (int, error) {
	v, err := d.readReg(regTEMP)
	if err != nil {
		return 0, fmt.Errorf("ap33772s: %w", err)
	}
	return int(v), nil
}

// MeasureCurrent reads the current in milliamperes (mA).
func (d *Device) MeasureCurrent() (int, error) {
	v, err := d.readReg(regCURRENT)
	if err != nil {
		return 0, fmt.Errorf("ap33772s: %w", err)
	}
	const milliAmperesPerUnit = 24
	return int(v) * milliAmperesPerUnit, nil
}

// MeasureVoltage reads the voltage in millivolts (mV).
func (d *Device) MeasureVoltage() (int, error) {
	v, err := d.readReg(regVOLTAGE)
	if err != nil {
		return 0, fmt.Errorf("ap33772s: %w", err)
	}
	const milliVoltsPerUnit = 80
	return int(v) * milliVoltsPerUnit, nil
}

// ReadState reads the interrupt status.
func (d *Device) ReadStatus() (int, error) {
	v, err := d.readReg(regSTATUS)
	if err != nil {
		return 0, fmt.Errorf("ap33772s: %w", err)
	}
	return int(v), nil
}

// LimitCurrent limits the current to the specified value.
// The limit is ignored if higher than the negotiated limit.
// A limit of zero represents the negotiated limit.
func (d *Device) LimitCurrent(limitmA int) error {
	// Read negotiated limit.
	req, resp := d.scratch[:1], d.scratch[1:3]
	req[0] = regIREQ
	if err := d.bus.Tx(ap33772sAddr, req, resp); err != nil {
		return fmt.Errorf("ap33772s: %w", err)
	}
	bo := binary.LittleEndian
	mA := int(bo.Uint16(resp)) * 10
	if mA < limitmA {
		return nil
	}
	// Set limit.
	req = d.scratch[:2]
	req[0] = regOCPTHR
	req[1] = uint8(limitmA / 50)
	if err := d.bus.Tx(ap33772sAddr, req, nil); err != nil {
		return fmt.Errorf("ap33772s: %w", err)
	}
	return nil
}

// AdjustVoltage negotiates the highest voltage in the range and returns
// it. An error is returned if the maximum current draw is less than 3A.
func (d *Device) AdjustVoltage(minVoltagemV, maxVoltagemV int) (int, error) {
	req, pdos := d.scratch[:1], d.scratch[1:1+(nSPRs+nEPRs)*2]
	req[0] = regSRCPDO
	if err := d.bus.Tx(ap33772sAddr, req, pdos); err != nil {
		return 0, fmt.Errorf("ap33772s: %w", err)
	}
	bestPDO := -1
	bestVoltage := uint16(0)
	bestVoltagemV := 0
	bestCurrent := uint16(0)
	bo := binary.LittleEndian
	for i := 0; i < nSPRs+nEPRs; i++ {
		// PDOs are 16 bit, big endian.
		pdo := bo.Uint16(pdos[i*2:])
		if detect := pdo>>15 == 0b1; !detect {
			continue
		}
		current := (pdo >> 10) & 0b1111
		voltage := pdo & 0xff
		voltagemV := int(voltage)
		if i < nSPRs {
			voltagemV *= 100
		} else {
			voltagemV *= 200
		}
		if minVoltagemV <= voltagemV && voltagemV <= maxVoltagemV {
			if voltagemV > bestVoltagemV || (voltagemV == bestVoltagemV && current > bestCurrent) {
				bestVoltage = voltage
				bestVoltagemV = voltagemV
				bestPDO = i
				bestCurrent = current
			}
		}
	}
	if bestPDO == -1 {
		return 0, errors.New("ap33772s: no suitable voltage found")
	}
	if bestCurrent < 8 { // 3A
		return 0, errors.New("ap33772s: no suitable current found")
	}
	pdoReq := uint16(
		uint16(bestPDO+1)<<12 | // PDOs are 1-indexed
			bestCurrent<<8 |
			bestVoltage,
	)
	req = d.scratch[:3]
	req[0] = regPD_REQMSG
	bo.PutUint16(req[1:], pdoReq)
	if err := d.bus.Tx(ap33772sAddr, req, nil); err != nil {
		return 0, fmt.Errorf("ap33772s: %w", err)
	}
	return bestVoltagemV, nil
}

func (d *Device) Voltage() (int, error) {
	req, resp := d.scratch[:1], d.scratch[1:3]
	req[0] = regVREQ
	if err := d.bus.Tx(ap33772sAddr, req, resp); err != nil {
		return 0, fmt.Errorf("ap33772s: %w", err)
	}
	const mV = 50
	return int(binary.LittleEndian.Uint16(resp)) * mV, nil
}

func (d *Device) Current() (int, error) {
	req, resp := d.scratch[:1], d.scratch[1:3]
	req[0] = regIREQ
	if err := d.bus.Tx(ap33772sAddr, req, resp); err != nil {
		return 0, fmt.Errorf("ap33772s: %w", err)
	}
	const mA = 10
	return int(binary.LittleEndian.Uint16(resp)) * mA, nil
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

	// Interrupt bits in the Mask and Status
	// registers.
	intSTARTED = 0b1 << 0
	intREADY   = 0b1 << 1
	intNEWPDO  = 0b1 << 2
)
