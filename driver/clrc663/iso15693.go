package clrc663

import "fmt"

func (d *Device) iso15693Read() error {
	if err := d.iso15693Inventory(); err != nil {
		return err
	}
	return nil
}

func (d *Device) iso15693Inventory() error {
	dsfidUID := make([]byte, 9)
	const maskLength = 0
	n, err := d.iso15693Transceive(iso15693CmdInventory, nil, []byte{maskLength}, dsfidUID)
	if err != nil {
		return err
	}
	if n != len(dsfidUID) {
		return fmt.Errorf("iso15693: unexpected inventory response: %v", dsfidUID[:n])
	}
	dsfidUID = dsfidUID[:n]
	// UID is after the 1-byte DSFID.
	uid := dsfidUID[1:]
	fmt.Printf("inventory response uid %#x\n", uid)
	sysInfo := make([]byte, 14)
	n, err = d.iso15693Transceive(iso15693CmdGetSystemInfo, uid, nil, sysInfo)
	if err != nil {
		return fmt.Errorf("iso15693: get system info: %w", err)
	}
	if n < 9 {
		return fmt.Errorf("iso15693: system info too short: %d", n)
	}
	flags := sysInfo[0]
	const (
		flagDSFID   = 0b1 << 0
		flagAFI     = 0b1 << 1
		flagMemSize = 0b1 << 2
		flagIC      = 0b1 << 3
	)
	otherInfoIdx := 9 // After flags and UID.
	if flags&flagDSFID != 0 {
		otherInfoIdx++
	}
	if flags&flagAFI != 0 {
		otherInfoIdx++
	}
	nblocks := 1 + int(sysInfo[otherInfoIdx+0])
	blockSize := 1 + int(sysInfo[otherInfoIdx+1]&0b11111)

	sysInfo = sysInfo[:n]
	fmt.Printf("sysinfo: %#x nblocks %d blockSize %d size %d\n", sysInfo, nblocks, blockSize, nblocks*blockSize)
	block := make([]byte, 32)
	// const blockNo = 0
	// n, err = d.iso15693Transceive(iso15693CmdReadSingleBlock, uid, []byte{blockNo}, block)
	// if err != nil {
	// 	return err
	// }
	// {
	// 	const nblocks = 32
	// 	args := []byte{
	// 		byte(0),
	// 		byte(nblocks - 1),
	// 	}
	// 	fmt.Println(args)
	// 	n, err = d.iso15693Transceive(iso15693CmdReadMultipleBlocks, uid, args, block)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	fmt.Println("block", n, block[:n])
	// }
	// {
	// 	const blockNo = 0
	// 	n, err = d.iso15693Transceive(iso15693CmdExtReadSingleBlock, uid, []byte{0, 0}, block)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	fmt.Println("block", n, block[:n])
	// }
	{
		blockSize = 4
		nblocks := uint16(len(block)/blockSize - 1)
		for i := range 100 {
			blockNo := i * (len(block) / blockSize)
			args := []byte{
				byte(blockNo),
				byte(blockNo >> 8),
				byte(nblocks),
				byte(nblocks >> 8),
			}
			// args := []byte{
			// 	0, 0, 1, 0,
			// }
			fmt.Println(args)
			n, err = d.iso15693Transceive(iso15693CmdExtReadMultipleBlocks, uid, args, block)
			if err != nil {
				return err
			}
			fmt.Println("block", n, block[:n])
		}
	}
	return nil
}

func (d *Device) iso15693Transceive(cmd uint8, uid, args, rx []byte) (int, error) {
	// configure a timeout timer_for_timeout.
	const timer_for_timeout = 0

	// Set timer to 221 kHz clock, start at the end of Tx.
	const MFRC630_TCONTROL_CLK_211KHZ = 0b01
	const MFRC630_TCONTROL_START_TX_END = 0b01 << 4
	const reloadTicks = 60000

	const (
		flagSubcarrier = 0b1 << 0
		flagDataRate   = 0b1 << 1
		flagInventory  = 0b1 << 2
		flagOption     = 0b1 << 6

		// Inventory flags.
		flagAFI     = 0b1 << 4
		flagNbSlots = 0b1 << 5

		// Other flags.
		flagSelect  = 0b1 << 4
		flagAddress = 0b1 << 5

		// Response flags.
		flagError = 0b1 << 0
	)
	flags := uint8(flagDataRate) // High speed
	if cmd == iso15693CmdInventory {
		flags |= flagInventory | // Inventory options
			flagNbSlots // 1 Slot
	}
	if len(uid) > 0 {
		flags |= flagAddress
	}
	tx := []byte{flags, cmd}
	if len(uid) > 0 {
		tx = append(tx, uid...)
	}
	tx = append(tx, args...)
	if err := d.writeRegs(
		regCommand, cmdIdle,
		regFIFOControl, 1<<4,

		// clear interrupts.
		regIRQ0, 0x7F,
		regIRQ1, 0x7F,

		regT0Control+(5*timer_for_timeout), MFRC630_TCONTROL_CLK_211KHZ|MFRC630_TCONTROL_START_TX_END,

		regT0ReloadHi+(5*timer_for_timeout), reloadTicks>>8,
		regT0ReloadLo+(5*timer_for_timeout), reloadTicks&0xFF,
		regT0CounterValHi+(5*timer_for_timeout), reloadTicks>>8,
		regT0CounterValLo+(5*timer_for_timeout), reloadTicks&0xFF,
	); err != nil {
		return 0, fmt.Errorf("clrc663: %w", err)
	}
	fmt.Println("transceive", tx)
	if err := d.runCommand(cmdTransceive, tx...); err != nil {
		return 0, fmt.Errorf("clrc663: %w", err)
	}
	if err := d.waitForRx(timer_for_timeout); err != nil {
		return 0, fmt.Errorf("clrc663: %w", err)
	}

	rx_len, err := d.readReg(regFIFOLength)
	if err != nil {
		return 0, fmt.Errorf("clrc663: read FIFO length: %w", err)
	}
	// Process response flags.
	respFlags, err := d.readReg(regFIFOData)
	if err != nil {
		return 0, fmt.Errorf("iso15693: read FIFO: %w", err)
	}
	rx_len--
	if respFlags&flagError != 0 {
		// Read error code.
		respErr, err := d.readReg(regFIFOData)
		if err != nil {
			return 0, fmt.Errorf("clrc663: read FIFO: %w", err)
		}
		return 0, fmt.Errorf("iso15693: response error: %#x", respErr)
	}
	n := min(int(rx_len), len(rx))
	if err := d.readRegs(regFIFOData, rx[:n]); err != nil {
		return 0, fmt.Errorf("clrc663: read FIFO: %w", err)
	}

	return n, nil
}

const (
	iso15693CmdInventory             = 0x01
	iso15693CmdReadSingleBlock       = 0x20
	iso15693CmdReadMultipleBlocks    = 0x23
	iso15693CmdSelect                = 0x25
	iso15693CmdGetSystemInfo         = 0x2b
	iso15693CmdExtReadSingleBlock    = 0x30
	iso15693CmdExtReadMultipleBlocks = 0x33
)
