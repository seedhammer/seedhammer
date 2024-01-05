//go:build tinygo && rp

// Package pio implements a driver for the Raspberry Pi
// rp2xxx series of microcontrollers
package pio

import (
	"device/rp"
	"machine"
	"runtime"
	"runtime/volatile"
	"unsafe"
)

// ConfigRegs is a [StateMachineConfig] represented
// in a form suitable for efficient programming of
// a state machine.
type ConfigRegs struct {
	clkdiv    uint32
	execctrl  uint32
	shiftctrl uint32
	pinctrl   uint32
}

type confMemType struct {
	CLKDIV    volatile.Register32
	EXECCTRL  volatile.Register32
	SHIFTCTRL volatile.Register32
	ADDR      volatile.Register32
	INSTR     volatile.Register32
	PINCTRL   volatile.Register32
}

type ioType struct {
	status volatile.Register32
	ctrl   volatile.Register32
}

const numStateMachines = 4
const maxMachineMask = 1<<numStateMachines - 1

const bank0Pins = 32

const JMP = 0b000 << 13
const SET = 0b111 << 13

func Program(pio *rp.PIO0_Type, off uint8, prog []uint16) {
	insts := unsafe.Slice(&pio.INSTR_MEM0, 32)
	insts = insts[off:]
	for i, inst := range prog {
		// Patch absolute addresses.
		if JMP == inst&(0b111<<13) {
			inst += uint16(off)
		}
		insts[i].Set(uint32(inst))
	}
}

func (c *StateMachineConfig) Build() ConfigRegs {
	if c.InCount < 0 || 32 < c.InCount {
		panic("invalid in count")
	}
	// In count 32 is encoded as 0.
	inCount := c.InCount & 0b11111
	if c.OutCount < 0 || 32 < c.OutCount {
		panic("invalid out count")
	}
	sidesetCount := c.SidesetCount
	if c.SidesetOptional {
		sidesetCount++
	}
	if c.SidesetCount < 0 || 5 < c.SidesetCount {
		panic("invalid sideset count")
	}
	if c.SetCount < 0 || 5 < c.SetCount {
		panic("invalid set count")
	}
	pullThres := validatePushPullThreshold(c.PullThreshold)
	pushThres := validatePushPullThreshold(c.PushThreshold)
	// Compute fractional clock divisor, rounded up.
	const fracBits = 8
	clkDiv64 := (uint64(machine.CPUFrequency())*(1<<fracBits) + uint64(c.Freq) - 1) / uint64(c.Freq)
	if clkDiv64 == 0 || clkDiv64 > 0x1000000 {
		panic("frequency out of range")
	}
	// Clock divisor 65536 is encoded as 0.
	clkDiv := uint32(clkDiv64) & 0xffffff
	fjoinRX := uint32(0b0)
	fjoinTX := uint32(0b0)
	switch c.FIFOMode {
	case FIFOJoinNone:
	case FIFOJoinRX:
		fjoinRX = 0b1
	case FIFOJoinTX:
		fjoinTX = 0b1
	default:
		panic("invalid FIFO mode")
	}
	return ConfigRegs{
		clkdiv: clkDiv << rp.PIO0_SM0_CLKDIV_FRAC_Pos,
		pinctrl: uint32(c.SidesetCount)<<rp.PIO0_SM0_PINCTRL_SIDESET_COUNT_Pos |
			uint32(c.SetCount)<<rp.PIO0_SM0_PINCTRL_SET_COUNT_Pos |
			uint32(c.OutCount)<<rp.PIO0_SM0_PINCTRL_OUT_COUNT_Pos |
			uint32(c.InBase)<<rp.PIO0_SM0_PINCTRL_IN_BASE_Pos |
			uint32(c.SidesetBase)<<rp.PIO0_SM0_PINCTRL_SIDESET_BASE_Pos |
			uint32(c.SetBase)<<rp.PIO0_SM0_PINCTRL_SET_BASE_Pos |
			uint32(c.OutBase)<<rp.PIO0_SM0_PINCTRL_OUT_BASE_Pos,
		shiftctrl: fjoinRX<<rp.PIO0_SM0_SHIFTCTRL_FJOIN_RX_Pos |
			fjoinTX<<rp.PIO0_SM0_SHIFTCTRL_FJOIN_TX_Pos |
			uint32(pullThres)<<rp.PIO0_SM0_SHIFTCTRL_PULL_THRESH_Pos |
			uint32(pushThres)<<rp.PIO0_SM0_SHIFTCTRL_PUSH_THRESH_Pos |
			0b1<<rp.PIO0_SM0_SHIFTCTRL_OUT_SHIFTDIR_Pos | // Shift right.
			0b1<<rp.PIO0_SM0_SHIFTCTRL_IN_SHIFTDIR_Pos | // Shift right.
			uint32(inCount)<<rp.PIO0_SM0_SHIFTCTRL_IN_COUNT_Pos |
			boolToUint32(c.Autopush)<<rp.PIO0_SM0_SHIFTCTRL_AUTOPUSH_Pos |
			boolToUint32(c.Autopull)<<rp.PIO0_SM0_SHIFTCTRL_AUTOPULL_Pos,

		execctrl: uint32(c.WrapTarget)<<rp.PIO0_SM0_EXECCTRL_WRAP_BOTTOM_Pos |
			uint32(c.Wrap)<<rp.PIO0_SM0_EXECCTRL_WRAP_TOP_Pos |
			uint32(c.JumpPin)<<rp.PIO0_SM0_EXECCTRL_JMP_PIN_Pos |
			boolToUint32(c.SidesetOptional)<<rp.PIO0_SM0_EXECCTRL_SIDE_EN_Pos |
			boolToUint32(c.SidesetDirs)<<rp.PIO0_SM0_EXECCTRL_SIDE_PINDIR_Pos,
	}
}

