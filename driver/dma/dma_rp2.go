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

type ChannelID uint8

const (
	nchannels = 16 // rp2350
	nirq      = 4

	// Sentinel for no channel.
	noChannel ChannelID = 0xff
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
	channel  ChannelID
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
		handlers[i].channel = noChannel
		handlers[i].num = uint8(i)
	}
	handlers[0].intr = interrupt.New(rp.IRQ_DMA_IRQ_0, handlers[0].handleInterrupt)
	handlers[1].intr = interrupt.New(rp.IRQ_DMA_IRQ_1, handlers[1].handleInterrupt)
	handlers[2].intr = interrupt.New(rp.IRQ_DMA_IRQ_2, handlers[2].handleInterrupt)
	handlers[3].intr = interrupt.New(rp.IRQ_DMA_IRQ_3, handlers[3].handleInterrupt)
}

func Reserve() (ChannelID, error) {
	mu.Lock()
	defer mu.Unlock()
	channel := 16 - bits.LeadingZeros16(reservedChans)
	if channel == nchannels {
		return 0, errors.New("no available DMA channel")
	}
	reservedChans |= 0b1 << channel
	return ChannelID(channel), nil
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

func SetInterrupt(ch ChannelID, callback func()) error {
	mu.Lock()
	defer mu.Unlock()
	if callback != nil {
		num := 16 - bits.LeadingZeros16(reservedIRQs)
		if num == nirq {
			return errors.New("no available interrupt")
		}
		reservedIRQs |= 0b1 << num
		h := &handlers[num]
		h.channel = ch
		h.callback = callback
		irq := &irqs[num]
		irq.INTE.Set(0b1 << ch)
		// Lower priority assuming that DMA completion interrupts
		// are both heavier and less time-critical than other kinds
		// of interrupts.
		h.intr.SetPriority(0xff)
		h.intr.Enable()
		return nil
	} else {
		num := irqForChannel(ch)
		h := &handlers[num]
		h.intr.Disable()
		h.channel = noChannel
		h.callback = nil
		reservedIRQs &^= 0b1 << num
		return nil
	}
}

func ForceInterrupt(ch ChannelID) {
	num := irqForChannel(ch)
	irqs[num].INTF.SetBits(0b1 << ch)
}

func ClearForceInterrupt(ch ChannelID) {
	num := irqForChannel(ch)
	irqs[num].INTF.ClearBits(0b1 << ch)
}

func irqForChannel(ch ChannelID) uint8 {
	for num, h := range handlers {
		if h.channel != ch {
			continue
		}
		return uint8(num)
	}
	panic("no IRQ for channel")
}
