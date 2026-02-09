// package uf2 implements the [UF2] file format.
//
// [UF2]: https://github.com/microsoft/uf2
package uf2

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type Reader struct {
	StartAddr uint32

	r      io.Reader
	addr   uint32
	family FamilyID
	header blockHeader
	footer blockFooter
	// idx into the payload of the current block.
	idx uint32
}

type FamilyID uint32

const (
	FamilyRP2350ARMSigned FamilyID = 0xe48bff59
)

type blockHeader struct {
	b [headerSize]byte
}

// blockFooter has enough space for the payload padding
// and the footer.
type blockFooter struct {
	b [blockSize - headerSize]byte
}

const (
	blockSize  = 512
	headerSize = 32
	footerSize = 4
	magic1     = 0x0A324655
	magic2     = 0x9E5D5157
	magicEnd   = 0x0AB16F30

	payloadSize = 256

	flagNotMainFlash  = 0x00000001
	flagFamilyID      = 0x00002000
	flagFileContainer = 0x00001000
	flagMD5Checksum   = 0x00004000
	flagExtTags       = 0x00008000
)

func NewReader(r io.Reader, family FamilyID) *Reader {
	return &Reader{
		r:      r,
		family: family,
		// Set index so the first read won't read a block footer.
		idx: blockSize - headerSize,
	}
}

func (r *Reader) Read(buf []byte) (int, error) {
	if err := r.loadBlock(); err != nil {
		return 0, err
	}
	n := min(len(buf), int(r.header.PayloadSize()-r.idx))
	n, err := r.r.Read(buf[:n])
	r.idx += uint32(n)
	return n, err
}

func (r *Reader) Write(buf []byte) (int, error) {
	bytes := 0
	w, ok := r.r.(io.Writer)
	if !ok {
		return 0, errors.New("writes not supported")
	}
	for {
		if err := r.loadBlock(); err != nil {
			return 0, err
		}
		n := min(int(r.header.PayloadSize()-r.idx), len(buf))
		var err error
		if n > 0 {
			n, err = w.Write(buf[:n])
		}
		buf = buf[n:]
		bytes += n
		r.idx += uint32(n)
		if err != nil || len(buf) == 0 {
			return bytes, err
		}
	}
}

func (r *Reader) loadBlock() error {
	if r.idx < r.header.PayloadSize() {
		return nil
	}
	prevPayload := r.header.PayloadSize()
	for {
		// Read footer of previous block, if any.
		if n := len(r.footer.b) - int(r.idx); n > 0 {
			footer := r.footer.b[:n]
			if _, err := io.ReadFull(r.r, footer); err != nil {
				return err
			}
			me := binary.LittleEndian.Uint32(footer[len(footer)-4:])
			if me != magicEnd {
				return errors.New("uf2: invalid footer magic")
			}
		}

		r.idx = 0
		// Read header.
		if _, err := io.ReadFull(r.r, r.header.b[:]); err != nil {
			return err
		}
		bo := binary.LittleEndian
		m0 := bo.Uint32(r.header.b[0:4])
		m1 := bo.Uint32(r.header.b[4:8])
		if m0 != magic1 || m1 != magic2 {
			return errors.New("uf2: invalid header magic")
		}
		flags := r.header.Flags()
		if flags&flagFamilyID == 0 || r.header.FamilyID() != uint32(r.family) {
			continue
		}
		flags &^= flagFamilyID
		// Reject all other flags.
		if flags != 0 {
			return fmt.Errorf("uf2: unsupported flags: %x", flags)
		}
		addr := r.header.TargetAddr()
		if r.StartAddr == 0 {
			r.StartAddr = addr
			r.addr = addr
		}
		// Reject non-contiguous data.
		if addr != r.addr+prevPayload {
			return errors.New("uf2: non-contiguous data")
		}
		r.addr = addr
		return nil
	}
}

func (b *blockHeader) Flags() uint32 {
	return b.getHeader(8)
}

func (b *blockHeader) SetFlags(f uint32) {
	b.setHeader(8, f)
}

func (b *blockHeader) TargetAddr() uint32 {
	return b.getHeader(12)
}

func (b *blockHeader) SetTargetAddr(a uint32) {
	b.setHeader(12, a)
}

func (b *blockHeader) PayloadSize() uint32 {
	return b.getHeader(16)
}

func (b *blockHeader) SetPayloadSize(s uint32) {
	b.setHeader(16, s)
}

func (b *blockHeader) BlockNo() uint32 {
	return b.getHeader(20)
}

func (b *blockHeader) SetBlockNo(bno uint32) {
	b.setHeader(20, bno)
}

func (b *blockHeader) NumBlocks() uint32 {
	return b.getHeader(24)
}

func (b *blockHeader) SetNumBlocks(nb uint32) {
	b.setHeader(24, nb)
}

func (b *blockHeader) FamilyID() uint32 {
	return b.getHeader(28)
}

func (b *blockHeader) SetFamilyID(f uint32) {
	b.setHeader(28, f)
}

func (b *blockHeader) getHeader(off int) uint32 {
	return binary.LittleEndian.Uint32(b.b[off : off+4])
}

func (b *blockHeader) setHeader(off int, v uint32) {
	binary.LittleEndian.PutUint32(b.b[off:off+4], v)
}