func Configure(p *rp.PIO0_Type, sm uint8, c ConfigRegs) {
	confMem := confFor(p, sm)
	confMem.PINCTRL.Set(c.pinctrl)
	confMem.SHIFTCTRL.Set(c.shiftctrl)
	confMem.EXECCTRL.Set(c.execctrl)
	confMem.CLKDIV.Set(c.clkdiv)
	// Jump to start of program (wrap target).
	wrapTarget := uint8(c.execctrl & rp.PIO0_SM0_EXECCTRL_WRAP_BOTTOM_Msk >> rp.PIO0_SM0_EXECCTRL_WRAP_BOTTOM_Pos)
	Jump(p, sm, wrapTarget)
}

func Jump(p *rp.PIO0_Type, sm, target uint8) {
	confMem := confFor(p, sm)
	confMem.INSTR.Set(uint32(JMP | target))
}

func confFor(p *rp.PIO0_Type, sm uint8) *confMemType {
	confs := unsafe.Slice((*confMemType)(unsafe.Pointer(&p.SM0_CLKDIV)), numStateMachines)
	return &confs[sm]
}

func ioBank0() []ioType {
	return unsafe.Slice((*ioType)(unsafe.Pointer(&rp.IO_BANK0.GPIO0_STATUS)), bank0Pins)
}

// ConfigurePins configure npin pins starting at base for PIO.
func ConfigurePins(p *rp.PIO0_Type, sm uint8, base machine.Pin, npins int) {
	var pioMode machine.PinMode
	switch p {
	case rp.PIO0:
		pioMode = machine.PinPIO0
	case rp.PIO1:
		pioMode = machine.PinPIO1
	case rp.PIO2:
		pioMode = machine.PinPIO2
	default:
		panic("unknown PIO")
	}
	for i := range npins {
		pin := base + machine.Pin(i)
		pin.Configure(machine.PinConfig{Mode: pioMode})
	}
}

func Pindirs(p *rp.PIO0_Type, sm uint8, base machine.Pin, npins int, mode machine.PinMode) {
	modeDir := uint32(0b0)
	switch mode {
	case machine.PinOutput:
		modeDir = 0b1
	}
	confMem := confFor(p, sm)
	pinCtrl := confMem.PINCTRL.Get()
	defer confMem.PINCTRL.Set(pinCtrl)
	// Temporarily enable optional side sets.
	execCtrl := confMem.EXECCTRL.Get()
	defer confMem.EXECCTRL.Set(execCtrl)
	confMem.EXECCTRL.SetBits(rp.PIO0_SM0_EXECCTRL_SIDE_EN_Msk)
	ioBank0 := ioBank0()
	for npins > 0 {
		n := min(5, npins)
		var dirs uint32
		for i := n - 1; i >= 0; i-- {
			pinCtrl := &ioBank0[int(base)+i].ctrl
			pinInvert := (pinCtrl.Get() & rp.IO_BANK0_GPIO0_CTRL_OEOVER_Msk) >> rp.IO_BANK0_GPIO0_CTRL_OEOVER_Pos
			dir := modeDir
			if pinInvert == 0b1 {
				dir = ^dir
			}
			dirs = dirs<<1 | dir
		}
		confMem.PINCTRL.Set(
			pinCtrl&^(rp.PIO0_SM0_PINCTRL_SET_BASE_Msk|rp.PIO0_SM0_PINCTRL_SET_COUNT_Msk) |
				uint32(base)<<rp.PIO0_SM0_PINCTRL_SET_BASE_Pos |
				uint32(n)<<rp.PIO0_SM0_PINCTRL_SET_COUNT_Pos,
		)
		// Execute the instruction "set pindir <dirs>".
		// Note that sidesets were made optional earlier.
		confMem.INSTR.Set(SET | 0b100<<5 | dirs)
		npins -= n
		base += machine.Pin(n)
	}
}

