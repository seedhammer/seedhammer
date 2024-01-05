// Package type5 implements the NFC Forum type 5 protocols.
package type5

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Reader implements a type 5 tag reader.
type Reader struct {
	bus io.ReadWriter
	cc  capContainer
	// fifoSize is the maximum receive size.
	fifoSize int
	// blockNo is the block number to issue the next
	// read from.
	blockNo int
	// Reserve enough space for two blocks and a status byte.
	scratch [2*maxSupportedBlockSize + 1]byte
}

// wakeupDelay is the time for the tag to power up after
// entering the field.
const wakeupDelay = 5 * time.Millisecond

type capContainer struct {
	FirstBlock   int
	BlockSize    int
	MemoryBlocks int
}

// NewReader creates a reader from an NFC transceiver.
// Size is the maximum number of bytes that can be received
// without overflowing the transceiver FIFO.
// Note that the transceiver is expected to implement the
// iso15693-2 codec. Use [Transceiver] if not.
func NewReader(bus io.ReadWriter, fifoSize int) (*Reader, error) {
	tag := &Reader{
		bus:      bus,
		fifoSize: fifoSize,
	}
	// Wait for tag to power up.
	time.Sleep(wakeupDelay)

	uid, err := tag.inventory()
	if err != nil {
		return nil, fmt.Errorf("type5: %w", err)
	}
	if err := tag.selectUID(uid); err != nil {
		return nil, fmt.Errorf("type5: %w", err)
	}
	cc, err := tag.readCC()
	if err != nil {
		return nil, fmt.Errorf("type5: %w", err)
	}
	tag.cc = cc
	return tag, nil
}

func (t *Reader) readCC() (capContainer, error) {
	const nblocks = 2
	// Read 2 blocks containing the
	// capability container (up to 8 bytes).
	if err := t.readMultiple(0, nblocks); err != nil {
		return capContainer{}, fmt.Errorf("cc: %w", err)
	}
	cc := t.scratch[:]
	n, err := t.read(cc)
	if err != nil && err != io.EOF {
		return capContainer{}, fmt.Errorf("cc: %w", err)
	}
	cc = cc[:n]
	// Minimum block size is 4.
	if len(cc) < 4*nblocks {
		return capContainer{}, fmt.Errorf("cc: short read: %d", len(cc))
	}
	blockSize := n / nblocks
	magic := cc[0]
	switch magic {
	case ccMagic1, ccMagic2:
	default:
		return capContainer{}, fmt.Errorf("cc: invalid magic: %x", magic)
	}
	mlen := int(cc[2])
	ccSize := 4
	if mlen == 0 {
		// 8-byte capability container.
		ccSize = 8
		mlen = int(binary.BigEndian.Uint16(cc[6:]))
	}
	if ccSize%blockSize != 0 {
		return capContainer{}, fmt.Errorf("cc: unsupported block size: %d", blockSize)
	}
	return capContainer{
		FirstBlock:   ccSize / blockSize,
		BlockSize:    blockSize,
		MemoryBlocks: 8 * mlen / blockSize,
	}, nil
}

func (t *Reader) selectUID(uid uint64) error {
	req := t.scratch[:2+8]
	req[0] = flagDataRate | // High speed
		flagAddress // Address particular tag.
	req[1] = cmdSelect
	bo.PutUint64(req[2:], uid)
	if _, err := t.bus.Write(req); err != nil {
		return fmt.Errorf("select: %w", err)
	}
	resp := t.scratch[:]
	if _, err := t.read(resp); err != nil && err != io.EOF {
		return fmt.Errorf("select: %w", err)
	}
	return nil
}

func (t *Reader) inventory() (uint64, error) {
	req := t.scratch[:3]
	const maskLength = 0
	req[0] = flagDataRate | // High speed
		flagInventory | // Inventory options
		flagNbSlots // 1 Slot
	req[1] = cmdInventory
	req[2] = maskLength
	if _, err := t.bus.Write(req); err != nil {
		return 0, fmt.Errorf("inventory: %w", err)
	}
	dsfidUID := t.scratch[:]
	n, err := t.read(dsfidUID)
	if err != nil && err != io.EOF {
		return 0, fmt.Errorf("inventory: %w", err)
	}
	dsfidUID = dsfidUID[:n]
	if len(dsfidUID) != 9 {
		return 0, fmt.Errorf("inventory: unexpected response length: %d", n)
	}
	// UID is after the 1-byte DSFID.
	uid := bo.Uint64(dsfidUID[1:])
	return uid, nil
}

