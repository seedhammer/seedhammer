// Package picobin implements the block format for the
// Rapsberry Pi rp2XXX family of microcontrollers.
// The format is described in section 5.9 of the rp2350
// [datasheet].
//
// [datasheet]: https://datasheets.raspberrypi.com/rp2350/rp2350-datasheet.pdf
package picobin

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
)

type Image struct {
	r                *imageReader
	blockStartOffset uint32
	loadMapOffset    uint32
	hashDefOffset    uint32
	hashValueOffset  uint32
	SignatureOffset  uint32
}

type itemHeader struct {
	itype byte
	size  uint16
	data  uint16
}

const (
	header = 0xffffded3
	footer = 0xab123579

	blockItemNextBlockOffset    = 0x41
	blockItemImageType          = 0x42
	blockItemVectorTable        = 0x03
	blockItemEntryPoint         = 0x44
	blockItemRollingWindowDelta = 0x05
	blockItemLoadMap            = 0x06
	blockItemHashDef            = 0x47
	blockItemVersion            = 0x48
	blockItemSignature          = 0x09
	blockItemPartitionTable     = 0x0a
	blockItemHashValue          = 0x4b
	blockItemSalt               = 0x0c
	blockItemIgnored            = 0x7e
	blockItemLast               = 0x7f

	hashSHA256   = 0x01
	sigSecp256k1 = 0x01

	sigSecp256k1Len = 128

	// Avoid "lollipop" loops.
	maxLoopLen = 100
)

func NewImage(img io.ReadSeeker) (*Image, error) {
	bin, err := read(img)
	if err != nil {
		return nil, fmt.Errorf("picobin: %v", err)
	}
	return bin, nil
}

// Sign image to w by replacing its HASH_VALUE or SIGNATURE with a SIGNATURE item
// containing the public key and signature.
// Sign assumes that the replaced item is the last item in the
// block with the highest address.
func (img *Image) Sign(w io.Writer, pubKey, signature []byte) error {
	var err error
	switch {
	case img.hashValueOffset != 0:
		err = img.signHashed(w, pubKey, signature)
	case img.SignatureOffset != 0:
		err = img.resign(w, pubKey, signature)
	default:
		err = errors.New("picobin: missing SIGNATURE or HASH_VALUE item")
	}
	if err != nil {
		return fmt.Errorf("picobin: %w", err)
	}
	return nil
}

func (inf *Image) resign(w io.Writer, pubKey, signature []byte) error {
	oldKey, oldSig, err := inf.Signature()
	if err != nil {
		return err
	}
	if len(oldKey) != len(pubKey) || len(oldSig) != len(signature) {
		return errors.New("incompatible signature")
	}
	if _, err := inf.r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.CopyN(w, inf.r, int64(inf.SignatureOffset)); err != nil {
		return err
	}
	w.Write(pubKey)
	w.Write(signature)
	n := int64(len(pubKey) + len(signature))
	if _, err := inf.r.Seek(n, io.SeekCurrent); err != nil {
		return err
	}
	_, err = io.Copy(w, inf.r)
	return err
}

func (inf *Image) signHashed(w io.Writer, pubKey, signature []byte) error {
	imgHash, err := inf.Hash()
	if err != nil {
		return err
	}
	hashItemSize := 4 + len(imgHash)
	lastItem := inf.hashValueOffset + uint32(hashItemSize)
	last := readItemHeader(inf.r, lastItem)
	if last.itype != blockItemLast {
		return errors.New("HASH_VALUE is not last in block")
	}
	// Read block link.
	link, err := readFooter(inf.r, lastItem+4)
	if err != nil {
		return err
	}
	// Copy image up to just before the HASH_VALUE item.
	if _, err := inf.r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.CopyN(w, inf.r, int64(inf.hashValueOffset)); err != nil {
		return err
	}
	bo := binary.LittleEndian
	// Write a SIGNATURE_ITEM.
	sigItemSize := 4 + len(pubKey) + len(signature)
	header := uint32(blockItemSignature) | // SIGNATURE item.
		uint32(sigItemSize/4)<<8 | // Size in words.
		sigSecp256k1<<24 // Algorithm.
	u32 := make([]byte, 4)
	bo.PutUint32(u32, header)
	w.Write(u32)
	w.Write(pubKey)
	w.Write(signature)
	// Adjust size because of expanded item.
	sizeAdj := uint32(sigItemSize - hashItemSize)
	// Add footer.
	header = 0x80 | uint32(blockItemLast) | // LAST_ITEM with 2-byte size.
		(uint32(last.size)+sizeAdj/4)<<8
	bo.PutUint32(u32, header)
	w.Write(u32)
	bo.PutUint32(u32, link)
	w.Write(u32)
	bo.PutUint32(u32, footer)
	w.Write(u32)
	return nil
}

type imageReader struct {
	r   io.ReadSeeker
	pos int64
	buf [4]byte
	err error
}

func newImageReader(r io.ReadSeeker) *imageReader {
	return &imageReader{r: r}
}

func (r *imageReader) Uint32(idx uint32) uint32 {
	if _, err := r.r.Seek(int64(idx), io.SeekStart); err != nil {
		return 0
	}
	buf := r.buf[:4]
	if _, err := io.ReadFull(r.r, buf); err != nil {
		return 0
	}
	return binary.LittleEndian.Uint32(buf)
}

func (r *imageReader) Seek(offset int64, whence int) (int64, error) {
	if r.err != nil {
		return r.pos, r.err
	}
	n, err := r.r.Seek(offset, whence)
	r.pos = n
	r.err = err
	return r.pos, r.err
}

func (r *imageReader) Read(d []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.r.Read(d)
	r.pos += int64(n)
	r.err = err
	return n, r.err
}

