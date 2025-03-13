//go:build tinygo && rp

package tmc2209

import (
	"device/rp"
	"errors"
	"machine"
	"time"

	"seedhammer.com/driver/pio"
)

// UART implements a driver for the 1-pin UART interface
// of the tmc2209.
type UART struct {
	pio    *rp.PIO0_Type
	pin    machine.Pin
	rxConf pio.ConfigRegs
	txConf pio.ConfigRegs
}

const syncNibble = 0b0101

const (
	pioSM = 0

	baud   = 57600
	period = time.Second / baud
)

func NewUART(p *rp.PIO0_Type, pin machine.Pin) (*UART, error) {
	pio.ConfigurePins(p, pioSM, pin, 1)
	d := &UART{
		pio: p,
		pin: pin,
	}
	// The target PIO clock speed.
	const pioClock = baud * cyclesPerBit

	progOff := uint8(0)
	// Set up RX machine.
	rxConf := uart_rxProgramDefaultConfig(progOff)
	rxConf.InBase = pin
	rxConf.InCount = 1
	rxConf.JumpPin = pin
	rxConf.FIFOMode = pio.FIFOJoinRX
	rxConf.Freq = pioClock
	d.rxConf = rxConf.Build()
	pio.Program(d.pio, progOff, uart_rxInstructions)
	progOff += uint8(len(uart_rxInstructions))

	// Set up TX machine.
	txConf := uart_txProgramDefaultConfig(progOff)
	txConf.OutBase = pin
	txConf.OutCount = 1
	txConf.SidesetBase = pin
	txConf.FIFOMode = pio.FIFOJoinTX
	txConf.Freq = pioClock
	d.txConf = txConf.Build()
	pio.Program(d.pio, progOff, uart_txInstructions)
	progOff += uint8(len(uart_txInstructions))
	return d, nil
}

func (d *UART) Write(tx []byte) (int, error) {
	// Add sync nibble and checksum.
	buf := make([]byte, 8)
	buf = buf[:len(tx)+2]
	buf[0] = syncNibble
	copy(buf[1:], tx)
	buf[len(buf)-1] = crc8(buf[:len(buf)-1])

	pio.Configure(d.pio, pioSM, d.txConf)
	time.Sleep(txWaitCycles * period)
	// Set UART pin direction.
	pio.Pindirs(d.pio, pioSM, d.pin, 1, machine.PinOutput)
	defer pio.Pindirs(d.pio, pioSM, d.pin, 1, machine.PinInputPullup)
	pio.Enable(d.pio, 0b1<<pioSM)
	defer pio.Disable(d.pio, 0b1<<pioSM)
	// Fill FIFO with transfer.
	txReg := pio.Tx(d.pio, pioSM)
	for _, b := range buf {
		txReg.Set(uint32(b))
	}

	// Wait for completion.
	pio.WaitTXStall(d.pio, 0b1<<pioSM)
	return len(tx), nil
}

func (d *UART) Read(rx []byte) (int, error) {
	irq := &d.pio.IRQ
	irq.ClearBits(0b1 << uart_rxErrIRQ)
	pio.Configure(d.pio, pioSM, d.rxConf)
	pio.Enable(d.pio, 0b1<<pioSM)
	defer pio.Disable(d.pio, 0b1<<pioSM)
	buf := make([]byte, 8)
	buf = buf[:len(rx)+3]
	rem := buf
	rxReg := pio.Rx(d.pio, pioSM)
	fstatReg := &d.pio.FSTAT
	// Every data byte includes a start and a stop bit.
	const cyclesPerByte = 8 + 1 + 1
	deadline := time.Duration(timeoutCycles+cyclesPerByte*len(rem)) * period
	now := time.Now()
	for len(rem) > 0 {
		if time.Since(now) > deadline {
			return 0, errors.New("rx: receive timeout")
		}
		if irq.HasBits(0b1 << uart_rxErrIRQ) {
			return 0, errors.New("rx: read error")
		}
		rxempty := fstatReg.Get() & rp.PIO0_FSTAT_RXEMPTY_Msk >> rp.PIO0_FSTAT_RXEMPTY_Pos
		if rxempty&(0b1<<pioSM) == 0 {
			rem[0] = uint8(rxReg.Get() >> 24)
			rem = rem[1:]
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
