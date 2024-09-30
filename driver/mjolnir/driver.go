// package mjolnir implements a driver for the MarkingWay engraving
// machine.
package mjolnir

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"

	"seedhammer.com/engrave"
)

type program struct {
	cmds  chan [cmdSize]byte
	count int
	sent  int
}

var Params = engrave.Params{
	StrokeWidth: 38,
	Millimeter:  126,
}

type Options struct {
	MoveSpeed  float32
	PrintSpeed float32
	End        image.Point
}

var safePoint = image.Pt(119, 43)

const (
	cmdSize = 10

	defaultMoveSpeed  = .5
	defaultPrintSpeed = .1
)

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

func Engrave(dev io.ReadWriter, opts Options, plan engrave.Plan, quit <-chan struct{}) (eerr error) {
	bufw := bufio.NewWriterSize(dev, progBatchSize*cmdSize)
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

	runProgram := func(plan engrave.Plan) {
		p := &program{}
		for c := range plan {
			p.Command(c)
		}
		p.Prepare()
		defer func() {
			for i := p.sent; i < p.count; i++ {
				<-p.cmds
			}
		}()
		go func() {
			for c := range plan {
				p.Command(c)
			}
		}()
		p.sent = 0
		// Round up to nearest batch size. Note that the rounding
		// adds another, empty, batch in case we fill up the last one.
		// Otherwise, the engraver won't send a completed status.
		nbatches := (p.count + progBatchSize) / progBatchSize
		if nbatches > 0xffff {
			eerr = errors.New("engrave: program too large")
			return
		}
		wr(initProgramCmd, byte(nbatches), byte(nbatches>>8))
	done:
		for {
			status := r(1)
			if eerr != nil {
				return
			}
			paddedCount := nbatches * progBatchSize
			switch status[0] {
			case bufferProgramStatus:
				if p.sent == paddedCount {
					break
				}
				ncmd := progBatchSize
				if rem := p.count - p.sent; ncmd > rem {
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
					p.sent++
					wr(pad[:]...)
				}
			case programStepStatus:
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
		runProgram(func(yield func(engrave.Command) bool) {
			yield(engrave.Move(p))
		})
	}

	setSpeeds(300, 300, 0xe6)

	// Prepare the machine: (1) reset the origin and
	// (2) move to safe point. The first is necessary because
	// the absolute position of the needle is not known at startup.
	// The second is to avoid needle collision with the tightening
	// nuts.
	origin()
	// Avoid a false home by moving out and re-homing.
	falseHome := 5 * Params.Millimeter
	moveTo(image.Pt(falseHome, falseHome))
	origin()
	sp := image.Point{
		X: safePoint.X * Params.Millimeter,
		Y: safePoint.Y * Params.Millimeter,
	}
	moveTo(sp)

	// 0 lowest, 1 highest.
	moveSpeed := opts.MoveSpeed
	printSpeed := opts.PrintSpeed
	if moveSpeed == 0 {
		moveSpeed = defaultMoveSpeed
	}
	if printSpeed == 0 {
		printSpeed = defaultPrintSpeed
	}
	mms := int(moveSpeed*float32(30) + (1.-moveSpeed)*float32(1000))
	mps := int(printSpeed*float32(30) + (1.-printSpeed)*float32(1000))
	setSpeeds(mps, mms, 0xe6)
	runProgram(plan)
	if eerr == nil || eerr == ErrCancelled {
		setSpeeds(300, 300, 0xe6)
		if opts.End != (image.Point{}) {
			moveTo(opts.End)
		} else {
			moveTo(sp)
			origin()
		}
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

func (p *program) cmd(c [cmdSize]byte) {
	if p.cmds != nil {
		p.cmds <- c
	} else {
		p.count++
	}
}

func (p *program) Prepare() {
	p.cmds = make(chan [cmdSize]byte)
}

func (p *program) Command(c engrave.Command) {
	var cmd [cmdSize]byte
	coords := mkcoords(c.Coord)
	copy(cmd[1:], coords[:])
	if c.Line {
		cmd[0] = lineCmd
	} else {
		cmd[0] = moveCmd
	}
	p.cmd(cmd)
	p.pause()
}

func (p *program) pause() {
	//	p.cmd([...]byte{0x82, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
}
