//go:build tinygo

// package mjolnir2 implements a driver for the particular
// engraving hardware in the Seedhammer II.
package mjolnir2

import (
	"device/rp"
	"errors"
	"fmt"
	"image"
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
	XDiag   machine.Pin
	YDiag   machine.Pin
	// Home is the homing vector, whose direction
	// specifies the direction of the origin,
	// and length specifies the distance before
	// giving up.
	Home image.Point
	// TopSpeed in steps/s.
	TopSpeed int
	// EngravingSpeed in steps/s.
	EngravingSpeed int
	// HomingSpeed in steps/s.
	HomingSpeed int
	// Acceleration in steps/s².
	Acceleration     int
	NeedlePeriod     time.Duration
	NeedleActivation time.Duration

	xnotify, ynotify chan struct{}
	channel          uint8
	buf, buf2        []uint32
}

const (
	pioSM      = 0
	progOffset = 0
	// pioStepsPerWord is the number of pio steps that
	// fit into a 32-bit pio FIFO entry.
	pioStepsPerWord = 32 / mjolnir2pinBits
)

func (d *Device) Configure() error {
	ch, err := dma.Reserve()
	if err != nil {
		return fmt.Errorf("mjolnir2: %w", err)
	}
	d.channel = ch
	d.xnotify = make(chan struct{}, 1)
	d.ynotify = make(chan struct{}, 1)
	const bufSize = 1024
	d.buf = make([]uint32, bufSize)
	d.buf2 = make([]uint32, bufSize)
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

	d.XDiag.Configure(machine.PinConfig{Mode: machine.PinInput})
	d.XDiag.SetInterrupt(machine.PinRising, d.diagInterrupt)
	d.YDiag.Configure(machine.PinConfig{Mode: machine.PinInput})
	d.YDiag.SetInterrupt(machine.PinRising, d.diagInterrupt)
	return nil
}

func (d *Device) diagInterrupt(pin machine.Pin) {
	var stepPin machine.Pin
	var notify chan struct{}
	switch pin {
	case d.XDiag:
		stepPin = pinStepX + d.BasePin
		notify = d.xnotify
	case d.YDiag:
		stepPin = pinStepY + d.BasePin
		notify = d.ynotify
	default:
		return
	}
	// Disconnect the step pin from the PIO program and set it low.
	stepPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	stepPin.Low()
	select {
	case notify <- struct{}{}:
	default:
	}
}

func (d *Device) Engrave(plan engrave.Plan, quit <-chan struct{}) error {
	if err := d.engrave(d.HomingSpeed, quit, true, func(yield func(engrave.Command) bool) {
		yield(engrave.Move(d.Home))
	}); err != nil {
		return err
	}

	return d.engrave(d.TopSpeed, quit, false, plan)
}

func (d *Device) engrave(moveSpeed int, quit <-chan struct{}, homing bool, plan engrave.Plan) error {
	// Clear notifications.
	select {
	case <-d.xnotify:
	default:
	}
	select {
	case <-d.ynotify:
	default:
	}
	pio.ConfigurePins(d.Pio, pioSM, d.BasePin, mjolnir2pinBits)
	pio.Pindirs(d.Pio, pioSM, d.BasePin, mjolnir2pinBits, machine.PinOutput)
	// Reset and start state machine.
	pio.Restart(d.Pio, 0b1<<pioSM)
	pio.Jump(d.Pio, pioSM, progOffset)
	pio.Enable(d.Pio, 0b1<<pioSM)
	defer pio.Disable(d.Pio, 0b1<<pioSM)
	txReg := pio.Tx(d.Pio, pioSM)
	defer func() {
		// Wait for all steps to complete. We can't wait for
		// TX FIFO stalling, because the pio program doesn't stall.
		// Instead, submit a no-op step and wait for empty FIFO.

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

	xdiag, ydiag := false, false
	e := engravingConfig{
		Speed:            moveSpeed,
		EngravingSpeed:   d.EngravingSpeed,
		Acceleration:     d.Acceleration,
		TicksPerSecond:   d.TopSpeed,
		NeedlePeriod:     d.NeedlePeriod,
		NeedleActivation: d.NeedleActivation,
	}.New()
	idx := 0
	stepIdx := 0
	for cmd := range plan {
		e.Command(cmd)
		done := false
		for !done {
			select {
			case <-quit:
				return nil
			case <-d.xnotify:
				if !homing {
					return errors.New("mjolnir2: x-axis stepper driver failed")
				} else if ydiag {
					return nil
				}
				xdiag = true
			case <-d.ynotify:
				if !homing {
					return errors.New("mjolnir2: y-axis stepper driver failed")
				} else if xdiag {
					return nil
				}
				ydiag = true
			default:
			}
		loop:
			for idx < len(d.buf) {
				for stepIdx < pioStepsPerWord {
					step, ok := e.Step()
					if !ok {
						done = true
						break loop
					}
					d.buf[idx] |= uint32(step) << (stepIdx * mjolnir2pinBits)
					stepIdx++
				}
				stepIdx = 0
				idx++
			}
			if done || idx == len(d.buf) {
				// Wait for previous transfer to complete.
				for ch.CTRL_TRIG.Get()&rp.DMA_CH0_CTRL_TRIG_BUSY != 0 {
				}
				// Start buffer transfer.
				ch.READ_ADDR.Set(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(d.buf)))))
				ch.WRITE_ADDR.Set(uint32(uintptr(unsafe.Pointer(pio.Tx(d.Pio, pioSM)))))
				ch.TRANS_COUNT.Set(uint32(len(d.buf[:idx])))
				ch.CTRL_TRIG.Set(
					// Increment read address on each transfer.
					rp.DMA_CH0_CTRL_TRIG_INCR_READ |
						// Transfer 32-bit words.
						rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_SIZE_WORD<<rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_Pos |
						// Don't chain.
						uint32(d.channel)<<rp.DMA_CH0_CTRL_TRIG_CHAIN_TO_Pos |
						// Pace transfers by the pio TX FIFO.
						pio.DreqTx(d.Pio, pioSM)<<rp.DMA_CH0_CTRL_TRIG_TREQ_SEL_Pos |
						// High-priority to minimize stall risk.
						rp.DMA_CH0_CTRL_TRIG_HIGH_PRIORITY |
						// Start transfer.
						rp.DMA_CH0_CTRL_TRIG_EN,
				)
				// Swap buffers.
				d.buf, d.buf2 = d.buf2, d.buf
				clear(d.buf)
				idx = 0
				stepIdx = 0
			}
		}
	}
	if homing {
		return errors.New("mjolnir2: homing timed out")
	}
	// All done without error. Wait for DMA to finish transfer.
	for ch.CTRL_TRIG.Get()&rp.DMA_CH0_CTRL_TRIG_BUSY != 0 {
	}
	return nil
}
