// Package bytewords implements the the bytewords standard
// as described in [BCR-2020-012].
//
// [BCR-2020-012]: https://github.com/BlockchainCommons/Research/blob/master/papers/bcr-2020-005-ur.md
package bytewords

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

type byteview interface {
	~string | ~[]byte
}

func Encode(data []byte) string {
	buf := make([]byte, 0, (len(data)+4)*2)
	for _, b := range data {
		w := words[b]
		buf = append(buf, w[0], w[3])
	}
	check := crc32.ChecksumIEEE(data)
	var checkb [4]byte
	binary.BigEndian.PutUint32(checkb[:], check)
	for _, b := range checkb {
		w := words[b]
		buf = append(buf, w[0], w[3])
	}
	return string(buf)
}

func Decode[T byteview](src T) ([]byte, error) {
	if len(src)%2 == 1 {
		return nil, errors.New("truncated input")
	}
	dst := make([]byte, len(src)/2)
	if len(dst) < 4 {
		return nil, errors.New("input too short")
	}
	n := 0
	for i := 0; i < len(src); i += 2 {
		w, ok := invMinWords[toU16(src[i], src[i+1])]
		if !ok {
			return nil, errors.New("invalid word")
		}
		dst[n] = w
		n++
	}
	res := dst[:len(dst)-4]
	got := binary.BigEndian.Uint32(dst[len(dst)-4:])
	want := crc32.ChecksumIEEE(res)
	if got != want {
		return nil, errors.New("crc32 checksum mismatch")
	}
	return res, nil
}

func toU16(first, last byte) uint16 {
	return uint16(first)<<8 | uint16(last)
}

var invMinWords = make(map[uint16]byte)

func init() {
	for i, w := range words {
		invMinWords[toU16(w[0], w[3])] = byte(i)
	}
}

var words = [256]string{
	"able", "acid", "also", "apex", "aqua", "arch", "atom", "aunt",
	"away", "axis", "back", "bald", "barn", "belt", "beta", "bias",
	"blue", "body", "brag", "brew", "bulb", "buzz", "calm", "cash",
	"cats", "chef", "city", "claw", "code", "cola", "cook", "cost",
	"crux", "curl", "cusp", "cyan", "dark", "data", "days", "deli",
	"dice", "diet", "door", "down", "draw", "drop", "drum", "dull",
	"duty", "each", "easy", "echo", "edge", "epic", "even", "exam",
	"exit", "eyes", "fact", "fair", "fern", "figs", "film", "fish",
	"fizz", "flap", "flew", "flux", "foxy", "free", "frog", "fuel",
	"fund", "gala", "game", "gear", "gems", "gift", "girl", "glow",
	"good", "gray", "grim", "guru", "gush", "gyro", "half", "hang",
	"hard", "hawk", "heat", "help", "high", "hill", "holy", "hope",
	"horn", "huts", "iced", "idea", "idle", "inch", "inky", "into",
	"iris", "iron", "item", "jade", "jazz", "join", "jolt", "jowl",
	"judo", "jugs", "jump", "junk", "jury", "keep", "keno", "kept",
	"keys", "kick", "kiln", "king", "kite", "kiwi", "knob", "lamb",
	"lava", "lazy", "leaf", "legs", "liar", "limp", "lion", "list",
	"logo", "loud", "love", "luau", "luck", "lung", "main", "many",
	"math", "maze", "memo", "menu", "meow", "mild", "mint", "miss",
	"monk", "nail", "navy", "need", "news", "next", "noon", "note",
	"numb", "obey", "oboe", "omit", "onyx", "open", "oval", "owls",
	"paid", "part", "peck", "play", "plus", "poem", "pool", "pose",
	"puff", "puma", "purr", "quad", "quiz", "race", "ramp", "real",
	"redo", "rich", "road", "rock", "roof", "ruby", "ruin", "runs",
	"rust", "safe", "saga", "scar", "sets", "silk", "skew", "slot",
	"soap", "solo", "song", "stub", "surf", "swan", "taco", "task",
	"taxi", "tent", "tied", "time", "tiny", "toil", "tomb", "toys",
	"trip", "tuna", "twin", "ugly", "undo", "unit", "urge", "user",
	"vast", "very", "veto", "vial", "vibe", "view", "visa", "void",
	"vows", "wall", "wand", "warm", "wasp", "wave", "waxy", "webs",
	"what", "when", "whiz", "wolf", "work", "yank", "yawn", "yell",
	"yoga", "yurt", "zaps", "zero", "zest", "zinc", "zone", "zoom",
}
