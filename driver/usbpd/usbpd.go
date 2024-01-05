// Package usbpd implements a TinyGo driver for the USB Power Delivery
// protocol.
package usbpd

import (
	"encoding/binary"
	"hash/crc32"
	"time"
)

// USB PD specification, PHY layer.
const (
	// Time window for detecting non-idle.
	tTransitionWindowMax = 20 * time.Microsecond
	// Transitions for signal detect.
	nTransitionCount = 3
	// Minimum bit rate in bits per second.
	fBitRateMin = 270_000
	// Maximum bit rate in bits per second.
	fBitRateMax = 330_000
	// Maximum time per transition at fBitRateMin.
	tUnitIntervalMax = time.Second / fBitRateMin
	// Minimum time per transition at fBitRateMax.
	tUnitIntervalMin = time.Second / fBitRateMax
	// Time to wait before sending message.
	tInterFrameGap = 25 * time.Microsecond
	// Time to drive the output after last message bit.
	tEndDriveBMC = 23 * time.Microsecond
	// Time to wait for a GoodCRC message.
	tTransmit   = 195 * time.Microsecond
	crcResidual = 0x2144df1c
	// Length of preamble, in bits.
	preambleBits = 64
)

// Protocol layer.
const (
	Sync1 = 0b11000
	Sync2 = 0b10001
	RST1  = 0b00111
	RST2  = 0b11001
	EOP   = 0b01101

	// Control messages.
	GoodCRC                 = 0b0_0001
	Accept                  = 0b0_0011
	Reject                  = 0b0_100
	PS_RDY                  = 0b0_0110
	Get_Sink_Cap            = 0b0_1000
	Soft_Reset              = 0b0_1101
	Not_Supported           = 0b1_0000
	Get_Source_Cap_Extended = 0b1_0001
	Get_Status              = 0b1_0010
	Get_Sink_Cap_Extended   = 0b1_0110

	// Data messages.
	Source_Capabilities = 0b0_0001
	Request             = 0b0_0010
	BIST                = 0b0_0011
	Alert               = 0b0_0110
	Get_Country_Info    = 0b0_0111
	Vendor_Defined      = 0b0_1111
)

func decode(buf, rx []uint8) ([]uint8, bool) {
	// Preamble is a string of 64 zeros and ones,
	// which in BMC is 3 intervals per 2 bits.
	const preambleBMC = preambleBits * 3 / 2
	if len(rx) < preambleBMC {
		// Message too short.
		return nil, false
	}
	preamble := rx[:preambleBMC]
	rx = rx[preambleBMC:]
	// Add up all intervals and average to
	// compute bit rate.
	total := uint(0)
	// The first interval is unreliable.
	for _, n := range preamble[1:] {
		total += uint(n)
	}
	interval := total / (preambleBits - 1)
	threshold := uint8(interval * 3 / 4)
	bits := uint8(0)
	w4b5b := uint8(0)
	// Decode 4b5b in place.
	payload := buf
	payload4b5b := buf
	for i := 0; i < len(rx); i++ {
		bit := uint8(0b0)
		if rx[i] < threshold {
			bit = 0b1
			i++
		}
		w4b5b |= bit << bits
		bits++
		if bits == 5 {
			payload4b5b = append(payload4b5b, w4b5b)
			bits = 0
			w4b5b = 0
		}
	}

	if len(payload4b5b) < 4+(2+4)*2+1 {
		// Message too short for SOP+Header+CRC+EOP.
		return nil, false
	}
	if !(payload4b5b[0] == Sync1 && payload4b5b[1] == Sync1 && payload4b5b[2] == Sync1 && payload4b5b[3] == Sync2 && payload4b5b[len(payload4b5b)-1] == EOP) {
		// Message corrupted or doesn't start with a SOP.
		return nil, false
	}
	payload4b5b = payload4b5b[4 : len(payload4b5b)-1]
	for i := 0; i < len(payload4b5b)-1; i += 2 {
		lo, ok1 := decode4b5b(payload4b5b[i+0])
		hi, ok2 := decode4b5b(payload4b5b[i+1])
		if !ok1 || !ok2 {
			return nil, false
		}
		payload = append(payload, hi<<4|lo)
	}

	if crc32.ChecksumIEEE(payload) != crcResidual {
		return nil, false
	}
	return payload[:len(payload)-4], true
}

func encodeHeader(buf []uint8, msg, mid, rev, nobjs uint8) []uint8 {
	header := uint16(nobjs)<<12 |
		uint16(mid)<<9 |
		uint16(rev)<<6 |
		uint16(msg)<<0
	buf = append(buf, uint8(header), uint8(header>>8))
	return buf
}

func transmit(pin func(bool), sleep func(time.Duration), msg []uint8) {
	pin(false)
	checksum := crc32.ChecksumIEEE(msg)
	cbytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(cbytes, checksum)
	v := true
	txBit := func(b bool) {
		v = !v
		pin(v)
		if b {
			sleep(tUnitIntervalMin / 2)
			v = !v
			pin(v)
			sleep(tUnitIntervalMin / 2)
		} else {
			sleep(tUnitIntervalMin)
		}
	}
	// Send preamble.
	for i := 0; i < preambleBits; i++ {
		txBit(i&0b1 == 1)
	}
	tx4b5b := func(w4b5b uint8) {
		for i := 0; i < 5; i++ {
			bit := (w4b5b>>i)&0b1 == 0b1
			txBit(bit)
		}
	}
	tx4b5b(Sync1)
	tx4b5b(Sync1)
	tx4b5b(Sync1)
	tx4b5b(Sync2)
	for _, chunk := range [][]byte{msg, cbytes} {
		for _, b := range chunk {
			lo := encode4b5b((b >> 0) & 0xf)
			hi := encode4b5b((b >> 4) & 0xf)
			tx4b5b(lo)
			tx4b5b(hi)
		}
	}
	tx4b5b(EOP)
	if !v {
		pin(true)
		sleep(tUnitIntervalMin)
	}
	pin(false)
	sleep(tEndDriveBMC)
}

func encode4b5b(nibble uint8) uint8 {
	tabl := [...]uint8{0b11110, 0b01001, 0b10100, 0b10101, 0b01010, 0b01011, 0b01110, 0b01111, 0b10010, 0b10011, 0b10110, 0b10111, 0b11010, 0b11011, 0b11100, 0b11101}
	return tabl[nibble]
}

func decode4b5b(w4b5b uint8) (uint8, bool) {
	var r uint8
	switch w4b5b {
	case 0b11110:
		r = 0
	case 0b01001:
		r = 0x1
	case 0b10100:
		r = 0x2
	case 0b10101:
		r = 0x3
	case 0b01010:
		r = 0x4
	case 0b01011:
		r = 0x5
	case 0b01110:
		r = 0x6
	case 0b01111:
		r = 0x7
	case 0b10010:
		r = 0x8
	case 0b10011:
		r = 0x9
	case 0b10110:
		r = 0xa
	case 0b10111:
		r = 0xb
	case 0b11010:
		r = 0xc
	case 0b11011:
		r = 0xd
	case 0b11100:
		r = 0xe
	case 0b11101:
		r = 0xf
	default:
		return 0, false
	}
	return r, true
}
