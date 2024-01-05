package type4

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestWriteFiles(t *testing.T) {
	r := &Reader{t: t}
	r.MsgSleep()
	r.MsgSENS_REQ()
	r.MsgDeselect()
	r.MsgSENS_REQ()
	r.MsgNAK()
	r.MsgSelectNDEF()
	r.MsgSelectCCFile()
	r.MsgReadCCFile()
	r.SelectFile()
	files := [][]byte{
		[]byte{1, 2, 3, 4},
		bytes.Repeat([]byte{5, 4, 0, 0}, 100),
	}
	for _, f := range files {
		r.WriteFile(f)
	}
	tag := NewTag(r)
	buf := make([]byte, 8192)
	// That that sleep and deselect commands result in sleeps
	// and EOF.
	for range 2 {
		n, err := tag.Read(buf)
		if n > 0 || err != io.EOF {
			t.Errorf("Sleep or deleselect didn't EOF")
		}
	}
	for _, f := range files {
		for {
			n, err := tag.Read(buf)
			if got, want := buf[:n], f[:n]; !bytes.Equal(got, want) {
				r.DumpTranscript()
				t.Fatalf("read %x, expected %x", got, want)
			}
			f = f[n:]
			if err != nil {
				if err == io.EOF && len(f) == 0 {
					break
				}
				r.DumpTranscript()
				t.Fatal(err)
			}
		}
	}
}

func TestDoubleSENSREQ(t *testing.T) {
	r := &Reader{t: t}
	r.MsgSENS_REQ()
	r.MsgSENS_REQ()
	tag := NewTag(r)
	if _, err := tag.Read(make([]byte, 100)); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

// Reader implements a NFC forum type 4 reader.
type Reader struct {
	t       *testing.T
	msgs    []message
	msgIdx  int
	blockNo byte
}

type message struct {
	state state
	msg   []byte
	resp  []byte
	sleep bool
}

type state int

const (
	stateReading state = iota
	stateSleeping
	stateResponding
	stateComplete
)

func (m message) String() string {
	var r strings.Builder
	if m.state > stateReading && len(m.msg) > 0 {
		r.WriteString(fmt.Sprintf("Reader: %x\n", m.msg))
	}
	if m.state > stateSleeping && m.sleep {
		r.WriteString("(sleep)\n")
	}
	if m.state > stateResponding && len(m.resp) > 0 {
		r.WriteString(fmt.Sprintf("Tag   : %x\n", m.resp))
	}
	return r.String()
}

func (r *Reader) MsgSENS_REQ() {
	r.msgs = append(r.msgs, message{
		msg:  []byte{cmdSENS_REQ, 0x80},
		resp: ats,
	})
}

func (r *Reader) MsgSleep() {
	r.msgs = append(r.msgs, message{
		msg:   cmdSLP_REQ,
		sleep: true,
	})
}

func (r *Reader) MsgNAK() {
	r.msgs = append(r.msgs, message{
		msg:  []byte{isodepR_NAK | r.blockNo},
		resp: []byte{isodepR_ACK | (1 - r.blockNo)},
	})
}

func (r *Reader) MsgDeselect() {
	r.msgs = append(r.msgs, message{
		msg:   []byte{isodepDESELECT},
		sleep: true,
		resp:  []byte{isodepDESELECT},
	})
}

func (r *Reader) MsgSelectNDEF() {
	r.msgISODEP(
		isodepTAG_SELECT,
		isodepACK,
	)
}

func (r *Reader) MsgSelectCCFile() {
	r.msgISODEP(
		isodepCC_SELECT,
		isodepACK,
	)
}

func (r *Reader) MsgReadCCFile() {
	const (
		offset = 0
		size   = 15
	)
	var req []byte
	req = append(req, isodepREAD)
	req = bo.AppendUint16(req, offset)
	req = append(req, size)
	r.msgISODEP(
		req,
		append(capContainer, isodepACK...),
	)
}

func (r *Reader) SelectFile() {
	r.msgISODEP(
		isodepFILE_SELECT,
		isodepACK,
	)
}

func (r *Reader) WriteFile(f []byte) {
	off := uint16(0)
	// Write length.
	r.writeFileChunk(off, bo.AppendUint16(nil, uint16(len(f))))
	off += 2
	for len(f) > 0 {
		n := min(chunkSize, len(f))
		r.writeFileChunk(off, f[:n])
		f = f[n:]
		off += uint16(n)
	}
}

func (r *Reader) writeFileChunk(off uint16, f []byte) {
	var req []byte
	req = append(req, isodepWRITE)
	req = bo.AppendUint16(req, off)
	req = append(req, byte(len(f)))
	req = append(req, f...)
	r.msgISODEP(
		req,
		isodepACK,
	)
}

func (r *Reader) msgISODEP(msg, resp []byte) {
	r.t.Helper()
	r.msgs = append(r.msgs, message{
		msg:  append([]byte{isodepI_BLOCK | r.blockNo, isodepCLA}, msg...),
		resp: append([]byte{isodepI_BLOCK | r.blockNo}, resp...),
	})
	r.blockNo = 1 - r.blockNo
}

func (r *Reader) Sleep() error {
	if r.msgIdx == len(r.msgs) {
		r.Fatalf("unexpected Sleep")
	}
	m := &r.msgs[r.msgIdx]
	if m.state != stateSleeping {
		r.Fatalf("unexpected Sleep")
	}
	switch {
	case len(m.resp) == 0:
		m.state = stateComplete
		r.msgIdx++
	default:
		m.state = stateResponding
	}
	return nil
}

func (r *Reader) Read(b []byte) (int, error) {
	if r.msgIdx == len(r.msgs) {
		return 0, io.EOF
	}
	m := &r.msgs[r.msgIdx]
	if m.state != stateReading {
		r.Fatalf("unexpected Read")
	}
	n := copy(b, m.msg)
	if n < len(m.msg) {
		return n, io.ErrShortBuffer
	}
	switch {
	case len(m.resp) == 0 && !m.sleep:
		m.state = stateComplete
		r.msgIdx++
	case !m.sleep:
		m.state = stateResponding
	default:
		m.state = stateSleeping
	}
	return n, nil
}

func (r *Reader) Write(b []byte) (int, error) {
	if r.msgIdx == len(r.msgs) {
		r.Fatalf("unexpected Write")
	}
	m := &r.msgs[r.msgIdx]
	if m.state != stateResponding {
		r.Fatalf("unexpected Write")
	}
	if !bytes.Equal(b, m.resp) {
		r.Fatalf("unexpected Write %x, want %x", b, m.resp)
	}
	m.state = stateComplete
	r.msgIdx++
	return len(b), nil
}

func (r *Reader) Fatalf(f string, args ...any) {
	r.DumpTranscript()
	r.t.Fatalf(f, args...)
}

func (r *Reader) DumpTranscript() {
	if len(r.msgs) == 0 {
		return
	}
	var transcript strings.Builder
	transcript.WriteString("Transcript:\n")
	for _, m := range r.msgs[:r.msgIdx+1] {
		transcript.WriteString(m.String())
	}
	r.t.Log(transcript.String())
}
