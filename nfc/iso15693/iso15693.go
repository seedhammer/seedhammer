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

	bus io.ReadWriter
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

// Open a tag and read its UID from an NFC transceiver.
// Size is the maximum number of bytes that can be received
// without overflowing the transceiver FIFO.
// Note that the transceiver is expected to implement the
// iso15693-2 codec. Use [Transceiver] if not.
func Open(bus io.ReadWriter, size int) (*Tag, error) {
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
	if err := tag.write(req); err != nil {
		return nil, fmt.Errorf("iso15693: Inventory: %w", err)
	}
	dsfidUID := tag.scratch[:]
	n, err := tag.read(dsfidUID)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("iso15693: Inventory: %w", err)
	}
	if n != 9 {
		return nil, fmt.Errorf("iso15693: unexpected Inventory response length: %d", n)
	}
	// UID is after the 1-byte DSFID.
	tag.UID = binary.LittleEndian.Uint64(dsfidUID[1:])
	return tag, nil
}

func (t *Tag) write(tx []byte) error {
	t.seenResponse = false
	_, err := t.bus.Write(tx)
	return err
}

// Read from the tag.
func (t *Tag) Read(rx []byte) (int, error) {
	if len(rx) < maxBlockSize {
		return 0, fmt.Errorf("iso15693: read: %w", io.ErrShortBuffer)
	}
	// First read, with unknown block size.
	if t.blockSize == 0 {
		// Read a single block.
		if err := t.issueRead(1); err != nil {
			return 0, fmt.Errorf("iso15693: read: %w", err)
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
				return n, fmt.Errorf("iso15693: read: %w", err)
			}
			if t.blocksRem > 0 {
				// EOF reached.
				return n, io.EOF
			}
			// Issue the maximal number of blocks that fits
			// len(rx) minus the response flag byte.
			nblocks := (t.bufSize - 1) / t.blockSize
			if err := t.issueRead(nblocks); err != nil {
				return 0, fmt.Errorf("iso15693: read: %w", err)
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
		req[10] = byte(t.blockNo)
		req[11] = byte(nblocks - 1) // block count, zero based.
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
	fmt.Printf("issueRead: %d blocks: %x\n", nblocks, req)
	if _, err := t.bus.Write(req); err != nil {
		return err
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
	txSOF     = 0x21
	txEOF     = 0x04
	rxSOF     = 0b10111
	rxEOF     = 0b11101
	rxSOFBits = 5

	data_00_1_4 = 0x02
	data_01_1_4 = 0x08
	data_10_1_4 = 0x20
	data_11_1_4 = 0x80

	frameSize   = 1 + 1 // SOF + EOF
	crcSize     = 2     // 16-bit CRC
	_1of4Size   = 4     // 1-of-4 encoding takes 4 bytes per byte.
	crcResidual = 0x0f47
)

// Transceiver implements the iso15693-2 physical framing, encoding
// and CRC. Use it on top an underlying modem that doesn't supply
// an implementation in hardware.
type Transceiver struct {
	bus io.ReadWriter
	buf []byte
}

// NewTransceiver returns a new Transceiver that can transfer
// up to size encoded bytes. Use its [DecodedSize] method
// to determine the maximum decoded message size.
func NewTransceiver(bus io.ReadWriter, size int) *Transceiver {
	return &Transceiver{
		bus: bus,
		buf: make([]byte, size),
	}
}

func (t *Transceiver) DecodedSize() int {
	// Report the transmission size; receive sizes are smaller.
	return (cap(t.buf)-frameSize)/_1of4Size - crcSize
}

func (t *Transceiver) Write(tx []byte) (int, error) {
	buf := t.buf[:0]
	buf = append(buf, txSOF)
	for _, b := range tx {
		buf = encode1of4(buf, b)
	}
	crc := crcCITT(tx)
	buf = encode1of4(buf, byte(crc))
	buf = encode1of4(buf, byte(crc>>8))
	buf = append(buf, txEOF)
	return t.bus.Write(buf)
}

// Read decoded bytes into rx. There must be room in
// rx for 2 CRC bytes in addition to the decoded payload.
func (t *Transceiver) Read(rx []byte) (int, error) {
	n, err := t.bus.Read(t.buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	buf := t.buf[:n]
	if len(buf) < 2 {
		return 0, errors.New("message too short")
	}
	// Check for minimum length and the framing.
	sof := buf[0] & (1<<rxSOFBits - 1)
	eof := (buf[len(buf)-1]&0b11)<<3 | buf[len(buf)-2]>>5
	if sof != rxSOF || eof != rxEOF {
		return 0, errors.New("invalid framing")
	}
	const (
		// Two bytes of framing: 5 bits SOF, 5 bits EOF, 6 bits of padding.
		framing = (5 + 5 + 6) / 8
		// Every bit is encoded as two Manchester bits.
		manchesterRate = 2
	)
	// The number of payload bytes.
	rxlen := (len(buf) - framing) / manchesterRate
	if len(rx) < rxlen {
		return 0, io.ErrShortBuffer
	}
	rx = rx[:rxlen]

	// Read each bit as a Manchester encoded pair of bits.
	// Start after the 5 bit SOF.
	inbit := rxSOFBits
	for i := range rx {
		outbyte := byte(0)
		for range 8 {
			bit := byte(0)
			for range 2 {
				bit <<= 1
				bit |= (buf[inbit/8] >> (inbit % 8)) & 0b1
				inbit++
			}
			outbyte >>= 1
			switch bit {
			case 0b10: //bit 0
			case 0b01: // bit 1.
				outbyte |= 0b1 << 7
			default:
				return 0, errors.New("tag collision")
			}
		}
		rx[i] = outbyte
	}
	crc := crcCITT(rx)
	if crc != crcResidual {
		return 0, errors.New("CRC mismatch")
	}
	return rxlen - 2, io.EOF
}

func encode1of4(buf []byte, b byte) []byte {
	for range 4 {
		var eb byte
		switch b & 0b11 {
		case 0b00:
			eb = data_00_1_4
		case 0b01:
			eb = data_01_1_4
		case 0b10:
			eb = data_10_1_4
		case 0b11:
			eb = data_11_1_4
		}
		buf = append(buf, eb)
		b >>= 2
	}
	return buf
}

func crcCITT(data []byte) uint16 {
	crc := uint16(0xffff)
	for _, b := range data {
		b ^= byte(crc & 0xFF)
		b ^= b << 4

		b16 := uint16(b)
		crc = (crc >> 8) ^ (b16 << 8) ^ (b16 << 3) ^ (b16 >> 4)
	}
	return ^crc
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
