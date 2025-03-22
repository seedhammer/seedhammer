// package iso14443a implements the ISO/IEC 14443a NFC protocol.
package iso14443a

import (
	"errors"
	"fmt"
	"io"
)

type Tag struct {
	UID uint32

	scratch [2]byte
}

// Transceiver represents an NFC modem.
type Transceiver interface {
	SetTxBits(bits int)
	SetCRC(enable bool)
	// Transceive transmits tx and starts receiving.
	Transceive(tx []byte) error
	// Reader reads the response from a Transceive.
	io.Reader
}

func Open(t Transceiver) (*Tag, error) {
	tag := new(Tag)
	reqa := tag.scratch[:1]
	reqa[0] = cmdREQA
	t.SetTxBits(7)
	t.SetCRC(false)
	if err := t.Transceive(reqa); err != nil {
		return nil, fmt.Errorf("iso14443a: reqa: %w", err)
	}
	fmt.Println("Sending REQA\n")
	atqa := tag.scratch[:2]
	n, err := t.Read(atqa)
	if err != nil {
		return nil, fmt.Errorf("iso14443a: atqa: %w", err)
	}
	if n != len(atqa) {
		return nil, fmt.Errorf("iso14443a: unexpected atqa response length: %d", n)
	}

	fmt.Printf("ATQA answer: %#.4x\n", atqa)
	const (
		_ISO14443_CAS_LEVEL_1 = 0x93
		_ISO14443_CAS_LEVEL_2 = 0x95
		_ISO14443_CAS_LEVEL_3 = 0x97
	)

	for cascade_level := uint8(1); cascade_level <= 3; cascade_level++ {
		cmd := uint8(0)
		switch cascade_level {
		case 1:
			cmd = _ISO14443_CAS_LEVEL_1
		case 2:
			cmd = _ISO14443_CAS_LEVEL_2
		case 3:
			cmd = _ISO14443_CAS_LEVEL_3
		}

		// disable CRC in anticipation of the anti collision protocol
		t.SetCRC(false)

		// max 32 loops of the collision loop.
		known_bits := uint8(0)      // known bits of the UID at this level so far.
		send_req := make([]byte, 7) // used as Tx buffer.
		uid_this_level := send_req[2:]
		// pointer to the UID so far, by placing this pointer in the send_req
		// array we prevent copying the UID continuously.
		message_length := uint8(0)
		for collision_n := range uint8(32) {
			fmt.Printf("\nCL: %d, coll loop: %d, kb %d long: %v\n", cascade_level, collision_n, known_bits, uid_this_level[:(known_bits+8-1)/8])

			send_req[0] = cmd
			send_req[1] = 0x20 + known_bits
			// send_req[2..] are filled with the UID via the uid_this_level pointer.

			rxalign := known_bits % 8
			fmt.Printf("Setting rx align to: %d\n", rxalign)

			t.SetTxBits(known_bits % 8)
			t.SetRxBitCtrl((0 << 7) | (rxalign << 4))
			if err := d.writeRegs(
				// clear interrupts
				regIRQ0, 0x7F,
				regIRQ1, 0x7F,

				// Only transmit the last 'x' bits of the current byte we are discovering
				// First limit the txdatanum, such that it limits the correct number of bits.
				regTxDataNum, (known_bits%8)|_TXDATANUM_DATAEN,

				// ValuesAfterColl: If cleared, every received bit after a collision is
				// replaced by a zero. This function is needed for ISO/IEC14443 anticollision (0<<7).
				// We want to shift the bits with RxAlign
				regRxBitCtrl, (0<<7)|(rxalign<<4),
			); err != nil {
				return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			}

			// then sent the send_req to the hardware,
			// (known_bits / 8) + 1): The ceiled number of bytes by known bits.
			// +2 for cmd and NVB.
			if (known_bits % 8) == 0 {
				message_length = (known_bits / 8) + 2
			} else {
				message_length = ((known_bits / 8) + 1) + 2
			}

			fmt.Printf("Send:%d long: %v\n", message_length, send_req[:message_length])

			if err := d.transceive(send_req[:message_length]...); err != nil {
				return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			}

			// block until we are done
			if err := d.waitForIRQ(irqRx, 0b1<<timer_for_timeout); err != nil {
				return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			}
			if err := d.writeRegs(regCommand, cmdIdle); err != nil {
				return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			}

			// next up, we have to check what happened.
			irq0, err := d.readReg(regIRQ0)
			if err != nil {
				return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			}
			error, err := d.readReg(regError)
			if err != nil {
				return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			}
			coll, err := d.readReg(regRxColl)
			if err != nil {
				return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			}
			fmt.Printf("irq0: %#x\n", irq0)
			fmt.Printf("error: %#.8b\n", error)
			collision_pos := uint8(0)
			if irq0&irqErr != 0 { // some error occured.
				// Check what kind of error.
				// error = mfrc630_read_reg(MFRC630_REG_ERROR);
				if error&errorCollDet == 0 {
					// Some other error occurred.
					return nil, 0, fmt.Errorf("clrc663: select: error: %#x\n", error)
				}
				// A collision was detected...
				if coll&(1<<7) != 0 {
					collision_pos = coll &^ (1 << 7)
					fmt.Printf("Collision at %#x\n", collision_pos)
					// This be a true collision... we have to select either the address
					// with 1 at this position or with zero
					// ISO spec says typically a 1 is added, that would mean:
					// uint8_t selection = 1;

					// However, it makes sense to allow some kind of user input for this, so we use the
					// current value of uid at this position, first index right byte, then shift such
					// that it is in the rightmost position, ten select the last bit only.
					// We cannot compensate for the addition of the cascade tag, so this really
					// only works for the first cascade level, since we only know whether we had
					// a cascade level at the end when the SAK was received.
					choice_pos := known_bits + collision_pos
					selection := (uid[((choice_pos+(cascade_level-1)*3)/8)] >> ((choice_pos) % 8)) & 1

					// We just OR this into the UID at the right position, later we
					// OR the UID up to this point into uid_this_level.
					uid_this_level[((choice_pos) / 8)] |= selection << ((choice_pos) % 8)
					known_bits++ // add the bit we just decided.

					fmt.Printf("uid_this_level now kb %d long: %v\n", known_bits, uid_this_level)

				} else {
					// Datasheet of mfrc630:
					// bit 7 (CollPosValid) not set:
					// Otherwise no collision is detected or
					// the position of the collision is out of the range of bits CollPos.
					fmt.Printf("Collision but no valid collpos.\n")
					collision_pos = 0x20 - known_bits
				}
			} else if irq0&irqRx != 0 {
				// we got data, and no collisions, that means all is well.
				collision_pos = 0x20 - known_bits
				fmt.Printf("Got data, no collision, setting to: %#x\n", collision_pos)
			} else {
				// We have no error, nor received an RX. No response, no card?
				return nil, 0, errors.New("clrc663: no tag detected")
			}
			fmt.Printf("collision_pos: %#x\n", collision_pos)

			// read the UID Cln so far from the buffer.

			buf := make([]byte, 5) // Size is maximum of 5 bytes, UID[0-3] and BCC.
			n, err := d.readFIFO(buf)
			if err != nil {
				return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			}
			buf = buf[:n]

			fmt.Printf("Fifo long: %v\n", buf)

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
			return nil, 0, errors.New("clrc663: select: BCC mismatch")
		}

		send_req[0] = cmd
		send_req[1] = 0x70
		// send_req[2,3,4,5] // contain the CT, UID[0-2] or UID[0-3]
		send_req[6] = bcc_calc
		message_length = 7

		rxalign := uint8(0)
		if err := d.writeRegs(
			// clear interrupts
			regIRQ0, 0x7F,
			regIRQ1, 0x7F,

			// Ok, almost done now, we reenable the CRC's
			regTxCrcPreset, _RECOM_14443A_CRC|_CRC_ON,
			regRxCrcPreset, _RECOM_14443A_CRC|_CRC_ON,

			// reset the Tx and Rx registers (disable alignment, transmit full bytes)
			regTxDataNum, (known_bits%8)|_TXDATANUM_DATAEN,
			regRxBitCtrl, (0<<7)|(rxalign<<4),
		); err != nil {
			return nil, 0, fmt.Errorf("clrc663: select: %w", err)
		}

		// actually send it!
		if err := d.transceive(send_req[:message_length]...); err != nil {
			return nil, 0, fmt.Errorf("clrc663: select: %w", err)
		}
		fmt.Printf("send_req %d long: %v\n", message_length, send_req[:message_length])

		if err := d.waitForIRQ(irqRx|irqErr, 0b1<<timer_for_timeout); err != nil {
			return nil, 0, fmt.Errorf("clrc663: select: %w", err)
		}
		if err := d.writeRegs(regCommand, cmdIdle); err != nil {
			return nil, 0, fmt.Errorf("clrc663: select: %v", err)
		}

		// Check the source of exiting the loop.
		irq0, err := d.readReg(regIRQ0)
		if err != nil {
			return nil, 0, fmt.Errorf("clrc663: select: %w", err)
		}
		if irq0&irqErr != 0 {
			// Check what kind of error.
			error, err := d.readReg(regError)
			if err != nil {
				return nil, 0, fmt.Errorf("clrc663: select: %w", err)
			}
			if error&errorCollDet != 0 {
				// a collision was detected with NVB=0x70, should never happen.
				return nil, 0, errors.New("clrc663: impossible collision")
			}
		}

		// Read the sak answer from the fifo.
		lenAndSak := make([]byte, 2)
		if err := d.readRegs(regFIFOLength, lenAndSak); err != nil {
			return nil, 0, fmt.Errorf("clrc663: select: %w", err)
		}
		sak_len, sak_value := lenAndSak[0], lenAndSak[1]
		if sak_len != 1 {
			return nil, 0, fmt.Errorf("clrc663: invalid sak length: %d", sak_len)
		}

		fmt.Printf("SAK answer: %d\n", sak_value)

		if sak_value&(1<<2) != 0 {
			// UID not yet complete, continue with next cascade.
			// This also means the 0'th byte of the UID in this level was CT, so we
			// have to shift all bytes when moving to uid from uid_this_level.
			for UIDn := uint8(0); UIDn < 3; UIDn++ {
				// uid_this_level[UIDn] = uid_this_level[UIDn + 1];
				uid[(cascade_level-1)*3+UIDn] = uid_this_level[UIDn+1]
			}
		} else {
			// Done according so SAK!
			// Add the bytes at this level to the UID.
			for UIDn := uint8(0); UIDn < 4; UIDn++ {
				uid[(cascade_level-1)*3+UIDn] = uid_this_level[UIDn]
			}

			// Finally, return the length of the UID that's now at the uid pointer.
			return uid[:cascade_level*3+1], sak_value, nil
		}

		fmt.Printf("Exit cascade %d long: %v\n", cascade_level, uid)
	} // cascade loop
	return nil, 0, errors.New("clrc663: uid select failed") // getting an UID failed.
}

