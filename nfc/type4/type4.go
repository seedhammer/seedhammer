// package type4 implements the NFC Forum Type 4 Tag specification.
package type4

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const chunkSize = 128

// Tag emulates a writable empty tag.
type Tag struct {
	d            Device
	state        protoState
	blockNo      byte
	nextWriteOff int

	buf       [maxFrameSize]byte
	readBytes int
}

type Device interface {
	// Sleep is called when the tag is instructed by
	// the writer device to sleep.
	Sleep() error
	io.ReadWriter
}

func NewTag(d Device) *Tag {
	return &Tag{
		d: d,
	}
}

func (t *Tag) Reset() {
	t.state = initState
	t.readBytes = 0
	t.nextWriteOff = 0
}

type protoState int

const (
	initState protoState = iota
	activeState
	ndefState
	ccFileState
	fileState
)

// ATS response (11.6.2).
const (
	atsTL = 5        // Length.
	atsT0 = 0b1<<4 | // Include TA(1).
		0b1<<5 | // Include TB(1).
		0b1<<6 | // Include TC(1).
		fsci
	atsTA1 = 0x00   // Bit rate 106kb/s only.
	atsTB1 = 8<<4 | // FWI = FWImax (~77ms)
		0 // SFGT = 0 (no guard time)
	atsTC1 = 0 // No support for NAD nor DID.

	fsci = 8
	// FSCI 8 corresponds to frame size 256 (table 66).
	maxFrameSize = 256
	ndefFileID   = 0x0001
	maxNDEFSize  = 8192
	blockSize    = 4
	readSize     = 16

	cmdSENS_REQ = 0xe0

	isodepDESELECT        = 0xc2
	isodepI_BLOCK         = 0x02
	isodepR_ACK           = 0xa2
	isodepR_NAK           = 0xb2
	isodepR_BLOCK         = 0b10100010
	isodepMAPPING_VERSION = 0x20
	isodepCLA             = 0x00 // The only CLA we support.
	isodepREAD            = 0xb0
	isodepWRITE           = 0xd6
)

var (
	cmdSLP_REQ = []byte{0x50, 0x00}
	ats        = []byte{
		atsTL,
		atsT0,
		atsTA1,
		atsTB1,
		atsTC1,
	}
	// NFC Type 4 Tag Operation Specification commands.
	isodepACK         = []byte{0x90, 0x00}
	isodepNAK         = []byte{0x67, 0x00}
	isodepTAG_SELECT  = []byte{0xa4, 0x04, 0x00, 0x07, 0xd2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01, 0x00}
	isodepCC_SELECT   = []byte{0xa4, 0x00, 0x0c, 0x02, 0xe1, 0x03}
	isodepFILE_SELECT = []byte{0xa4, 0x00, 0x0c, 0x02, 0x00, 0x01}
	capContainer      = make([]byte, 0, 15)
	emptyFile         = []byte{
		0x00, 0x00, // Length 0.
	}
)

var bo = binary.BigEndian

func init() {
	// Table 5.
	capContainer = bo.AppendUint16(capContainer, uint16(cap(capContainer))) // Container size
	capContainer = append(capContainer, isodepMAPPING_VERSION)
	capContainer = bo.AppendUint16(capContainer, chunkSize) // ReadBinary chunk size
	capContainer = bo.AppendUint16(capContainer, chunkSize) // UpdateBinary chunk size

	// Control block TLV. Section 5.1.2.1.
	capContainer = append(capContainer, 0x04)
	capContainer = append(capContainer, 0x06)
	capContainer = bo.AppendUint16(capContainer, ndefFileID)  // File identifier.
	capContainer = bo.AppendUint16(capContainer, maxNDEFSize) // Maximum NDEF size.
	capContainer = append(capContainer, 0x00)                 // Read allowed.
	capContainer = append(capContainer, 0x00)                 // Write allowed.
}

