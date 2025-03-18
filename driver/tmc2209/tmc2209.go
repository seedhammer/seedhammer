// Package tmc2209 is a TinyGo implementation for the TMC2209
// stepper motor driver.
package tmc2209

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// Motor configuration.
const (
	// currentRMS is the motor current in amperes (A).
	currentRMS = 0.9
	// rsense is the sense resistor in ohms (Ω).
	rsense = 0.150
	// vfs is the sense voltage, in volts (V).
	vfs = 0.325
)

// Settings.
const (
	// The number of cycles to wait for the reply of
	// a read request.
	timeoutCycles = 50
	// txWaitCycles is the number of cycles to wait
	// before transmitting to the driver. The manual
	// specifies 4 cycles for the switch from input to
	// output and 63 cycles for resetting the automatic
	// baud detection.
	txWaitCycles = max(4, 63) + 1
	// 2^stepExp is the number of microsteps to a full step.
	stepExp = 8
	// Microsteps to a full step.
	Microsteps = 1 << stepExp
	// StandstillTuningPeriod is the minimum duration
	// the driver should be kept at full power in standstill
	// after enabling the motor.
	StandstillTuningPeriod = 130 * time.Millisecond

	// Compute IRUN from motor current and sense resistor.
	// The formula from the reference manual is
	//
	//  Irms = ((CS+1)/32) * (Vfs/(Rsense+20mΩ)) * (1/√2).
	//
	// Solving for CS,
	//
	//  CS = 32*Irms*√2*(Rsense+20mΩ)/Vfs - 1
	IRUN = 32*currentRMS*math.Sqrt2*(rsense+.02)/vfs - 1

	// IHOLDDELAY is the number of clock cycles to delay
	// current switch from IRUN to IHOLD on standstill.
	iholdDelay = 0

	// fclk is the clock frequency in Hz.
	fclk = 12e6
)

type Device struct {
	Bus    io.ReadWriter
	Addr   uint8
	Invert bool
}

const (
	GCONF      = 0x00
	GSTAT      = 0x01
	IFCNT      = 0x02
	SLAVECONF  = 0x03
	OTP_READ   = 0x05
	IOIN       = 0x06
	IHOLD_IRUN = 0x10
	TSTEP      = 0x12
	TCOOLTHRS  = 0x14
	VACTUAL    = 0x22
	SGTHRS     = 0x40
	SG_RESULT  = 0x41
	COOLCONF   = 0x42
	CHOPCONF   = 0x6c
	DRV_STATUS = 0x6f
	PWM_AUTO   = 0x72

	// GCONF settings.
	I_scale_analog   = 0b1 << 0
	shaft            = 0b1 << 3
	pdn_disable      = 0b1 << 6
	mstep_reg_select = 0b1 << 7
	multistep_filt   = 0b1 << 8

	// CHOPCONF settings
	mres_shift = 24
	intpol     = 1 << 28

	min_SENDDELAY = 2

	// attempts is the number of attempts for a read or a write
	// before giving up.
	attempts = 5
)

// SetupSharedUART a stepper driver by increasing its SENDDELAY, to
// avoid cross talk when multiple drivers share UART pin.
func (d *Device) SetupSharedUART() error {
	// Reading from a slave may confuse another until
	// SENDDELAY is raised. Don't read anything until
	// then.
	dg := writeDatagram(d.Addr, SLAVECONF, min_SENDDELAY<<8)
	for i := 0; i < attempts; i++ {
		d.Bus.Write(dg[:])
	}
	return nil
}

func (d *Device) Configure() error {
	// This is redundant with [SetupSharedUART], but do it anyway in case the setting
	// didn't stick.
	if err := d.write(SLAVECONF, min_SENDDELAY<<8); err != nil {
		return fmt.Errorf("tmc2209: set SLAVECONF: %w", err)
	}
	gconf, err := d.read(GCONF)
	if err != nil {
		return fmt.Errorf("tmc2209: read GCONF: %w", err)
	}
	// Disable standstill operation through the UART pin (we're using it for UART).
	gconf |= pdn_disable
	// Enable step resolution setting through MRES.
	gconf |= mstep_reg_select
	// Don't scale IRUN/IHOLD by Vref.
	gconf &^= I_scale_analog
	if d.Invert {
		gconf |= shaft
	}
	if err := d.write(GCONF, gconf); err != nil {
		return fmt.Errorf("tmc2209: set GCONF: %w", err)
	}
	irun := IRUN
	if irun > 31 {
		irun = 31
	}
	// IHOLD is the standstill current, equal to IRUN.
	ihold := irun
	ihold_irun := iholdDelay<<16 | uint32(irun)<<8 | uint32(ihold)
	if err := d.write(IHOLD_IRUN, ihold_irun); err != nil {
		return fmt.Errorf("tmc2209: set IHOLD/IRUN: %w", err)
	}

	chopconf, err := d.read(CHOPCONF)
	if err != nil {
		return fmt.Errorf("tmc2209: read CHOPCONF: %w", err)
	}
	// Set microstep resolution.
	chopconf &^= 0b1111 << mres_shift
	chopconf |= (8 - stepExp) << mres_shift
	// Disable step interpolation.
	chopconf &^= intpol
	d.write(CHOPCONF, chopconf)

	// Reset GSTAT.
	if err := d.write(GSTAT, 0b111); err != nil {
		return fmt.Errorf("tmc2209: set GSTAT: %w", err)
	}

	return nil
}