func (t *Tag) Read(rx []byte) (int, error) {
	return 0, errors.New("TODO")
}

// func (d *Device) readBlock(block_address uint8, blk []byte) (int, error) {
// 	const (
// 		_RECOM_14443A_CRC = 0x18
// 		_CRC_OFF          = 0
// 		_CRC_ON           = 1
// 		_TXDATANUM_DATAEN = 1 << 3

// 		_MF_CMD_READ = 0x30 //!< To read a block from mifare card.
// 	)
// 	if err := d.writeRegs(
// 		regCommand, cmdIdle,
// 		regFIFOControl, 1<<4, // Flush FIFO.
// 		regTxCrcPreset, _RECOM_14443A_CRC|_CRC_ON,
// 		regRxCrcPreset, _RECOM_14443A_CRC|_CRC_ON,
// 		regIRQ0, 0x7F,
// 		regIRQ1, 0x7F,
// 	); err != nil {
// 		return 0, fmt.Errorf("read block: %w", err)
// 	}

// 	// // configure a timeout timer.
// 	// uint8_t timer_for_timeout = 0;  // should match the enabled interupt.

// 	// Set timer to 221 kHz clock, start at the end of Tx.
// 	// mfrc630_timer_set_control(timer_for_timeout, MFRC630_TCONTROL_CLK_211KHZ | MFRC630_TCONTROL_START_TX_END);
// 	// // Frame waiting time: FWT = (256 x 16/fc) x 2 FWI
// 	// // FWI defaults to four... so that would mean wait for a maximum of ~ 5ms
// 	// mfrc630_timer_set_reload(timer_for_timeout, 2000);  // 2000 ticks of 5 usec is 10 ms.
// 	// mfrc630_timer_set_value(timer_for_timeout, 2000);

