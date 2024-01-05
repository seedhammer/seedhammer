// Package tmc2209 is a TinyGo implementation for the TMC2209
// stepper motor driver.
package tmc2209

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// Motor configuration.
const (
	// vfs is the sense voltage, in volts (V).
	vfs = 0.325
)

// Settings.
const (
	// 2^stepExp is the number of microsteps to a full step.
	stepExp = 8
	// Microsteps to a full step.
	Microsteps = 1 << stepExp
	// StandstillTuningPeriod is the minimum duration
	// the driver should be kept at full power in standstill
	// after enabling the motor.
	StandstillTuningPeriod = 130 * time.Millisecond

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
	// Sense is the sense resistance in milliohm (mΩ).
	Sense   int
	scratch [7]byte
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
	attempts = 1
)

// SetupSharedUART a stepper driver by increasing its SENDDELAY, to
// avoid cross talk when multiple drivers share UART pin.
func (d *Device) SetupSharedUART() error {
	// Reading from a slave may confuse another until
	// SENDDELAY is raised. Don't read anything until
	// then.
	wr := d.scratch[:6]
	writeDatagram(wr, d.Addr, SLAVECONF, min_SENDDELAY<<8)
	var lerr error
	for range attempts {
		if _, err := d.Bus.Write(wr); err != nil {
			lerr = err
		}
	}
	return lerr
}

func (d *Device) Configure() error {
	if d.Sense == 0 {
		return errors.New("invalid configuration")
	}

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
	if err := d.Enable(0); err != nil {
		return err
	}

	// Reset GSTAT.
	if err := d.write(GSTAT, 0b111); err != nil {
		return fmt.Errorf("tmc2209: set GSTAT: %w", err)
	}

	return nil
}

// Enable the driver and set the driving current in mA.
// Setting the current to 0 disables it.
func (d *Device) Enable(current int) error {
	irun := computeIRUN(current, d.Sense)
	// IHOLD is the standstill current, equal to IRUN.
	ihold := irun
	ihold_irun := iholdDelay<<16 | uint32(irun)<<8 | uint32(ihold)
	if err := d.write(IHOLD_IRUN, ihold_irun); err != nil {
		return fmt.Errorf("tmc2209: set IHOLD/IRUN: %w", err)
	}

	chopconf, err := d.read(CHOPCONF)
	if err != nil {
		return fmt.Errorf("tmc2209: enable: %w", err)
	}
	// Set microstep resolution.
	chopconf &^= 0b1111 << mres_shift
	chopconf |= (8 - stepExp) << mres_shift
	// Disable step interpolation.
	chopconf &^= intpol
	// Stash TOFF, and set it to zero to disable the driver.
	const toffMask = 0b1111
	const toff = 3
	if current > 0 {
		chopconf |= toff
	} else {
		chopconf &^= toffMask
	}
	if err := d.write(CHOPCONF, chopconf); err != nil {
		return fmt.Errorf("tmc2209: enable: %w", err)
	}
	return nil
}

func (d *Device) PWMAuto() (int, error) {
	res, err := d.read(PWM_AUTO)
	return int(res), err
}

func (d *Device) Load() (int, error) {
	res, err := d.read(SG_RESULT)
	return 255 - int(res/2), err
}

func (d *Device) StepDuration() (time.Duration, error) {
	res, err := d.read(TSTEP)
	dt := time.Duration(res) * 256 * time.Second / (Microsteps * fclk)
	return dt, err
}

// SetStallMinimumVelocity sets the minimum velocity in
// steps/second for detecting stalls.
func (d *Device) SetMinimumStallVelocity(stepsPerSecond int) error {
	// tcoolThrs is the TCOOLTHRS value for the stall guard velocity.
	// It is represented in time in clock cycles between each microstep
	// at maximum resolution (256).
	const scale = 256 / Microsteps
	tcoolThrs := fclk / (stepsPerSecond * scale)
	tcoolThrs = min(tcoolThrs, 0xfffff)
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

func (d *Device) read(addr byte) (uint32, error) {
	wr, rx := d.scratch[:2], d.scratch[2:7]
	wr[0] = d.Addr
	wr[1] = addr
	var lerr error
	for range attempts {
		if _, err := d.Bus.Write(wr); err != nil {
			lerr = fmt.Errorf("write: %v", err)
			continue
		}
		if _, err := d.Bus.Read(rx); err != nil {
			lerr = fmt.Errorf("read: %v", err)
			continue
		}
		if rx[0] != addr {
			lerr = errors.New("read: unexpected receive address")
			continue
		}
		return binary.BigEndian.Uint32(rx[1:]), nil
	}
	return 0, lerr
}

func (d *Device) write(addr uint8, val uint32) error {
	ifcnt, err := d.read(IFCNT)
	if err != nil {
		return err
	}
	wr := d.scratch[:6]
	writeDatagram(wr, d.Addr, addr, val)
	var lerr error
	for range attempts {
		if _, err := d.Bus.Write(wr); err != nil {
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

func writeDatagram(b []byte, node, addr uint8, val uint32) {
	const WRITE = 0x80
	b[0] = node
	b[1] = addr | WRITE
	binary.BigEndian.PutUint32(b[2:], val)
}

// computeIRUN from motor current (in mA) and sense resistance (in mΩ).
func computeIRUN(current, sense int) byte {
	// The formula from the reference manual is
	//
	//  Irms = ((CS+1)/32) * (Vfs/(Rsense+20mΩ)) * (1/√2).
	//
	// Solving for CS,
	//
	//  CS = 32*Irms*√2*(Rsense+20mΩ)/Vfs - 1
	irun := 32*float64(current)/1000*math.Sqrt2*(float64(sense)/1000+.02)/vfs - 1
	irun = min(31, irun)
	return byte(max(0, irun))
}
