// package type2 implements the NFC Forum type 2 protocols.
package type2

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Reader implements a NFC Forum type 2 reader.
type Reader struct {
	bus       io.ReadWriter
	block     uint8
	memBlocks int
	scratch   [readSize]byte
}

// _GTa[ANALOG] is the maximum time a tag is allowed to
// activate once in a field.
//
// [ANALOG]: NFC Digital Protocol Technical Specification 1.0
// table 114.
const _GTa = 5 * time.Millisecond

func NewReader(d io.ReadWriter) (*Reader, error) {
	tag := &Reader{
		bus: d,
	}
	time.Sleep(_GTa)
	if _, err := tag.sensReq(); err != nil {
		return nil, fmt.Errorf("type2: %w", err)
	}
	sak, err := tag.selectTag()
	if err != nil {
		return nil, fmt.Errorf("type2: %w", err)
	}
	if (sak>>5)&0b11 != 0b00 {
		return nil, errors.New("type2: tag not recognized")
	}

	memBlocks, err := tag.readCC()
	if err != nil {
		return nil, fmt.Errorf("type2: %w", err)
	}
	tag.memBlocks = memBlocks
	return tag, nil
}

func (t *Reader) readCC() (int, error) {
	if err := t.issueRead(ccBlock); err != nil {
		return 0, fmt.Errorf("cc: %w", err)
	}
	cc := t.scratch[:readSize]
	if _, err := io.ReadFull(t.bus, cc); err != nil {
		return 0, fmt.Errorf("cc: %w", err)
	}
	if magic := cc[0]; magic != ccMagic {
		return 0, fmt.Errorf("cc: invalid magic: %x", magic)
	}
	memBlocks := int(cc[2]) * 8 / blockSize
	return memBlocks, nil
}

func (t *Reader) sensReq() (uint16, error) {
	sensReq := t.scratch[:1]
	sensReq[0] = cmdSENS_REQ
	if _, err := t.bus.Write(sensReq); err != nil {
		return 0, fmt.Errorf("SENS_REQ: %w", err)
	}
	atqa := t.scratch[:2]
	if _, err := io.ReadFull(t.bus, atqa); err != nil {
		return 0, fmt.Errorf("SENS_REQ: %w", err)
	}

	return binary.LittleEndian.Uint16(atqa), nil
}

// selectTag perform tag selection.
// Note that this method doesn't implement the anti-collision
// algorithm for selecting a tag among several active tags. For two
// reasons: first, it's simpler without and second, we avoid confusion
// as to which of the tags the user means to present.
func (t *Reader) selectTag() (byte, error) {
	for _, cmd := range []byte{casLevel1, casLevel2, casLevel3} {
		req := t.scratch[:2]
		req[0] = cmd
		req[1] = 0x20
		if _, err := t.bus.Write(req); err != nil {
			return 0, fmt.Errorf("select: %w", err)
		}
		req2, resp := t.scratch[:7], t.scratch[7:7+5]
		n, err := t.bus.Read(resp)
		resp = resp[:n]
		if err != nil && err != io.EOF {
			return 0, fmt.Errorf("select: %w", err)
		}

		req2[0] = cmd
		req2[1] = 0x70
		uid := req2[2:]
		// Copy (partial) UID response to request.
		copy(uid, resp)
		bcc_val := uid[4]
		bcc_calc := bcc(uid)
		if bcc_val != bcc_calc {
			return 0, errors.New("select: BCC mismatch")
		}
		req2[6] = bcc_calc
		if _, err := t.bus.Write(req2); err != nil {
			return 0, fmt.Errorf("select: %w", err)
		}
		sakBuf := t.scratch[:1]
		if _, err := io.ReadFull(t.bus, sakBuf); err != nil {
			return 0, fmt.Errorf("select: %w", err)
		}
		sak := sakBuf[0]

		if sak&(0b1<<2) == 0 {
			// UID complete, selection procedure done.
			return sak, nil
		}
	}
	return 0, errors.New("select failed")
}

// Read from the tag user memory. The buffer must be at least
// 16 bytes long.
func (t *Reader) Read(rx []byte) (int, error) {
	if t.block == uint8(t.memBlocks) {
		return 0, io.EOF
	}
	bno := t.block + ccBlock + 1
	if err := t.issueRead(bno); err != nil {
		return 0, fmt.Errorf("type2: read: %w", err)
	}
	n, err := t.bus.Read(rx)
	if err != nil && err != io.EOF {
		return n, fmt.Errorf("type2: read: %w", err)
	}
	// Trim reads that go beyond the end block.
	nblocks := n / blockSize
	t.block += uint8(nblocks)
	rem := int(t.block) - t.memBlocks
	rem = max(0, rem)
	t.block -= uint8(rem)
	n -= rem * blockSize
	if t.block == uint8(t.memBlocks) {
		return n, io.EOF
	}
	return n, nil
}

func (t *Reader) issueRead(block byte) error {
	req := t.scratch[:2]
	req[0] = cmdRead
	req[1] = block
	_, err := t.bus.Write(req)
	return err
}

func bcc(uid []byte) byte {
	return uid[0] ^ uid[1] ^ uid[2] ^ uid[3]
}

const (
	cmdSENS_REQ = 0x26

	cmdRead = 0x30

	// blockSize in bytes.
	blockSize = 4
	// The start page of user memory.
	ccBlock = 3
	// The number of bytes returned from a read
	// operation.
	readSize = 16

	casLevel1 = 0x93
	casLevel2 = 0x95
	casLevel3 = 0x97

	ccSize  = 4
	ccMagic = 0xe1
)