// Read file contents written by a NFC writer.
func (t *Tag) Read(b []byte) (int, error) {
	var readErr error
	for {
		if readErr != nil {
			return 0, readErr
		}
		if t.readBytes > 0 {
			d := t.buf[:t.readBytes]
			n := copy(b, d)
			t.readBytes -= n
			copy(d, d[n:])
			return n, nil
		}
		n, err := t.d.Read(t.buf[:])
		buf := t.buf[:n]
		if err != nil {
			if err != io.EOF {
				t.Reset()
				return 0, fmt.Errorf("type4: %w", err)
			}
			if len(buf) == 0 {
				return 0, io.EOF
			}
		}
		var readData []byte
		// Re-use the receive buffer for the response. This is
		// ok because the request is read before being overwritten.
		resp := t.buf[:0]
		switch {
		case t.state <= activeState && len(buf) == 2 && buf[0] == cmdSENS_REQ:
			// Initialize I-block number to 1 (13.2.4.2).
			t.blockNo = 0b1
			t.state = activeState
			resp = append(resp, ats...)
		case t.state <= activeState && bytes.Equal(buf, cmdSLP_REQ):
			// Go to sleep, waiting for WUPA.
			if err := t.d.Sleep(); err != nil {
				return 0, fmt.Errorf("type4: %w", err)
			}
			t.Reset()
			readErr = io.EOF
		case t.state >= activeState && len(buf) == 1 && buf[0] == isodepDESELECT:
			// Go to sleep, waiting for WUPA.
			if err := t.d.Sleep(); err != nil {
				return 0, fmt.Errorf("type4: %w", err)
			}
			t.Reset()
			readErr = io.EOF
			resp = append(resp, isodepDESELECT)
		case t.state >= activeState && len(buf) == 1 && (buf[0]&^0b1) == isodepR_NAK:
			rbno := buf[0] & 0b1
			if rbno != t.blockNo {
				// Respond with R(ACK) (13.2.5.10).
				resp = append(resp, isodepR_ACK|t.blockNo)
			}
		case t.state >= activeState && (buf[0]&^0b1) == isodepI_BLOCK:
			buf = buf[1:]
			t.blockNo = 1 - t.blockNo
			resp = append(resp, isodepI_BLOCK|t.blockNo)
			if len(buf) < 4 || buf[0] != isodepCLA {
				break
			}
			buf = buf[1:]
			switch {
			case bytes.Equal(buf, isodepTAG_SELECT):
				t.state = ndefState
				resp = append(resp, isodepACK...)
			case t.state >= ndefState && bytes.Equal(buf, isodepCC_SELECT):
				t.state = ccFileState
				resp = append(resp, isodepACK...)
			case t.state >= ndefState && bytes.Equal(buf, isodepFILE_SELECT):
				t.state = fileState
				t.nextWriteOff = 0
				resp = append(resp, isodepACK...)
			case t.state >= ccFileState && len(buf) == 4 && buf[0] == isodepREAD:
				buf = buf[1:]
				var ok bool
				resp, ok = t.read(resp, buf)
				if !ok {
					break
				}
				resp = append(resp, isodepACK...)
			case t.state >= ccFileState && buf[0] == isodepWRITE:
				buf = buf[1:]
				readData, readErr = t.write(buf)
				if readErr != nil && readErr != io.EOF {
					break
				}
				t.readBytes = len(readData)
				resp = append(resp, isodepACK...)
			}
			if len(resp) == 1 {
				resp = append(resp, isodepNAK...)
			}
		}
		if len(resp) > 0 {
			if _, err := t.d.Write(resp); err != nil {
				return 0, fmt.Errorf("type4: %w", err)
			}
		}
		// Re-use the receive buffer to hold written data (if any).
		copy(t.buf[:], readData)
	}
}

func (t *Tag) read(out, in []byte) ([]byte, bool) {
	off := int(bo.Uint16(in))
	in = in[2:]
	size := int(in[0])
	in = in[1:]
	var file []byte
	switch t.state {
	case ccFileState:
		file = capContainer
	case fileState:
		file = emptyFile
	}
	if off+size > len(file) {
		return out, false
	}
	out = append(out, file[off:off+size]...)
	return out, true
}

func (t *Tag) write(in []byte) ([]byte, error) {
	if len(in) < 3 {
		return nil, errors.New("type4: write request too short")
	}
	off := int(bo.Uint16(in))
	in = in[2:]
	size := int(in[0])
	in = in[1:]
	if len(in) != size {
		return nil, errors.New("type4: invalid size in write request")
	}
	eof := false
	// Chop off writes to the first 2 size bytes.
	off -= 2
	if off < 0 {
		in = in[min(len(in), -off):]
		off = 0
		if t.nextWriteOff != 0 {
			eof = true
		}
		t.nextWriteOff = 0
	}
	if len(in) > 0 && off != t.nextWriteOff {
		// Reject random writes.
		return nil, errors.New("type4: non-contiguous write")
	}
	t.nextWriteOff = off + len(in)
	if eof {
		return in, io.EOF
	}
	return in, nil
}
