package ndef

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type Reader struct {
	r       *bufio.Reader
	scratch [4]byte
}

func NewReader(rd io.Reader) *Reader {
	return &Reader{
		// Limit buffer size to the (usually) slow
		// NFC interface.
		r: bufio.NewReaderSize(rd, 256),
	}
}

// Read the next NDEF record, or [io.EOF] if no more records
// are available.
func (r *Reader) Read(buf []byte) (int, error) {
	bo := binary.BigEndian
	for {
		typLen := r.scratch[:2]
		if _, err := io.ReadFull(r.r, typLen); err != nil {
			if err == io.EOF {
				return 0, io.EOF
			}
			return 0, fmt.Errorf("ndef: %w", err)
		}
		// Read type and length.
		typ, length8 := typLen[0], typLen[1]
		length := int(length8)
		if length8 == 0xff {
			// 2-byte length.
			l16 := r.scratch[:2]
			if _, err := io.ReadFull(r.r, l16); err != nil {
				return 0, fmt.Errorf("ndef: %w", err)
			}
			length = int(bo.Uint16(l16))
		}
		// Skip non-NDEF containers.
		if typ != ndefType {
			if _, err := r.r.Discard(length); err != nil {
				return 0, fmt.Errorf("ndef: %w", err)
			}
			continue
		}
		var discard int
		for {
			if discard > 0 {
				if _, err := r.r.Discard(discard); err != nil {
					return 0, fmt.Errorf("ndef: %w", err)
				}
				discard = 0
			}
			// Read the header and type length.
			h := r.scratch[:2]
			if length < len(h) {
				return 0, fmt.Errorf("ndef: record too short")
			}
			if _, err := io.ReadFull(r.r, h); err != nil {
				return 0, fmt.Errorf("ndef: %w", err)
			}
			flags, tlen := h[0], h[1]
			var plen int
			// Read payload length.
			if flags&flagSR == 0 {
				// 32-bit length.
				b := r.scratch[:4]
				if _, err := io.ReadFull(r.r, b); err != nil {
					return 0, fmt.Errorf("ndef: %w", err)
				}
				plen = int(bo.Uint32(b))
			} else {
				// Short record.
				l, err := r.r.ReadByte()
				if err != nil {
					return 0, fmt.Errorf("ndef: %w", err)
				}
				plen = int(l)
			}
			// Read ID length.
			var idLen uint8
			if flags&flagIR != 0 {
				l, err := r.r.ReadByte()
				if err != nil {
					return 0, fmt.Errorf("ndef: %w", err)
				}
				idLen = l
			}
			// Read the well-known type byte, if any.
			var wellKnown byte
			if tlen == 1 {
				t, err := r.r.ReadByte()
				if err != nil {
					return 0, fmt.Errorf("ndef: %w", err)
				}
				tlen--
				wellKnown = t
			}
			// Skip the (remaining) type and id.
			if _, err := r.r.Discard(int(tlen) + int(idLen)); err != nil {
				return 0, fmt.Errorf("ndef: %w", err)
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
			switch wellKnown {
			case 'T': // Text
				header, err := r.r.ReadByte()
				if err != nil {
					return 0, fmt.Errorf("ndef: %w", err)
				}
				plen--
				if header&(0b1<<7) != 0 { // Don't bother with UTF-16.
					discard = plen
					continue
				}
				// Skip language.
				langLen := int(header & 0b111111)
				if langLen > plen {
					return 0, errors.New("ndef: text language too long")
				}
				if _, err := r.r.Discard(int(langLen)); err != nil {
					return 0, fmt.Errorf("ndef: %w", err)
				}
				plen := plen - langLen
				rem := min(int(plen), len(buf))
				n, err := io.ReadFull(r.r, buf[:rem])
				if err != nil {
					return n, fmt.Errorf("ndef: %w", err)
				}
				if n < plen {
					return n, io.ErrShortBuffer
				}
				return n, io.EOF
			case 'U': // URI
				header, err := r.r.ReadByte()
				if err != nil {
					return 0, fmt.Errorf("ndef: %w", err)
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
				n := copy(buf, prefix)
				buf = buf[n:]
				rem := min(int(plen), len(buf))
				pn, err := io.ReadFull(r.r, buf[:rem])
				n += pn
				if err != nil {
					return n, fmt.Errorf("ndef: %w", err)
				}
				if n < int(plen)+len(prefix) {
					return n, io.ErrShortBuffer
				}
				return n, io.EOF
			}
			discard = plen
		}
	}
}

const (
	ndefType = 0x03

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
