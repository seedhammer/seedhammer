// Package poller implements a NFC device poller for accepting
// data from either tags or writers.
package poller

import (
	"bufio"
	"io"

	"seedhammer.com/nfc/ndef"
	"seedhammer.com/nfc/type2"
	"seedhammer.com/nfc/type4"
	"seedhammer.com/nfc/type5"
)

type Device interface {
	Close() error
	Detect() (bool, error)
	SetProtocol(prot Protocol) error
	Sleep() error
	ReadCapacity() int
	io.ReadWriter
}

type Poller struct {
	d    Device
	bufr *bufio.Reader
	emu  *type4.Tag
	// r is the active reader.
	r io.Reader
}

type Protocol int

const (
	ISO14443a Protocol = iota
	ISO15693
)

func New(d Device) *Poller {
	return &Poller{
		d:    d,
		bufr: bufio.NewReaderSize(nil, 256),
		emu:  type4.NewTag(d),
	}
}

func (p *Poller) Read(buf []byte) (int, error) {
	for {
		if p.r != nil {
			n, err := p.r.Read(buf)
			if err != nil {
				if err != io.EOF || n == 0 {
					p.r = nil
				}
			}
			return n, err
		}
		active, err := p.d.Detect()
		if err != nil {
			return 0, err
		}
		var r io.Reader
		if active {
			// Reset the tag emulator when the
			// external field is off.
			p.emu.Reset()

			r, err = p.poll()
			if err != nil {
				return 0, err
			}
			if r == nil {
				continue
			}
			p.bufr.Reset(r)
			r = ndef.NewMessageReader(p.bufr)
		} else {
			p.bufr.Reset(p.emu)
			r = p.bufr
		}
		p.r = ndef.NewRecordReader(r)
	}
}

// poll attempts to select a tag, trying each protocol in turn.
func (p *Poller) poll() (io.Reader, error) {
	if err := p.d.SetProtocol(ISO15693); err != nil {
		return nil, err
	}
	tag15693, err := type5.NewReader(p.d, p.d.ReadCapacity())
	if err == nil {
		return tag15693, nil
	}
	if err := p.d.SetProtocol(ISO14443a); err != nil {
		return nil, err
	}
	tag14443, err := type2.NewReader(p.d)
	if err != nil {
		// Ignore read errors.
		return nil, nil
	}
	return tag14443, nil
}
