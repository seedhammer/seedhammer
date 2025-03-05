//go:build tinygo

// Package clrc663 implements a TinyGo driver for the CLRC663 NFC writer.
//
// Datasheet: https://www.nxp.com/docs/en/data-sheet/CLRC663.pdf
package clrc663

import (
	"bytes"
	"errors"
	"fmt"
	"machine"
	"time"
)

type Device struct {
	bus *machine.I2C

	scratch [2]byte
}

func New(bus *machine.I2C) *Device {
	return &Device{
		bus: bus,
	}
}

func (d *Device) Configure() error {
	if err := d.runCommand(cmdSoftReset); err != nil {
		return fmt.Errorf("clrc663: soft reset: %w", err)
	}
	return nil
}

func (d *Device) SetPadEnable(padEn uint8) error {
	return d.writeRegs(regPadEn, padEn)
}

func (d *Device) SetPadOutput(padOut uint8) error {
	return d.writeRegs(regPadOut, padOut)
}

func (d *Device) readReg(reg uint8) (uint8, error) {
	if err := d.bus.Tx(i2cAddr, []byte{reg}, d.scratch[:1]); err != nil {
		return 0, fmt.Errorf("clrc663: read register %#x: %w", reg, err)
	}
	return d.scratch[0], nil
}

func (d *Device) readRegs(reg uint8, val []uint8) error {
	if err := d.bus.Tx(i2cAddr, []byte{reg}, val); err != nil {
		return fmt.Errorf("clrc663:read registers %#x (%d): %w", reg, len(val), err)
	}
	return nil
}

func (d *Device) readFIFO(data []byte) (int, error) {
	rx_len, err := d.readReg(regFIFOLength)
	if err != nil {
		return 0, fmt.Errorf("clrc663:read FIFOLength: %w", err)
	}

	n := min(int(rx_len), len(data))
	if err := d.readRegs(regFIFOData, data[:n]); err != nil {
		return 0, fmt.Errorf("clrc663:read FIFOData: %w", err)
	}
	return n, nil
}

// writeRegs write a list of (register, value) pairs.
func (d *Device) writeRegs(regVals ...uint8) error {
	if len(regVals)%2 != 0 {
		panic("register values not paired")
	}
	for i := 0; i < len(regVals); i += 2 {
		if err := d.bus.Tx(i2cAddr, regVals[i:i+2], nil); err != nil {
			return fmt.Errorf("clrc663: %w", err)
		}
	}
	return nil
}

func (d *Device) writeFIFO(data ...uint8) error {
	// Idle and flush FIFO.
	if err := d.writeRegs(
		regCommand, cmdIdle,
		regFIFOControl, 1<<4,
	); err != nil {
		return err
	}
	data = append([]byte{regFIFOData}, data...)
	if err := d.bus.Tx(i2cAddr, data, nil); err != nil {
		return err
	}
	return nil
}

func (d *Device) runCommand(cmd uint8, args ...uint8) error {
	if err := d.writeFIFO(args...); err != nil {
		return err
	}
	if err := d.writeRegs(regCommand, cmd); err != nil {
		return fmt.Errorf("command %#x: %w", cmd, err)
	}
	return d.waitForIRQ(irqIdle, 0)
}

func (d *Device) waitForIRQ(irq0Mask, irq1Mask uint8) error {
	irqs := d.scratch[:2]
	for {
		if err := d.readRegs(regIRQ0, irqs); err != nil {
			return fmt.Errorf("read IRQ0-1: %w", err)
		}
		irq0, irq1 := irqs[0], irqs[1]
		if irq0&irqErr != 0 {
			e, err := d.readReg(regError)
			if err != nil {
				return fmt.Errorf("read Error: %w", err)
			}
			return fmt.Errorf("command error: %#x", e)
		}
		if irq0&irq0Mask != 0 || irq1&irq1Mask != 0 {
			return nil
		}
	}
}

func (d *Device) transceive(data ...uint8) error {
	if err := d.writeFIFO(data...); err != nil {
		return err
	}
	if err := d.writeRegs(regCommand, cmdTransceive); err != nil {
		return err
	}
	return nil
}

