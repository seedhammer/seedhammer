//go:build tinygo

// package ap33772s implements a driver for the Diodes AP33772S
// USB PD 3.1 sink controller.
package ap33772s

import (
	"fmt"
	"machine"
)

const (
	ap3372sAddr = 0x52
	nSPRs       = 7
	nEPRs       = 6

	// I2C registers.
	regSRCPDO  = 0x20
	regVOLTAGE = 0x11
	regCURRENT = 0x12
	regTEMP    = 0x13
	regVREQ    = 0x14
	regIREQ    = 0x15

	// Commands.
	regPD_REQMSG = 0x31
)

type Device struct {
	bus *machine.I2C
}

func New(bus *machine.I2C) *Device {
	return &Device{
		bus: bus,
	}
}

// ReadTemperature reads the temperature in degrees celcius
// off the connected thermistor.
func (d *Device) ReadTemperature() (int, error) {
	temp := make([]byte, 1)
	if err := d.bus.Tx(ap3372sAddr, []byte{regTEMP}, temp); err != nil {
		return 0, fmt.Errorf("ap3372s: %w", err)
	}
	return int(temp[0]), nil
}

// AdjustVoltage negotiates the highest voltage that doesn't
// exceed the maximum. It returns the voltage negotiated.
func (d *Device) AdjustVoltage(maxVoltagemV int) error {
	pdos := make([]byte, (nSPRs+nEPRs)*2)
	if err := d.bus.Tx(ap3372sAddr, []byte{regSRCPDO}, pdos); err != nil {
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
		if err := d.bus.Tx(ap3372sAddr, []byte{regPD_REQMSG, uint8(req), uint8(req >> 8)}, nil); err != nil {
			return fmt.Errorf("ap3372s: %w", err)
		}
	}
	return nil
}
