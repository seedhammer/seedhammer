// package mjolnir implements a driver for the MarkgWay engraving
// machine.
package mjolnir

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"runtime"

	"github.com/tarm/serial"
)

type Program struct {
	DryRun     bool
	MoveSpeed  float32
	PrintSpeed float32
	End        image.Point
	cmds       chan [cmdSize]byte
	count      int
	sent       int
}

const (
	// StrokeWidth in millimeters.
	StrokeWidth = 0.3
	// Step is the step distance in millimeters per machine unit.
	Step = 0.00796
	// Millimeters is machine units per millimeter.
	Millimeter = 1 / Step
)

const (
	cmdSize = 10

	defaultMoveSpeed  = .75
	defaultPrintSpeed = .1
)

func Open(dev string) (io.ReadWriteCloser, error) {
	// Hardware parameters.
	const (
		baudRate         = 115200
		stopBits         = 1
		parity           = false
		wordLen          = 8
		controlHandshake = 0
		flowReplace      = 0
		xonLimit         = 2048
		xoffLimit        = 512
	)

	var devices []string
	if dev != "" {
		devices = append(devices, dev)
	} else {
		switch runtime.GOOS {
		case "windows":
			devices = append(devices, "COM3")
		case "linux":
			devices = append(devices, "/dev/ttyUSB0", "/dev/ttyUSB1")
		}
	}
	if len(devices) == 0 {
		return nil, errors.New("no device specified")
	}
	var firstErr error
	for _, dev := range devices {
		c := &serial.Config{Name: dev, Baud: baudRate}
		s, err := serial.OpenPort(c)
		if err == nil {
			return s, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

const (
	initCmd                 = 0x00
	cancelCmd               = 0xaf
	setSpeedCmd             = 0x30
	setDelaysCmd            = 0x31
	moveToOriginCmd         = 0x21
	moveToOriginCmdExtra    = 0x50
	moveToOriginCmdResponse = 0x00
	initProgramCmd          = 0x60
	moveCmd                 = 0x80
	lineCmd                 = 0x00
	nopCmd                  = 0xff
)

const (
	initializedStatus     = 0x00
	cancellingStatus      = 0x62
	cancelledStatus       = 0x65
	bufferProgramStatus   = 0x60
	programStepStatus     = 0x6f
	programCompleteStatus = 0x6a
)

// The engraver expects program commands in batches.
const progBatchSize = 80

func Engrave(dev io.ReadWriter, prog *Program, progress chan float32, quit <-chan struct{}) (eerr error) {
	bufw := bufio.NewWriterSize(dev, progBatchSize*cmdSize)
	defer func() {
		for i := prog.sent; i < prog.count; i++ {
			<-prog.cmds
		}
	}()
	writeMut := make(chan struct{}, 1)
	writeMut <- struct{}{}
	flush := func() {
		<-writeMut
		defer func() { writeMut <- struct{}{} }()
		if eerr != nil {
			return
		}
		eerr = bufw.Flush()
	}
	defer flush()
	wr := func(data ...byte) {
		<-writeMut
		defer func() { writeMut <- struct{}{} }()
		if eerr != nil {
			return
		}
		_, eerr = bufw.Write(data)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-quit:
			select {
			case <-writeMut:
			case <-done:
				return
			}
			dev.Write([]byte{cancelCmd})
			writeMut <- struct{}{}
			<-done
		case <-done:
		}
	}()
	bufr := bufio.NewReaderSize(dev, 100)
	r := func(c int) []byte {
		flush()
		if eerr != nil {
			return nil
		}
		data := make([]byte, c)
		n, err := bufr.Read(data)
		eerr = err
		data = data[:n]
		return data
	}
	expect := func(exp ...byte) {
		for len(exp) > 0 && eerr == nil {
			got := r(len(exp))
			n := len(got)
			if !bytes.Equal(exp[:n], got) {
				eerr = fmt.Errorf("unexpected reply\nexp: %#x\ngot: %#x", exp, got)
				return
			}
			exp = exp[n:]
		}
	}
	atleast := func(n int) []byte {
		var res []byte
		for n > 0 {
			data := r(n)
			res = append(res, data...)
			n -= len(data)
		}
		return res
	}
	origin := func() {
		wr(moveToOriginCmd, moveToOriginCmdExtra)
		expect(moveToOriginCmd, moveToOriginCmdResponse)
	}
	cancel := func() {
		wr(cancelCmd)
	}
	initialize := func() {
		cancel()
		wr(initCmd)
		for {
			status := r(1)
			if eerr != nil {
				break
			}
			switch status[0] {
			case initializedStatus:
				return
			case cancelledStatus:
				// Re-initialize.
				wr(initCmd)
			}
		}
	}
	parseCoords := func(coords []byte) (x int, y int, z int) {
		x = int(coords[0]) | int(coords[1])<<8 | int(coords[2])<<16
		y = int(coords[3]) | int(coords[4])<<8 | int(coords[5])<<16
		z = int(coords[6]) | int(coords[7])<<8 | int(coords[8])<<16
		return
	}
	queryPos := func() (x int, y int, z int) {
		wr(0x16)
		expect(0x16)
		x, y, z = parseCoords(atleast(9))
		return
	}
	_, _ = atleast, queryPos

	initialize()

	// Speed range: [1000,30].
	setSpeeds := func(print, move, xxx int) {
		wr(setSpeedCmd, byte(print), byte(print>>8), byte(move), byte(move>>8), byte(xxx), byte(xxx>>8))
		expect(setSpeedCmd)
	}

	// Delay range: 0-255.
	setDelays := func(penDown, penUp int) {
		wr(setDelaysCmd, byte(penDown), byte(penUp))
		expect(setDelaysCmd)
	}
	setDelays(0x14, 0x14)

	// Init done.

	runProgram := func(p *Program, progress chan float32) {
		p.sent = 0
		nbatches := (p.count + progBatchSize - 1) / progBatchSize
		if nbatches > 0xffff {
			eerr = errors.New("engrave: program too large")
			return
		}
		wr(initProgramCmd, byte(nbatches), byte(nbatches>>8))
		completed := 0
	done:
		for {
			status := r(1)
			if eerr != nil {
				return
			}
			paddedCount := (p.count + progBatchSize - 1) / progBatchSize * progBatchSize
			switch status[0] {
			case bufferProgramStatus:
				rem := p.count - p.sent
				if rem == 0 {
					break
				}
				ncmd := progBatchSize
				if ncmd > rem {
					ncmd = rem
				}
				for i := 0; i < ncmd; i++ {
					cmd := <-p.cmds
					p.sent++
					wr(cmd[:]...)
				}
				// Pad with 0xff.
				pad := [cmdSize]byte{}
				for i := range pad {
					pad[i] = nopCmd
				}
				for i := ncmd; i < progBatchSize; i++ {
					wr(pad[:]...)
				}
			case programStepStatus:
				completed++
				if progress == nil {
					break
				}
				// Don't spam the progress channel.
				if completed%10 != 0 && completed < paddedCount {
					break
				}
				select {
				case <-progress:
				default:
				}
				progress <- float32(completed) / float32(paddedCount)
			case programCompleteStatus:
				break done
			case cancellingStatus:
			case cancelledStatus:
				if eerr == nil {
					eerr = ErrCancelled
				}
			}
		}
	}

	moveTo := func(p image.Point) {
		move := new(Program)
		f := func() {
			move.Move(p)
		}
		f()
		move.Prepare()
		go f()
		runProgram(move, nil)
	}

	setSpeeds(300, 300, 0xe6)
	// Move to origin.
	origin()
	// Avoid false origin.
	off := int(math.Round(10 * Millimeter))
	moveTo(image.Pt(off, off))
	origin()
	// 0 lowest, 1 highest.
	moveSpeed := prog.MoveSpeed
	printSpeed := prog.PrintSpeed
	if moveSpeed == 0 {
		moveSpeed = defaultMoveSpeed
	}
	if printSpeed == 0 {
		printSpeed = defaultPrintSpeed
	}
	mms := int(moveSpeed*float32(30) + (1.-moveSpeed)*float32(1000))
	mps := int(printSpeed*float32(30) + (1.-printSpeed)*float32(1000))
	setSpeeds(mps, mms, 0xe6)
	runProgram(prog, progress)
	if eerr == nil || eerr == ErrCancelled {
		setSpeeds(300, 300, 0xe6)
		moveTo(prog.End)
	}

	return eerr
}

var ErrCancelled = errors.New("cancelled")

func mkcoords(p image.Point) [9]byte {
	x, y := p.X, p.Y
	if x < 0 || x > 0xffffff || y < 0 || y > 0xffffff {
		panic(fmt.Errorf("(%d,%d) out of range", x, y))
	}
	return [...]byte{
		byte(x), byte(x >> 8), byte(x >> 16),
		byte(y), byte(y >> 8), byte(y >> 16),
		0x00, 0x00, 0x00, // Z = 0.
	}
}

func (p *Program) cmd(c [cmdSize]byte) {
	if p.cmds != nil {
		p.cmds <- c
	} else {
		p.count++
	}
}

func (p *Program) Prepare() {
	p.cmds = make(chan [cmdSize]byte)
}

func (p *Program) Move(to image.Point) {
	var cmd [cmdSize]byte
	cmd[0] = moveCmd
	coords := mkcoords(to)
	copy(cmd[1:], coords[:])
	p.cmd(cmd)
	p.pause()
}

func (p *Program) pause() {
	//	p.cmd([...]byte{0x82, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
}

func (p *Program) Line(to image.Point) {
	if p.DryRun {
		p.Move(to)
		return
	}
	var cmd [cmdSize]byte
	cmd[0] = lineCmd
	coords := mkcoords(to)
	copy(cmd[1:], coords[:])
	p.cmd(cmd)
	p.pause()
}
