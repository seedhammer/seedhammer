package ndef

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MessageReader is an [io.Reader] for parsing NDEF
// messages from NDEF TLV blocks.
type MessageReader struct {
	r       io.Reader
	scratch [2]byte
	length  int
	skip    bool
}

// RecordReader is an [io.Reader] for parsing NDEF
// records from NDEF messages.
type RecordReader struct {
	notBegin bool
	r        io.Reader
	scratch  [4]byte
	length   int
	skip     bool
}

func NewMessageReader(rd io.Reader) *MessageReader {
	return &MessageReader{
		r: rd,
	}
}

// Read the contents of all available NDEF messages.
func (r *MessageReader) Read(buf []byte) (int, error) {
	for {
		if r.length > 0 {
			l := min(len(buf), r.length)
			n, err := r.r.Read(buf[:l])
			r.length -= n
			if err != nil {
				return n, fmt.Errorf("ndef: tlv: %w", err)
			}
			if r.skip {
				continue
			}
			return n, nil
		}
		// Read type.
		buf := r.scratch[:1]
		if _, err := io.ReadFull(r.r, buf); err != nil {
			if err == io.EOF {
				return 0, io.EOF
			}
			return 0, fmt.Errorf("ndef: tlv: %w", err)
		}
		typ := buf[0]
		// The null and terminator blocks have no length.
		switch typ {
		case nullType:
			continue
		case termType:
			return 0, io.EOF
		}
		// Read length.
		buf = r.scratch[:1]
		if _, err := io.ReadFull(r.r, buf); err != nil {
			return 0, fmt.Errorf("ndef: tlv: %w", err)
		}
		length8 := buf[0]
		length := int(length8)
		if length8 == 0xff {
			// 2-byte length.
			buf = r.scratch[:2]
			if _, err := io.ReadFull(r.r, buf); err != nil {
				return 0, fmt.Errorf("ndef: tlv: %w", err)
			}
			length = int(binary.BigEndian.Uint16(buf))
		}
		r.length = length
		// Skip non-NDEF containers.
		r.skip = typ != ndefType
	}
}

func NewRecordReader(rd io.Reader) *RecordReader {
	return &RecordReader{
		r: rd,
	}
}

// Read the next NDEF record, or [io.EOF] if no more records
// are available.
func (r *RecordReader) Read(buf []byte) (int, error) {
	for {
		if r.length > 0 {
			l := min(len(buf), r.length)
			n, err := r.r.Read(buf[:l])
			r.length -= n
			if err != nil {
				if err != io.EOF || r.length > 0 {
					return n, fmt.Errorf("ndef: message: %w", err)
				}
			}
			if r.skip {
				continue
			}
			if r.length == 0 {
				return n, io.EOF
			}
			return n, nil
		}
		r.skip = false
		// Read the header and type length.
		h := r.scratch[:2]
		if _, err := io.ReadFull(r.r, h); err != nil {
			if err == io.EOF {
				return 0, io.EOF
			}
			return 0, fmt.Errorf("ndef: message: %w", err)
		}
		flags, tlen := h[0], h[1]
		begin := flags&flagMB != 0
		end := flags&flagME != 0
		if begin == r.notBegin {
			return 0, errors.New("ndef: message: expected start record")
		}
		r.notBegin = !end
		// Read payload length.
		if flags&flagSR == 0 {
			// 32-bit length.
			b := r.scratch[:4]
			if _, err := io.ReadFull(r.r, b); err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			r.length = int(binary.BigEndian.Uint32(b))
		} else {
			// Short record.
			b, err := r.readByte()
			if err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			r.length = int(b)
		}
		// Read ID length.
		var idLen uint8
		if flags&flagIR != 0 {
			b, err := r.readByte()
			if err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			idLen = b
		}
		// Read the well-known type byte, if any.
		var wellKnown byte
		if tlen == 1 {
			b := r.scratch[:1]
			if _, err := io.ReadFull(r.r, b); err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			tlen--
			wellKnown = b[0]
		}
		// Skip the (remaining) type and id.
		if err := r.discard(buf, int(tlen)+int(idLen)); err != nil {
			return 0, fmt.Errorf("ndef: message: %w", err)
		}
		// Reject chunked records.
		if flags&flagCF != 0 {
			r.skip = true
			continue
		}
		// Skip unknown formats.
		switch tnf := flags & 0b111; tnf {
		case tnfWellKnown:
		default:
			r.skip = true
			continue
		}
		n := 0
		switch wellKnown {
		case 'T': // Text
			header, err := r.readByte()
			if err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			r.length--
			if header&(0b1<<7) != 0 { // Don't bother with UTF-16.
				r.skip = true
				continue
			}
			// Skip language.
			langLen := int(header & 0b111111)
			if langLen > r.length {
				return 0, errors.New("ndef: message: text language too long")
			}
			if err := r.discard(buf, int(langLen)); err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			r.length -= langLen
		case 'U': // URI
			header, err := r.readByte()
			if err != nil {
				return 0, fmt.Errorf("ndef: message: %w", err)
			}
			r.length--
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
				r.skip = true
				continue
			}
			n = copy(buf, prefix)
			return n, nil
		default:
			r.skip = true
			continue
		}
	}
}

func (r *RecordReader) readByte() (byte, error) {
	b := r.scratch[:1]
	_, err := io.ReadFull(r.r, b)
	return b[0], err
}

func (r *RecordReader) discard(buf []byte, n int) error {
	for n > 0 {
		l := min(len(buf), n)
		rn, err := r.r.Read(buf[:l])
		n -= rn
		if err != nil {
			return err
		}
	}
	return nil
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
