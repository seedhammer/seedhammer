package uf2

import (
	"bytes"
	_ "embed"
	"io"
	"slices"
	"testing"
)

//go:embed testdata/2blocks.uf2
var twoBlocksUF2 []byte

func TestRead(t *testing.T) {
	r := NewReader(bytes.NewReader(twoBlocksUF2), FamilyRP2350ARMSigned)
	buf := make([]byte, blockSize)
	n, err := r.Read(buf[:])
	if err != nil {
		t.Fatal(err)
	}
	buf = buf[:n]
	const addr = 0x10000000
	if r.StartAddr != addr {
		t.Errorf("got start address %x, expected %x", r.StartAddr, addr)
	}
}

func TestWrite(t *testing.T) {
	data := make([]byte, 500)
	for i := range data {
		data[i] = byte(i)
	}
	w := &seekableBuffer{data: slices.Clone(twoBlocksUF2)}
	r2 := NewReader(w, FamilyRP2350ARMSigned)
	if _, err := r2.Write(data); err != nil {
		t.Fatal(err)
	}
	r := &seekableBuffer{data: w.data}
	r3 := NewReader(r, FamilyRP2350ARMSigned)
	buf2 := make([]byte, len(data))
	n, err := io.ReadFull(r3, buf2[:])
	if err != nil {
		t.Fatal(err)
	}
	buf2 = buf2[:n]
	if !slices.Equal(data, buf2) {
		t.Errorf("wrote %x..., yet read %x...", data[:10], buf2[:10])
	}
}

type seekableBuffer struct {
	data []byte
	idx  int
}

func (s *seekableBuffer) Read(b []byte) (int, error) {
	n := copy(b, s.data[s.idx:])
	s.idx += n
	return n, nil
}

func (s *seekableBuffer) Write(b []byte) (int, error) {
	n := copy(s.data[s.idx:], b)
	s.idx += n
	return n, nil
}