func ClearFIFOs(p *rp.PIO0_Type, sm uint8) {
	conf := confFor(p, sm)
	// Toggling FJOIN_RX clears both FIFOs.
	r := conf.SHIFTCTRL.Get()
	conf.SHIFTCTRL.Set(r ^ rp.PIO0_SM0_SHIFTCTRL_FJOIN_RX)
	conf.SHIFTCTRL.Set(r)
}

func Restart(p *rp.PIO0_Type, machines uint8) {
	if machines > maxMachineMask {
		panic("machines out of range")
	}
	p.CTRL.SetBits(uint32(machines) << rp.PIO0_CTRL_SM_RESTART_Pos)
}

func Enable(p *rp.PIO0_Type, machines uint8) {
	if machines > maxMachineMask {
		panic("machines out of range")
	}
	p.CTRL.SetBits(uint32(machines) << rp.PIO0_CTRL_SM_ENABLE_Pos)
}

func Disable(p *rp.PIO0_Type, machines uint8) {
	if machines > maxMachineMask {
		panic("machines out of range")
	}
	p.CTRL.ClearBits(uint32(machines) << rp.PIO0_CTRL_SM_ENABLE_Pos)
}

func Addr(p *rp.PIO0_Type, sm uint8) int {
	conf := confFor(p, sm)
	return int(conf.ADDR.Get())
}

func PullThreshold(p *rp.PIO0_Type, sm uint8, thresh int) {
	conf := confFor(p, sm)
	conf.SHIFTCTRL.Set(conf.SHIFTCTRL.Get()&^rp.PIO0_SM0_SHIFTCTRL_PULL_THRESH_Msk | validatePushPullThreshold(thresh)<<rp.PIO0_SM0_SHIFTCTRL_PULL_THRESH_Pos)
}

func Tx(p *rp.PIO0_Type, sm uint8) *volatile.Register32 {
	txs := unsafe.Slice((*volatile.Register32)(unsafe.Pointer(&p.TXF0)), numStateMachines)
	return &txs[sm]
}

func Rx(p *rp.PIO0_Type, sm uint8) *volatile.Register32 {
	rxs := unsafe.Slice((*volatile.Register32)(unsafe.Pointer(&p.RXF0)), numStateMachines)
	return &rxs[sm]
}

func IsRxEmpty(p *rp.PIO0_Type, sm uint8) bool {
	rxempty := p.FSTAT.Get() & rp.PIO0_FSTAT_RXEMPTY_Msk >> rp.PIO0_FSTAT_RXEMPTY_Pos
	return rxempty&(0b1<<sm) != 0
}

func DreqTx(p *rp.PIO0_Type, sm uint8) uint32 {
	dreq := uint32(sm)
	switch p {
	case rp.PIO0:
	case rp.PIO1:
		dreq += 8
	case rp.PIO2:
		dreq += 16
	}
	return dreq
}

func Instr(p *rp.PIO0_Type, sm uint8) *volatile.Register32 {
	conf := confFor(p, sm)
	return &conf.INSTR
}

func WaitTxStall(p *rp.PIO0_Type, machinesMask int) {
	m := uint32(machinesMask)
	// Clear stall flag(s).
	p.SetFDEBUG_TXSTALL(m)
	// Wait.
	for p.GetFDEBUG_TXSTALL()&m != m {
		runtime.Gosched()
	}
}

func WaitTxNotFull(p *rp.PIO0_Type, machinesMask int) {
	for p.GetFSTAT_TXFULL()&uint32(machinesMask) != 0 {
		runtime.Gosched()
	}
}

func WaitTxEmpty(p *rp.PIO0_Type, machinesMask int) {
	for p.GetFSTAT_TXEMPTY()&uint32(machinesMask) == 0 {
		runtime.Gosched()
	}
}

func validatePushPullThreshold(t int) uint32 {
	if t < 0 || 32 < t {
		panic("invalid pull threshold")
	}
	// Pull threshold 32 is encoded as 0.
	return uint32(t & 0b11111)
}

func boolToUint32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}
