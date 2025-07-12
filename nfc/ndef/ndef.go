package ndef

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MessageReader is an [io.Reader] for parsing NDEF
// messages from NDEF TLV blocks.
type MessageReader struct {
	r       *bufio.Reader
	scratch [2]byte
}

// RecordReader is an [io.Reader] for parsing NDEF
// records from NDEF messages.
type RecordReader struct {
	afterBegin bool
	r          *bufio.Reader
	scratch    [4]byte
}

// A buffer size reasonable for skipping unknown records.
const discardBuffer = 16

func NewMessageReader(rd io.Reader) *MessageReader {
	return &MessageReader{
		r: bufio.NewReaderSize(rd, discardBuffer),
	}
}

// Read the next NDEF message, or [io.EOF] if no more messages
// are available.
func (r *MessageReader) Read(buf []byte) (int, error) {
	for {
		// Read type.
		typ, err := r.r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("ndef: tlv: %w", err)
		}
		// The null and terminator blocks have no length.
		switch typ {
		case nullType:
			continue
		case termType:
			return 0, io.EOF
		}
		// Read length.
		length8, err := r.r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("ndef: tlv: %w", err)
		}
		length := int(length8)
		if length8 == 0xff {
			// 2-byte length.
			l16 := r.scratch[:2]
			if _, err := io.ReadFull(r.r, l16); err != nil {
				return 0, fmt.Errorf("ndef: tlv: %w", err)
			}
			length = int(binary.BigEndian.Uint16(l16))
		}
		// Skip non-NDEF containers.
		if typ != ndefType {
			if _, err := r.r.Discard(length); err != nil {
				return 0, fmt.Errorf("ndef: tlv: %w", err)
			}
			continue
		}
		n := min(length, len(buf))
		n, err = io.ReadFull(r.r, buf[:n])
		if err != nil {
			return n, fmt.Errorf("ndef: tlv: %w", err)
		}
		if n < length {
			return n, io.ErrShortBuffer
		}
		return n, nil
	}
}

func NewRecordReader(rd io.Reader) *RecordReader {
	return &RecordReader{
		r: bufio.NewReaderSize(rd, discardBuffer),
	}
}

// Read the next NDEF record, or [io.EOF] if no more records
// are available.
func (r *RecordReader) Read(buf []byte) (int, error) {
	var discard int
	eof := false
	for {
		if discard > 0 {
			if _, err := r.r.Discard(discard); err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			discard = 0
		}
		if eof {
			return 0, io.EOF
		}
		// Read the header and type length.
		h := r.scratch[:2]
		if _, err := io.ReadFull(r.r, h); err != nil {
			return 0, fmt.Errorf("ndef: message: %w", err)
		}
		flags, tlen := h[0], h[1]
		eof = flags&flagME != 0
		afterBegin := flags&flagMB == 0
		if afterBegin != r.afterBegin {
			return 0, errors.New("ndef: message: expected start record")
		}
		r.afterBegin = true
		var plen int
		// Read payload length.
		if flags&flagSR == 0 {
			// 32-bit length.
			b := r.scratch[:4]
			if _, err := io.ReadFull(r.r, b); err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			plen = int(binary.BigEndian.Uint32(b))
		} else {
			// Short record.
			l, err := r.r.ReadByte()
			if err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			plen = int(l)
		}
		// Read ID length.
		var idLen uint8
		if flags&flagIR != 0 {
			l, err := r.r.ReadByte()
			if err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			idLen = l
		}
		// Read the well-known type byte, if any.
		var wellKnown byte
		if tlen == 1 {
			t, err := r.r.ReadByte()
			if err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			tlen--
			wellKnown = t
		}
		// Skip the (remaining) type and id.
		if _, err := r.r.Discard(int(tlen) + int(idLen)); err != nil {
			return 0, fmt.Errorf("ndef: message: %w", err)
		}
		// Reject chunked records.
		if flags&flagCF != 0 {
			discard = plen
			continue
		}
		// Skip unknown formats.
		switch tnf := flags & 0b111; tnf {
		case tnfWellKnown:
		default:
			discard = plen
			continue
		}
		n := 0
		switch wellKnown {
		case 'T': // Text
			header, err := r.r.ReadByte()
			if err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			plen--
			if header&(0b1<<7) != 0 { // Don't bother with UTF-16.
				discard = plen
				continue
			}
			// Skip language.
			langLen := int(header & 0b111111)
			if langLen > plen {
				return 0, errors.New("ndef: message: text language too long")
			}
			if _, err := r.r.Discard(int(langLen)); err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			plen -= langLen
		case 'U': // URI
			header, err := r.r.ReadByte()
			if err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			plen--
			prefix := ""
			switch p := header; p {
			case uriPrefixNone:
			case uriPrefixHttpWww:
				prefix = "http://www."
			case uriPrefixHttpsWww:
				prefix = "https://www."
			case uriPrefixHttp:
				prefix = "http://"
			case uriPrefixHttps:
				prefix = "https://"
			default:
				discard = plen
				continue
			}
			n = copy(buf, prefix)
			if n < len(prefix) {
				return n, io.ErrShortBuffer
			}
		default:
			discard = plen
			continue
		}
		buf = buf[n:]
		rem := min(int(plen), len(buf))
		pn, err := io.ReadFull(r.r, buf[:rem])
		n += pn
		if err != nil {
			return n, fmt.Errorf("ndef: message: %w", err)
		}
		if pn < int(plen) {
			return n, io.ErrShortBuffer
		}
		if eof {
			return n, io.EOF
		}
		return n, nil
	}
}

const (
	nullType = 0x00
	ndefType = 0x03
	termType = 0xfe

	flagIR = 0b1 << 3
	flagSR = 0b1 << 4
	flagCF = 0b1 << 5
	flagME = 0b1 << 6
	flagMB = 0b1 << 7

	tnfWellKnown = 0x01

	uriPrefixNone     = 0x00
	uriPrefixHttpWww  = 0x01
	uriPrefixHttpsWww = 0x02
	uriPrefixHttp     = 0x03
	uriPrefixHttps    = 0x04
)