// Read from the tag.
func (t *Reader) Read(rx []byte) (int, error) {
	bufLen := min(len(rx), t.fifoSize)
	// Issue the maximum number of blocks that fit
	// in bufLen. Subtract 1 for the response flag byte.
	nblocks := (bufLen - 1) / t.cc.BlockSize
	if nblocks == 0 {
		return 0, io.ErrShortBuffer
	}
	// Don't read past the end of memory.
	nblocks = min(t.cc.MemoryBlocks-t.blockNo, nblocks)
	if nblocks == 0 {
		return 0, io.EOF
	}
	bno := t.cc.FirstBlock + t.blockNo
	if err := t.readMultiple(bno, nblocks); err != nil {
		return 0, fmt.Errorf("type5: read: %w", err)
	}
	n, err := t.read(rx)
	if err != nil {
		return n, fmt.Errorf("type5: read: %w", err)
	}
	t.blockNo += nblocks
	if t.blockNo == t.cc.MemoryBlocks {
		return n, io.EOF
	}
	return n, nil
}

func (t *Reader) readMultiple(blockNo, nblocks int) error {
	var req []byte
	switch {
	case blockNo <= 0xff && nblocks <= 0xff+1:
		req = t.scratch[:4]
		req[0] = flagDataRate | flagSelect
		req[1] = cmdReadMultipleBlocks
		req[2] = byte(blockNo)
		req[3] = byte(nblocks - 1) // block count, zero based.
	case blockNo <= 0xffff && nblocks <= 0xffff+1:
		req = t.scratch[:6]
		req[0] = flagDataRate | flagSelect
		req[1] = cmdExtReadMultipleBlocks
		bo.PutUint16(req[2:], uint16(blockNo))
		// Block count is zero-based.
		bo.PutUint16(req[4:], uint16(nblocks-1))
	default:
		return errors.New("read out of bounds")
	}
	_, err := t.bus.Write(req)
	return err
}

func (t *Reader) read(rx []byte) (int, error) {
	n, err := t.bus.Read(rx)
	rx = rx[:n]
	if len(rx) == 0 {
		if err != nil && err != io.EOF {
			return 0, err
		}
		return 0, io.ErrUnexpectedEOF
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
			return 0, io.ErrUnexpectedEOF
		}
		errCode := rx[0]
		return 0, fmt.Errorf("read: tag error response (code %#.2x)", errCode)
	}
	return len(rx), nil
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

	writeFrameSize = 1 + 1 // SOF + EOF
	crcSize        = 2     // 16-bit CRC
	_1of4Size      = 4     // 1-of-4 encoding takes 4 bytes per byte.
	crcResidual    = 0x0f47
	// Two bytes of readFrameSize: 5 bits SOF, 5 bits EOF, 6 bits of padding.
	readFrameSize = (5 + 5 + 6) / 8
	// Every bit is encoded as two Manchester bits.
	manchesterRate = 2
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

// WriteCapacity reports the maximum size of a [Write].
func (t *Transceiver) WriteCapacity() int {
	return (cap(t.buf)-writeFrameSize)/_1of4Size - crcSize
}

// ReadCapacity reports the maximum size of a [Read].
func (t *Transceiver) ReadCapacity() int {
	return (cap(t.buf)-readFrameSize)/manchesterRate - crcSize
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
	if err != nil && err != io.EOF {
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
	// The number of payload bytes.
	rxlen := (len(buf) - readFrameSize) / manchesterRate
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
	return rxlen - crcSize, io.EOF
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

var bo = binary.LittleEndian

const (
	ccMagic1 = 0xe1
	ccMagic2 = 0xe2

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
	// Support up to 8 byte blocks.
	maxSupportedBlockSize = 8
)
