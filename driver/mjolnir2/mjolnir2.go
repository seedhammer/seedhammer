//go:build tinygo && rp

// package mjolnir2 implements a driver for the particular
// engraving hardware in the Seedhammer II.
package mjolnir2

import (
	"device/rp"
	"errors"
	"fmt"
	"machine"
	"math"
	"runtime"
	"time"
	"unsafe"

	"seedhammer.com/driver/dma"
	"seedhammer.com/driver/pio"
)

type Device struct {
	Pio            *rp.PIO0_Type
	BasePin        machine.Pin
	TicksPerSecond uint
	PulseADC       *machine.ADC

	// Needle period and activation duration.
	needlePeriod time.Duration
	needleAct    time.Duration
	// Needle period tick.
	tneedle  uint
	channels dma.ChannelSet

	// DMA transfer state.
	status status
	// inFlight is the number of words that have been
	// submitted for DMA transfer but not yet tranferred.
	inFlight int
	dmaBuf   []uint32
	// A single-word DMA buffer for restarting the
	// DMA transfer.
	dmaCtrl *uint32
	ring    *ring
	// Ticker paces the rate of buffer filling.
	ticker <-chan time.Time
}

const (
	engraverSM = 0
	// The solenoid program overrides the engraver
	// program, so its SM index must be higher than
	// the engraver's.
	solenoidSM = engraverSM + 1
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

type status int

const (
	idle status = iota
	running
)

func (d *Device) Configure(bufDur time.Duration, needleActivation, needlePeriod time.Duration) error {
	d.needleAct = needleActivation
	d.needlePeriod = needlePeriod
	chans, err := dma.ReserveChannels(2)
	if err != nil {
		return fmt.Errorf("mjolnir2: %w", err)
	}
	d.channels = chans
	// Calculate the number of buffer words that correspond
	// to the buffer duration.
	bufferWords := uint(uint64(bufDur) * uint64(d.TicksPerSecond) / uint64(stepsPerWord*time.Second))
	d.dmaBuf = make([]uint32, bufferWords)
	d.dmaCtrl = new(uint32)
	d.ring = newRing(d.dmaBuf)
	// 1/writeableDenom is the fraction of the buffer to wait
	// for being available.
	const writableDenom = 8
	d.ticker = time.Tick(bufDur / writableDenom)
	return nil
}

func (d *Device) configurePIO() {
	progOff := uint8(0)
	conf := engraverProgramDefaultConfig(progOff)
	conf.SidesetBase = uint8(pinStepY + d.BasePin)
	conf.OutBase = uint8(pinDirY + d.BasePin)
	conf.OutCount = engraverPinBits
	conf.FIFOMode = pio.FIFOJoinTX
	conf.PullThreshold = engraverPinBits * stepsPerWord
	conf.Autopull = true
	conf.Freq = uint32(d.TicksPerSecond) * engraverCyclesPerStep
	pio.Configure(d.Pio, engraverSM, conf.Build())
	pio.Program(d.Pio, progOff, engraverInstructions)
	pioIRQ := &d.Pio.IRQ
	pioIRQ.SetBits(0b1 << engraverStallIRQ)
	pio.ConfigurePins(d.Pio, engraverSM, d.BasePin, engraverPinBits)
	pio.Pindirs(d.Pio, engraverSM, d.BasePin, engraverPinBits, machine.PinOutput)
	pio.ClearFIFOs(d.Pio, engraverSM)
	pio.Jump(d.Pio, engraverSM, progOff)

	progOff += uint8(len(engraverInstructions))
	conf = solenoidProgramDefaultConfig(progOff)
	freq := uint32(machine.CPUFrequency())
	conf.Freq = freq
	conf.SetBase = uint8(pinNeedle + d.BasePin)
	conf.SetCount = 1
	conf.FIFOMode = pio.FIFOJoinRxGet
	conf.OutSticky = true
	conf.InlineOut = true
	conf.InlineOutBit = 0
	pio.Configure(d.Pio, solenoidSM, conf.Build())
	pio.Program(d.Pio, progOff, solenoidInstructions)
	pio.ClearFIFOs(d.Pio, solenoidSM)
	pio.Jump(d.Pio, solenoidSM, progOff)

	pio.Restart(d.Pio, 0b1<<engraverSM|0b1<<solenoidSM)

	// Convert durations to PIO cycles.
	if d.PulseADC == nil {
		d.updatePeriods(d.needlePeriod, d.needleAct)
	}
}

// setupDMARing configures the DMA transfer from the device ring buffer
// to the PIO program driving the engraver.
func (d *Device) configureDMARing() {
	// Ideally, a single DMA channel in ring buffer mode. However, ring
	// buffer mode supports power of two ring buffer sizes (CTRL.RING_SIZE)
	// and, worse, power of two aligned addresses (READ_ADDR) only.
	//
	// Instead, use 2 channels: channel chA transfers from the buffer to PIO,
	// and chains to channel B when done. Channel B resets READ_ADDR of channel
	// A and restarts it.
	chA := d.channels.At(0)
	chB := d.channels.At(1)

	A := dma.ChannelFor(chA)
	// Set DMA destination to pio TX FIFO.
	A.WRITE_ADDR.Set(uint32(uintptr(unsafe.Pointer(pio.Tx(d.Pio, engraverSM)))))
	// Set the transfer count only; channel B sets READ_ADDR.
	A.TRANS_COUNT.Set(uint32(len(d.dmaBuf)))
	A.AL1_CTRL.Set(
		// Increment read address on each transfer.
		rp.DMA_CH0_CTRL_TRIG_INCR_READ |
			// Transfer 32-bit words.
			rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_SIZE_WORD<<rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_Pos |
			// Chain to B.
			uint32(chB)<<rp.DMA_CH0_CTRL_TRIG_CHAIN_TO_Pos |
			// Pace transfers by the pio TX FIFO.
			pio.DreqTx(d.Pio, engraverSM)<<rp.DMA_CH0_CTRL_TRIG_TREQ_SEL_Pos |
			// High-priority to minimize stall risk.
			rp.DMA_CH0_CTRL_TRIG_HIGH_PRIORITY |
			// Enable, waiting for trigger.
			rp.DMA_CH0_CTRL_TRIG_EN,
	)

	B := dma.ChannelFor(chB)
	*d.dmaCtrl = uint32(uintptr(unsafe.Pointer(unsafe.SliceData(d.dmaBuf))))
	B.READ_ADDR.Set(uint32(uintptr(unsafe.Pointer(d.dmaCtrl))))
	// Write to the READ_ADDR of channel A and trigger it.
	B.WRITE_ADDR.Set(uint32(uintptr(unsafe.Pointer(&A.AL3_READ_ADDR_TRIG))))
	// Write a single word.
	B.TRANS_COUNT.Set(1)
	B.AL1_CTRL.Set(
		// Transfer 32-bit words.
		rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_SIZE_WORD<<rp.DMA_CH0_CTRL_TRIG_DATA_SIZE_Pos |
			// Chain to A.
			uint32(chA)<<rp.DMA_CH0_CTRL_TRIG_CHAIN_TO_Pos |
			// Don't pace.
			rp.DMA_CH0_CTRL_TRIG_TREQ_SEL_PERMANENT<<rp.DMA_CH0_CTRL_TRIG_TREQ_SEL_Pos |
			// High-priority to minimize stall risk.
			rp.DMA_CH0_CTRL_TRIG_HIGH_PRIORITY,
	)
}

func (d *Device) Reset() {
	pio.Disable(d.Pio, 0b1<<engraverSM|0b1<<solenoidSM)

	// Abort any pending transfer.
	for i := range 2 {
		ch := dma.ChannelFor(d.channels.At(i))
		ch.CTRL_TRIG.ClearBits(rp.DMA_CH0_CTRL_TRIG_EN)
	}
	rp.DMA.CHAN_ABORT.Set(uint32(d.channels))
	for rp.DMA.CHAN_ABORT.Get()&uint32(d.channels) != 0 {
		runtime.Gosched()
	}
	// Keep the DMA buffers alive.
	// TODO: use runtime.Pinner to pin the buffer before
	// starting DMA and unpin it after stopping DMA.
	runtime.KeepAlive(d.dmaBuf)
	runtime.KeepAlive(d.dmaCtrl)

	d.inFlight = 0
	d.ring.Reset()
	d.status = idle
}

func (d *Device) Write(steps []uint32) (completed int, err error) {
	for {
		// Advance read index from the DMA remaining transaction
		// count.
		nr := d.advanceDMA()
		completed += nr
		nw := d.ring.Write(steps)
		steps = steps[nw:]
		d.inFlight = d.inFlight + nw - nr

		if len(steps) == 0 {
			// Update pulse length if pin control enabled.
			if adc := d.PulseADC; adc != nil {
				v := adc.Get()
				const adjustAct = false
				period, act := d.needlePeriod, d.needleAct
				if adjustAct {
					act = d.needlePeriod * time.Duration(v) / math.MaxUint16
				} else {
					period = 20*time.Millisecond*time.Duration(v)/math.MaxUint16 + 10*time.Millisecond
				}
				d.updatePeriods(period, act)
			}
			return completed, nil
		}
		// If there is data to write, the DMA buffer is full and
		// we can start.
		d.ensureStarted()
		// Wait for buffer space.
		<-d.ticker
		// Check for stalls.
		if d.stalled() {
			return completed, errors.New("mjolnir2: buffer underrun")
		}
	}
}

func (d *Device) updatePeriods(period, act time.Duration) {
	freq := uint32(machine.CPUFrequency())
	actTicks := uint32(uint64(act) * uint64(freq) / (solenoidCycles * uint64(time.Second)))
	periodTicks := uint32(uint64(period) * uint64(freq) / (solenoidCycles * uint64(time.Second)))
	rxfifo := pio.RxFIFOFor(d.Pio, solenoidSM)
	rxfifo.PUTGET[solenoidZeroIndex].Set(0)
	rxfifo.PUTGET[solenoidActIndex].Set(actTicks)
	rxfifo.PUTGET[solenoidPeriodIndex].Set(periodTicks)
}

func (d *Device) stalled() bool {
	return d.Pio.IRQ.HasBits(0b1 << engraverStallIRQ)
}

func (d *Device) advanceDMA() int {
	dch := dma.ChannelFor(d.channels.At(0))
	remaining := int(dch.TRANS_COUNT.Get())
	completed := d.ring.AdvanceRead(remaining)
	return completed
}

func (d *Device) ensureStarted() {
	if d.status != idle {
		return
	}
	d.status = running
	d.configureDMARing()
	d.configurePIO()
	// Kick off the DMA transfer.
	chB := dma.ChannelFor(d.channels.At(1))
	chB.CTRL_TRIG.SetBits(rp.DMA_CH0_CTRL_TRIG_EN)
	// Start the PIO program when its FIFO is full.
	pio.WaitTxFull(d.Pio, 0b1<<engraverSM)
	// Since the PIO hasn't started yet, the number
	// of DMA transferred words is the effective FIFO
	// depth. Advance the ring buffer so that future
	// calls to advanceDMA reports the number of steps
	// written to the pins, not just buffered in the FIFO.
	d.advanceDMA()
	pio.Enable(d.Pio, 0b1<<engraverSM|0b1<<solenoidSM)
}

func (d *Device) Flush() error {
	d.ensureStarted()
	defer d.Reset()
	// Wait for stall.
	for {
		if d.stalled() {
			d.inFlight -= d.advanceDMA()
			if d.inFlight > 0 {
				return errors.New("mjolnir2: buffer underrun")
			}
			return nil
		}
		<-d.ticker
	}
}
