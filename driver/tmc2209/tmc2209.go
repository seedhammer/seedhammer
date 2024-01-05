//go:build tinygo

// Package tmc2209 is a TinyGo implementation for the TMC2209
// stepper motor driver.
package tmc2209

import (
	"errors"
	"fmt"
	"math"
	"time"

	"machine"

	"tinygo.org/x/drivers/delay"
)

// Motor configuration.
const (
	// currentRMS is the motor current in amperes (A).
	currentRMS = 0.9
	// rsense is the sense resistor in ohms (Ω).
	rsense = 0.120
	// vfs is the sense voltage, in volts (V).
	vfs = 0.325
)

// Settings.
const (
	baud = 9600
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
	stepExp = 2
	// Microsteps to a full step.
	Microsteps = 1 << stepExp

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

	// retries is the number of attempts for a read or a write
	// before giving up.
	retries = 10
)

const period = time.Second / baud

type Device struct {
	port uint8
	uart machine.Pin
	step machine.Pin
	dir  machine.Pin
	diag machine.Pin
	// diag is for applying hysteresis to the
	// diag pin.
	hysteresis struct {
		step  bool
		steps int
	}
}

const (
	GCONF      = 0x00
	GSTAT      = 0x01
	IFCNT      = 0x02
	SLAVECONF  = 0x03
	OTP_READ   = 0x05
	IOIN       = 0x06
	IHOLD_IRUN = 0x10
	TCOOLTHRS  = 0x14
	VACTUAL    = 0x22
	SGTHRS     = 0x40
	SG_RESULT  = 0x41
	COOLCONF   = 0x42
	CHOPCONF   = 0x6c
	DRV_STATUS = 0x6f

	// GCONF settings.
	I_scale_analog   = 1 << 0
	pdn_disable      = 1 << 6
	mstep_reg_select = 1 << 7
	multistep_filt   = 1 << 8

	// CHOPCONF settings
	mres_shift = 24
	intpol     = 1 << 28
)

const syncNibble = 0b0101

func New(port uint8, uart, diag, dir, step machine.Pin) (*Device, error) {
	step.Configure(machine.PinConfig{Mode: machine.PinOutput})
	dir.Configure(machine.PinConfig{Mode: machine.PinOutput})
	diag.Configure(machine.PinConfig{Mode: machine.PinInput})
	d := &Device{
		port: port,
		uart: uart,
		step: step,
		dir:  dir,
		diag: diag,
	}
	// Reading from a slave may confuse another until
	// SENDDELAY is raised. Don't read anything until
	// then.
	const min_SENDDELAY = 2
	if err := d.writeNoRead(port, SLAVECONF, min_SENDDELAY<<8, 0); err != nil {
		return nil, err
	}
	gconf, err := d.read(port, GCONF)
	if err != nil {
		return nil, err
	}
	// Disable standstill operation through the UART pin (we're using it for UART).
	gconf |= pdn_disable
	// Enable step resolution setting through MRES.
	gconf |= mstep_reg_select
	// Use IRUN/IHOLD for current setting.
	gconf &^= I_scale_analog
	if err := d.write(port, GCONF, gconf); err != nil {
		return nil, err
	}
	irun := IRUN
	if irun > 31 {
		irun = 31
	}
	// IHOLD is the standstill current, equal to IRUN.
	ihold := irun
	ihold_irun := iholdDelay<<16 | uint32(irun)<<8 | uint32(ihold)
	if err := d.write(port, IHOLD_IRUN, ihold_irun); err != nil {
		return nil, err
	}

	chopconf, err := d.read(port, CHOPCONF)
	if err != nil {
		return nil, err
	}
	// Set microstep resolution.
	chopconf &^= 0b1111 << mres_shift
	chopconf |= (8 - stepExp) << mres_shift
	// Disable step interpolation.
	chopconf &^= intpol
	d.write(port, CHOPCONF, chopconf)

	// Reset GSTAT.
	if err := d.write(port, GSTAT, 0b111); err != nil {
		return nil, err
	}

	// Coolstep interferes with sensorless homing (SGTHRS)
	// and requires tuning. Disable for now.
	if err := d.write(port, TCOOLTHRS, 0xfffff); err != nil {
		return nil, err
	}
	if err := d.StallThreshold(0); err != nil {
		return nil, err
	}

	return d, nil
}

func (d *Device) StallResult() (int, error) {
	res, err := d.read(d.port, SG_RESULT)
	return int(res) / 2, err
}

func (d *Device) StallThreshold(threshold uint8) error {
	return d.write(d.port, SGTHRS, uint32(threshold))
}

func (d *Device) Reset() {
	d.hysteresis.steps = 0
	d.hysteresis.step = false
}

func (d *Device) Error() error {
	stat, err := d.read(d.port, GSTAT)
	if err != nil {
		return err
	}
	if stat != 0 {
		return fmt.Errorf("tmc2209: error status: %.3b", stat)
	}
	return nil
}

