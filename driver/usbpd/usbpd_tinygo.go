//go:build tinygo

package usbpd

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"machine"
	"time"

	"tinygo.org/x/drivers/delay"
)

type Device struct {
	data machine.Pin
	dir  machine.Pin

	msgID uint8
	buf   []uint8
}

func New(dataPin, dirPin machine.Pin) *Device {
	// Make space for longest message, BMC encoded,
	// 4b5b encoded data, one byte per 4-bit payload,
	// and transmit data.
	// const maxBufSize = max(((preambleBits+maxMessageLen)*3+1)/2 + (maxMessageLen+3)/4)
	const maxBufSize = 1024
	return &Device{
		data: dataPin,
		dir:  dirPin,
		buf:  make([]uint8, maxBufSize),
	}
}

func (d *Device) Run() {
done:
	for {
		buf := d.buf[:0]
		msg, ok := d.receive(buf)
		if !ok {
			continue
		}
		buf = append(buf[:0], msg...)
		msg = buf
		header := uint16(msg[1])<<8 | uint16(msg[0])
		msg = msg[2:]
		rev := uint8(header>>6) & (1<<2 - 1)
		mid := uint8(header>>9) & (1<<3 - 1)

		mtyp := header & (1<<5 - 1)
		nobjs := int(header>>12) & (1<<3 - 1)
		if nobjs*4 != len(msg) {
			// Argument count mismatch.
			continue
		}
		if nobjs > 0 || mtyp != GoodCRC {
			txmsg := encodeHeader(buf[len(buf):], GoodCRC, mid, rev, 0)
			d.transmit(txmsg)
		}

		if ext := (header >> 15) & 0b1; ext != 0 {
			// No support for extended messages.
			continue
		}
		// fmt.Printf("msg %v %v header %.16b mid %.3b rev %.2b nobjs %d typ %.5b\n", dt, dt2, header, mid, rev, nobjs, mtyp)
		if nobjs > 0 {
			switch mtyp {
			case Source_Capabilities:
				const maxVoltage = 28000
				bestVoltage := uint32(0)
				var bestRDO uint32
				for i := 0; i < nobjs; i++ {
					pdo := binary.LittleEndian.Uint32(msg)
					msg = msg[4:]
					switch ptyp := (pdo >> 30) & 0b11; ptyp {
					case 0b00: // Fixed supply.
						maxCurrentmA := 10 * (pdo & (1<<10 - 1))
						voltagemV := 50 * ((pdo >> 10) & (1<<10 - 1))
						if bestVoltage < voltagemV && voltagemV <= maxVoltage {
							bestRDO = uint32(0 |
								uint32(i+1)<<28 |
								// 1<<27 | // GotoMin support.
								1<<24 | // No USB Suspend.
								// 1<<22 | // EPR Capable.
								(maxCurrentmA/10)<<10 | // Maximum current.
								(maxCurrentmA/10)<<0 | // Maximum current.
								// (500/10)<<0 | // Minimum current 500mA
								0)
							bestVoltage = voltagemV
						}
						// fmt.Printf("fixed pdo %.32b: %d mA %d mV nobjs %d/%d\n", pdo, maxCurrentmA, voltagemV, i+1, nobjs)
					case 0b01: // Battery.
						// maxPowermW := 250 * (pdo & (1<<10 - 1))
						// minVoltagemV := 50 * ((pdo >> 10) & (1<<10 - 1))
						// maxVoltagemV := 50 * ((pdo >> 20) & (1<<10 - 1))
						// fmt.Printf("battery pdo: %d mW %d-%d mV\n", maxPowermW, minVoltagemV, maxVoltagemV)
					case 0b10: // Variable supply.
						// maxCurrentmA := 10 * (pdo & (1<<10 - 1))
						// minVoltagemV := 50 * ((pdo >> 10) & (1<<10 - 1))
						// maxVoltagemV := 50 * ((pdo >> 20) & (1<<10 - 1))
						// fmt.Printf("variable pdo: %d mA %d-%d mV\n", maxCurrentmA, minVoltagemV, maxVoltagemV)
					case 0b11: // Augmented PDO.
						switch ptyp := (pdo >> 28) & (1<<2 - 1); ptyp {
						case 0b00: // SPR supply.
							// maxCurrentmA := 50 * (pdo & (1<<7 - 1))
							// minVoltagemV := 100 * ((pdo >> 8) & (1<<8 - 1))
							// maxVoltagemV := 100 * ((pdo >> 17) & (1<<8 - 1))
							// fmt.Printf("spr pdo %.32b: %d mA %d-%d mV\n", pdo, maxCurrentmA, minVoltagemV, maxVoltagemV)
						case 0b01: // EPR supply.
							// maxPowerW := pdo & (1<<8 - 1)
							// minVoltagemV := 100 * ((pdo >> 8) & (1<<8 - 1))
							// maxVoltagemV := 100 * ((pdo >> 17) & (1<<8 - 1))
							// peakCurrent := (pdo >> 26) & 0b11
							// fmt.Printf("epr pdo: %d W %d-%d mV peak %.2b\n", maxPowerW, minVoltagemV, maxVoltagemV, peakCurrent)
						}
					}
				}
				txmsg := encodeHeader(buf[len(buf):], Request, d.msgID, rev, 1)
				txmsg = binary.LittleEndian.AppendUint32(txmsg, bestRDO)
				d.transmit(txmsg)
			case Alert:
				fmt.Println("Alert")
			case Get_Country_Info:
				fmt.Println("Get_Country_Info")
			case Vendor_Defined:
				fmt.Println("Vendor_Defined")
			default:
				fmt.Printf("unknown msg type h %.16b mtyp %.5b p %.8b\n", header, mtyp, msg)
			}
		} else {
			switch mtyp {
			case GoodCRC:
				d.msgID++
			case Accept:
				// fmt.Println("Accept")
			case Reject:
				fmt.Println("Reject")
			case PS_RDY:
				fmt.Println("PS_RDY")
				break done
			case Get_Sink_Cap:
				fmt.Println("Get_Sink_Cap")
			case Soft_Reset:
				fmt.Println("Soft_Reset :(")
			case Get_Source_Cap_Extended:
				// d.transmit(encodeHeader(buf, Not_Supported, mid, rev, 0))
				fmt.Println("Get_Source_Cap_Extended")
			case Get_Status:
				fmt.Println("Get_Status")
			case Get_Sink_Cap_Extended:
				fmt.Println("Get_Sink_Cap_Extended")
			default:
				fmt.Printf("unknown control msg type h %.16b %.5b\n", header, mtyp)
			}
		}
	}
}

