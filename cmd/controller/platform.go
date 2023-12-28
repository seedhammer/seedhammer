package main

import (
	"time"
)

var sdcard = make(chan bool, 1)

func (p *Platform) Now() time.Time {
	return time.Now()
}

// SDCard returns a channel that is notified whenever
// an microSD card is inserted or removed.
func (p *Platform) SDCard() <-chan bool {
	return sdcard
}
