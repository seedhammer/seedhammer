package bip380

import "strings"

const (
	alphabet         = "0123456789()[],'/*abcdefgh@:$%{}IJKLMNOPQRSTUVWXYZ&+-.;<=>?!^_|~ijklmnopqrstuvwxyzABCDEFGH`#\"\\ "
	checksumAlphabet = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
)

var generator = []uint64{0xf5dee51989, 0xa9fdca3312, 0x1bab10e32d, 0x3706b1677a, 0x644d626ffd}

// checksumExpand expands a string to symbols. It returns false
// if one of more characters are outside the alphabet.
func checksumExpand(s string) ([]byte, bool) {
	groups := make([]byte, 0, 3)
	syms := make([]byte, 0, len(s)*4/3)
	for i := range len(s) {
		c := s[i]
		idx := strings.IndexByte(alphabet, c)
		if idx == -1 {
			return nil, false
		}
		v := byte(idx)
		syms = append(syms, v&31)
		groups = append(groups, v>>5)
		if len(groups) == 3 {
			syms = append(syms, groups[0]*9+groups[1]*3+groups[2])
			groups = groups[:0]
		}
	}
	switch len(groups) {
	case 1:
		syms = append(syms, groups[0])
	case 2:
		syms = append(syms, groups[0]*3+groups[1])
	}
	return syms, true
}

// Polymod computes the checksum of symbols.
func polymod(syms []byte) uint64 {
	chk := uint64(1)
	for _, v := range syms {
		top := chk >> 35
		chk = (chk&0x7ffffffff)<<5 ^ uint64(v)
		for i := range 5 {
			if (top>>i)&1 != 0 {
				chk ^= generator[i]
			}
		}
	}
	return chk
}

// validChecksum reports whether c is a valid checksum
// for s.
func validChecksum(s, c string) bool {
	if len(c) != 8 {
		return false
	}
	syms, ok := checksumExpand(s)
	if !ok {
		return false
	}
	for i := range len(c) {
		idx := strings.IndexByte(checksumAlphabet, c[i])
		if idx == -1 {
			return false
		}
		syms = append(syms, byte(idx))
	}
	return polymod(syms) == 1
}

// checksum computes the checksum of s. It returns false
// if s contains invalid characters.
func checksum(s string) (string, bool) {
	syms, ok := checksumExpand(s)
	if !ok {
		return "", false
	}
	syms = append(syms, 0, 0, 0, 0, 0, 0, 0, 0)
	sum := polymod(syms) ^ 1
	var res [8]byte
	for i := range len(res) {
		res[i] = checksumAlphabet[(sum>>(5*(7-i)))&31]
	}
	return string(res[:]), true
}
