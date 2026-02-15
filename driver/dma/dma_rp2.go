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
	ChannelSet uint16
	ChannelID  uint8
	IRQ        uint8
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
	ints     *volatile.Register32
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
		handlers[i].ints = &irqs[i].INTS
	}
	handlers[0].intr = interrupt.New(rp.IRQ_DMA_IRQ_0, handlers[0].handleInterrupt)
	handlers[1].intr = interrupt.New(rp.IRQ_DMA_IRQ_1, handlers[1].handleInterrupt)
	handlers[2].intr = interrupt.New(rp.IRQ_DMA_IRQ_2, handlers[2].handleInterrupt)
	handlers[3].intr = interrupt.New(rp.IRQ_DMA_IRQ_3, handlers[3].handleInterrupt)
}

func ReserveChannels(n int) (ChannelSet, error) {
	mu.Lock()
	defer mu.Unlock()
	res := reservedChans
	var chans ChannelSet
	for range n {
		ch := 16 - bits.LeadingZeros16(res)
		if ch == nchannels {
			return 0, errors.New("dma: no channel available")
		}
		res |= 0b1 << ch
		chans |= 0b1 << ch
	}
	reservedChans = res
	return chans, nil
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

func (c ChannelSet) At(idx int) ChannelID {
	var ch int
	for idx >= 0 {
		ch = bits.TrailingZeros16(uint16(c))
		idx--
		c &^= 0b1 << ch
	}
	return ChannelID(ch)
}

func ChannelFor(id ChannelID) *Channel {
	return &channels[id]
}

func (h *irqHandler) handleInterrupt(interrupt.Interrupt) {
	// Acknowledge interrupt.
	h.ints.Set(h.ints.Get())
	if h.callback != nil {
		h.callback()
	}
}

func (irq IRQ) SetPriority(pri uint8) {
	h := &handlers[irq]
	h.intr.SetPriority(pri)
}

func (irq IRQ) SetInterrupt(chans ChannelSet, callback func()) {
	h := &handlers[irq]
	h.intr.Disable()
	h.callback = callback
	if callback != nil {
		irq := &irqs[irq]
		irq.INTE.Set(uint32(chans))
		h.intr.Enable()
	}
}
