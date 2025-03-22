// Package iso15693 implements the ISO/IEC 15693 (NFC-V) protocol.
package iso15693

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type Tag struct {
	UID uint64

	bus Transceiver
	// bufSize is the largest unmber of bytes to request
	// from bus.
	bufSize int
	// blockNo is the block number to issue the next
	// read from.
	blockNo int
	// blocksRem tracks the remaining number of blocks
	// in the read.
	blocksRem int
	// blockSize is the block size determined in the
	// first [Read].
	blockSize int
	// seenResponse tracks whether the response flags
	// have been processed for a transceive.
	seenResponse bool
	scratch      [14]byte
}

// Transceiver represents an NFC modem.
type Transceiver interface {
	// Transceive transmits tx and starts receiving.
	Transceive(tx []byte) error
	// Reader reads the response from a Transceive.
	io.Reader
}

// Open a tag and read its UID.
// Size is the maximum number of bytes to request from
// the transceiver.
func Open(bus Transceiver, size int) (*Tag, error) {
	tag := &Tag{
		bus:     bus,
		bufSize: size,
	}
	req := tag.scratch[:3]
	const maskLength = 0
	req[0] = flagDataRate | // High speed
		flagInventory | // Inventory options
		flagNbSlots // 1 Slot
	req[1] = cmdInventory
	req[2] = maskLength
	if err := tag.transceive(req); err != nil {
		return nil, fmt.Errorf("iso15693: Inventory: %w", err)
	}
	dsfidUID := tag.scratch[:]
	n, err := tag.read(dsfidUID)
	if err != nil {
		return nil, fmt.Errorf("iso15693: Inventory: %w", err)
	}
	if n != 9 {
		return nil, fmt.Errorf("iso15693: unexpected Inventory response length: %d", n)
	}
	// UID is after the 1-byte DSFID.
	tag.UID = binary.LittleEndian.Uint64(dsfidUID[1:])
	return tag, nil
}

func (t *Tag) transceive(tx []byte) error {
	t.seenResponse = false
	return t.bus.Transceive(tx)
}

// Read from the tag.
func (t *Tag) Read(rx []byte) (int, error) {
	if len(rx) < maxBlockSize {
		return 0, fmt.Errorf("iso15693: buffer too small")
	}
	// First read, with unknown block size.
	if t.blockSize == 0 {
		// Read a single block.
		if err := t.issueRead(1); err != nil {
			return 0, fmt.Errorf("iso15693: %w", err)
		}
	}
	for {
		n, err := t.read(rx)
		if t.blockSize == 0 {
			if n == 0 {
				// Empty first block. We must have reached the
				// end of the tag memory.
				return 0, io.EOF
			}
			// First read is 1 block.
			t.blockSize = n
		}
		t.blocksRem -= n / t.blockSize
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return n, fmt.Errorf("iso15693: %w", err)
			}
			if t.blocksRem > 0 {
				// EOF reached.
				return n, io.EOF
			}
			// Issue the maximal number of blocks that fits
			// len(rx) minus the response flag byte.
			nblocks := (t.bufSize - 1) / t.blockSize
			if err := t.issueRead(nblocks); err != nil {
				return 0, fmt.Errorf("iso15693: %w", err)
			}
		}
		if n > 0 {
			return n, nil
		}
	}
}

func (t *Tag) issueRead(nblocks int) error {
	var req []byte
	switch {
	case t.blockNo <= 0xff && nblocks <= 0xff+1:
		req = t.scratch[:12]
		req[0] = flagDataRate | // High speed
			flagAddress // Address particular tag.
		req[1] = cmdReadMultipleBlocks
		binary.LittleEndian.PutUint64(req[2:], t.UID)
		req[10] = uint8(t.blockNo)
		req[11] = uint8(nblocks - 1) // block count, zero based.
	case t.blockNo <= 0xffff && nblocks <= 0xffff+1:
		req = t.scratch[:14]
		req[0] = flagDataRate | // High speed
			flagAddress // Address particular tag.
		req[1] = cmdExtReadMultipleBlocks
		binary.LittleEndian.PutUint64(req[2:], t.UID)
		binary.LittleEndian.PutUint16(req[10:], uint16(t.blockNo))
		// Block count is zero-based.
		binary.LittleEndian.PutUint16(req[12:], uint16(nblocks-1))
	default:
		return io.EOF
	}
	if err := t.bus.Transceive(req); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	t.blocksRem = nblocks
	t.blockNo += nblocks
	return nil
}

func (t *Tag) read(rx []byte) (int, error) {
	n, err := t.bus.Read(rx)
	if t.seenResponse {
		return n, err
	}
	t.seenResponse = false

	rx = rx[:n]
	if len(rx) == 0 {
		if err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("read: response too short")
	}
	// Process response flags.
	respFlags := rx[0]
	// Shift response to the left, leaving the message contents.
	copy(rx, rx[1:])
	rx = rx[:len(rx)-1]
	if respFlags&flagError != 0 {
		if err != nil {
			return 0, err
		}
		if len(rx) == 0 {
			return 0, fmt.Errorf("read: response too short")
		}
		errCode := rx[0]
		return 0, fmt.Errorf("read: tag error response (code %#.2x)", errCode)
	}
	return len(rx), err
}

const (
	cmdInventory             = 0x01
	cmdReadSingleBlock       = 0x20
	cmdReadMultipleBlocks    = 0x23
	cmdSelect                = 0x25
	cmdGetSystemInfo         = 0x2b
	cmdExtReadSingleBlock    = 0x30
	cmdExtReadMultipleBlocks = 0x33

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

	// maxBlockSize in bytes according to the specification.
	maxBlockSize = 256 / 8
)
