package ndef

import (
	"bufio"
	"encoding/binary"
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

func (r *Reader) Next() error {
	typ, err := r.r.ReadByte()
	if err != nil {
		return fmt.Errorf("ndef: %w", err)
	}
	length8, err := r.r.ReadByte()
	if err != nil {
		return fmt.Errorf("ndef: %w", err)
	}
	length := uint16(length8)
	if length8 == 0xff {
		// 2-byte length.
		l16 := r.scratch[:2]
		if _, err := io.ReadFull(r.r, l16); err != nil {
			return fmt.Errorf("ndef: %w", err)
		}
		length = binary.BigEndian.Uint16(l16)
	}
	msg := make([]byte, length)
	if _, err := io.ReadFull(r.r, msg); err != nil {
		return fmt.Errorf("ndef: %w", err)
	}

	fmt.Printf("NFC Scan result: %x %q\n", msg, string(msg))
	const ndefType = 0x03
	switch typ {
	case ndefType:
		header, tlen, plen := msg[0], msg[1], msg[2]
		if header != 0b11010_001 || tlen != 1 { // TODO: do better
			break
		}
		typ := msg[3]
		if typ != 0x55 { // TODO: handle other well-known types.
			break
		}
		fmt.Print("\n\nNFC result, parsed *****:    ")
		payload := msg[4 : 4+plen]
		switch payload[0] {
		case 0x04:
			fmt.Print("https://")
		}
		fmt.Println(string(payload[1:]), "\n\n")
	}
	return nil
}
