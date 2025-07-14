package gui

import (
	"bytes"
	"io"
	"iter"
	"log"
	"time"

	"seedhammer.com/bip39"
	"seedhammer.com/nfc/poller"
)

type scanResult struct {
	Content any
	Status  scanStatus
}

type scanStatus int

const (
	scanIdle scanStatus = iota
	scanStarted
	scanFailed
	scanOverflow
	scanUnknownFormat
)

func Scan(d poller.Device) iter.Seq[scanResult] {
	return func(yield func(scanResult) bool) {
		buf := make([]byte, 8*1024)
		p := poller.New(d)
		defer d.Close()
		for {
			var err error
			n := 0
			st := scanStarted
			for err == nil {
				if n == len(buf) {
					// Use the buffer to discard the rest of the content.
					n = 0
					st = scanOverflow
				}
				var nn int
				nn, err = p.Read(buf[n:])
				if n == 0 && !yield(scanResult{Status: st}) {
					return
				}
				n += nn
			}

			if err == io.EOF {
				err = nil
			}
			if err != nil {
				log.Printf("nfc poller: %v", err)
				if !yield(scanResult{Status: scanFailed}) {
					return
				}
				time.Sleep(1 * time.Second)
				continue
			}
			res := scanResult{}
			if buf := buf[:n]; len(buf) > 0 {
				if m, err := bip39.Parse(buf); err == nil {
					res.Content = m
				} else if bytes.Equal(buf, []byte("FOREVERLAURA!")) {
					res.Content = debugPlan{}
				} else {
					res.Status = scanUnknownFormat
				}
			}
			if !yield(res) {
				return
			}
		}
	}
}

type debugPlan struct{}
