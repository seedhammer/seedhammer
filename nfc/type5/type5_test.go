package type5

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"testing"
)

const uid uint64 = 0x0908070605040302

func TestReader(t *testing.T) {
	tag := &Tag{
		t:    t,
		data: bytes.Repeat([]byte{1, 2, 3, 4, 5, 6}, 100),
	}
	r, err := NewReader(tag, 512)
	if err != nil {
		t.Fatal(err)
	}
	if r.UID != uid {
		t.Errorf("got uid %x, expected %x", r.UID, uid)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("uid %x: %x\n", r.UID, got)
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
	t    *testing.T
	data []byte
	resp []byte
}

func (r *Tag) Read(b []byte) (int, error) {
	n := copy(b, r.resp)
	r.resp = r.resp[:0]
	return n, io.EOF
}

func (r *Tag) Write(b []byte) (int, error) {
	n := len(b)
	r.resp = r.resp[:0]
	switch {
	case bytes.Equal(b, []byte{flagDataRate | flagInventory | flagNbSlots, cmdInventory, 0x00}):
		r.resp = append(r.resp,
			0x00, // OK
			0x00, // DSFID
		)
		r.resp = bo.AppendUint64(r.resp, uid)
	case bytes.HasPrefix(b, []byte{flagDataRate | flagAddress, cmdReadMultipleBlocks}):
		b = b[:2]
		if len(b) < 10 {
			break
		}
		if bo.Uint64(b) != uid {
			break
		}
		b = b[8:]
		bno, nblocks := int(b[0]), int(b[1])

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