func (d *Device) receive(buf []uint8) ([]uint8, bool) {
	// Start of message.
	for d.data.Get() {
	}
	// Read BMC encoded bits.
	v := false
message:
	for {
		n := uint8(0)
		for d.data.Get() == v {
			// Long high means message is done.
			if v && n == 50 {
				break message
			}
			n++
		}
		v = !v
		if len(buf) == cap(buf) {
			panic("overflow")
		}
		buf = append(buf, n)
	}
	return decode(buf[len(buf):], buf)
}

func (d *Device) transmit(msg []uint8) {
	buf2, n := transmit2(msg)
	d.waitIdle()

	d.dir.High()
	d.data.Configure(machine.PinConfig{Mode: machine.PinOutput})

	for i := 0; i < n; i++ {
		bit := (buf2[i/8] >> (i % 8)) & 0b1
		d.data.Set(bit == 0b1)
		delay.Sleep(tUnitIntervalMin / 2)
	}
	delay.Sleep(tEndDriveBMC)
	d.data.Configure(machine.PinConfig{Mode: machine.PinInput})
	d.dir.Low()
}

var buffeluf = make([]byte, 2048)

func transmit2(msg []uint8) ([]uint8, int) {
	buf2 := buffeluf[:0]
	n := 0
	var byt uint8
	tx := func(v bool) {
		bit := n % 8
		b := uint8(0)
		if v {
			b = 1
		}
		byt |= b << bit
		if bit == 7 {
			buf2 = append(buf2, byt)
			byt = 0
		}
		n++
	}
	checksum := crc32.ChecksumIEEE(msg)
	cbytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(cbytes, checksum)
	v := true
	txBit := func(b bool) {
		v = !v
		tx(v)
		if b {
			v = !v
		}
		tx(v)
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
		tx(true)
		tx(true)
	}
	tx(false)
	buf2 = append(buf2, byt)
	return buf2, n
}

func (d *Device) waitIdle() {
	for {
		// Wait for idle.
		for !d.data.Get() {
		}
		idle := time.Now()
		for d.data.Get() {
			if time.Since(idle) >= tInterFrameGap {
				return
			}
		}
	}
}
