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
		i := int(b) * 2
		buf = append(buf, abbrev[i:i+2]...)
	}
	check := crc32.ChecksumIEEE(data)
	var checkb [4]byte
	binary.BigEndian.PutUint32(checkb[:], check)
	for _, b := range checkb {
		i := int(b) * 2
		buf = append(buf, abbrev[i:i+2]...)
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
	for i := range dst {
		w, ok := lookup(src[i*2], src[i*2+1])
		if !ok {
			return nil, errors.New("invalid word")
		}
		dst[i] = w
	}
	res := dst[:len(dst)-4]
	got := binary.BigEndian.Uint32(dst[len(dst)-4:])
	want := crc32.ChecksumIEEE(res)
	if got != want {
		return nil, errors.New("crc32 checksum mismatch")
	}
	return res, nil
}

func lookup(l1, l2 byte) (byte, bool) {
	idx := l1 - 'a'
	if int(idx) >= len(firstLetters) {
		return 0, false
	}
	start := firstLetters[l1-'a']
	for i := int(start); i < len(abbrev)/2; i++ {
		w0, w3 := abbrev[i*2], abbrev[i*2+1]
		if w0 != l1 {
			break
		}
		if w3 == l2 {
			return byte(i), true
		}
	}
	return 0, false
}

var firstLetters [26]uint8

func init() {
	var letter byte = 'a' - 1
	for i := range len(abbrev) / 2 {
		if l1 := abbrev[i*2]; l1 != letter {
			letter = l1
			firstLetters[letter-'a'] = uint8(i)
		}
	}
}

// abbrev contains the two-letter abbreviations for the bytewords word list:
// able, acid, also, apex, aqua, arch, atom, aunt,
// away, axis, back, bald, barn, belt, beta, bias,
// blue, body, brag, brew, bulb, buzz, calm, cash,
// cats, chef, city, claw, code, cola, cook, cost,
// crux, curl, cusp, cyan, dark, data, days, deli,
// dice, diet, door, down, draw, drop, drum, dull,
// duty, each, easy, echo, edge, epic, even, exam,
// exit, eyes, fact, fair, fern, figs, film, fish,
// fizz, flap, flew, flux, foxy, free, frog, fuel,
// fund, gala, game, gear, gems, gift, girl, glow,
// good, gray, grim, guru, gush, gyro, half, hang,
// hard, hawk, heat, help, high, hill, holy, hope,
// horn, huts, iced, idea, idle, inch, inky, into,
// iris, iron, item, jade, jazz, join, jolt, jowl,
// judo, jugs, jump, junk, jury, keep, keno, kept,
// keys, kick, kiln, king, kite, kiwi, knob, lamb,
// lava, lazy, leaf, legs, liar, limp, lion, list,
// logo, loud, love, luau, luck, lung, main, many,
// math, maze, memo, menu, meow, mild, mint, miss,
// monk, nail, navy, need, news, next, noon, note,
// numb, obey, oboe, omit, onyx, open, oval, owls,
// paid, part, peck, play, plus, poem, pool, pose,
// puff, puma, purr, quad, quiz, race, ramp, real,
// redo, rich, road, rock, roof, ruby, ruin, runs,
// rust, safe, saga, scar, sets, silk, skew, slot,
// soap, solo, song, stub, surf, swan, taco, task,
// taxi, tent, tied, time, tiny, toil, tomb, toys,
// trip, tuna, twin, ugly, undo, unit, urge, user,
// vast, very, veto, vial, vibe, view, visa, void,
// vows, wall, wand, warm, wasp, wave, waxy, webs,
// what, when, whiz, wolf, work, yank, yawn, yell,
// yoga, yurt, zaps, zero, zest, zinc, zone, zoom.
const abbrev = "aeadaoaxaaahamatayasbkbdbnbtbabsbebybgbwbbbzcmchcscfcycwcecackctcxclcpcndkdadsdidedtdrdndwdpdmdldyeheyeoeeecenemetesftfrfnfsfmfhfzfpfwfxfyfefgflfdgagegrgsgtglgwgdgygmgughgohfhghdhkhthphhhlhyhehnhsidiaieihiyioisinimjejzjnjtjljojsjpjkjykpkoktkskkknkgkekikblblalylflslrlplnltloldlelulklgmnmymhmemomumwmdmtmsmknlnyndnsntnnnenboyoeotoxonolospdptpkpypspmplpepfpaprqdqzrerprlrorhrdrkrfryrnrsrtsesasrssskswstspsosgsbsfsntotktitttdtetytltbtstptatnuyuoutueurvtvyvovlvevwvavdvswlwdwmwpwewywswtwnwzwfwkykynylyaytzszoztzczezm"
