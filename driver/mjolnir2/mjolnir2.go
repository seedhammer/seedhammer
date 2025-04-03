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
	driver           engravingDriver
}

const (
	pioSM      = 0
	progOffset = 0
	// pioStepsPerWord is the number of pio steps that
	// fit into a 32-bit pio FIFO entry.
	pioStepsPerWord = 32 / mjolnir2pinBits
)

// engravingDriver drives an engraving through interrupts
// and DMA transfers.
type engravingDriver struct {
	engraving engraving
	channel   uint8
	commands  chan engrave.Command
	stall     chan struct{}
	buf, buf2 []uint32
	idx       int
	stepIdx   int
	eof       bool
}

func (d *Device) Configure() error {
	ch, err := dma.Reserve()
	if err != nil {
		return fmt.Errorf("mjolnir2: %w", err)
	}
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
	// frequency, but also increase interrupt latency because
	// buffers are filled in the interrupt handler.
	const bufSize = 256
	d.driver = engravingDriver{
		channel:  ch,
		buf:      make([]uint32, bufSize),
		buf2:     make([]uint32, bufSize),
		commands: make(chan engrave.Command, 64),
		stall:    make(chan struct{}, 1),
	}
	d.xnotify = make(chan struct{}, 1)
	d.ynotify = make(chan struct{}, 1)
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

func (e *engravingDriver) Reset(eng engraving) {
	e.engraving = eng
	e.idx = 0
	e.stepIdx = 0
	e.eof = true
	clear(e.buf)
}

func (e *engravingDriver) handleInterrupt() {
	dma.ClearForceInterrupt(e.channel)
	// The buffer may not be full when starting or restarting
	// after a stall.
	e.fillBuffer()
	// If there's nothing to flush, we're stalled. It's ok
	// to stall here because the engraver is at standstill
	// between commands.
	if !e.flush() {
		select {
		case e.stall <- struct{}{}:
		default:
		}
		return
	}
	// Fill buffer in the interrupt handler to avoid stalling
	// the engraver while moving because of unfortunate goroutine
	// scheduling.
	e.fillBuffer()
}

func (e *engravingDriver) fillBuffer() {
	for e.idx < len(e.buf) {
		if e.eof {
			// Fetch next command.
			select {
			case cmd := <-e.commands:
				e.engraving.Command(cmd)
				e.eof = false
			default:
				return
			}
		}
		// Fill buffer.
		for e.idx < len(e.buf) {
			for e.stepIdx < pioStepsPerWord {
				step, ok := e.engraving.Step()
				if !ok {
					e.eof = true
					return
				}
				e.buf[e.idx] |= uint32(step) << (e.stepIdx * mjolnir2pinBits)
				e.stepIdx++
			}
			e.stepIdx = 0
			e.idx++
		}
	}
}

func (e *engravingDriver) flush() bool {
	idx := e.idx
	// Round buffer size up to include any partly filled word.
	if e.stepIdx > 0 {
		idx++
	}
	if idx == 0 {
		return false
	}
	ch := dma.ChannelAt(e.channel)
	ch.READ_ADDR.Set(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(e.buf)))))
	ch.TRANS_COUNT.Set(uint32(len(e.buf[:idx])))
	ch.CTRL_TRIG.SetBits(rp.DMA_CH0_CTRL_TRIG_EN)
	// Swap buffers.
	e.buf, e.buf2 = e.buf2, e.buf
	clear(e.buf)
	e.idx = 0
	e.stepIdx = 0
	return true
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
	// Wait for all steps to complete. We can't wait for
	// TX FIFO stalling, because the pio program doesn't stall.
	// Instead, submit a no-op step and wait for empty FIFO.
	defer func() {
		pio.WaitTxNotFull(d.Pio, 0b1<<pioSM)
		txReg.Set(noop)
		pio.WaitTxEmpty(d.Pio, 0b1<<pioSM)
	}()
	ch := dma.ChannelAt(d.driver.channel)
	// Abort any pending transfer.
	defer func() {
		ch.CTRL_TRIG.ClearBits(rp.DMA_CH0_CTRL_TRIG_EN)
		rp.DMA.CHAN_ABORT.Set(0b1 << d.driver.channel)
		for rp.DMA.CHAN_ABORT.Get() != 0 {
		}
	}()

	xdiag, ydiag := false, false
	dd := &d.driver
	dd.Reset(engravingConfig{
		Speed:            moveSpeed,
		EngravingSpeed:   d.EngravingSpeed,
		Acceleration:     d.Acceleration,
		TicksPerSecond:   d.TopSpeed,
		NeedlePeriod:     d.NeedlePeriod,
		NeedleActivation: d.NeedleActivation,
	}.New())
	if err := dma.SetInterrupt(dd.channel, dd.handleInterrupt); err != nil {
		return fmt.Errorf("mjolnir2: engrave: %w", err)
	}
	defer dma.SetInterrupt(dd.channel, nil)
	stalled := true
cmds:
	for cmd := range plan {
	loop:
		for {
			select {
			case <-quit:
				break cmds
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
			case <-dd.stall:
				stalled = true
			case dd.commands <- cmd:
				if stalled {
					// (Re-)start driver.
					stalled = false
					dma.ForceInterrupt(d.driver.channel)
				}
				break loop
			}
		}
	}
	for {
		if stalled {
			if homing {
				return errors.New("mjolnir2: homing timed out")
			}
			return nil
		}
		select {
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
		case <-dd.stall:
			stalled = true
		}
	}
}
