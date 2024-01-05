package type5

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestReader(t *testing.T) {
	tests := [][]byte{
		bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 100),
		bytes.Repeat([]byte{1, 2, 3, 4}, 10000),
	}
	for _, data := range tests {
		var capContainer []byte
		mlen := len(data) / 8
		if mlen <= 255 {
			capContainer = []byte{
				ccMagic1,
				0x40,                // Mapping version 1.0, read/write allowed.
				byte(len(data) / 8), // Data length.
				0x00,                // No extra features.
			}
		} else {
			capContainer = []byte{
				ccMagic2,
				0x40,       // Mapping version 1.0, read/write allowed.
				0x00,       // 8-byte capability container.
				0x01,       // Read multiple supported.
				0x00, 0x00, // RFU.
			}
			capContainer = binary.BigEndian.AppendUint16(capContainer, uint16(mlen))
		}
		tag := &Tag{
			uid: 0x0908070605040302,
			mem: append(capContainer, data...),
		}
		r, err := NewReader(tag, 64)
		if err != nil {
			t.Fatal(err)
		}
		// Buffer reading to ensure a minimum read size.
		bufr := bufio.NewReaderSize(r, 128)
		got, err := io.ReadAll(bufr)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("\nread\n%x\nexpected\n%x", got, data)
		}
	}
}

func TestTransceiverEncode(t *testing.T) {
	tests := []struct {
		data, enc []byte
	}{
		{
			data: h("010203040506"),
			enc:  h("21080202022002020280020202020802020808020220080202208002020220802004"),
		},
	}
	for _, test := range tests {
		buf := new(bytes.Buffer)
		trans := NewTransceiver(buf, len(test.data))
		if _, err := trans.Write(test.data); err != nil {
			t.Fatalf("%x: %v", test.data, err)
		}
		if got := buf.Bytes(); !bytes.Equal(got, test.enc) {
			t.Errorf("%x encoded to %x, want %x", test.data, got, test.enc)
		}
	}
}

func TestTransceiverDecode(t *testing.T) {
	tests := []struct {
		enc, data []byte
	}{
		{
			enc:  h("b7aaaaaacacc52ab52b3d4b2b2aacaaaaaacaa2ad52ccb2aad03"),
			data: h("00009583cb89000104e0"),
		},
	}
	for _, test := range tests {
		buf := new(bytes.Buffer)
		buf.Write(test.enc)
		trans := NewTransceiver(buf, len(test.enc))
		got, err := io.ReadAll(trans)
		if err != nil {
			t.Fatalf("%x: %v", test.enc, err)
		}
		if !bytes.Equal(got, test.data) {
			t.Errorf("%x decoded to %x, want %x", test.enc, got, test.data)
		}
	}
}

// Tag implements a NFC forum type 2 tag.
type Tag struct {
	uid   uint64
	state tagState
	mem   []byte
	resp  []byte
}

const blockSize = 4

type tagState int

const (
	tagActive tagState = iota
	tagSelected
)

func (t *Tag) Read(b []byte) (int, error) {
	n := copy(b, t.resp)
	if n < len(t.resp) {
		return n, io.ErrShortBuffer
	}
	t.resp = t.resp[:0]
	return n, io.EOF
}

func (t *Tag) Write(req []byte) (int, error) {
	n := len(req)
	if len(t.resp) > 0 {
		return 0, errors.New("write: double write")
	}
	if len(req) < 2 {
		return 0, errors.New("read: double read or short write")
	}
	flags := req[0]
	if flags&flagDataRate == 0 {
		return 0, errors.New("read: data rate flag not set")
	}
	flags &^= flagDataRate
	cmd := req[1]
	req = req[2:]
	switch {
	case flags == flagInventory|flagNbSlots && cmd == cmdInventory:
		t.resp = append(t.resp,
			0x00, // OK
			0x00, // DSFID
		)
		t.resp = bo.AppendUint64(t.resp, t.uid)
	case flags == flagAddress && cmd == cmdSelect && len(req) == 8:
		if bo.Uint64(req) != t.uid {
			break
		}
		t.resp = append(t.resp, 0x00) // OK
		t.state = tagSelected
	case t.state == tagSelected && flags == flagSelect && cmd == cmdReadMultipleBlocks && len(req) == 2:
		bno := int(req[0])
		nblocks := int(req[1]) + 1
		start := bno * blockSize
		end := start + nblocks*blockSize
		if end > len(t.mem) {
			return 0, errors.New("write: memory read out of bounds")
		}
		t.resp = append(t.resp, 0x00) // OK
		t.resp = append(t.resp, t.mem[start:end]...)
	case t.state == tagSelected && flags == flagSelect && cmd == cmdExtReadMultipleBlocks && len(req) == 4:
		bno := int(bo.Uint16(req))
		nblocks := int(bo.Uint16(req[2:])) + 1
		start := bno * blockSize
		end := start + nblocks*blockSize
		if end > len(t.mem) {
			return 0, errors.New("write: memory read out of bounds")
		}
		t.resp = append(t.resp, 0x00) // OK
		t.resp = append(t.resp, t.mem[start:end]...)
	default:
		return 0, errors.New("write: unknown command")
	}
	return n, nil
}

func h(h string) []byte {
	v, err := hex.DecodeString(h)
	if err != nil {
		panic(err)
	}
	return v
}