func read(data io.ReadSeeker) (*Image, error) {
	img := &Image{
		r: newImageReader(data),
	}
	idx := uint32(0)
	// Scan first 4k for first block header.
	for range 1024 {
		h := img.r.Uint32(idx)
		if h == header {
			break
		}
		idx += 4
	}
	firstBlock := idx
	hidx := idx
	nblocks := 0
	for {
		h := img.r.Uint32(idx)
		if h != header {
			return nil, errors.New("missing block header")
		}
		img.blockStartOffset = idx
		idx += 4
		totalSize := uint(0)
		// Scan items.
		for {
			h := readItemHeader(img.r, idx)
			if h.size == 0 {
				return nil, errors.New("zero-sized block item")
			}
			if h.itype == blockItemLast {
				if totalSize != uint(h.size) {
					return nil, errors.New("mismatched total item size")
				}
				break
			}
			totalSize += uint(h.size)
			switch h.itype {
			case blockItemLoadMap:
				img.loadMapOffset = idx
				img.hashDefOffset = 0
				img.SignatureOffset = 0
				img.hashValueOffset = 0
			case blockItemHashDef:
				img.hashDefOffset = idx
				img.SignatureOffset = 0
				img.hashValueOffset = 0
			case blockItemHashValue:
				img.hashValueOffset = idx
			case blockItemSignature:
				if int(h.size) != 32+1 {
					return nil, errors.New("invalid SIGNATURE item size")
				}
				img.SignatureOffset = idx + 4
			}
			idx += uint32(h.size) * 4
		}
		// Verify footer and jump to next block in loop.
		link, err := readFooter(img.r, idx+4)
		if err != nil {
			return nil, err
		}
		nblocks++
		if nblocks == maxLoopLen {
			return nil, errors.New("block loop too long")
		}
		hidx += link
		if hidx == firstBlock {
			break
		}
		idx = hidx
	}
	return img, img.r.err
}

func (in *Image) Signature() (pubKey []byte, sig []byte, err error) {
	off := in.SignatureOffset
	h := readItemHeader(in.r, off-4)
	if h.itype != blockItemSignature {
		return nil, nil, errors.New("picobin: missing SIGNATURE item")
	}
	data := make([]byte, 128)
	_, err = io.ReadFull(in.r, data)
	pubKey, sig = data[:64], data[64:]
	return pubKey, sig, err
}

func (in *Image) Hash() ([]byte, error) {
	h := readItemHeader(in.r, in.hashValueOffset)
	if h.itype != blockItemHashValue {
		return nil, errors.New("picobin: missing HASH_VALUE item")
	}
	hash := make([]byte, h.size*4-4)
	_, err := io.ReadFull(in.r, hash)
	return hash, err
}

func (in *Image) HashData(img io.ReadSeeker, imageAddr uint32) ([]byte, error) {
	r := newImageReader(img)
	// Read HASH_DEF item.
	h := readItemHeader(r, in.hashDefOffset)
	if h.itype != blockItemHashDef {
		return nil, errors.New("picobin: missing HASH_DEF item")
	}
	if a := h.data >> 8; a != hashSHA256 {
		return nil, errors.New("unknown HASH_DEF hash algorithm")
	}
	// Read number of block bytes to hash.
	blockHashed := 4 * (r.Uint32(in.hashDefOffset+4) & 0xffff)
	hasher := sha256.New()
	buf := make([]byte, 1024)
	// Read LOAD_MAP item.
	h = readItemHeader(r, in.loadMapOffset)
	if h.itype != blockItemLoadMap {
		return nil, errors.New("picobin: missing LOAD_MAP item")
	}
	// Read LOAD_MAP entries.
	nentries := (h.size - 1) / 3
	absolute := h.data&0x8000 != 0
	eidx := in.loadMapOffset + 4
	for i := range uint32(nentries) {
		storageStart := r.Uint32(eidx + i*12 + 0)
		size := r.Uint32(eidx + i*12 + 8)
		if storageStart == 0 {
			// The size itself is hashed, not the storage.
			if err := hashData(r, hasher, buf, eidx+8, 4); err != nil {
				return nil, err
			}
			continue
		}
		if absolute {
			size -= storageStart
			storageStart -= imageAddr
		} else {
			storageStart += in.loadMapOffset
		}
		if err := hashData(r, hasher, buf, storageStart, size); err != nil {
			return nil, err
		}
	}
	// Hash block.
	if err := hashData(r, hasher, buf, in.blockStartOffset, blockHashed); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), r.err
}

func hashData(r io.ReadSeeker, h hash.Hash, buf []byte, idx, size uint32) error {
	if _, err := r.Seek(int64(idx), io.SeekStart); err != nil {
		return err
	}
	for size > 0 {
		buf := buf[:min(len(buf), int(size))]
		n, err := r.Read(buf)
		size -= uint32(n)
		h.Write(buf[:n])
		if err != nil {
			if err == io.EOF && size == 0 {
				break
			}
			return err
		}
	}
	return nil
}

func readFooter(r *imageReader, idx uint32) (uint32, error) {
	link, f := r.Uint32(idx), r.Uint32(idx+4)
	if f != footer {
		return 0, errors.New("missing block footer")
	}
	return link, nil
}

func readItemHeader(r *imageReader, idx uint32) itemHeader {
	w := r.Uint32(idx)
	typeAndSize := byte(w)
	sflag := typeAndSize & 0x80
	h := itemHeader{
		itype: typeAndSize & 0x7f,
		size:  uint16((w >> 8) & 0xff),
		data:  uint16(w >> 16),
	}
	if sflag != 0 {
		// 2-byte size.
		h.size |= uint16((w >> 8) & 0xff00)
	}
	return h
}