func (d *Device) Diag() bool {
	if d.diag.Get() {
		// The DIAG pin is reset once per fullstep.
		const steps = 2
		if d.hysteresis.steps > Microsteps*steps {
			d.hysteresis.steps = 0
			return true
		}
	}
	return false
}

func (d *Device) Step(step bool) {
	d.step.Set(step)
	if step {
		d.hysteresis.steps++
	} else if !d.hysteresis.step {
		// Reset hysteresis when stopped.
		d.hysteresis.steps = 0
	}
	d.hysteresis.step = step
}

func (d *Device) Dir(dir bool) {
	d.dir.Set(dir)
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

func (d *Device) tx(tx []byte) error {
	// Add sync nibble and checksum.
	buf := make([]byte, 8)
	buf = buf[:len(tx)+2]
	buf[0] = syncNibble
	copy(buf[1:], tx)
	buf[len(buf)-1] = crc8(buf[:len(buf)-1])

	d.uart.Configure(machine.PinConfig{Mode: machine.PinOutput})
	d.uart.High()

	delay.Sleep(txWaitCycles * period)
	// Transmit.
	rem := buf
	for len(rem) > 0 {
		// Start bit.
		d.uart.Low()
		delay.Sleep(period)
		for i := 0; i < 8; i++ {
			bit := rem[0]&0b1 == 0b1
			rem[0] >>= 1
			d.uart.Set(bit)
			delay.Sleep(period)
		}
		// Stop bit.
		d.uart.High()
		rem = rem[1:]
		delay.Sleep(period)
	}
	d.uart.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	return nil
}

func (d *Device) rx(rx []byte) error {
	buf := make([]byte, 8)
	buf = buf[:len(rx)+3]
	rem := buf
	now := time.Now()
	// Wait for start bit.
	for d.uart.Get() {
		if time.Since(now) > timeoutCycles*period {
			return errors.New("tmc2209: receive timeout")
		}
	}
	// Shift period a half cycle to sample input
	// in the center of a bit.
	delay.Sleep(period / 2)
	var berr error
	for len(rem) > 0 {
		// Start bit.
		if d.uart.Get() {
			berr = errors.New("tmc2209: received invalid start bit")
		}
		delay.Sleep(period)
		for i := 0; i < 8; i++ {
			rem[0] >>= 1
			if d.uart.Get() {
				rem[0] |= 0x80
			}
			delay.Sleep(period)
		}
		rem = rem[1:]
		// Stop bit.
		if !d.uart.Get() {
			berr = errors.New("tmc2209: received invalid stop bit")
		}
		delay.Sleep(period)
	}
	if berr != nil {
		return berr
	}
	if crc8(buf[:len(buf)-1]) != buf[len(buf)-1] {
		return errors.New("tmc2209: invalid CRC for receive datagram")
	}
	if (buf[0] & 0b1111) != syncNibble {
		return errors.New("tmc2209: invalid sync nibble")
	}
	if buf[1] != 0xff {
		return errors.New("tmc2209: invalid node address")
	}
	copy(rx, buf[2:])

	return nil
}

func (d *Device) read(node, addr byte) (uint32, error) {
	dg := []byte{
		node,
		addr,
	}
	rx := make([]byte, 5)
	var lerr error
	for i := 0; i < retries; i++ {
		if err := d.tx(dg); err != nil {
			lerr = err
			continue
		}
		if err := d.rx(rx); err != nil {
			lerr = err
			continue
		}
		if rx[0] != addr {
			lerr = errors.New("tmc2209: unexpected receive address")
			continue
		}
		return uint32(rx[1])<<24 | uint32(rx[2])<<16 | uint32(rx[3])<<8 | uint32(rx[4]), nil
	}
	return 0, lerr
}

func (d *Device) write(node, addr byte, val uint32) error {
	ifcnt, err := d.read(node, IFCNT)
	if err != nil {
		return err
	}
	return d.writeNoRead(node, addr, val, ifcnt)
}

// writeFirst is like write, except that it won't attempt to
// read IFCNT before writing.
func (d *Device) writeNoRead(node, addr byte, val uint32, ifcnt uint32) error {
	const WRITE = 0x80
	dg := []byte{
		node,
		addr | WRITE,
		byte(val >> 24), byte(val >> 16), byte(val >> 8), byte(val),
	}
	var lerr error
	for i := 0; i < retries; i++ {
		if err := d.tx(dg); err != nil {
			lerr = err
			continue
		}
		ifcnt2, err := d.read(node, IFCNT)
		if err != nil {
			lerr = err
			continue
		}
		// Check for write error.
		if uint8(ifcnt2)-uint8(ifcnt) != 1 {
			ifcnt = ifcnt2
			lerr = errors.New("tmc2209: write error")
			continue
		}
		return nil
	}
	return lerr
}
