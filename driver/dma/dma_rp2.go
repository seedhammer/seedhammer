//go:build tinygo && rp2350

// package dma implements a driver for the platform's
// DMA device.
package dma

import (
	"device/rp"
	"errors"
	"math/bits"
	"runtime/interrupt"
	"runtime/volatile"
	"sync"
	"unsafe"
)

// Channel represents a DMA channel.
type Channel struct {
	READ_ADDR            volatile.Register32
	WRITE_ADDR           volatile.Register32
	TRANS_COUNT          volatile.Register32
	CTRL_TRIG            volatile.Register32
	AL1_CTRL             volatile.Register32
	AL1_READ_ADDR        volatile.Register32
	AL1_WRITE_ADDR       volatile.Register32
	AL1_TRANS_COUNT_TRIG volatile.Register32
	AL2_CTRL             volatile.Register32
	AL2_TRANS_COUNT      volatile.Register32
	AL2_READ_ADDR        volatile.Register32
	AL2_WRITE_ADDR_TRIG  volatile.Register32
	AL3_CTRL             volatile.Register32
	AL3_WRITE_ADDR       volatile.Register32
	AL3_TRANS_COUNT      volatile.Register32
	AL3_READ_ADDR_TRIG   volatile.Register32
}

type (
	ChannelID uint8
	IRQ       uint8
)

const (
	nchannels = 16 // rp2350
	nirq      = 4
)

var (
	mu sync.Mutex
	// reservedChans tracks the bitset of reserved
	// DMA channels.
	reservedChans uint16
	// reservedIRQs tracks the bitset of reserved IRQs.
	reservedIRQs uint16
)

type irq struct {
	INTE volatile.Register32
	INTF volatile.Register32
	INTS volatile.Register32
}

type irqHandler struct {
	num      uint8
	intr     interrupt.Interrupt
	callback func()
}

var (
	channels = unsafe.Slice((*Channel)(unsafe.Pointer(&rp.DMA.CH0_READ_ADDR)), nchannels)
	irqs     = unsafe.Slice((*irq)(unsafe.Pointer(&rp.DMA.INTE0)), nirq)
	handlers [nirq]irqHandler
)

func init() {
	for i := range handlers {
		handlers[i].num = uint8(i)
	}
	handlers[0].intr = interrupt.New(rp.IRQ_DMA_IRQ_0, handlers[0].handleInterrupt)
	handlers[1].intr = interrupt.New(rp.IRQ_DMA_IRQ_1, handlers[1].handleInterrupt)
	handlers[2].intr = interrupt.New(rp.IRQ_DMA_IRQ_2, handlers[2].handleInterrupt)
	handlers[3].intr = interrupt.New(rp.IRQ_DMA_IRQ_3, handlers[3].handleInterrupt)
	// Lower priority assuming that DMA completion interrupts
	// are both heavier and less time-critical than other kinds
	// of interrupts.
	for i := range handlers {
		handlers[i].intr.SetPriority(0xff)
	}
}

func ReserveChannel() (ChannelID, error) {
	mu.Lock()
	defer mu.Unlock()
	channel := 16 - bits.LeadingZeros16(reservedChans)
	if channel == nchannels {
		return 0, errors.New("no available DMA channel")
	}
	reservedChans |= 0b1 << channel
	return ChannelID(channel), nil
}

func ReserveIRQ() (IRQ, error) {
	mu.Lock()
	defer mu.Unlock()
	num := IRQ(16 - bits.LeadingZeros16(reservedIRQs))
	if num == nirq {
		return 0xff, errors.New("no available interrupt")
	}
	reservedIRQs |= 0b1 << num
	return num, nil
}

func (irq IRQ) Free() {
	mu.Lock()
	defer mu.Unlock()
	reservedIRQs &^= 0b1 << irq
}

func ChannelAt(ch ChannelID) *Channel {
	return &channels[ch]
}

func (h *irqHandler) handleInterrupt(interrupt.Interrupt) {
	// Acknowledge interrupt.
	irq := &irqs[h.num]
	irq.INTS.Set(irq.INTS.Get())
	if h.callback != nil {
		h.callback()
	}
}

func (irq IRQ) Set(ch ChannelID, callback func()) {
	h := &handlers[irq]
	h.intr.Disable()
	h.callback = callback
	if callback != nil {
		irq := &irqs[irq]
		irq.INTE.Set(0b1 << ch)
		h.intr.Enable()
	}
}
