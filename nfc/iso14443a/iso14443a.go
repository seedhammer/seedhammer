// package iso14443a implements the ISO/IEC 14443a NFC protocol
// and reading of Mifare Ultralight tags.
package iso14443a

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type Tag struct {
	bus     Transceiver
	uid     [10]byte
	uidLen  int
	page    uint8
	scratch [6]byte
}

// Transceiver represents an NFC modem.
type Transceiver interface {
	SetTxBits(bits int)
	SetCRC(tx, rx bool)
	SetRxBitCtrl(ctrl uint8)
	// Transceive transmits tx and starts receiving.
	Transceive(tx []byte) error
	// Reader reads the response from a Transceive.
	io.Reader
}

func Open(t Transceiver) (*Tag, error) {
	tag := &Tag{
		bus:  t,
		page: memStartPage,
	}
	if _, err := tag.reqa(); err != nil {
		return nil, fmt.Errorf("iso14443a: %w", err)
	}
	if err := tag.selectTag(); err != nil {
		return nil, fmt.Errorf("iso14443a: %w", err)
	}
	return tag, nil
}

func (t *Tag) reqa() (uint16, error) {
	reqa := t.scratch[:1]
	reqa[0] = cmdREQA
	t.bus.SetTxBits(7)
	t.bus.SetCRC(false, false)
	if err := t.bus.Transceive(reqa); err != nil {
		return 0, fmt.Errorf("iso14443a: REQA: %w", err)
	}
	atqa := t.scratch[:2]
	if _, err := io.ReadFull(t.bus, atqa); err != nil {
		return 0, fmt.Errorf("iso14443a: REQA: %w", err)
	}

	return binary.LittleEndian.Uint16(atqa), nil
}

