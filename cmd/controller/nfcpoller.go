package main

import (
	"errors"
	"io"
	"log"
	"time"

	"seedhammer.com/bip39"
	"seedhammer.com/driver/st25r3916"
	"seedhammer.com/nfc/iso14443a"
	"seedhammer.com/nfc/iso15693"
	"seedhammer.com/nfc/ndef"
)

type nfcPoller struct {
	scans chan<- any
	dev   *st25r3916.Device
	buf   []byte
	trans *iso15693.Transceiver
}

const pollFrequency = 500 * time.Millisecond

func newNFCPoller(d *st25r3916.Device, scans chan<- any) *nfcPoller {
	return &nfcPoller{
		scans: scans,
		dev:   d,
		buf:   make([]byte, 8*1024),
		trans: iso15693.NewTransceiver(d, st25r3916.FIFOSize),
	}
}

func (p *nfcPoller) Run() {
	defer p.dev.RadioOff()
	for {
		content, err := p.poll()
		if err != nil {
			log.Printf("nfc poller failed: %v", err)
			break
		}
		select {
		case p.scans <- content:
		default:
		}
	}
}

func (p *nfcPoller) poll() (any, error) {
	var lastPoll time.Time
	for {
		if err := p.dev.RadioOn(st25r3916.Detect); err != nil {
			return nil, err
		}
		for {
			if err := p.dev.Detect(); err != nil {
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

func poll(d *st25r3916.Device, trans *iso15693.Transceiver) (io.Reader, error) {
	if err := d.RadioOn(st25r3916.ISO15693); err != nil {
		return nil, err
	}
	tag15693, err := iso15693.Open(trans, trans.DecodedSize())
	if err == nil {
		return tag15693, nil
	}
	if err := d.RadioOn(st25r3916.ISO14443a); err != nil {
		return nil, err
	}
	tag14443, err := iso14443a.Open(d)
	if err != nil {
		// Ignore read errors.
		return nil, nil
	}
	return tag14443, nil
}
