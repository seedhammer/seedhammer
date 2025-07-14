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

func Scan(d poller.Device) iter.Seq[any] {
	return func(yield func(any) bool) {
		buf := make([]byte, 8*1024)
		p := poller.New(d)
		defer d.Close()
		for {
			var err error
			n := 0
			for err == nil {
				var nn int
				nn, err = p.Read(buf[n:])
				n += nn
				if err == nil && n == len(buf) {
					err = io.ErrShortBuffer
				}
			}

			if err != nil && err != io.EOF {
				log.Printf("nfc poller: %v", err)
				time.Sleep(1 * time.Second)
				continue
			}
			y := true
			if buf := buf[:n]; len(buf) > 0 {
				if m, err := bip39.Parse(buf); err == nil {
					y = yield(m)
					continue
				}
				if bytes.Equal(buf, []byte("FOREVERLAURA!")) {
					y = yield(debugPlan{})
					continue
				}
			}
			if !y {
				break
			}
		}
	}
}

type debugPlan struct{}
