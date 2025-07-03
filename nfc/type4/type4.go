// package type4 implements the NFC Forum Type 4 Tag specification.
package type4

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// Tag emulates a writable empty tag.
type Tag struct {
	d     Device
	state protoState

	buf  [128]byte
	resp [128]byte
}

type Device interface {
	// Sleep is called when the tag is instructed by
	// the writer device to sleep.
	Sleep() error
	io.ReadWriter
}

func (t *Tag) Reset(d Device) {
	t.d = d
	t.state = initState
}

type protoState int

const (
	initState protoState = iota
	isoDepState
	ccFileState
	fileState
)

func dbg(strs ...any) {
	fmt.Println(strs...)
}

func dbgf(f string, args ...any) {
	fmt.Printf(f+"\n", args...)
}

// page0 := []byte{0x04, 0xf7, 0x73, 0x08, 0x7c, 0x8f, 0x61, 0x81, 0x13, 0x48, 0x00, 0x00, 0xe1, 0x10, 0x3e, 0x00}
// // page4 := []byte{0x03, 0x14, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x62, 0x69, 0x74, 0x63, 0x6f, 0x69, 0x6e, 0x2e, 0x6f}
// // page8 := []byte{0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f, 0xfe, 0x00, 0x63, 0x65, 0x2f, 0x70, 0x6f, 0x64, 0x63, 0x61}
// page4 := []byte{0x03, 0x14, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x48, 0x69, 0x20, 0x4e, 0x69, 0x63, 0x6b, 0x21, 0x20}
// page8 := []byte{0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f, 0xfe, 0x00, 0x63, 0x65, 0x2f, 0x70, 0x6f, 0x64, 0x63, 0x61}

// ATS response (11.6.2).
const (
	ATS_TL = 5        // Length.
	ATS_T0 = 0b1<<4 | // Include TA(1).
		0b1<<5 | // Include TB(1).
		0b1<<6 | // Include TC(1).
		0x8 // FSCI (64 byte frame size)
	ATS_TA1 = 0x00   // Bit rate 106kb/s only.
	ATS_TB1 = 8<<4 | // FWI = FWImax (~77ms)
		0 // SFGT = 0 (no guard time)
	ATS_TC1 = 0 // No support for NAD nor DID.
)
const (
	SLP_REQ = 0x50

	T2T_READ      = 0x30
	T2T_WRITE     = 0xa2
	T2T_WRITE_ACK = 0x0a

	blockSize = 4
	readSize  = 16

	T4T_RATS = 0xe0

	ISODEP_DESELECT = 0xc2
	I_BLOCK         = 0x02
	R_ACK           = 0xa2
	R_NAK           = 0xb2
	R_BLOCK         = 0b10100010
)

var (
	ATS = []byte{
		ATS_TL,
		ATS_T0,
		ATS_TA1,
		ATS_TB1,
		ATS_TC1,
	}
	// NFC Type 4 Tag Operation Specification, Table 10.
	T4T_NDEF_SELECT_CAPDU    = []byte{0x00, 0xa4, 0x04, 0x00, 0x07, 0xd2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01, 0x00}
	T4T_NDEF_SELECT_CAPDU2   = []byte{0x00, 0xa4, 0x04, 0x00, 0x07, 0xd2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x00, 0x00}
	T4T_NDEF_ACK             = []byte{0x90, 0x00}
	T4T_NDEF_NAK             = []byte{0x67, 0x00}
	T4T_NDEF_CC_SELECT_CAPDU = []byte{0x00, 0xa4, 0x00, 0x0c, 0x02, 0xe1, 0x03}
	// T4T_NDEF_CC_SELECT_CAPDU2 := []byte{0x00, 0xa4, 0x00, 0x0c, 0x02, 0x01, 0x00}
	T4T_NDEF_CC_READ_CAPDU     = []byte{0x00, 0xb0, 0x00, 0x00, 0x0f}
	T4T_NDEF_FILE_SELECT_CAPDU = []byte{0x00, 0xa4, 0x00, 0x0c, 0x02, 0x00, 0x01}
	T4T_NDEF_FILE_READ_CAPDU   = []byte{0x00, 0xb0, 0x00, 0x00, 0x02}
	T4T_NDEF_FILE_READ2_CAPDU  = []byte{0x00, 0xb0, 0x00, 0x02, 0x14}
	writes                     = make([]byte, 0, 1000)
	dirs                       = make([]bool, 0, 100)
	sep                        = []byte{0xde, 0xad, 0xbe, 0xef}
	cc                         = make([]byte, 0, 15)
)