func (d *Device) PWMAuto() (int, error) {
	res, err := d.read(PWM_AUTO)
	return int(res), err
}

func (d *Device) TStep() (int, error) {
	res, err := d.read(TSTEP)
	return int(res), err
}

func (d *Device) StallResult() (int, error) {
	res, err := d.read(SG_RESULT)
	return int(res) / 2, err
}

// SetStallMinimumVelocity sets the minimum velocity in
// full-steps/second for detecting stalls.
func (d *Device) SetMinimumStallVelocity(stepsPerSecond int) error {
	// tcoolThrs is the TCOOLTHRS value for the stall guard velocity.
	// It is represented in time in clock cycles between each microstep
	// at maximum resolution (256).
	tcoolThrs := fclk / (stepsPerSecond * 256)
	if err := d.write(TCOOLTHRS, uint32(tcoolThrs)); err != nil {
		return fmt.Errorf("tmc2209: set TCOOLHRS: %w", err)
	}
	return nil
}

// SetStallThreshold sets the SGTHRS threshold that triggers
// the StallGuard stall detection and raises the DIAG pin.
func (d *Device) SetStallThreshold(threshold int) error {
	if err := d.write(SGTHRS, uint32(threshold)); err != nil {
		return fmt.Errorf("set threshold: set SGTHRS: %w", err)
	}
	return nil
}

func (d *Device) Error() error {
	stat, err := d.read(GSTAT)
	if err != nil {
		return err
	}
	if stat != 0 {
		return fmt.Errorf("tmc2209: error status: %.3b", stat)
	}
	return nil
}

func crc8(data []byte) byte {
	crc := byte(0)
	for _, b := range data {
		for i := 0; i < 8; i++ {
			xor := (crc>>7)^(b&0b1) != 0
			crc <<= 1
			b >>= 1
			if xor {
				crc ^= 0b111
			}
		}
	}
	return crc
}

func (d *Device) read(addr byte) (uint32, error) {
	dg := []byte{
		d.Addr,
		addr,
	}
	rx := make([]byte, 5)
	var lerr error
	for i := 0; i < attempts; i++ {
		if _, err := d.Bus.Write(dg); err != nil {
			lerr = err
			continue
		}
		if _, err := d.Bus.Read(rx); err != nil {
			lerr = err
			continue
		}
		if rx[0] != addr {
			lerr = errors.New("read: unexpected receive address")
			continue
		}
		return uint32(rx[1])<<24 | uint32(rx[2])<<16 | uint32(rx[3])<<8 | uint32(rx[4]), nil
	}
	return 0, lerr
}

func (d *Device) write(addr uint8, val uint32) error {
	ifcnt, err := d.read(IFCNT)
	if err != nil {
		return err
	}
	dg := writeDatagram(d.Addr, addr, val)
	var lerr error
	for i := 0; i < attempts; i++ {
		if _, err := d.Bus.Write(dg[:]); err != nil {
			lerr = err
			continue
		}
		ifcnt2, err := d.read(IFCNT)
		if err != nil {
			lerr = err
			continue
		}
		// Check for write error.
		if uint8(ifcnt2)-uint8(ifcnt) != 1 {
			ifcnt = ifcnt2
			lerr = errors.New("write count not updated")
			continue
		}
		return nil
	}
	return lerr
}

func writeDatagram(node, addr uint8, val uint32) [6]byte {
	const WRITE = 0x80
	return [...]byte{
		node,
		addr | WRITE,
		byte(val >> 24), byte(val >> 16), byte(val >> 8), byte(val),
	}
}