func (t *Tag) selectTag() error {
	uid := t.uid[:]
	for casLvl, cmd := range []byte{casLevel1, casLevel2, casLevel3} {
		// disable CRC in anticipation of the anti collision protocol
		t.bus.SetCRC(false, false)

		// max 32 loops of the collision loop.
		known_bits := uint8(0)      // known bits of the UID at this level so far.
		send_req := make([]byte, 7) // used as Tx buffer.
		uid_this_level := send_req[2:]
		// pointer to the UID so far, by placing this pointer in the send_req
		// array we prevent copying the UID continuously.
		message_length := uint8(0)
		for collision_n := range uint8(32) {
			fmt.Printf("\nCL: %d, coll loop: %d, kb %d long: %v\n", casLvl, collision_n, known_bits, uid_this_level[:(known_bits+8-1)/8])

			send_req[0] = cmd
			send_req[1] = 0x20 + known_bits
			// send_req[2..] are filled with the UID via the uid_this_level pointer.

			rxalign := known_bits % 8
			fmt.Printf("Setting rx align to: %d\n", rxalign)

			t.bus.SetTxBits(int(known_bits % 8))
			t.bus.SetRxBitCtrl((0 << 7) | (rxalign << 4))

			// then sent the send_req to the hardware,
			// (known_bits / 8) + 1): The ceiled number of bytes by known bits.
			// +2 for cmd and NVB.
			if (known_bits % 8) == 0 {
				message_length = (known_bits / 8) + 2
			} else {
				message_length = ((known_bits / 8) + 1) + 2
			}

			fmt.Printf("Send:%d long: %v\n", message_length, send_req[:message_length])

			if err := t.bus.Transceive(send_req[:message_length]); err != nil {
				return fmt.Errorf("select: %w", err)
			}

			buf := make([]byte, 5) // Size is maximum of 5 bytes, UID[0-3] and BCC.
			n, err := t.bus.Read(buf)
			if err != nil {
				return fmt.Errorf("select: %w", err)
			}
			buf = buf[:n]

			fmt.Printf("Fifo long: %v\n", buf)

			// // next up, we have to check what happened.
			// irq0, err := d.readReg(regIRQ0)
			// if err != nil {
			// 	return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			// }
			// error, err := d.readReg(regError)
			// if err != nil {
			// 	return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			// }
			// coll, err := d.readReg(regRxColl)
			// if err != nil {
			// 	return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			// }
			// fmt.Printf("irq0: %#x\n", irq0)
			// fmt.Printf("error: %#.8b\n", error)
			collision_pos := uint8(0)
			// if irq0&irqErr != 0 { // some error occured.
			// 	// Check what kind of error.
			// 	// error = mfrc630_read_reg(MFRC630_REG_ERROR);
			// 	if error&errorCollDet == 0 {
			// 		// Some other error occurred.
			// 		return nil, 0, fmt.Errorf("clrc663: select: error: %#x\n", error)
			// 	}
			// 	// A collision was detected...
			// 	if coll&(1<<7) != 0 {
			// 		collision_pos = coll &^ (1 << 7)
			// 		fmt.Printf("Collision at %#x\n", collision_pos)
			// 		// This be a true collision... we have to select either the address
			// 		// with 1 at this position or with zero
			// 		// ISO spec says typically a 1 is added, that would mean:
			// 		// uint8_t selection = 1;

			// 		// However, it makes sense to allow some kind of user input for this, so we use the
			// 		// current value of uid at this position, first index right byte, then shift such
			// 		// that it is in the rightmost position, ten select the last bit only.
			// 		// We cannot compensate for the addition of the cascade tag, so this really
			// 		// only works for the first cascade level, since we only know whether we had
			// 		// a cascade level at the end when the SAK was received.
			// 		choice_pos := known_bits + collision_pos
			// 		selection := (uid[((choice_pos+(cascade_level-1)*3)/8)] >> ((choice_pos) % 8)) & 1

			// 		// We just OR this into the UID at the right position, later we
			// 		// OR the UID up to this point into uid_this_level.
			// 		uid_this_level[((choice_pos) / 8)] |= selection << ((choice_pos) % 8)
			// 		known_bits++ // add the bit we just decided.

			// 		fmt.Printf("uid_this_level now kb %d long: %v\n", known_bits, uid_this_level)

			// 	} else {
			// 		// Datasheet of mfrc630:
			// 		// bit 7 (CollPosValid) not set:
			// 		// Otherwise no collision is detected or
			// 		// the position of the collision is out of the range of bits CollPos.
			// 		fmt.Printf("Collision but no valid collpos.\n")
			// 		collision_pos = 0x20 - known_bits
			// 	}
			// } else if irq0&irqRx != 0 {
			// we got data, and no collisions, that means all is well.
			collision_pos = 0x20 - known_bits
			fmt.Printf("Got data, no collision, setting to: %#x\n", collision_pos)
			// } else {
			// 	// We have no error, nor received an RX. No response, no card?
			// 	return nil, 0, errors.New("clrc663: no tag detected")
			// }
			fmt.Printf("collision_pos: %#x\n", collision_pos)

			// read the UID Cln so far from the buffer.

			fmt.Printf("uid_this_level kb %d long: %v\n", known_bits, uid_this_level[:(known_bits+8-1)/8])
			// move the buffer into the uid at this level, but OR the result such that
			// we do not lose the bit we just set if we have a collision.
			for rbx := uint8(0); rbx < uint8(len(buf)); rbx++ {
				uid_this_level[(known_bits/8)+rbx] |= buf[rbx]
			}
			known_bits += collision_pos
			fmt.Printf("known_bits: %#x\n", known_bits)

			if known_bits >= 32 {
				fmt.Printf("exit collision loop: uid_this_level kb %d long: %v\n", known_bits, uid_this_level)

				break // done with collision loop
			}
		} // end collission loop

		// check if the BCC matches
		bcc_val := uid_this_level[4] // always at position 4, either with CT UID[0-2] or UID[0-3] in front.
		bcc_calc := uid_this_level[0] ^ uid_this_level[1] ^ uid_this_level[2] ^ uid_this_level[3]
		if bcc_val != bcc_calc {
			return errors.New("select: BCC mismatch")
		}

		send_req[0] = cmd
		send_req[1] = 0x70
		// send_req[2,3,4,5] // contain the CT, UID[0-2] or UID[0-3]
		send_req[6] = bcc_calc
		message_length = 7

		rxalign := uint8(0)
		t.bus.SetCRC(true, true)
		t.bus.SetTxBits(int(known_bits % 8))
		t.bus.SetRxBitCtrl((0 << 7) | (rxalign << 4))
		fmt.Println("known_bits", known_bits%8, "rxbits", (0<<7)|(rxalign<<4))

		// actually send it!
		if err := t.bus.Transceive(send_req[:message_length]); err != nil {
			return fmt.Errorf("select: %w", err)
		}
		fmt.Printf("send_req %d long: %v\n", message_length, send_req[:message_length])

		// Read the sakBuf answer from the fifo.
		sakBuf := t.scratch[:1]
		if _, err := io.ReadFull(t.bus, sakBuf); err != nil {
			return fmt.Errorf("select: %w", err)
		}
		sak := sakBuf[0]

		fmt.Printf("SAK answer: %d\n", sak)

		if sak&(1<<2) != 0 {
			// UID not yet complete, continue with next cascade.
			// This also means the 0'th byte of the UID in this level was CT, so we
			// have to shift all bytes when moving to uid from uid_this_level.
			for UIDn := range 3 {
				// uid_this_level[UIDn] = uid_this_level[UIDn + 1];
				uid[casLvl*3+UIDn] = uid_this_level[UIDn+1]
			}
		} else {
			// Done according so SAK!
			// Add the bytes at this level to the UID.
			for UIDn := range 4 {
				uid[casLvl*3+UIDn] = uid_this_level[UIDn]
			}

			// Finally, return the length of the UID that's now at the uid pointer.
			t.uidLen = int(casLvl+1)*3 + 1
			fmt.Println("GOT UID", t.uid[:t.uidLen])
			return nil
		}

		fmt.Printf("Exit cascade %d long: %v\n", casLvl, uid)
	} // cascade loop
	return errors.New("select failed")
}

// Read from the tag user memory. The buffer must be at least
// 16 bytes long.
func (t *Tag) Read(rx []byte) (int, error) {
	req := t.scratch[:2]
	req[0] = cmdMifareRead
	req[1] = t.page
	if err := t.bus.Transceive(req); err != nil {
		return 0, fmt.Errorf("iso14443a: Read: %w", err)
	}

	n, err := t.bus.Read(rx)
	if err != nil {
		return n, fmt.Errorf("iso14443a: Read: %w", err)
	}
	if len(rx) < n {
		return 0, fmt.Errorf("iso14443a: Read: buffer too small: %d", len(rx))
	}
	t.page += uint8(n / pageSize)
	return n, nil
}

const (
	cmdREQA = 0x26

	cmdMifareRead = 0x30
	cmdMifareAuth = 0x60

	// pageSize in bytes.
	pageSize = 4
	// The start page of user memory.
	memStartPage = 4

	casLevel1 = 0x93
	casLevel2 = 0x95
	casLevel3 = 0x97
)