// 	if err := d.transceive(_MF_CMD_READ, block_address); err != nil {
// 		return 0, fmt.Errorf("clrc663: read block: %w", err)
// 	}
// 	fmt.Println("***** SET UP TIMER ******")
// 	const timer_for_timeout = 0
// 	if err := d.waitForRx(timer_for_timeout); err != nil {
// 		return 0, fmt.Errorf("clrc663: %w", err)
// 	}

// 	n, err := d.readFIFO(blk)
// 	if err != nil {
// 		return 0, fmt.Errorf("clrc663: read block: %w", err)
// 	}
// 	return n, nil
// }

// func (d *Device) iso14443aRead() error {
// 	if err := d.reqa(); err != nil {
// 		return err
// 	}
// 	uid, sak, err := d.selectTag()
// 	if err != nil {
// 		return err
// 	}
// 	fmt.Printf("UID %x sak %#x\n", uid, sak)
// 	blk := make([]byte, type2BlkSize*4)
// 	// Read first TLV starting at block 4.
// 	n, err := d.readBlock(4, blk[:type2BlkSize])
// 	if err != nil {
// 		return err
// 	}
// 	if n < 4 {
// 		return errors.New("clrc663: block too short")
// 	}
// 	header := blk[:4]
// 	typ := header[0]
// 	length := int(header[1])
// 	content := new(bytes.Buffer)
// 	if length == 0xff {
// 		// 2-byte length
// 		length = int(header[3])<<8 | int(header[2])
// 	} else {
// 		// Rest of the block is content.
// 		rem := min(length, 2)
// 		content.Write(header[2 : 2+rem])
// 	}
// 	rem := length - content.Len()
// 	bno := uint8(4 + 1)
// 	for rem > 0 {
// 		siz := min(len(blk), rem)
// 		n, err := d.readBlock(bno, blk[:siz])
// 		content.Write(blk[:n])
// 		rem -= n
// 		bno += 4
// 		if err != nil {
// 			return err
// 		}
// 		if n < siz {
// 			break
// 		}
// 	}

// 	const ndefType = 0x03
// 	switch typ {
// 	case ndefType:
// 		msg := content.Bytes()
// 		fmt.Printf("NFC Scan result: %x %q\n", content.Bytes(), content.String())
// 		header, tlen, plen := msg[0], msg[1], msg[2]
// 		if header != 0b11010_001 || tlen != 1 { // TODO: do better
// 			break
// 		}
// 		typ := msg[3]
// 		if typ != 0x55 { // TODO: handle other well-known types.
// 			break
// 		}
// 		fmt.Print("\n\nNFC result, parsed *****:    ")
// 		payload := msg[4 : 4+plen]
// 		switch payload[0] {
// 		case 0x04:
// 			fmt.Print("https://")
// 		}
// 		fmt.Println(string(payload[1:]), "\n\n")
// 	}
// 	return nil
// }

const (
	cmdREQA = 0x26
)