func (d *Device) reqa() error {
	// ready the request.
	const ISO14443_CMD_REQA = 0x26
	// configure a timeout timer_for_timeout.
	const timer_for_timeout = 0

	// Set timer to 221 kHz clock, start at the end of Tx.
	const MFRC630_TCONTROL_CLK_211KHZ = 0b01
	const MFRC630_TCONTROL_START_TX_END = 0b01 << 4
	const reloadTicks = 1000

	if err := d.writeRegs(
		regCommand, cmdIdle,
		regFIFOControl, 1<<4,
		// Set register such that we sent 7 bits, set DataEn such that we can send
		// data.
		regTxDataNum, 7|TxDataNumDataEn,

		// disable the CRC registers.
		regTxCrcPreset, 0x18|0,
		regRxCrcPreset, 0x18|0,

		regRxBitCtrl, 0,

		// clear interrupts.
		regIRQ0, 0x7F,
		regIRQ1, 0x7F,

		regT0Control+(5*timer_for_timeout), MFRC630_TCONTROL_CLK_211KHZ|MFRC630_TCONTROL_START_TX_END,
		// Frame waiting time: FWT = (256 x 16/fc) x 2 FWI
		// FWI defaults to four... so that would mean wait for a maximum of ~ 5ms

		regT0ReloadHi+(5*timer_for_timeout), reloadTicks>>8, // 1000 ticks of 5 usec is 5 ms.
		regT0ReloadLo+(5*timer_for_timeout), reloadTicks&0xFF,
		regT0CounterValHi+(5*timer_for_timeout), reloadTicks>>8,
		regT0CounterValLo+(5*timer_for_timeout), reloadTicks&0xFF,

		// Go into send, then straight after in receive.
		regCommand, cmdIdle,
		regFIFOControl, 1<<4,
		regFIFOData, ISO14443_CMD_REQA,
		regCommand, cmdTransceive,
	); err != nil {
		return fmt.Errorf("clrc663: reqa: %w", err)
	}
	fmt.Println("Sending REQA\n")
	// block until we are done
	irqs := make([]byte, 2)
	for {
		if err := d.readRegs(regIRQ0, irqs); err != nil {
			return fmt.Errorf("clrc663: reqa: %w", err)
		}
		irq0, irq1 := irqs[0], irqs[1]
		// either ERR_IRQ or RX_IRQ or timeout
		if irq0&(irqRx|irqErr) != 0 || irq1&(1<<timer_for_timeout) != 0 {
			break
		}
	}
	if err := d.writeRegs(regCommand, cmdIdle); err != nil {
		return fmt.Errorf("clrc663: reqa: %v", err)
	}
	if err := d.readRegs(regIRQ0, irqs); err != nil {
		return fmt.Errorf("clrc663: reqa: %w", err)
	}
	irq0, irq1 := irqs[0], irqs[1]
	fmt.Printf("After waiting for answer, IRQ1: %.8b\n", irq1)

	// if no Rx IRQ, or if there's an error somehow, return 0
	if irq0&irqRx == 0 || irq0&irqErr != 0 {
		fmt.Printf("No RX, irq1: %.8b irq0: %.8b\n", irq1, irq0)
	}

	rx_len, err := d.readReg(regFIFOLength)
	if err != nil {
		return fmt.Errorf("clrc663: reqa: %w", err)
	}
	fmt.Printf("rx_len: %d\n", rx_len)
	if rx_len != 2 {
		// ATQA should answer with 2 bytes.
		return fmt.Errorf("clrc663: want 2 atqa bytes, got %d", rx_len)
	}
	res := make([]byte, rx_len)
	if err := d.readRegs(regFIFOData, res); err != nil {
		return fmt.Errorf("clrc663: reqa: %w", err)
	}

	atqa := uint16(res[1])<<8 | uint16(res[0])
	fmt.Printf("ATQA answer: %#.4x\n", atqa)
	return nil
}

