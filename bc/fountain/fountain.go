// Package fountain implements the fountain encoding used by
// the Uniform Resources (UR) format described in [BCR-2020-005].
//
// [BCR-2020-005]: https://github.com/BlockchainCommons/Research/blob/master/papers/bcr-2020-005-ur.md
package fountain

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/fxamacker/cbor/v2"
	"seedhammer.com/bc/xoshiro256"
)

type Decoder struct {
	header    partHeader
	queue     []*part
	mixed     map[string]*part
	completed map[int]*part
}

func Encode(message []byte, seqNum, seqLen int) []byte {
	if seqLen == 1 {
		return message
	}
	n := (len(message) + seqLen - 1) / seqLen
	payload := make([]byte, n)
	checksum := Checksum(message)
	sn32 := uint32(seqNum)
	if int(sn32) != seqNum {
		panic("seqNum out of range")
	}
	fragments := chooseFragments(sn32, seqLen, checksum)
	for _, idx := range fragments {
		start := idx * n
		if start > len(message) {
			continue
		}
		frag := message[start:]
		if len(frag) > len(payload) {
			frag = frag[:len(payload)]
		}
		for i, b := range frag {
			payload[i] = payload[i] ^ b
		}
	}
	p := part{
		SeqNum: sn32,
		partHeader: partHeader{
			SeqLen:     seqLen,
			MessageLen: len(message),
			Checksum:   checksum,
		},
		Data: payload,
	}
	enc, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	b, err := enc.Marshal(p)
	if err != nil {
		// Valid by construction.
		panic(err)
	}
	return b
}

type part struct {
	_      struct{} `cbor:",toarray"`
	SeqNum uint32
	partHeader
	Data []byte

	fragments []int
}

type partHeader struct {
	SeqLen     int
	MessageLen int
	Checksum   uint32
}

func (d *Decoder) Progress() float32 {
	estimated := float32(d.header.SeqLen) * 1.75
	p := float32(len(d.completed)+len(d.mixed)) / estimated
	if p > 1 {
		p = 1
	}
	return p
}

func (d *Decoder) Add(data []byte) error {
	mode, err := cbor.DecOptions{
		ExtraReturnErrors: cbor.ExtraDecErrorUnknownField,
	}.DecMode()
	if err != nil {
		return fmt.Errorf("fountain: failed to initialize decoder: %w", err)
	}

	p := new(part)
	if err := mode.Unmarshal(data, p); err != nil {
		return fmt.Errorf("fountain: failed to decode fragment: %w", err)
	}
	if d.header.SeqLen > 0 {
		if d.header != p.partHeader {
			return fmt.Errorf("fountain: incompatible fragment")
		}
	} else {
		d.header = p.partHeader
	}
	p.fragments = chooseFragments(p.SeqNum, p.SeqLen, p.Checksum)
	d.queue = append(d.queue, p)

	for len(d.queue) > 0 {
		p := d.queue[len(d.queue)-1]
		d.queue = d.queue[:len(d.queue)-1]
		if len(p.fragments) == 1 {
			if d.completed == nil {
				d.completed = make(map[int]*part)
			}
			d.completed[p.fragments[0]] = p
			d.reduceMixed(p)
		} else {
			if d.mixed == nil {
				d.mixed = make(map[string]*part)
			}
			for _, other := range d.completed {
				reducePart(p, other)
			}
			for _, other := range d.mixed {
				reducePart(p, other)
			}
			if len(p.fragments) == 1 {
				d.queue = append(d.queue, p)
			} else {
				d.reduceMixed(p)
				d.mixed[mixedKey(p.fragments)] = p
			}
		}
	}
	return nil
}

func reducePart(a, b *part) {
	// return if b is not a strict subset of a.
	if len(b.fragments) >= len(a.fragments) {
		return
	}
	m := make(map[int]bool)
	for _, f := range a.fragments {
		m[f] = true
	}
	for _, f := range b.fragments {
		if !m[f] {
			return
		}
		delete(m, f)
	}

	// Subtract b from a.
	a.fragments = nil
	for f := range m {
		a.fragments = append(a.fragments, f)
	}
	for i := range a.Data {
		a.Data[i] ^= b.Data[i]
	}
}

