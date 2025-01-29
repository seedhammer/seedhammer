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
	err error

	scratch [2]byte
}

func New(bus *machine.I2C) *Device {
	return &Device{
		bus: bus,
	}
}

func (d *Device) readReg(reg uint8) (uint8, error) {
	if err := d.bus.Tx(i2cAddr, []byte{reg}, d.scratch[:1]); err != nil {
		return 0, fmt.Errorf("read register %#x: %w", reg, err)
	}
	return d.scratch[0], nil
}

func (d *Device) readRegs(reg uint8, val []uint8) error {
	if err := d.bus.Tx(i2cAddr, []byte{reg}, val); err != nil {
		return fmt.Errorf("read registers %#x (%d): %w", reg, len(val), err)
	}
	return nil
}

func (d *Device) readFIFO(data []byte) (int, error) {
	rx_len, err := d.readReg(regFIFOLength)
	if err != nil {
		return 0, fmt.Errorf("read FIFOLength: %w", err)
	}

	n := min(int(rx_len), len(data))
	if err := d.readRegs(regFIFOData, data[:n]); err != nil {
		return 0, fmt.Errorf("read FIFOData: %w", err)
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

func (d *Device) reqa2() error {
	if err := d.writeRegs(
		//> =============================================
		//>  I14443p3a_Sw_RequestA
		//> =============================================
		regTXWaitCtrl, 0xC0, //  TxWaitStart at the end of Rx data
		regTxWaitLo, 0x0B, // Set min.time between Rx and Tx or between two Tx
		regT0ReloadHi, 0x08, //> Set timeout for this command cmd. Init reload values for timers-0,1
		regT0ReloadLo, 0x94,
		regT1ReloadHi, 0x00,
		regT1ReloadLo, 0x00,
		regRxWait, 0x90, // bit9,If set to 1, the RxWait time is RxWait x(0.5/DBFreq).  bit0--bit6,Defines the time after sending, where every input is ignored
		regTxDataNum, 0x0F,
		regCommand, cmdIdle, // Terminate any running command.
		regFIFOControl, 0xB0, // Flush_FiFo

		regIRQ0, 0x7F, // Clear all IRQ 0,1 flags
		regIRQ1, 0x7F, //
	); err != nil {
		return fmt.Errorf("clrc663: reqa: %w", err)
	}

	time.Sleep(10 * time.Millisecond)
	//> ---------------------
	//> Send the ReqA command
	//> ---------------------
	if err := d.writeRegs(
		regFIFOData, 0x26, //Write ReqA=26(wake up all the idle card ,not sleeping，0x52) into FIFO
		regCommand, cmdTransceive, // Start RC663 command "Transcieve"=0x07. Activate Rx after Tx finishes.
	); err != nil {
		return fmt.Errorf("clrc663: reqa: %w", err)
	}
	const timer_for_timeout = 1
	irqs := make([]byte, 2)
	for {
		if err := d.readRegs(regIRQ0, irqs); err != nil {
			return fmt.Errorf("clrc663: select: %w", err)
		}
		irq0, irq1 := irqs[0], irqs[1]
		// either ERR_IRQ or RX_IRQ or Timer
		if irq0&irqRx != 0 {
			break // stop polling irq1 and quit the timeout loop.
		}
		if irq0&irqErr != 0 || irq1&(0b1<<timer_for_timeout) != 0 {
			e, err := d.readReg(regError)
			if err != nil {
				panic(err)
			}
			fmt.Printf("error: %#.8b\n", e)
			return fmt.Errorf("clrc663: reqa timeout or error: irq0 %#.8b irq1 %#.8b", irq0, irq1)
		}
	}
	len, err := d.readReg(regFIFOLength) //read FIFO length
	if err != nil {
		return fmt.Errorf("clrc663: reqa: %w", err)
	}
	atqa := make([]byte, len)
	if err := d.readRegs(regFIFOData, atqa); err != nil {
		return fmt.Errorf("clrc663: reqa: %w", err)
	}
	fmt.Println("atqa", atqa)
	return nil
}

func (d *Device) init2() error {
	if err := d.writeRegs(
		regT0Control, 0x98, //Starts at the end of Tx. Stops after Rx of first data. Auto-reloaded. 13.56 MHz input clock.
		regT1Control, 0x92, //Starts at the end of Tx. Stops after Rx of first data. Input clock - cascaded with Timer-0.
		regT2Control, 0x20, //Set Timer-2, T2Control_Reg:  Timer used for LFO trimming
		regT2ReloadHi, 0x03, //Set Timer-2 reload value (T2ReloadHi_Reg and T2ReloadLo_Reg)
		regT2ReloadLo, 0xFF, //
		regT3Control, 0x00, // Not started automatically. Not reloaded. Input clock 13.56 MHz
		regFIFOControl, 0x10,

		regWaterLevel, 0xFE, //Set WaterLevel =(FIFO length -1),cause fifo length has been set to 255=0xff,so water level is oxfe
		regRxBitCtrl, 0x80, //RxBitCtrl_Reg(0x0c)  Received bit after collision are replaced with 1.
		regDrvMode, 0x80, //DrvMod reg(0x28), Tx2Inv=1,Inverts transmitter 1 at TX1 pin
		regTxAmp, 0x00, // TxAmp_Reg(0x29),output amplitude  0: TVDD -100 mV(maxmum)
		regDrvCon, 0x01, // TxCon register (address 2Ah),TxEnvelope
		regTxl, 0x05, //
		regRxSofD, 0x00, //
		regRcv, 0x12, //
		regCommand, cmdIdle, // Terminate any running command.
		regFIFOControl, 0xB0, // Flush_FiFo,low alert
		regIRQ0, 0x7F, // Clear all IRQ 0,1 flags
		regIRQ1, 0x7F, //
		//> =============================================
		//>  LoadProtocol( bTxProtocol=0, bRxProtocol=0)
		//> =============================================
		//> Write in Fifo: Tx and Rx protocol numbers(0,0)
		regFIFOData, 0x00, //
		regFIFOData, 0x00, //
		regCommand, cmdLoadProtocol, // Start RC663 command "Load Protocol"=0x0d
	); err != nil {
		return fmt.Errorf("clrc663: init: %w", err)
	}

	// Wait for idle interrupt meaning the command has finished.
	for {
		irq0, err := d.readReg(regIRQ0)
		if err != nil {
			return fmt.Errorf("clrc663: init: %w", err)
		}
		if irq0&irqIdle != 0 {
			break // stop polling irq1 and quit the timeout loop.
		}
	}

	if err := d.writeRegs(
		regFIFOControl, 0xB0, // Flush_FiFo

		// Apply RegisterSet
		//
		//> Configure CRC-16 calculation, preset value(0x6363) for Tx&Rx

		regTxCrcPreset, 0x18, //means preset value is 6363,and uses CRC 16,but CRC is not automaticlly apended to the data
		regRxCrcPreset, 0x18, //

		regTxDataNum, 0x08, //
		regTxModWidth, 0x20, // Length of the pulse modulation in carrier clks+1
		regTxSym10BurstLen, 0x00, // Symbol 1 and 0 burst lengths = 8 bits.
		regFrameCon, 0xCF, // Start symbol=Symbol2, Stop symbol=Symbol3
		regRxCtrl, 0x04, // Set Rx Baudrate 106 kBaud

		regRxThreshold, 0x32, // Set min-levels for Rx and phase shift
		regRxAna, 0x00,
		regRxWait, 0x90, // Set Rx waiting time
		regTXWaitCtrl, 0xC0,
		regTxWaitLo, 0x0B,
		regT0ReloadHi, 0x08, // Set Timeout. Write T0,T1 reload values(hi,Low)
		regT0ReloadLo, 0xD8,
		regT1ReloadHi, 0x00,
		regT1ReloadLo, 0x00,

		regDrvMode, 0x81, // Write DrvMod register

		//> MIFARE Crypto1 state is further disabled.
		regStatus, 0x00,
	); err != nil {
		return fmt.Errorf("clrc663: init: %w", err)
	}
	return nil
}

func (d *Device) selectTag2() error {
	// Get UID, Apply cascade level-1
	if err := d.writeRegs(
		regTxDataNum, 0x08, //BIT3 If cleared - it is possible to send a single symbol pattern.If set - data is sent.
		regRxBitCtrl, 0x00, //
	); err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}

	if err := d.writeFIFO(
		0x93, //Write "Select" cmd into FIFO (SEL=93, NVB=20,cascade level-1)
		0x20, //字节计数=2
	); err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}
	const timer_for_timeout = 1
	if err := d.writeRegs(regCommand, cmdTransceive); err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}
	irqs := make([]byte, 2)
	for {
		if err := d.readRegs(regIRQ0, irqs); err != nil {
			return fmt.Errorf("clrc663: select: %w", err)
		}
		irq0, irq1 := irqs[0], irqs[1]
		// either ERR_IRQ or RX_IRQ or Timer
		if irq0&irqRx != 0 {
			break // stop polling irq1 and quit the timeout loop.
		}
		if irq0&irqErr != 0 || irq1&(0b1<<timer_for_timeout) != 0 {
			e, err := d.readReg(regError)
			if err != nil {
				panic(err)
			}
			fmt.Printf("error: %#.8b\n", e)
			return fmt.Errorf("clrc663: select timeout or error: irq0 %#.8b irq1 %#.8b", irq0, irq1)
		}
	}

	rx_len, err := d.readReg(regFIFOLength) //read FIFO length
	if err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}
	uid := make([]byte, rx_len)
	if err := d.readRegs(regFIFOData, uid); err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}

	//now we got UID ,we continue to use this UID to select the card
	//this command needs CRC appended
	if err := d.writeRegs(
		regTxCrcPreset, 0x19, //preset value is6363,use crc16,crc is apended to the data stream
		regRxCrcPreset, 0x19, //
	); err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}

	if err := d.writeFIFO(
		0x93, //select
		0x70, //字节计数=7
	); err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}
	if err := d.writeFIFO(uid...); err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}
	//Start tranceive command ,expecting to receive SAK ,select acknowlegement
	if err := d.writeRegs(regCommand, cmdTransceive); err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}
	for {
		if err := d.readRegs(regIRQ0, irqs); err != nil {
			return fmt.Errorf("clrc663: select: %w", err)
		}
		irq0, irq1 := irqs[0], irqs[1]
		// either ERR_IRQ or RX_IRQ or Timer
		if irq0&irqRx != 0 {
			break // stop polling irq1 and quit the timeout loop.
		}
		if irq0&irqErr != 0 || irq1&(0b1<<timer_for_timeout) != 0 {
			e, err := d.readReg(regError)
			if err != nil {
				panic(err)
			}
			fmt.Printf("error2: %#.8b\n", e)
			return fmt.Errorf("clrc663: select timeout or error")
		}
	}
	sak, err := d.readReg(regFIFOData) // Read FIFO,Expecting SAK,here wo should next level of anti-collision
	if err != nil {
		return fmt.Errorf("clrc663: select: %w", err)
	}
	//if SAK's bit2=1,then UID is not finished yet
	//结束防冲突环.Here we assuming the UID is 4 bytes ,so just finish the anti-collision loop
	fmt.Println("done!", uid, sak)
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
	irq1_value := uint8(0)
	for {
		irq1, err := d.readReg(regIRQ1)
		if err != nil {
			return fmt.Errorf("clrc663: reqa: %w", err)
		}
		// either ERR_IRQ or RX_IRQ or timeout
		if irq1&(irqGlobal|1<<timer_for_timeout) != 0 {
			break
		}
	}
	if err := d.writeRegs(regCommand, cmdIdle); err != nil {
		return fmt.Errorf("clrc663: reqa: %v", err)
	}
	irqs := make([]byte, 2)
	if err := d.readRegs(regIRQ0, irqs); err != nil {
		return fmt.Errorf("clrc663: reqa: %w", err)
	}
	irq0, irq1 := irqs[0], irqs[1]
	fmt.Printf("After waiting for answer, IRQ1: %.8b\n", irq1)

	// if no Rx IRQ, or if there's an error somehow, return 0
	if ((irq0 & irqRx) == 0) || (irq0&irqErr) != 0 {
		fmt.Printf("No RX, irq1: %.8b irq0: %.8b\n", irq1_value, irq0)
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
	); err != nil {
		return 0, fmt.Errorf("read block: %w", err)
	}

	// // configure a timeout timer.
	// uint8_t timer_for_timeout = 0;  // should match the enabled interupt.

	// // enable the global IRQ for idle, errors and timer.
	// mfrc630_write_reg(MFRC630_REG_IRQ0EN, MFRC630_IRQ0EN_IDLE_IRQEN | MFRC630_IRQ0EN_ERR_IRQEN);
	// mfrc630_write_reg(MFRC630_REG_IRQ1EN, MFRC630_IRQ1EN_TIMER0_IRQEN);

	// Set timer to 221 kHz clock, start at the end of Tx.
	// mfrc630_timer_set_control(timer_for_timeout, MFRC630_TCONTROL_CLK_211KHZ | MFRC630_TCONTROL_START_TX_END);
	// // Frame waiting time: FWT = (256 x 16/fc) x 2 FWI
	// // FWI defaults to four... so that would mean wait for a maximum of ~ 5ms
	// mfrc630_timer_set_reload(timer_for_timeout, 2000);  // 2000 ticks of 5 usec is 10 ms.
	// mfrc630_timer_set_value(timer_for_timeout, 2000);

	// uint8_t irq1_value = 0;
	// uint8_t irq0_value = 0;

	// mfrc630_clear_irq0();  // clear irq0
	// mfrc630_clear_irq1();  // clear irq1

	if err := d.transceive(_MF_CMD_READ, block_address); err != nil {
		return 0, fmt.Errorf("clrc663: read block: %w", err)
	}
	if err := d.waitForIRQ(irqRx, 0); err != nil {
		return 0, fmt.Errorf("clrc663: read block: %w", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := d.writeRegs(regCommand, cmdIdle); err != nil {
		return 0, fmt.Errorf("clrc663: read block: %w", err)
	}

	n, err := d.readFIFO(blk)
	if err != nil {
		return 0, fmt.Errorf("clrc663: read block: %w", err)
	}
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

func (d *Device) lpcd() error {
	// Part-1, configurate LPCD Mode
	// Please remove any PICC from the HF of the reader.
	// "I" and the "Q" values read from reg 0x42 and 0x43
	// shall be used in part-2 "Detect PICC"
	d.writeRegs(
		0, 0,
		// disable IRQ0, IRQ1 interrupt sources
		0x06, 0x7F,
		0x07, 0x7F,
		0x08, 0x00,
		0x09, 0x00,
		0x02, 0xB0, // Flush FIFO
		// LPCD_config
		0x3F, 0xC0, // Set Qmin register
		0x40, 0xFF, // Set Qmax register
		0x41, 0xC0, // Set Imin register
		0x28, 0x89, // set DrvMode register
		// Execute trimming procedure
		0x1F, 0x00, // Write default. T3 reload value Hi
		0x20, 0x10, // Write default. T3 reload value Lo
		0x24, 0x00, // Write min. T4 reload value Hi
		0x25, 0x05, // Write min. T4 reload value Lo
		0x23, 0xF8, // Config T4 for AutoLPCD&AutoRestart.Set AutoTrimm bit.Start T4.
		0x43, 0x40, // Clear LPCD result
		0x38, 0x52, // Set Rx_ADCmode bit
		0x39, 0x03, // Raise receiver gain to maximum
		0x00, 0x01, // Execute Rc663 command "Auto_T4" (Low power card detection and/or Auto trimming)
	)
	time.Sleep(100 * time.Millisecond)
	d.writeRegs(
		0x00, 0x00,
		0x02, 0xB0,
		0x38, 0x12, // Clear Rx_ADCmode bit
	)
	//> ------------ I and Q Value for LPCD ----------------
	I_Q := make([]byte, 2)
	if err := d.readRegs(regLPCD_I_Result, I_Q); err != nil {
		return fmt.Errorf("clrc663: lpcd calibration: %w", err)
	}
	I := I_Q[0] & 0x3F
	Q := I_Q[1] & 0x3F
	fmt.Println(I, Q)
	return nil
}

func (d *Device) TestDump() error {
	// if err := d.init2(); err != nil {
	// 	return err
	// }
	// if err := d.reqa2(); err != nil {
	// 	return err
	// }
	// if err := d.selectTag2(); err != nil {
	// 	return err
	// }
	if err := d.runCommand(cmdSoftReset); err != nil {
		return fmt.Errorf("clrc663: soft reset: %w", err)
	}
	// Load recommended register values.
	wr := append([]byte{regDrvMode}, recommendedRegs_14443A_ID1_106...)
	if err := d.bus.Tx(i2cAddr, wr, nil); err != nil {
		return fmt.Errorf("clrc663: protocol registers: %w", err)
	}
	// Load protocol.
	if err := d.runCommand(
		cmdLoadProtocol,
		protocol_ISO14443A_106_MILLER_MANCHESTER, protocol_ISO14443A_106_MILLER_MANCHESTER,
	); err != nil {
		return fmt.Errorf("clrc663: LoadProtocol: %w", err)
	}
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

// Recommended register settings for protocols,
// from NXP's AN11022: CLRC663 Quickstart Guide.
var (
	recommendedRegs_14443A_ID1_106 = []uint8{0x8A, 0x08, 0x21, 0x1A, 0x18, 0x18, 0x0F, 0x27, 0x00, 0xC0, 0x12, 0xCF, 0x00, 0x04, 0x90, 0x32, 0x12, 0x0A}
)

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
	cmdLoadProtocol = 0x0d
	cmdSoftReset    = 0x1f

	TxDataNumDataEn = 0b1 << 3

	t0Clk_211kHz  = 0b01
	t0Start_TxEnd = 0b01 << 4

	drvModeTx2Inv = 0b1 << 7
	drvModeTxEn   = 0b1 << 3

	error_CollDet = 1 << 2

	// Protocol numbers for the LoadProtocol command.
	protocol_ISO14443A_106_MILLER_MANCHESTER = 0x00

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