func (d *Device) readBlock(block_address uint8, blk []byte) (int, error) {
	if err := d.writeRegs(
		regCommand, cmdIdle,
		regFIFOControl, 1<<4, // Flush FIFO.
		regTxCrcPreset, _RECOM_14443A_CRC|_CRC_ON,
		regRxCrcPreset, _RECOM_14443A_CRC|_CRC_ON,
		regIRQ0, 0x7F,
		regIRQ1, 0x7F,
	); err != nil {
		return 0, fmt.Errorf("read block: %w", err)
	}

	// // configure a timeout timer.
	// uint8_t timer_for_timeout = 0;  // should match the enabled interupt.

	// Set timer to 221 kHz clock, start at the end of Tx.
	// mfrc630_timer_set_control(timer_for_timeout, MFRC630_TCONTROL_CLK_211KHZ | MFRC630_TCONTROL_START_TX_END);
	// // Frame waiting time: FWT = (256 x 16/fc) x 2 FWI
	// // FWI defaults to four... so that would mean wait for a maximum of ~ 5ms
	// mfrc630_timer_set_reload(timer_for_timeout, 2000);  // 2000 ticks of 5 usec is 10 ms.
	// mfrc630_timer_set_value(timer_for_timeout, 2000);

	if err := d.transceive(_MF_CMD_READ, block_address); err != nil {
		return 0, fmt.Errorf("clrc663: read block: %w", err)
	}
	if err := d.waitForIRQ(irqIdle, 0); err != nil {
		return 0, fmt.Errorf("clrc663: read block: %w", err)
	}
	irqs := d.scratch[:2]
	if err := d.readRegs(regIRQ0, irqs); err != nil {
		panic(err)
	}
	irq0, irq1 := irqs[0], irqs[1]
	if err := d.writeRegs(regCommand, cmdIdle); err != nil {
		return 0, fmt.Errorf("clrc663: read block: %w", err)
	}

	n, err := d.readFIFO(blk)
	if err != nil {
		return 0, fmt.Errorf("clrc663: read block: %w", err)
	}
	fmt.Printf("irq0 %.8b irq1 %.8b\n", irq0, irq1)
	return n, nil
}

