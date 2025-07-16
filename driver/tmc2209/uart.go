//go:build tinygo && rp

package tmc2209

import (
	"device/rp"
	"encoding/binary"
	"errors"
	"machine"
	"runtime"
	"time"

	"seedhammer.com/driver/pio"
)

// UART implements a driver for the 1-pin UART interface
// of the tmc2209.
type UART struct {
	pio     *rp.PIO0_Type
	pin     machine.Pin
	progOff uint8
	scratch [8]byte
}

const syncNibble = 0b0101

const (
	pioSM = 0

	// The number of cycles to wait for the reply of
	// a read request.
	timeoutCycles = 100
	// txWaitCycles is the number of cycles to wait
	// before transmitting to the driver. The manual
	// specifies 4 cycles for the switch from input to
	// output and 63 cycles for resetting the automatic
	// baud detection, plus 12 cycles for recovery.
	txWaitCycles = max(4, 63+12) + 1
	baud         = 57600
	period       = time.Second / baud
	// Squeeze in 2 bytes per FIFO slot, to ensure all
	// transactions fit in the FIFO.
	rxBytesPerFIFO = 2
)

func NewUART(p *rp.PIO0_Type, pin machine.Pin) (*UART, error) {
	d := &UART{
		pio: p,
		pin: pin,
	}
	// The target PIO clock speed.
	const pioClock = baud * cyclesPerBit

	pin.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
	pio.ConfigurePins(p, pioSM, pin, 1)

	d.progOff = uint8(0)
	conf := uartProgramDefaultConfig(d.progOff)
	conf.InBase = uint8(pin)
	conf.InCount = 1
	conf.OutBase = uint8(pin)
	conf.OutCount = 1
	conf.JumpPin = uint8(pin)
	conf.SidesetBase = uint8(pin)
	conf.SetBase = uint8(pin)
	conf.SetCount = 1
	conf.Freq = pioClock
	conf.Autopush = true
	conf.PushThreshold = rxBytesPerFIFO * 8
	pio.Program(d.pio, d.progOff, uartInstructions)
	pio.Configure(d.pio, pioSM, conf.Build())

	return d, nil
}

func (d *UART) Write(tx []byte) (int, error) {
	// Add sync nibble and checksum.
	buf := d.scratch[:8]
	buf = buf[:len(tx)+2]
	buf[0] = syncNibble
	copy(buf[1:], tx)
	buf[len(buf)-1] = crc8(buf[:len(buf)-1])

	time.Sleep(txWaitCycles * period)
	pio.Pindirs(d.pio, pioSM, d.pin, 1, machine.PinOutput)
	// Load register y with the transaction size.
	n := len(buf)
	if n > 0b11111 {
		panic("tx too large")
	}
	pio.Disable(d.pio, 0b1<<pioSM)
	pio.Restart(d.pio, 0b1<<pioSM)
	pio.Jump(d.pio, pioSM, d.progOff+uartoffset_transmit)
	pio.Instr(d.pio, pioSM).Set(setYInst | uint32(n))
	irq := &d.pio.IRQ
	irq.SetBits(0b1<<uartErrIRQ | 0b1<<uartRxIRQ)
	pio.Enable(d.pio, 0b1<<pioSM)
	// Fill FIFO.
	txReg := pio.Tx(d.pio, pioSM)
	for _, b := range buf {
		pio.WaitTxNotFull(d.pio, 0b1<<pioSM)
		txReg.Set(uint32(b))
	}
	// Wait for transmit to complete.
	for !irq.HasBits(0b1 << uartRxIRQ) {
		runtime.Gosched()
	}

	return len(tx), nil
}

func (d *UART) Read(rx []byte) (int, error) {
	defer pio.Disable(d.pio, 0b1<<pioSM)
	buf := d.scratch[:8]
	buf = buf[:len(rx)+3]
	rem := buf
	if len(rem)%rxBytesPerFIFO != 0 {
		panic("uneven receive length")
	}
	rxReg := pio.Rx(d.pio, pioSM)
	fstatReg := &d.pio.FSTAT
	// Every data byte includes a start and a stop bit.
	const cyclesPerByte = cyclesPerBit + 1 + 1
	deadline := time.Duration(timeoutCycles+cyclesPerByte*len(rem)) * period
	now := time.Now()
	irq := &d.pio.IRQ
	for len(rem) > 0 {
		if time.Since(now) > deadline {
			return 0, errors.New("rx: receive timeout")
		}
		if irq.HasBits(0b1 << uartErrIRQ) {
			return 0, errors.New("rx: read error")
		}
		rxempty := fstatReg.Get() & rp.PIO0_FSTAT_RXEMPTY_Msk >> rp.PIO0_FSTAT_RXEMPTY_Pos
		if rxempty&(0b1<<pioSM) == 0 {
			w16 := uint16(rxReg.Get() >> (rxBytesPerFIFO * 8))
			binary.LittleEndian.PutUint16(rem, w16)
			rem = rem[rxBytesPerFIFO:]
		}
	}
	if crc8(buf[:len(buf)-1]) != buf[len(buf)-1] {
		return 0, errors.New("rx: invalid CRC for receive datagram")
	}
	if (buf[0] & 0b1111) != syncNibble {
		return 0, errors.New("rx: invalid sync nibble")
	}
	if buf[1] != 0xff {
		return 0, errors.New("rx: invalid node address")
	}
	copy(rx, buf[2:])
	return len(rx), nil
}
