//go:build tinygo && rp

// package mjolnir2 implements a driver for the particular
// engraving hardware in the Seedhammer II.
package mjolnir2

import (
	"device/rp"
	"fmt"
	"machine"
	"runtime"
	"unsafe"

	"seedhammer.com/driver/dma"
	"seedhammer.com/driver/pio"
)

type Device struct {
	Pio            *rp.PIO0_Type
	BasePin        machine.Pin
	TicksPerSecond uint

	// Needle period and activation in ticks.
	needlePeriod uint
	needleAct    uint
	// Needle period tick.
	tneedle   uint
	channel   dma.ChannelID
	buf, buf2 []uint32
}

const (
	pioSM      = 0
	progOffset = 0
	// No-op is the pio step that clears every pin
	// and stops the needle.
	noop = 0b00000
)

const (
	// Pin offsets from base pin.
	pinDirY = iota
	pinDirX
	pinNeedle
	pinStepY
	pinStepX
)

const (
	pinBits = 5
	// stepsPerWord is the number of pio steps that
	// fit into a 32-bit pio FIFO entry.
	stepsPerWord = 32 / pinBits
)

func (d *Device) Configure(dmaBufSize int) error {
	ch, err := dma.Reserve()
	if err != nil {
		return fmt.Errorf("mjolnir2: %w", err)
	}
	d.channel = ch
	dmaChan := dma.ChannelAt(ch)
	// Set DMA destination to pio TX FIFO.
	dmaChan.WRITE_ADDR.Set(uint32(uintptr(unsafe.Pointer(pio.Tx(d.Pio, pioSM)))))
	// Configure channel.
	dmaChan.CTRL_TRIG.Set(
		// Increment read address on each transfer.
		rp.DMA_CH0_CTRL_TRIG_INCR_READ |
			// Transfer 32-bit words.
			rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_SIZE_WORD<<rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_Pos |
			// Don't chain.
			uint32(ch)<<rp.DMA_CH0_CTRL_TRIG_CHAIN_TO_Pos |
			// Pace transfers by the pio TX FIFO.
			pio.DreqTx(d.Pio, pioSM)<<rp.DMA_CH0_CTRL_TRIG_TREQ_SEL_Pos |
			// High-priority to minimize stall risk.
			rp.DMA_CH0_CTRL_TRIG_HIGH_PRIORITY,
	)
	conf := mjolnir2ProgramDefaultConfig(progOffset)
	conf.SidesetBase = uint8(pinStepY + d.BasePin)
	conf.OutBase = uint8(pinDirY + d.BasePin)
	conf.OutCount = mjolnir2pinBits
	conf.FIFOMode = pio.FIFOJoinTX
	conf.PullThreshold = mjolnir2pinBits * stepsPerWord
	conf.Autopull = true
	conf.Freq = uint32(d.TicksPerSecond) * pioCyclesPerStep
	pio.Configure(d.Pio, pioSM, conf.Build())
	pio.Program(d.Pio, progOffset, mjolnir2Instructions)
	// Register x must be cleared for stall to disable all pins.
	pio.Instr(d.Pio, pioSM).Set(clearXInst)
	d.buf = make([]uint32, dmaBufSize)
	d.buf2 = make([]uint32, dmaBufSize)
	return nil
}

func (d *Device) Enable(transferCompleted func(), needleActivation, needlePeriod uint) error {
	if err := dma.SetInterrupt(d.channel, transferCompleted); err != nil {
		return fmt.Errorf("mjolnir2: %w", err)
	}
	d.needleAct = needleActivation
	d.needlePeriod = needlePeriod
	pio.ConfigurePins(d.Pio, pioSM, d.BasePin, mjolnir2pinBits)
	pio.Pindirs(d.Pio, pioSM, d.BasePin, mjolnir2pinBits, machine.PinOutput)
	// Reset and start state machine.
	pio.Restart(d.Pio, 0b1<<pioSM)
	pio.Jump(d.Pio, pioSM, progOffset)
	pio.Enable(d.Pio, 0b1<<pioSM)
	return nil
}

func (d *Device) Disable() {
	dma.SetInterrupt(d.channel, nil)
	ch := dma.ChannelAt(d.channel)
	// Abort any pending transfer.
	ch.CTRL_TRIG.ClearBits(rp.DMA_CH0_CTRL_TRIG_EN)
	rp.DMA.CHAN_ABORT.Set(0b1 << d.channel)
	for rp.DMA.CHAN_ABORT.Get() != 0 {
	}
	// Keep the DMA buffers alive.
	runtime.KeepAlive(d.buf)
	runtime.KeepAlive(d.buf2)

	// Wait for all steps to complete. We can't wait for
	// TX FIFO stalling, because the pio program doesn't stall.
	// Instead, submit a no-op step and wait for empty FIFO.
	txReg := pio.Tx(d.Pio, pioSM)
	pio.WaitTxNotFull(d.Pio, 0b1<<pioSM)
	txReg.Set(noop)

	pio.WaitTxEmpty(d.Pio, 0b1<<pioSM)
	pio.Disable(d.Pio, 0b1<<pioSM)
}

func (d *Device) NextBuffer() []uint32 {
	return d.buf2
}

func (d *Device) Transfer(steps int) {
	if steps == 0 {
		return
	}
	// Compute number of words.
	n := (steps + stepsPerWord - 1) / stepsPerWord
	buf := d.buf[:n]
	// Modulate the needle enable bit with the
	// needle waveform.
	for i, w := range buf {
		for j := range stepsPerWord {
			if d.tneedle >= d.needleAct {
				bit := j*pinBits + pinNeedle
				w &^= 0b1 << bit
			}
			// Advance cycle.
			d.tneedle = (d.tneedle + 1) % d.needlePeriod
		}
		buf[i] = w
	}
	ch := dma.ChannelAt(d.channel)
	ch.READ_ADDR.Set(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(buf)))))
	ch.TRANS_COUNT.Set(uint32(len(buf)))
	ch.CTRL_TRIG.SetBits(rp.DMA_CH0_CTRL_TRIG_EN)
	// Swap buffers.
	d.buf, d.buf2 = d.buf2, d.buf
}
