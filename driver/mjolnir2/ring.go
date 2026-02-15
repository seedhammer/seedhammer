package mjolnir2

import (
	"sync/atomic"
)

// ring implements a ring buffer suitable
// for feeding a DMA transfer.
type ring struct {
	buf      []uint32
	readIdx  int
	writeIdx int
}

func newRing(buf []uint32) *ring {
	return &ring{
		buf: buf,
	}
}

func (r *ring) AdvanceRead(readIdxFromEnd int) (completed int) {
	readIdx := len(r.buf) - readIdxFromEnd
	if readIdx == len(r.buf) {
		readIdx = 0
	}
	readEnd := readIdx
	if readIdx < r.readIdx {
		// Reader wrapped around.
		readEnd += len(r.buf)
	}
	completed = int(readEnd - r.readIdx)
	r.readIdx = readIdx
	return completed
}

func (r *ring) Write(data []uint32) int {
	wrote := 0
	for {
		var writable int
		if ridx := r.readIdx; ridx <= r.writeIdx {
			writable = len(r.buf) - (r.writeIdx - ridx)
		} else {
			writable = ridx - r.writeIdx
		}
		endWritable := len(r.buf) - r.writeIdx
		n := min(
			writable-1,  // Leave room for the halting zero.
			len(data),   // Available data.
			endWritable, // Writable until wrap-around.
		)
		if n <= 0 {
			return wrote
		}
		dst := r.buf[r.writeIdx : r.writeIdx+n]
		if r.writeIdx += len(dst); r.writeIdx == len(r.buf) {
			r.writeIdx = 0
		}
		// Write halting zero.
		r.buf[r.writeIdx] = 0
		// Copy data, yet don't overwrite the previous
		// halting zero.
		copy(dst[1:], data[1:n])
		// Use atomics to overwrite the previous zero to
		// avoid the write being re-ordered with the copy
		// above.
		atomic.StoreUint32(&dst[0], data[0])
		data = data[n:]
		wrote += n
	}
}

func (r *ring) Reset() {
	r.writeIdx = 0
	r.readIdx = 0
	// Maintain the invariant that r.buf[d.writeIdx]
	// always contains a halting 0.
	r.buf[r.writeIdx] = 0
}