func init() {
	// Table 5.
	bo := binary.BigEndian
	const ccSize = 15
	cc = bo.AppendUint16(cc, ccSize) // Container size
	cc = append(cc, T4T_MAPPING_VERSION)
	cc = bo.AppendUint16(cc, 0x3b) // ReadBinary chunk size
	cc = bo.AppendUint16(cc, 0x34) // UpdateBinary chunk size

	// Control block TLV. Section 5.1.2.1.
	cc = append(cc, 0x04)
	cc = append(cc, 0x06)
	cc = bo.AppendUint16(cc, 0x0001) // File identifier.
	cc = bo.AppendUint16(cc, 0x0032) // Maximum NDEF size.
	cc = append(cc, 0x00)            // Read allowed.
	cc = append(cc, 0x00)            // Write allowed.
	if len(cc) != ccSize {
		panic("wrong cc size")
	}
}

const T4T_MAPPING_VERSION = 0x20

// Read file contents written by a NFC writer.
func (t *Tag) Read(b []byte) (int, error) {
	nwrites, nreads, ncmds := 0, 0, 0
	writes := writes[:0]
	dirs := dirs[:0]
	var blockNo byte
	defer func() {
		dbgf("...done. stats nwrites %d nreads %d ncmds %d", nwrites, nreads, ncmds)
		if len(writes) > 0 {
			for i, msg := range bytes.Split(writes, sep) {
				unit := "Tag      "
				if dirs[i] {
					unit = "NFC Tools"
				}
				dbgf("%s: %x", unit, msg)
			}
		}
	}()
	for {
		n, err := t.d.Read(t.buf[:])
		buf := t.buf[:n]
		if err != nil {
			if err != io.EOF {
				return 0, fmt.Errorf("type4: %w", err)
			}
			if len(buf) == 0 {
				return 0, io.EOF
			}
		}
		ncmds++
		if len(writes) > 0 {
			writes = append(writes, sep...)
		}
		writes = append(writes, buf...)
		dirs = append(dirs, true)
		cmd := buf[0]
		buf = buf[1:]
		resp := t.resp[:0]
		// switch d.state {
		// case initState:
		switch cmd {
		case T4T_RATS:
			// Initialize I-block number to 1 (13.2.4.2).
			blockNo = 0b1
			resp = append(resp, ATS...)
		case ISODEP_DESELECT:
			if len(buf) != 0 {
				return 0, fmt.Errorf("type4: unsupported S-block")
			}
			// Go to sleep, waiting for WUPA.
			if err := t.Sleep(); err != nil {
				return 0, fmt.Errorf("type4: %w", err)
			}
			resp = append(resp, ISODEP_DESELECT)
			dirs = append(dirs, false)
			writes = append(writes, sep...)
			writes = append(writes, resp...)
			if _, err := t.d.Write(resp); err != nil {
				return 0, fmt.Errorf("type4: %w", err)
			}
			continue
		case R_NAK, R_NAK + 1:
			rbno := cmd & 0b1
			if rbno != blockNo {
				// Send R(NAK) back (13.2.5.10).
				resp = append(resp, R_ACK|blockNo)
			}
		case I_BLOCK, I_BLOCK + 1:
			if len(buf) < 4 {
				// return fmt.Errorf("type4: listen: S-block too short")
				continue
			}
			blockNo = 1 - blockNo
			resp = append(resp, I_BLOCK|blockNo)
			switch {
			case bytes.Equal(buf, T4T_NDEF_SELECT_CAPDU):
				resp = append(resp, T4T_NDEF_ACK...)
			case bytes.Equal(buf, T4T_NDEF_SELECT_CAPDU2) ||
				bytes.Equal(buf, []byte{0x00, 0xa4, 0x04, 0x00, 0x07, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}):
				resp = append(resp, 0x6A, 0x82) // Not found.
			case bytes.Equal(buf, T4T_NDEF_CC_SELECT_CAPDU):
				//  ||
				// bytes.Equal(buf, T4T_NDEF_CC_SELECT_CAPDU2):
				resp = append(resp, T4T_NDEF_ACK...)
			case bytes.Equal(buf, T4T_NDEF_CC_READ_CAPDU):
				resp = append(resp, cc...)
				resp = append(resp, T4T_NDEF_ACK...)
			case bytes.Equal(buf, T4T_NDEF_FILE_SELECT_CAPDU):
				resp = append(resp, T4T_NDEF_ACK...)
			case bytes.Equal(buf, T4T_NDEF_FILE_READ_CAPDU):
				// resp = append(resp, 0x00, 0x03) // File length.
				resp = append(resp, 0x00, 0x14) // File length.
				resp = append(resp, T4T_NDEF_ACK...)
			case bytes.Equal(buf, T4T_NDEF_FILE_READ2_CAPDU):
				// resp = append(resp, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x48, 0x69, 0x20, 0x4e, 0x69, 0x63, 0x6b, 0x21, 0x20,
				// 	0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f)
				resp = append(resp, 0xd1, 0x01, 0x10, 0x55, 0x04, 0x62, 0x69, 0x74, 0x63, 0x6f, 0x69, 0x6e, 0x2e, 0x6f, 0x72, 0x67, 0x2f, 0x64, 0x65, 0x2f)
				// resp = append(resp, 0xd1, 0x00, 0x00)
				resp = append(resp, T4T_NDEF_ACK...)
			case bytes.Equal(buf, []byte{0x00, 0xd6, 0x00, 0x00, 0x02, 0x00, 0x00}): // T4T_Update_Binary
				// Write length = 0
				resp = append(resp, T4T_NDEF_ACK...)
			case bytes.Equal(buf, []byte{0x00, 0xd6, 0x00, 0x02, 0x0d, 0xd1, 0x01, 0x09, 0x54, 0x02, 0x65, 0x6e, 0x62, 0x6f, 0x69, 0x6e, 0x67, 0x21}) ||
				bytes.Equal(buf, []byte{0x00, 0xd6, 0x00, 0x02, 0x13, 0xd1, 0x01, 0x0f, 0x54, 0x02, 0x65, 0x6e, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x20, 0x77, 0x6f, 0x72, 0x6c, 0x64, 0x21}) ||
				bytes.Equal(buf, []byte{0x00, 0xd6, 0x00, 0x00, 0x0f, 0x00, 0x00, 0xd1, 0x01, 0x09, 0x54, 0x02, 0x65, 0x6e, 0x62, 0x6f, 0x69, 0x6e, 0x67, 0x21}): // T4T_Update_Binary
				// Write data
				resp = append(resp, T4T_NDEF_ACK...)
			case bytes.Equal(buf, []byte{0x00, 0xd6, 0x00, 0x00, 0x02, 0x00, 0x0d}) ||
				bytes.Equal(buf, []byte{0x00, 0xd6, 0x00, 0x00, 0x02, 0x00, 0x13}):
				// Write length = 0x0d
				resp = append(resp, T4T_NDEF_ACK...)
			default:
				resp = append(resp, T4T_NDEF_NAK...)
				dbgf("C-APDU: %x", buf)
				// return fmt.Errorf("type4: listen: unknown C-APDU")
			}
		case SLP_REQ:
			if len(buf) < 1 || buf[0] != 0 {
				return 0, fmt.Errorf("type4: invalid SLP_REQ")
			}
			// Go to sleep, waiting for WUPA.
			if err := t.Sleep(); err != nil {
				return 0, fmt.Errorf("type4: %w", err)
			}
			continue
		default:
			continue
			// return fmt.Errorf("type4: listen: unknown type 4a command: %x/%x", cmd, buf)
		}
		dirs = append(dirs, false)
		writes = append(writes, sep...)
		writes = append(writes, resp...)
		if _, err := t.d.Write(resp); err != nil {
			return 0, fmt.Errorf("type4: %w", err)
		}
	}
}

func (t *Tag) Sleep() error {
	t.state = initState
	return t.d.Sleep()
}
