package gui

import (
	"io"
	"iter"
	"log"
	"time"

	"seedhammer.com/bip39"
	"seedhammer.com/nfc/poller"
)

func Scan(d poller.Device) iter.Seq[any] {
	return func(yield func(any) bool) {
		buf := make([]byte, 8*1024)
		p := poller.New(d)
		for {
			var err error
			n := 0
			for n < len(buf) && err == nil {
				var nn int
				nn, err = p.Read(buf[n:])
				n += nn
			}

			if buf := buf[:n]; len(buf) > 0 {
				m, err := bip39.Parse(buf)
				if err != nil {
					continue
				}
				if !yield(m) {
					break
				}
			}
			if err != nil && err != io.EOF {
				log.Printf("nfc poller: %v", err)
				time.Sleep(1 * time.Second)
				continue
			}
		}
	}
}
