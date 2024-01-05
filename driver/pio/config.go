package pio

// Below is the API to please pioasm -o go.

// StateMachineConfig represents a configuration
// of a PIO state machine.
// Note that the pioasm tool expects this particular
// type name.
type StateMachineConfig struct {
	SidesetBase     uint8
	SidesetCount    int
	SidesetOptional bool
	SidesetDirs     bool
	OutBase         uint8
	OutCount        int
	InBase          uint8
	InCount         int
	SetBase         uint8
	SetCount        int
	JumpPin         uint8
	FIFOMode        FIFOMode
	PullThreshold   int
	PushThreshold   int
	Autopull        bool
	Autopush        bool
	Freq            uint32
	Wrap            uint8
	WrapTarget      uint8
}

type FIFOMode uint8

const (
	FIFOJoinNone FIFOMode = iota
	FIFOJoinTX
	FIFOJoinRX
)

func DefaultStateMachineConfig() StateMachineConfig {
	return StateMachineConfig{}
}

func (s *StateMachineConfig) SetWrap(target, wrap uint8) {
	s.WrapTarget = target
	s.Wrap = wrap
}

func (s *StateMachineConfig) SetSidesetParams(sidecount int, optional, pindirs bool) {
	s.SidesetCount = sidecount
	s.SidesetOptional = optional
	s.SidesetDirs = pindirs
}
