package gui

import (
	"bytes"
	"errors"
	"io"
	"log"

	"seedhammer.com/bip39"
	"seedhammer.com/codex32"
	"seedhammer.com/nonstandard"
)

type scanner struct {
	buf      []byte
	n        int
	overflow bool
}

var (
	errScanInProgress    = errors.New("scan: in progress")
	errScanOverflow      = errors.New("scan: buffer overflow")
	errScanUnknownFormat = errors.New("scan: unknown format")
)

func (s *scanner) Scan(r io.Reader) (any, error) {
	if cap(s.buf) == 0 {
		s.buf = make([]byte, 8*1024)
	}
	nn, err := r.Read(s.buf[s.n:])
	s.n += nn
	s.overflow = s.overflow || s.n == len(s.buf)
	if s.overflow {
		// Discard the rest of the content.
		s.n = 0
		return nil, errScanOverflow
	}
	s.overflow = false
	switch err {
	case io.EOF:
	case nil:
		// Report progress.
		return nil, errScanInProgress
	default:
		log.Printf("nfc poller: %v", err)
		s.n = 0
		return nil, err
	}

	buf := s.buf[:s.n]
	s.n = 0
	if len(buf) == 0 {
		return nil, nil
	}
	const cmdPrefix = "command: "
	if bytes.HasPrefix(buf, []byte(cmdPrefix)) {
		cmd := debugCommand{string(buf[len(cmdPrefix):])}
		return cmd, nil
	} else if m, err := bip39.Parse(buf); err == nil {
		return m, nil
		// TODO: re-enable SLIP39 support. Note that
		// github.com/gavincarr/go-slip39 adds ~55kb of RAM use in the unicode
		// package.
		// } else if m, err := slip39.ParseShare(sbuf); err == nil {
		// 	res.Content = m
	} else if d, err := nonstandard.OutputDescriptor(buf); err == nil {
		return d, nil
	} else if s, err := codex32.New(string(buf)); err == nil {
		return s, nil
	} else {
		return nil, errScanUnknownFormat
	}
}

type debugCommand struct {
	Command string
}
