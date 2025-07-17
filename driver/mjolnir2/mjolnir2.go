//go:build tinygo

// package mjolnir2 implements a driver for the particular
// engraving hardware in the Seedhammer II.
package mjolnir2

import (
	"device/rp"
	"fmt"
	"machine"
	"time"
	"unsafe"

	"seedhammer.com/driver/dma"
	"seedhammer.com/driver/pio"
	"seedhammer.com/engrave"
)

type Device struct {
	Pio     *rp.PIO0_Type
	BasePin machine.Pin
	// TopSpeed in steps/s.
	TopSpeed int
	// EngravingSpeed in steps/s.
	EngravingSpeed int
	// HomingSpeed in steps/s.
	HomingSpeed int
	// Acceleration in steps/s².
	Acceleration int
	NeedlePeriod time.Duration

	channel dma.ChannelID
	homing  bool
	diag    chan axis
	driver  engravingDriver
}

type DiagPin int

const (
	XDiag DiagPin = iota
	YDiag
)

const (
	pioSM      = 0
	progOffset = 0
)

func (d *Device) Configure() error {
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
	// bufSize is a compromise: larger buffers decrease interrupt
	// frequency at the cost of longer interrupt pauses because
	// buffers are filled in the interrupt handler.
	const bufSize = 256
	d.driver = engravingDriver{
		buf:  make([]uint32, bufSize),
		buf2: make([]uint32, bufSize),
	}
	conf := mjolnir2ProgramDefaultConfig(progOffset)
	conf.SidesetBase = uint8(pinStepY + d.BasePin)
	conf.OutBase = uint8(pinDirY + d.BasePin)
	conf.OutCount = mjolnir2pinBits
	conf.FIFOMode = pio.FIFOJoinTX
	conf.PullThreshold = mjolnir2pinBits * pioStepsPerWord
	conf.Autopull = true
	conf.Freq = uint32(d.TopSpeed) * pioCyclesPerStep
	pio.Configure(d.Pio, pioSM, conf.Build())
	pio.Program(d.Pio, progOffset, mjolnir2Instructions)
	// Register x must be cleared for stall to disable all pins.
	pio.Instr(d.Pio, pioSM).Set(clearXInst)
	return nil
}

// DiagInterrupt should be called when a stepper diagnostics pin is
// raised. It can be called from an interrupt.
func (d *Device) DiagInterrupt(pin DiagPin) {
	var stepPin, otherPin machine.Pin
	var a axis
	switch pin {
	case XDiag:
		stepPin = pinStepX + d.BasePin
		otherPin = pinStepY + d.BasePin
		a = xaxis
	case YDiag:
		stepPin = pinStepY + d.BasePin
		otherPin = pinStepX + d.BasePin
		a = yaxis
	default:
		return
	}
	// Disconnect the step pin from the PIO program and set it low.
	stepPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	stepPin.Low()
	if !d.homing {
		// Disable both axes in case of blockage.
		otherPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
		otherPin.Low()
	}
	select {
	case d.diag <- a:
	default:
	}
}

func (d *Device) Engrave(needleActivation time.Duration, homing bool, plan engrave.Plan, quit <-chan struct{}) error {
	moveSpeed := d.TopSpeed
	if homing {
		moveSpeed = d.HomingSpeed
	}
	pio.ConfigurePins(d.Pio, pioSM, d.BasePin, mjolnir2pinBits)
	pio.Pindirs(d.Pio, pioSM, d.BasePin, mjolnir2pinBits, machine.PinOutput)
	// Reset and start state machine.
	pio.Restart(d.Pio, 0b1<<pioSM)
	pio.Jump(d.Pio, pioSM, progOffset)
	pio.Enable(d.Pio, 0b1<<pioSM)
	defer pio.Disable(d.Pio, 0b1<<pioSM)
	txReg := pio.Tx(d.Pio, pioSM)
	// Wait for all steps to complete. We can't wait for
	// TX FIFO stalling, because the pio program doesn't stall.
	// Instead, submit a no-op step and wait for empty FIFO.
	defer func() {
		pio.WaitTxNotFull(d.Pio, 0b1<<pioSM)
		txReg.Set(noop)
		pio.WaitTxEmpty(d.Pio, 0b1<<pioSM)
	}()
	ch := dma.ChannelAt(d.channel)
	// Abort any pending transfer.
	defer func() {
		ch.CTRL_TRIG.ClearBits(rp.DMA_CH0_CTRL_TRIG_EN)
		rp.DMA.CHAN_ABORT.Set(0b1 << d.channel)
		for rp.DMA.CHAN_ABORT.Get() != 0 {
		}
	}()

	transfer := func(buf []uint32) {
		ch.READ_ADDR.Set(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(buf)))))
		ch.TRANS_COUNT.Set(uint32(len(buf)))
		ch.CTRL_TRIG.SetBits(rp.DMA_CH0_CTRL_TRIG_EN)
	}
	d.diag = make(chan axis, 1)
	d.homing = homing
	dd := &d.driver
	if err := dma.SetInterrupt(d.channel, dd.handleTransferCompleted); err != nil {
		return fmt.Errorf("mjolnir2: engrave: %w", err)
	}
	defer dma.SetInterrupt(d.channel, nil)
	conf := engravingConfig{
		Speed:            moveSpeed,
		EngravingSpeed:   d.EngravingSpeed,
		Acceleration:     d.Acceleration,
		TicksPerSecond:   d.TopSpeed,
		NeedlePeriod:     d.NeedlePeriod,
		NeedleActivation: needleActivation,
	}.New()
	return dd.engrave(transfer, d.diag, conf, quit, homing, plan)
}
