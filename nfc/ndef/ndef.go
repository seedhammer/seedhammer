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
	scratch [2]byte
}

func NewReader(rd io.Reader) *Reader {
	return &Reader{
		// Limit buffer size to the (usually) slow
		// NFC interface.
		r: bufio.NewReaderSize(rd, 256),
	}
}

func (r *Reader) Read(buf []byte) (int, error) {
	// Read until a NDEF message is reached.
	var length int
	for {
		typ, err := r.r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("ndef: %w", err)
		}
		length8, err := r.r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("ndef: %w", err)
		}
		length = int(length8)
		if length8 == 0xff {
			// 2-byte length.
			l16 := r.scratch[:2]
			if _, err := io.ReadFull(r.r, l16); err != nil {
				return 0, fmt.Errorf("ndef: %w", err)
			}
			length = int(binary.BigEndian.Uint16(l16))
		}
		if typ == ndefType {
			break
		}
		// Skip record.
		if _, err := r.r.Discard(length); err != nil {
			return 0, fmt.Errorf("ndef: %w", err)
		}
	}
	if length < 6 {
		return 0, fmt.Errorf("ndef: record too short")
	}
	if len(buf) < length {
		return 0, io.ErrShortBuffer
	}
	msg := buf[:length]
	if _, err := io.ReadFull(r.r, msg); err != nil {
		return 0, fmt.Errorf("ndef: %w", err)
	}
	header, tlen, plen := msg[0], msg[1], msg[2]
	if header != 0b11010_001 || tlen != 1 { // TODO: do better
		return 0, errors.New("ndef: unsupported ndef header")
	}
	payload := msg[4 : 4+plen]
	switch mimeType := msg[3]; mimeType {
	case 'T':
		header := payload[0]
		if header&(0b1<<7) != 0 { // Don't bother with UTF-16.
			return 0, errors.New("ndef: unsupported text encoding")
		}
		payload = payload[1:]
		// Skip language.
		langLen := int(header & 0b111111)
		if langLen > len(payload) {
			return 0, errors.New("ndef: text language too long")
		}
		payload = payload[langLen:]
		copy(buf, payload)
		return len(payload), io.EOF
	case 'U':
		// URI.
		prefix := ""
		switch p := payload[0]; p {
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
			return 0, errors.New("ndef: unsupported URI prefix")
		}
		payload = payload[1:]
		n := len(payload) + len(prefix)
		if len(buf) < n {
			return 0, io.ErrShortBuffer
		}
		copy(buf[len(prefix):], payload)
		copy(buf, prefix)
		return n, io.EOF
	}
	return 0, errors.New("ndef: unsupported payload type")
}

const (
	ndefType = 0x03

	uriPrefixNone     = 0x00
	uriPrefixHttpWww  = 0x01
	uriPrefixHttpsWww = 0x02
	uriPrefixHttp     = 0x03
	uriPrefixHttps    = 0x04
)
