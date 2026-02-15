package mjolnir2

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestRing(t *testing.T) {
	buf := make([]uint32, 1024)
	r := newRing(buf)
	want := make([]uint32, 8000)
	for i := range want {
		want[i] = uint32(i + 1)
	}
	var got []uint32
	data := want
	avail := 0
	s1, s2 := rand.Uint64(), rand.Uint64()
	rng := rand.New(rand.NewPCG(s1, s2))
	for len(got) != len(want) {
		nw := rng.IntN(len(data) + 1)
		wrote := r.Write(data[:nw])
		data = data[wrote:]
		avail += wrote
		if r.buf[r.writeIdx] != 0 {
			t.Fatalf("seed %x/%x: missing halting zero after writing %d", s1, s2, wrote)
		}
		nr := rng.IntN(avail + 1)
		for i := range nr {
			got = append(got, ringRead(r, i))
		}
		avail -= nr
		ridx := (r.readIdx + nr) % len(r.buf)
		completed := r.AdvanceRead(len(r.buf) - ridx)
		if completed != nr {
			t.Fatalf("seed %x/%x: ring reported %d reads, expected %d", s1, s2, completed, nr)
		}
	}
	if !slices.Equal(want, got) {
		t.Fatalf("seed %x/%x: data read did not match data written", s1, s2)
	}
}

func ringRead(r *ring, idx int) uint32 {
	i := (r.readIdx + idx) % len(r.buf)
	return r.buf[i]
}