func (d *Device) selectTag() ([]byte, uint8, error) {
	// configure a timeout timer, use timer 0.
	const timer_for_timeout = 0
	const reloadTicks = 1000

	fmt.Printf("\nStarting select\n")
	uid := make([]byte, 10) // Maximum UID length.

	// we do not need atqa.
	// Bitshift to get uid_size; 0b00: single, 0b01: double, 0b10: triple, 0b11 RFU
	// uint8_t uid_size = (atqa & (0b11 << 6)) >> 6;
	// uint8_t bit_frame_collision = atqa & 0b11111;

	err := d.writeRegs(
		regCommand, cmdIdle,
		// mfrc630_AN1102_recommended_registers_no_transmitter(MFRC630_PROTO_ISO14443A_106_MILLER_MANCHESTER);
		regFIFOControl, 1<<4, // Flush FIFO.

		// enable the global IRQ for Rx done and Errors.
		regIRQ0En, irqRx|irqErr,
		regIRQ1En, 0b1<<timer_for_timeout, // only trigger on timer for irq1

		// Set timer to 221 kHz clock, start at the end of Tx.
		regT0Control+(5*timer_for_timeout), t0Clk_211kHz|t0Start_TxEnd,
		// Frame waiting time: FWT = (256 x 16/fc) x 2 FWI
		// FWI defaults to four... so that would mean wait for a maximum of ~ 5ms

		regT0ReloadHi+(5*timer_for_timeout), reloadTicks>>8, // 1000 ticks of 5 usec is 5 ms.
		regT0ReloadLo+(5*timer_for_timeout), reloadTicks&0xFF,
		regT0CounterValHi+(5*timer_for_timeout), reloadTicks>>8,
		regT0CounterValLo+(5*timer_for_timeout), reloadTicks&0xFF,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("clrc663: select: %w", err)
	}

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
		if err := d.writeRegs(
			regTxCrcPreset, _RECOM_14443A_CRC|_CRC_OFF,
			regRxCrcPreset, _RECOM_14443A_CRC|_CRC_OFF,
		); err != nil {
			return nil, 0, fmt.Errorf("clrc663: select: %w", err)
		}

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
				if error&error_CollDet == 0 {
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
			if error&error_CollDet != 0 {
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

func (d *Device) measureLPCD() error {
	// Part-1, configurate LPCD Mode
	// Please remove any PICC from the HF of the reader.
	// "I" and the "Q" values read from reg 0x42 and 0x43
	// shall be used in part-2 "Detect PICC"
	d.writeRegs(
		regCommand, 0,
		regFIFOControl, 0xB0, // Flush FIFO
		// LPCD_config
		regLPCD_QMin, 0xC0, // Set Qmin register
		regLPCD_QMax, 0xFF, // Set Qmax register
		regLPCD_IMin, 0xC0, // Set Imin register
		regDrvMode, 0x89, // set DrvMode register
		regLPCD_Options, lpcdTxHigh|lpcdFilter, // Stronger detection field, filter measurements.
		// Execute trimming procedure
		regT3ReloadHi, 0x00, // Write default. T3 reload value Hi
		regT3ReloadLo, 0x10, // Write default. T3 reload value Lo
		regT4ReloadHi, 0x00, // Write min. T4 reload value Hi
		regT4ReloadLo, 0x05, // Write min. T4 reload value Lo
		regT4Control, 0b1111_1000, // Config T4 for AutoLPCD&AutoRestart.Set AutoTrimm bit.Start T4.
		regRcv, 0x52, // Set Rx_ADCmode bit
		regRxAna, 0x03, // Raise receiver gain to maximum
		regCommand, cmdLPCD, // Execute Rc663 command "Auto_T4" (Low power card detection and/or Auto trimming)
	)

	//> ------------ I and Q Value for LPCD ----------------
	I_Q := make([]byte, 2)
	for {
		// Measure.
		time.Sleep(100 * time.Millisecond)

		if err := d.readRegs(regLPCD_I_Result, I_Q); err != nil {
			return fmt.Errorf("clrc663: lpcd calibration: %w", err)
		}
		I := I_Q[0] & 0x3F
		Q := I_Q[1] & 0x3F
		fmt.Println(I, Q)
	}
	d.writeRegs(
		regCommand, 0x00,
		regRcv, 0x12, // Clear Rx_ADCmode bit
	)
	return nil
}

func (d *Device) TestDump() error {
	// Load preset protocol registers.
	if err := d.runCommand(
		cmdLoadProtocol,
		protocol_ISO14443A_106_MILLER_MANCHESTER, protocol_ISO14443A_106_MILLER_MANCHESTER,
		// protocol_ISO15693_26_SSC, protocol_ISO15693_26_SSC,
	); err != nil {
		return fmt.Errorf("clrc663: LoadProtocol: %w", err)
	}

	// Load preset antenna registers.
	const (
		eepromAddr = eepromAddrISO14443A_106
		// eepromAddr   = eepromAddrISO15693_SLI_1_4_SSC_26
		eepromLength = regRxAna - regDrvMode + 1
	)
	if err := d.runCommand(
		cmdLoadReg,
		// Source EEPROM address.
		uint8(eepromAddr>>8), uint8(eepromAddr&0xff),
		// Destination register
		regDrvMode,
		// Length
		eepromLength,
	); err != nil {
		return fmt.Errorf("clrc663: LoadReg: %w", err)
	}
	// if err := d.measureLPCD(); err != nil {
	// 	return err
	// }
	if err := d.reqa(); err != nil {
		return err
	}
	uid, sak, err := d.selectTag()
	if err != nil {
		return err
	}
	fmt.Printf("UID %x sak %#x\n", uid, sak)
	blk := make([]byte, type2BlkSize*4)
	// Read first TLV starting at block 4.
	n, err := d.readBlock(4, blk[:type2BlkSize])
	if err != nil {
		return err
	}
	if n < 4 {
		return errors.New("clrc663: block too short")
	}
	header := blk[:4]
	typ := header[0]
	length := int(header[1])
	content := new(bytes.Buffer)
	if length == 0xff {
		// 2-byte length
		length = int(header[3])<<8 | int(header[2])
	} else {
		// Rest of the block is content.
		rem := min(length, 2)
		content.Write(header[2 : 2+rem])
	}
	rem := length - content.Len()
	bno := uint8(4 + 1)
	for rem > 0 {
		siz := min(len(blk), rem)
		n, err := d.readBlock(bno, blk[:siz])
		content.Write(blk[:n])
		rem -= n
		bno += 4
		if err != nil {
			return err
		}
		if n < siz {
			break
		}
	}
	switch typ {
	case ndefType:
		msg := content.Bytes()
		fmt.Printf("NFC Scan result: %x %q\n", content.Bytes(), content.String())
		header, tlen, plen := msg[0], msg[1], msg[2]
		if header != 0b11010_001 || tlen != 1 { // TODO: do better
			break
		}
		typ := msg[3]
		if typ != 0x55 { // TODO: handle other well-known types.
			break
		}
		fmt.Print("\n\nNFC result, parsed *****:    ")
		payload := msg[4 : 4+plen]
		switch payload[0] {
		case 0x04:
			fmt.Print("https://")
		}
		fmt.Println(string(payload[1:]), "\n\n")
	}
	return nil
}

const (
	i2cAddr = 0b01010_00 // Last two bits depend on pin settings.

	regCommand          = 0x00 //  Starts and stops command execution
	regHostCtrl         = 0x01 //  Host control register
	regFIFOControl      = 0x02 //  Control register of the FIFO
	regWaterLevel       = 0x03 //  Level of the FIFO underflow and overflow warning
	regFIFOLength       = 0x04 //  Length of the FIFO
	regFIFOData         = 0x05 //  Data In/Out exchange register of FIFO buffer
	regIRQ0             = 0x06 //  Interrupt register 0
	regIRQ1             = 0x07 //  Interrupt register 1
	regIRQ0En           = 0x08 //  Interrupt enable register 0
	regIRQ1En           = 0x09 //  Interrupt enable register 1
	regError            = 0x0A //  Error bits showing the error status of the last command execution
	regStatus           = 0x0B //  Contains status of the communication
	regRxBitCtrl        = 0x0C //  Control register for anticollision adjustments for bit oriented protocols
	regRxColl           = 0x0D //  Collision position register
	regTControl         = 0x0E //  Control of Timer 0..3
	regT0Control        = 0x0F //  Control of Timer0
	regT0ReloadHi       = 0x10 //  High register of the reload value of Timer0
	regT0ReloadLo       = 0x11 //  Low register of the reload value of Timer0
	regT0CounterValHi   = 0x12 //  Counter value high register of Timer0
	regT0CounterValLo   = 0x13 //  Counter value low register of Timer0
	regT1Control        = 0x14 //  Control of Timer1
	regT1ReloadHi       = 0x15 //  High register of the reload value of Timer1
	regT1ReloadLo       = 0x16 //  Low register of the reload value of Timer1
	regT1CounterValHi   = 0x17 //  Counter value high register of Timer1
	regT1CounterValLo   = 0x18 //  Counter value low register of Timer1
	regT2Control        = 0x19 //  Control of Timer2
	regT2ReloadHi       = 0x1A //  High byte of the reload value of Timer2
	regT2ReloadLo       = 0x1B //  Low byte of the reload value of Timer2
	regT2CounterValHi   = 0x1C //  Counter value high byte of Timer2
	regT2CounterValLo   = 0x1D //  Counter value low byte of Timer2
	regT3Control        = 0x1E //  Control of Timer3
	regT3ReloadHi       = 0x1F //  High byte of the reload value of Timer3
	regT3ReloadLo       = 0x20 //  Low byte of the reload value of Timer3
	regT3CounterValHi   = 0x21 //  Counter value high byte of Timer3
	regT3CounterValLo   = 0x22 //  Counter value low byte of Timer3
	regT4Control        = 0x23 //  Control of Timer4
	regT4ReloadHi       = 0x24 //  High byte of the reload value of Timer4
	regT4ReloadLo       = 0x25 //  Low byte of the reload value of Timer4
	regT4CounterValHi   = 0x26 //  Counter value high byte of Timer4
	regT4CounterValLo   = 0x27 //  Counter value low byte of Timer4
	regDrvMode          = 0x28 //  Driver mode register
	regTxAmp            = 0x29 //  Transmitter amplifier register
	regDrvCon           = 0x2A //  Driver configuration register
	regTxl              = 0x2B //  Transmitter register
	regTxCrcPreset      = 0x2C //  Transmitter CRC control register, preset value
	regRxCrcPreset      = 0x2D //  Receiver CRC control register, preset value
	regTxDataNum        = 0x2E //  Transmitter data number register
	regTxModWidth       = 0x2F //  Transmitter modulation width register
	regTxSym10BurstLen  = 0x30 //  Transmitter symbol 1 + symbol 0 burst length register
	regTXWaitCtrl       = 0x31 //  Transmitter wait control
	regTxWaitLo         = 0x32 //  Transmitter wait low
	regFrameCon         = 0x33 //  Transmitter frame control
	regRxSofD           = 0x34 //  Receiver start of frame detection
	regRxCtrl           = 0x35 //  Receiver control register
	regRxWait           = 0x36 //  Receiver wait register
	regRxThreshold      = 0x37 //  Receiver threshold register
	regRcv              = 0x38 //  Receiver register
	regRxAna            = 0x39 //  Receiver analog register
	regLPCD_Options     = 0x3A //  LPCD options (CLRC66303 only)
	regSerialSpeed      = 0x3B //  Serial speed register
	regLFO_Trimm        = 0x3C //  Low-power oscillator trimming register
	regPLL_Ctrl         = 0x3D //  IntegerN PLL control register, for microcontroller clock output adjustment
	regPLL_DivOut       = 0x3E //  IntegerN PLL control register, for microcontroller clock output adjustment
	regLPCD_QMin        = 0x3F //  Low-power card detection Q channel minimum threshold
	regLPCD_QMax        = 0x40 //  Low-power card detection Q channel maximum threshold
	regLPCD_IMin        = 0x41 //  Low-power card detection I channel minimum threshold
	regLPCD_I_Result    = 0x42 //  Low-power card detection I channel result register
	regLPCD_Q_Result    = 0x43 //  Low-power card detection Q channel result register
	regPadEn            = 0x44 //  PIN enable register
	regPadOut           = 0x45 //  PIN out register
	regPadIn            = 0x46 //  PIN in register
	regSigOut           = 0x47 //  Enables and controls the SIGOUT Pin
	regTxBitMod         = 0x48 //  Transmitter bit mode register
	regTxDataCon        = 0x4A //  Transmitter data configuration register
	regTxDataMod        = 0x4B //  Transmitter data modulation register
	regTxSymFreq        = 0x4C //  Transmitter symbol frequency
	regTxSym0H          = 0x4D //  Transmitter symbol 0 high register
	regTxSym0L          = 0x4E //  Transmitter symbol 0 low register
	regTxSym1H          = 0x4F //  Transmitter symbol 1 high register
	regTxSym1L          = 0x50 //  Transmitter symbol 1 low register
	regTxSym2           = 0x51 //  Transmitter symbol 2 register
	regTxSym3           = 0x52 //  Transmitter symbol 3 register
	regTxSym10Len       = 0x53 //  Transmitter symbol 1 + symbol 0 length register
	regTxSym32Len       = 0x54 //  Transmitter symbol 3 + symbol 2 length register
	regTxSym10BurstCtrl = 0x55 //  Transmitter symbol 1 + symbol 0 burst control register
	regTxSym10Mod       = 0x56 //  Transmitter symbol 1 + symbol 0 modulation register
	regTxSym32Mod       = 0x57 //  Transmitter symbol 3 + symbol 2 modulation register
	regRxBitMod         = 0x58 //  Receiver bit modulation register
	regRxEofSym         = 0x59 //  Receiver end of frame symbol register
	regRxSyncValH       = 0x5A //  Receiver synchronisation value high register
	regRxSyncValL       = 0x5B //  Receiver synchronisation value low register
	regRxSyncMod        = 0x5C //  Receiver synchronisation mode register
	regRxMod            = 0x5D //  Receiver modulation register
	regRxCorr           = 0x5E //  Receiver correlation register
	regFabCal           = 0x5F //  Calibration register of the receiver, calibration performed at production
	regVersion          = 0x7F //  Version and subversion register

	irqErr    = 0b1 << 1
	irqRx     = 0b1 << 2
	irqTx     = 0b1 << 3
	irqIdle   = 0b1 << 4
	irqGlobal = 0b1 << 6

	cmdIdle         = 0x00
	cmdLPCD         = 0x01
	cmdReceive      = 0x05
	cmdTransceive   = 0x07
	cmdLoadReg      = 0x0c
	cmdLoadProtocol = 0x0d
	cmdSoftReset    = 0x1f

	TxDataNumDataEn = 0b1 << 3

	t0Clk_211kHz  = 0b01
	t0Start_TxEnd = 0b01 << 4

	drvModeTx2Inv = 0b1 << 7
	drvModeTxEn   = 0b1 << 3

	lpcdFilter = 0b1 << 2
	lpcdTxHigh = 0b1 << 3

	lpcdIRQClr = 0b1 << 6

	irq0ErrEn     = 0b1 << 1
	irq0RxEn      = 0b1 << 2
	irq0IdleEn    = 0b1 << 4
	irq0LoAlertEn = 0b1 << 5
	irq0Inv       = 0b1 << 7

	irq1LPCDEn = 0b1 << 5
	irq1PinEn  = 0b1 << 6

	error_CollDet = 1 << 2

	_ISO14443_CAS_LEVEL_1 = 0x93
	_ISO14443_CAS_LEVEL_2 = 0x95
	_ISO14443_CAS_LEVEL_3 = 0x97

	_RECOM_14443A_CRC = 0x18
	_CRC_OFF          = 0
	_CRC_ON           = 1
	_TXDATANUM_DATAEN = 1 << 3

	_MF_CMD_READ = 0x30 //!< To read a block from mifare card.

	type2BlkSize = 4

	ndefType = 0x03
)

// Protocol numbers for the LoadProtocol command.
const (
	protocol_ISO14443A_106_MILLER_MANCHESTER = 0
	protocol_ISO15693_26_SSC                 = 10
)

// Antenna configuration EEPROM addresses.
const (
	eepromAddrISO14443A_106           = 0xc0
	eepromAddrISO15693_SLI_1_4_SSC_26 = 0x194
)
