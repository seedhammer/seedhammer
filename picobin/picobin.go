// Package picobin implements the block format for the
// Rapsberry Pi rp2XXX family of microcontrollers.
// The format is described in section 5.9 of the rp2350
// [datasheet].
//
// [datasheet]: https://datasheets.raspberrypi.com/rp2350/rp2350-datasheet.pdf
package picobin

import (
	"encoding/binary"
	"errors"
	"fmt"
)

type Image struct {
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

func Read(image []byte) (Image, error) {
	img, err := read(image)
	if err != nil {
		return Image{}, fmt.Errorf("picobin: %v", err)
	}
	return img, nil
}

// Sign image by replacing its HASH_VALUE or SIGNATURE with a SIGNATURE item
// containing the public key and signature.
// Sign assumes that the replaced item is the last item in the
// block with the highest address.
func Sign(image, pubKey, signature []byte) ([]byte, error) {
	inf, err := read(image)
	if err != nil {
		return nil, fmt.Errorf("picobin: %v", err)
	}
	var signed []byte
	switch {
	case inf.hashValueOffset != 0:
		signed, err = inf.signHashed(image, pubKey, signature)
	case inf.SignatureOffset != 0:
		signed, err = inf.resign(image, pubKey, signature)
	default:
		err = errors.New("picobin: missing SIGNATURE or HASH_VALUE item")
	}
	if err != nil {
		return nil, fmt.Errorf("picobin: %w", err)
	}
	return signed, nil
}

func (inf *Image) resign(image, pubKey, signature []byte) ([]byte, error) {
	oldKey, oldSig, err := inf.Signature(image)
	if err != nil {
		return nil, err
	}
	if len(oldKey) != len(pubKey) || len(oldSig) != len(signature) {
		return nil, errors.New("incompatible signature")
	}
	signed := append([]byte{}, image...)
	sigOff := inf.SignatureOffset
	copy(signed[sigOff:], pubKey)
	copy(signed[sigOff+uint32(len(pubKey)):], signature)
	return signed, nil
}

func (inf *Image) signHashed(image, pubKey, signature []byte) ([]byte, error) {
	imgHash, err := inf.Hash(image)
	if err != nil {
		return nil, err
	}
	hashItemSize := 4 + len(imgHash)
	lastItem := inf.hashValueOffset + uint32(hashItemSize)
	last := readItemHeader(image[lastItem:])
	if last.itype != blockItemLast {
		return nil, errors.New("HASH_VALUE is not last in block")
	}
	// Read block link.
	link, err := readFooter(image[lastItem+4:])
	if err != nil {
		return nil, err
	}
	// Write image up to just before the HASH_VALUE item.
	signed := append([]byte{}, image[:inf.hashValueOffset]...)
	bo := binary.LittleEndian
	// Write a SIGNATURE_ITEM.
	sigItemSize := 4 + len(pubKey) + len(signature)
	header := uint32(blockItemSignature) | // SIGNATURE item.
		uint32(sigItemSize/4)<<8 | // Size in words.
		sigSecp256k1<<24 // Algorithm.
	signed = bo.AppendUint32(signed, header)
	signed = append(signed, pubKey...)
	signed = append(signed, signature...)
	// Adjust size because of expanded item.
	sizeAdj := uint32(sigItemSize - hashItemSize)
	// Add footer.
	header = 0x80 | uint32(blockItemLast) | // LAST_ITEM with 2-byte size.
		(uint32(last.size)+sizeAdj/4)<<8
	signed = bo.AppendUint32(signed, header)
	signed = bo.AppendUint32(signed, link)
	signed = bo.AppendUint32(signed, footer)
	return signed, nil
}

func read(data []byte) (Image, error) {
	bo := binary.LittleEndian
	idx := uint32(0)
	// Scan first 4k for first block header.
	for range 1024 {
		h := bo.Uint32(data[idx:])
		if h == header {
			break
		}
		idx += 4
	}
	firstBlock := idx
	hidx := idx
	nblocks := 0
	var img Image
	for {
		h := bo.Uint32(data[idx:])
		if h != header {
			return Image{}, errors.New("missing block header")
		}
		img.blockStartOffset = idx
		idx += 4
		totalSize := uint(0)
		// Scan items.
		for {
			h := readItemHeader(data[idx:])
			if h.size == 0 {
				return Image{}, errors.New("zero-sized block item")
			}
			if h.itype == blockItemLast {
				if totalSize != uint(h.size) {
					return Image{}, errors.New("mismatched total item size")
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
					return Image{}, errors.New("invalid SIGNATURE item size")
				}
				img.SignatureOffset = idx + 4
			}
			idx += uint32(h.size) * 4
		}
		// Verify footer and jump to next block in loop.
		link, err := readFooter(data[idx+4:])
		if err != nil {
			return Image{}, err
		}
		nblocks++
		if nblocks == maxLoopLen {
			return Image{}, errors.New("block loop too long")
		}
		hidx += link
		if hidx == firstBlock {
			break
		}
		idx = hidx
	}
	return img, nil
}

func (in *Image) Signature(image []byte) (pubKey []byte, sig []byte, err error) {
	off := in.SignatureOffset
	h := readItemHeader(image[off-4:])
	if h.itype != blockItemSignature {
		return nil, nil, errors.New("picobin: missing SIGNATURE item")
	}
	pubKey = image[off : off+64]
	sig = image[off+64 : off+sigSecp256k1Len]
	return pubKey, sig, nil
}

func (in *Image) Hash(img []byte) ([]byte, error) {
	h := readItemHeader(img[in.hashValueOffset:])
	if h.itype != blockItemHashValue {
		return nil, errors.New("picobin: missing HASH_VALUE item")
	}
	return img[in.hashValueOffset+4 : in.hashValueOffset+uint32(h.size)*4], nil
}

func (in *Image) HashData(img []byte, imageAddr uint32) ([]byte, error) {
	// Read HASH_DEF item.
	h := readItemHeader(img[in.hashDefOffset:])
	if h.itype != blockItemHashDef {
		return nil, errors.New("picobin: missing HASH_DEF item")
	}
	if a := h.data >> 8; a != hashSHA256 {
		return nil, errors.New("unknown HASH_DEF hash algorithm")
	}
	bo := binary.LittleEndian
	// Read number of block bytes to hash.
	blockHashed := 4 * (bo.Uint32(img[in.hashDefOffset+4:]) & 0xffff)
	var hashData []byte
	// Read LOAD_MAP item.
	h = readItemHeader(img[in.loadMapOffset:])
	if h.itype != blockItemLoadMap {
		return nil, errors.New("picobin: missing LOAD_MAP item")
	}
	// Read LOAD_MAP entries.
	nentries := (h.size - 1) / 3
	absolute := h.data&0x8000 != 0
	eidx := in.loadMapOffset + 4
	for i := range uint32(nentries) {
		storageStart := bo.Uint32(img[eidx+i*12+0:])
		size := bo.Uint32(img[eidx+i*12+8:])
		if storageStart == 0 {
			// The size itself is hashed, not the storage.
			hashData = append(hashData, img[eidx+8:eidx+12]...)
			continue
		}
		if absolute {
			size -= storageStart
			storageStart -= imageAddr
		} else {
			storageStart += in.loadMapOffset
		}
		hashData = append(hashData, img[storageStart:storageStart+size]...)
	}
	// Hash block.
	blockHashData := img[in.blockStartOffset : in.blockStartOffset+blockHashed]
	hashData = append(hashData, blockHashData...)
	return hashData, nil
}

func readFooter(img []byte) (uint32, error) {
	bo := binary.LittleEndian
	link, f := bo.Uint32(img), bo.Uint32(img[4:])
	if f != footer {
		return 0, errors.New("missing block footer")
	}
	return link, nil
}

func readItemHeader(img []byte) itemHeader {
	bo := binary.LittleEndian
	w := bo.Uint32(img)
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
