package mjolnir

import (
	"errors"
)

type Simulator struct {
	state     deviceState
	ncmds     int
	nbuffered int

	Cmds  []Cmd
	close chan struct{}
	in    chan ioRequest
	out   chan ioResult
}

type Cmd struct {
	Type CmdType
	X, Y uint32
}

type CmdType int

const (
	MoveTo CmdType = iota
	LineTo
)

func NewSimulator() *Simulator {
	sim := &Simulator{
		close: make(chan struct{}),
		in:    make(chan ioRequest),
		out:   make(chan ioResult),
	}
	go sim.run()
	return sim
}

type deviceState int

const (
	stateReady deviceState = iota
	stateInitializing
	stateSetSpeed
	stateSetDelays
	stateMoveToOrigin
	stateExecuting
)

type ioRequest struct {
	write bool
	data  []byte
}

type ioResult struct {
	bytes int
	err   error
}

func (s *Simulator) run() {
	for {
		select {
		case <-s.close:
			s.close <- struct{}{}
			return
		case r := <-s.in:
			var n int
			var err error
			if r.write {
				n, err = s.doWrite(r.data)
			} else {
				n, err = s.doRead(r.data)
			}
			s.out <- ioResult{n, err}
		}
	}
}

func coordsFromCmd(cmd []byte) (uint32, uint32) {
	x := uint32(cmd[0]) | uint32(cmd[1])<<8 | uint32(cmd[2])<<16
	y := uint32(cmd[3]) | uint32(cmd[4])<<8 | uint32(cmd[5])<<16
	return x, y
}

func (s *Simulator) doRead(data []byte) (int, error) {
	read := func(resp []byte) (int, error) {
		if len(resp) > len(data) {
			return 0, errors.New("read overflow")
		}
		copy(data, resp)
		return len(resp), nil
	}
	switch s.state {
	case stateInitializing:
		s.state = stateReady
		return read([]byte{initializedStatus})
	case stateSetSpeed:
		s.state = stateReady
		return read([]byte{setSpeedCmd})
	case stateSetDelays:
		s.state = stateReady
		return read([]byte{setDelaysCmd})
	case stateMoveToOrigin:
		s.state = stateReady
		return read([]byte{moveToOriginCmd, moveToOriginCmdResponse})
	case stateExecuting:
		switch {
		case s.nbuffered == 0 && s.ncmds > 0:
			return read([]byte{bufferProgramStatus})
		case s.nbuffered == 0 && s.ncmds == 0:
			return read([]byte{programCompleteStatus})
		default:
			s.nbuffered--
			return read([]byte{programStepStatus})
		}
	default:
		return 0, errors.New("invalid device state")
	}
}

func (s *Simulator) doWrite(data []byte) (n int, err error) {
	skip := func(bytes int) {
		if len(data) < bytes {
			err = errors.New("buffer underflow")
			return
		}
		n += bytes
		data = data[bytes:]
	}
	read := func(bytes int) []byte {
		res := make([]byte, bytes)
		copy(res, data)
		skip(bytes)
		return res
	}
	batchCmd := func() {
		s.nbuffered++
		s.ncmds--
		skip(9)
	}
	for len(data) > 0 {
		n += 1
		cmd := data[0]
		data = data[1:]
		switch cmd {
		case cancelCmd:
			s.state = stateReady
		case initCmd:
			if s.state == stateExecuting {
				// 0x00 is line to in programming mode.
				x, y := coordsFromCmd(data)
				s.Cmds = append(s.Cmds, Cmd{LineTo, x, y})
				batchCmd()
			} else {
				s.state = stateInitializing
			}
		case setSpeedCmd:
			s.state = stateSetSpeed
			skip(6)
		case setDelaysCmd:
			s.state = stateSetDelays
			skip(2)
		case moveToOriginCmd:
			s.state = stateMoveToOrigin
			subCmd := read(1)
			if err == nil && subCmd[0] != moveToOriginCmdExtra {
				err = errors.New("invalid origin command")
			}
			s.Cmds = append(s.Cmds, Cmd{MoveTo, 0, 0})
		case initProgramCmd:
			s.state = stateExecuting
			ncmds := read(2)
			s.ncmds = (int(ncmds[0]) | int(ncmds[1])<<8) * progBatchSize
		case moveCmd:
			x, y := coordsFromCmd(data)
			s.Cmds = append(s.Cmds, Cmd{MoveTo, x, y})
			batchCmd()
		case nopCmd:
			batchCmd()
		default:
			return n, errors.New("invalid command")
		}
	}
	return
}

func (s *Simulator) Read(data []byte) (int, error) {
	s.in <- ioRequest{false, data}
	r := <-s.out
	return r.bytes, r.err
}

func (s *Simulator) Write(data []byte) (int, error) {
	s.in <- ioRequest{true, data}
	r := <-s.out
	return r.bytes, r.err
}

func (s *Simulator) Close() error {
	s.close <- struct{}{}
	<-s.close
	return nil
}