func (d *Decoder) reduceMixed(p *part) {
	for k, other := range d.mixed {
		delete(d.mixed, k)
		reducePart(other, p)
		if len(other.fragments) == 1 {
			d.queue = append(d.queue, other)
		} else {
			d.mixed[mixedKey(other.fragments)] = other
		}
	}
}

func mixedKey(ids []int) string {
	sort.Ints(ids)
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = strconv.Itoa(id)
	}
	return strings.Join(strs, "|")
}

func (d *Decoder) Result() ([]byte, error) {
	if len(d.completed) != d.header.SeqLen {
		return nil, nil
	}
	var sorted []*part
	for _, p := range d.completed {
		sorted = append(sorted, p)
	}
	slices.SortFunc(sorted, func(part1, part2 *part) int {
		return part1.fragments[0] - part2.fragments[0]
	})
	var msg []byte
	for _, p := range sorted {
		msg = append(msg, p.Data...)
	}
	if len(msg) < d.header.MessageLen {
		return nil, fmt.Errorf("fountain: message too short")
	}
	msg = msg[:d.header.MessageLen]
	check := Checksum(msg)
	if check != d.header.Checksum {
		return nil, fmt.Errorf("fountain: mismatched checksum or message too short")
	}
	return msg, nil
}

func Checksum(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// SeqNumFor searches for a seqNum that outpus the xor of fragments.
func SeqNumFor(seqLen int, checksum uint32, fragments []int) int {
	seqNum := 1
	sort.Ints(fragments)
	for {
		got := chooseFragments(uint32(seqNum), seqLen, checksum)
		sort.Ints(got)
		if slices.Equal(got, fragments) {
			return seqNum
		}
		seqNum++
	}
}

func chooseFragments(seqNum uint32, seqLen int, checksum uint32) []int {
	if seqNum <= uint32(seqLen) {
		return []int{int(seqNum - 1)}
	} else {
		seed := binary.BigEndian.AppendUint32(nil, seqNum)
		seed = binary.BigEndian.AppendUint32(seed, checksum)
		h := sha256.Sum256(seed)
		rng := new(xoshiro256.Source)
		rng.Seed(h)
		degree := chooseDegree(seqLen, rng)
		indexes := make([]int, seqLen)
		for i := range indexes {
			indexes[i] = i
		}
		shuffled := shuffle(indexes, rng)
		return shuffled[:degree]
	}
}

func shuffle(items []int, rng *xoshiro256.Source) []int {
	var result []int
	for len(items) > 0 {
		idx := rng.Intn(len(items))
		it := items[idx]
		items = append(items[:idx], items[idx+1:]...)
		result = append(result, it)
	}
	return result
}

func chooseDegree(seqLen int, rng *xoshiro256.Source) int {
	probs := make([]float64, seqLen)
	for i := range probs {
		probs[i] = 1. / float64(i+1)
	}
	return sample(probs, rng.Float64) + 1
}

func sample(probs []float64, rng func() float64) int {
	var sum float64
	for _, p := range probs {
		sum += p
	}

	n := len(probs)
	P := make([]float64, n)
	for i, p := range probs {
		P[i] = p * float64(n) / sum
	}

	var S, L []int

	for i := n - 1; i >= 0; i-- {
		if P[i] < 1 {
			S = append(S, i)
		} else {
			L = append(L, i)
		}
	}

	probs = make([]float64, n)
	aliases := make([]int, n)
	for len(S) > 0 && len(L) > 0 {
		a := S[len(S)-1]
		S = S[:len(S)-1]
		g := L[len(L)-1]
		L = L[:len(L)-1]
		probs[a] = P[a]
		aliases[a] = g
		P[g] += P[a] - 1
		if P[g] < 1 {
			S = append(S, g)
		} else {
			L = append(L, g)
		}
	}

	for len(L) > 0 {
		g := L[len(L)-1]
		L = L[:len(L)-1]
		probs[g] = 1
	}

	for len(S) > 0 {
		a := S[len(S)-1]
		S = S[:len(S)-1]
		probs[a] = 1
	}

	r1 := rng()
	r2 := rng()
	i := int(float64(n) * r1)
	if r2 < probs[i] {
		return i
	} else {
		return aliases[i]
	}
}
