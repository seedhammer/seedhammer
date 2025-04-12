//go:build tinygo

// package mjolnir2 implements a driver for the particular
// engraving hardware in the Seedhammer II.
package mjolnir2

import (
	"device/rp"
	"errors"
	"fmt"
	"image"
	"iter"
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

	driver engravingDriver
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
	xdiag     machine.Pin
	ydiag     machine.Pin
	basePin   machine.Pin
	engraving engraving
	channel   dma.ChannelID
	commands  chan engrave.Command
	stall     chan struct{}
	diag      chan axis
	homing    bool
	buf, buf2 []uint32
	idx       int
	eof       bool
}

type axis uint8

const (
	xaxis axis = 0b1 << iota
	yaxis
)

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
	// frequency at the cost of longer interrupt pauses because
	// buffers are filled in the interrupt handler.
	const bufSize = 256
	d.driver = engravingDriver{
		channel: ch,
		buf:     make([]uint32, bufSize),
		buf2:    make([]uint32, bufSize),
		basePin: d.BasePin,
		xdiag:   d.XDiag,
		ydiag:   d.YDiag,
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

	d.XDiag.Configure(machine.PinConfig{Mode: machine.PinInput})
	d.YDiag.Configure(machine.PinConfig{Mode: machine.PinInput})
	return nil
}

func (e *engravingDriver) Reset(homing bool, eng engraving) {
	const bufSize = 64
	e.commands = make(chan engrave.Command, bufSize)
	e.stall = make(chan struct{}, 1)
	e.diag = make(chan axis, 1)
	e.homing = homing
	e.engraving = eng
	e.eof = true
	e.idx = 0
}

func (e *engravingDriver) handleDMA() {
	if e.empty() {
		// If there's nothing to flush, we're stalled. Stalling
		// is acceptable because the engraver is at standstill
		// between commands.
		select {
		case e.stall <- struct{}{}:
		default:
		}
		return
	}
	e.setDMA()
	e.startDMA()
	// Fill buffer here in the interrupt handler to avoid stalling
	// the engraver because of unfortunate goroutine scheduling.
	e.fillBuffer()
}

func (e *engravingDriver) fillBuffer() {
outer:
	for !e.full() {
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
		step, ok := e.engraving.Step()
		if !ok {
			e.eof = true
			continue outer
		}
		idx := e.idx / pioStepsPerWord
		stepIdx := e.idx % pioStepsPerWord
		w := e.buf[idx]
		if stepIdx == 0 {
			w = 0
		}
		w |= uint32(step) << (stepIdx * mjolnir2pinBits)
		e.buf[idx] = w
		e.idx++
	}
}

func (e *engravingDriver) full() bool {
	return e.idx == len(e.buf)*pioStepsPerWord
}

func (e *engravingDriver) empty() bool {
	return e.idx == 0
}

func (e *engravingDriver) setDMA() {
	// Round buffer size up to include any partly filled word.
	n := (e.idx + pioStepsPerWord - 1) / pioStepsPerWord
	ch := dma.ChannelAt(e.channel)
	ch.READ_ADDR.Set(uint32(uintptr(unsafe.Pointer(unsafe.SliceData(e.buf)))))
	ch.TRANS_COUNT.Set(uint32(len(e.buf[:n])))
	// Swap buffers.
	e.buf, e.buf2 = e.buf2, e.buf
	e.idx = 0
}

func (e *engravingDriver) startDMA() {
	ch := dma.ChannelAt(e.channel)
	ch.CTRL_TRIG.SetBits(rp.DMA_CH0_CTRL_TRIG_EN)
}

func (e *engravingDriver) handleDiag(pin machine.Pin) {
	var stepPin, otherPin machine.Pin
	var a axis
	switch pin {
	case e.xdiag:
		stepPin = pinStepX + e.basePin
		otherPin = pinStepY + e.basePin
		a = xaxis
	case e.ydiag:
		stepPin = pinStepY + e.basePin
		otherPin = pinStepX + e.basePin
		a = yaxis
	default:
		return
	}
	// Disconnect the step pin from the PIO program and set it low.
	stepPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	stepPin.Low()
	if !e.homing {
		// Disable both axes in case of blockage.
		otherPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
		otherPin.Low()
	}
	select {
	case e.diag <- a:
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

	dd := &d.driver
	dd.Reset(homing, engravingConfig{
		Speed:            moveSpeed,
		EngravingSpeed:   d.EngravingSpeed,
		Acceleration:     d.Acceleration,
		TicksPerSecond:   d.TopSpeed,
		NeedlePeriod:     d.NeedlePeriod,
		NeedleActivation: d.NeedleActivation,
	}.New())
	for _, pin := range []machine.Pin{d.XDiag, d.YDiag} {
		if err := pin.SetInterrupt(machine.PinRising, dd.handleDiag); err != nil {
			return fmt.Errorf("mjolnir2: engrave: %w", err)
		}
		defer pin.SetInterrupt(0, nil)
	}
	if err := dma.SetInterrupt(dd.channel, dd.handleDMA); err != nil {
		return fmt.Errorf("mjolnir2: engrave: %w", err)
	}
	defer dma.SetInterrupt(dd.channel, nil)
	cmds, c := iter.Pull(iter.Seq[engrave.Command](plan))
	defer c()
	cmd, moreCommands := cmds()
	stalled := true
	var blocked axis
	for {
		stallCmds := dd.commands
		if !moreCommands {
			stallCmds = nil
		}
		select {
		case <-quit:
			return nil
		case axis := <-dd.diag:
			if !homing {
				switch axis {
				case xaxis:
					return errors.New("mjolnir2: x-axis blocked")
				case yaxis:
					return errors.New("mjolnir2: y-axis blocked")
				default:
					panic("invalid axis")
				}
			}
			blocked |= axis
			if blocked == (xaxis | yaxis) {
				return nil
			}
		case <-dd.stall:
			stalled = true
		case stallCmds <- cmd:
			cmd, moreCommands = cmds()
		}
		// During stalls, we're responsible for filling the buffer
		// and restarting the interrupt handler.
		if stalled {
			dd.fillBuffer()
			if !moreCommands && dd.empty() {
				// We're done.
				break
			}
			// Restart engraver when both the buffer and command channel
			// are full.
			if dd.full() && len(dd.commands) == cap(dd.commands) || !moreCommands {
				stalled = false
				dd.setDMA()
				// The interrupt handler assumes a filled buffer.
				dd.fillBuffer()
				dd.startDMA()
			}
		}
	}
	if homing {
		return errors.New("mjolnir2: homing timed out")
	}
	return nil
}
