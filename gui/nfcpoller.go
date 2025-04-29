package gui

import (
	"errors"
	"io"
	"iter"
	"log"
	"time"

	"seedhammer.com/bip39"
	"seedhammer.com/nfc/iso14443a"
	"seedhammer.com/nfc/iso15693"
	"seedhammer.com/nfc/ndef"
)

type nfcPoller struct {
	dev   NFCDevice
	buf   []byte
	trans *iso15693.Transceiver
}

const pollFrequency = 500 * time.Millisecond

func Scan(d NFCDevice, quit <-chan struct{}) iter.Seq[any] {
	return func(yield func(any) bool) {
		p := &nfcPoller{
			dev:   d,
			buf:   make([]byte, 8*1024),
			trans: iso15693.NewTransceiver(d, d.FIFOSize()),
		}
		defer p.dev.RadioOff()
		for {
			content, err := p.poll(quit)
			if err != nil {
				log.Printf("nfc poller failed: %v", err)
				break
			}
			if !yield(content) {
				break
			}
		}
	}
}

func (p *nfcPoller) poll(quit <-chan struct{}) (any, error) {
	var lastPoll time.Time
	for {
		if err := p.dev.RadioOn(ModeDetect); err != nil {
			return nil, err
		}
		for {
			if err := p.dev.Detect(quit); err != nil {
				return nil, err
			}
			now := time.Now()
			// Don't poll too often.
			if now.Sub(lastPoll) < pollFrequency {
				// But keep the detection loop running on the
				// device.
				continue
			}
			lastPoll = now
			break
		}
		r, err := poll(p.dev, p.trans)
		if err != nil {
			return nil, err
		}
		if r == nil {
			continue
		}
		nr := ndef.NewReader(r)
		n, err := nr.Read(p.buf)
		if err != nil && !errors.Is(err, io.EOF) {
			// Ignore read errors.
			continue
		}
		m, err := bip39.Parse(p.buf[:n])
		return m, nil
	}
}

func poll(d NFCDevice, trans *iso15693.Transceiver) (io.Reader, error) {
	if err := d.RadioOn(ModeISO15693); err != nil {
		return nil, err
	}
	tag15693, err := iso15693.Open(trans, trans.DecodedSize())
	if err == nil {
		return tag15693, nil
	}
	if err := d.RadioOn(ModeISO14443a); err != nil {
		return nil, err
	}
	tag14443, err := iso14443a.Open(d)
	if err != nil {
		// Ignore read errors.
		return nil, nil
	}
	return tag14443, nil
}
