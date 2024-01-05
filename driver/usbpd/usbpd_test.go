package usbpd

import (
	"bytes"
	"testing"
	"time"
)

func TestRoundtrip(t *testing.T) {
	var intervals []uint8
	state := false
	var interval uint8
	tx := func(v bool) {
		if v == state {
			return
		}
		state = v
		intervals = append(intervals, interval)
		interval = 0
	}
	sleep := func(d time.Duration) {
		interval += uint8(d / (100 * time.Nanosecond))
	}
	want := encodeHeader(nil, GoodCRC, 0b101, 0b10, 0)
	transmit(tx, sleep, want)
	got, ok := decode(nil, intervals)
	if !ok {
		t.Fatal("message failed to roundtrip")
	}
	if !bytes.Equal(want, got) {
		t.Errorf("message rountripped to %v, want %v", got, want)
	}
}
