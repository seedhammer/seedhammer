// package iso14443a implements the ISO/IEC 14443a NFC protocol
// and reading of Mifare Ultralight tags.
package iso14443a

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

type Tag struct {
	bus     io.ReadWriter
	page    uint8
	scratch [12]byte
}

// _GTa[ANALOG] is the maximum time a tag is allowed to
// activate once in a field.
//
// [ANALOG]: NFC Digital Protocol Technical Specification 1.0
// table 114.
const _GTa = 5 * time.Millisecond

func Open(d io.ReadWriter) (*Tag, error) {
	tag := &Tag{
		bus:  d,
		page: memStartPage,
	}
	time.Sleep(_GTa)
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
	if _, err := t.bus.Write(reqa); err != nil {
		return 0, fmt.Errorf("REQA: %w", err)
	}
	atqa := t.scratch[:2]
	if _, err := io.ReadFull(t.bus, atqa); err != nil {
		return 0, fmt.Errorf("REQA: %w", err)
	}

	return binary.LittleEndian.Uint16(atqa), nil
}

// selectTag perform tag selection.
// Note that this method doesn't implement the anti-collision
// algorithm for selecting a tag among several active tags. For two
// reasons: first, it's simpler without and second, we avoid confusion
// as to which of the tags the user means to present.
func (t *Tag) selectTag() error {
	for _, cmd := range []byte{casLevel1, casLevel2, casLevel3} {
		req := t.scratch[:2]
		req[0] = cmd
		req[1] = 0x20
		if _, err := t.bus.Write(req); err != nil {
			return fmt.Errorf("select: %w", err)
		}
		req2, resp := t.scratch[:7], t.scratch[7:7+5]
		n, err := t.bus.Read(resp)
		resp = resp[:n]
		if err != nil && err != io.EOF {
			return fmt.Errorf("select: %w", err)
		}

		req2[0] = cmd
		req2[1] = 0x70
		uid := req2[2:]
		// Copy (partial) UID response to request.
		copy(uid, resp)
		bcc_val := uid[4]
		bcc_calc := uid[0] ^ uid[1] ^ uid[2] ^ uid[3]
		if bcc_val != bcc_calc {
			return errors.New("select: BCC mismatch")
		}
		req2[6] = bcc_calc
		if _, err := t.bus.Write(req2); err != nil {
			return fmt.Errorf("select: %w", err)
		}
		sakBuf := t.scratch[:1]
		if _, err := io.ReadFull(t.bus, sakBuf); err != nil {
			return fmt.Errorf("select: %w", err)
		}
		sak := sakBuf[0]

		if sak&(0b1<<2) == 0 {
			// UID complete, selection procedure done.
			return nil
		}
	}
	return errors.New("select failed")
}

// Read from the tag user memory. The buffer must be at least
// 16 bytes long.
func (t *Tag) Read(rx []byte) (int, error) {
	req := t.scratch[:2]
	req[0] = cmdMifareRead
	req[1] = t.page
	if _, err := t.bus.Write(req); err != nil {
		return 0, fmt.Errorf("iso14443a: read: %w", err)
	}

	n, err := t.bus.Read(rx)
	if err != nil && err != io.EOF {
		return n, fmt.Errorf("iso14443a: read: %w", err)
	}
	if len(rx) < n {
		return 0, fmt.Errorf("iso14443a: read: buffer too small: %d", len(rx))
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
	memStartPage = 3

	casLevel1 = 0x93
	casLevel2 = 0x95
	casLevel3 = 0x97
)
