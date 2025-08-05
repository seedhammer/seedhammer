package codex32

// Field Implementation
//
// Implements GF32 arithmetic, defined and encoded as in [BIP-0173] "bech32".
//
// [BIP-0173]: https://bips.dev/173/

// logTbl is a logarithm table of each bech32 element, as a power of alpha = Z.
//
// Includes Q as 0 but this is false; you need to exclude Q because
// it has no discrete log. If we could have a 1-indexed array that
// would panic on a 0 index that would be better.
var logTbl = [32]uint8{
	0, 0, 1, 14, 2, 28, 15, 22,
	3, 5, 29, 26, 16, 7, 23, 11,
	4, 25, 6, 10, 30, 13, 27, 21,
	17, 18, 8, 19, 24, 9, 12, 20,
}

// invLogTbl maps of powers of 2 to the numeric value of the element.
var invLogTbl = [31]fe{
	1, 2, 4, 8, 16, 9, 18, 13,
	26, 29, 19, 15, 30, 21, 3, 6,
	12, 24, 25, 27, 31, 23, 7, 14,
	28, 17, 11, 22, 5, 10, 20,
}

// charLowerTbl maps from numeric value to bech32 character.
var charsLowerTbl = [32]byte{
	'q', 'p', 'z', 'r', 'y', '9', 'x', '8', //  +0
	'g', 'f', '2', 't', 'v', 'd', 'w', '0', //  +8
	's', '3', 'j', 'n', '5', '4', 'k', 'h', // +16
	'c', 'e', '6', 'm', 'u', 'a', '7', 'l', // +24
}

// invCharsTbl maps from bech32 character (either case) to numeric value.
var invCharsTbl = [128]int8{
	-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
	15, -1, 10, 17, 21, 20, 26, 30, 7, 5, -1, -1, -1, -1, -1, -1,
	-1, 29, -1, 24, 13, 25, 9, 8, 23, -1, 18, 22, 31, 27, 19, -1,
	1, 0, 3, 16, 11, 28, 12, 14, 6, 4, 2, -1, -1, -1, -1, -1,
	-1, 29, -1, 24, 13, 25, 9, 8, 23, -1, 18, 22, 31, 27, 19, -1,
	1, 0, 3, 16, 11, 28, 12, 14, 6, 4, 2, -1, -1, -1, -1, -1,
}

// fe is an element of GF32.
type fe uint8

const (
	feQ fe = iota
	feP
	feZ
	feR
	feY
	fe9
	feX
	fe8
	feG
	feF
	fe2
	feT
	feV
	feD
	feW
	fe0
	feS
	fe3
	feJ
	feN
	fe5
	fe4
	feK
	feH
	feC
	feE
	fe6
	feM
	feU
	feA
	fe7
	feL
)

func (e fe) Add(e2 fe) fe {
	return e ^ e2
}

func (e fe) Sub(e2 fe) fe {
	// Subtraction is the same as addition in a char-2 field.
	return e.Add(e2)
}

func (e fe) Mul(e2 fe) fe {
	if e == 0 || e2 == 0 {
		return 0
	}
	log1 := uint16(logTbl[e])
	log2 := uint16(logTbl[e2])
	return invLogTbl[(log1+log2)%31]
}

func (e fe) Div(e2 fe) fe {
	if e == 0 {
		return 0
	}
	if e2 == 0 {
		panic("divide by zero")
	}
	log1 := uint16(logTbl[e])
	log2 := uint16(logTbl[e2])
	return invLogTbl[(31+log1-log2)%31]
}

// feFromInt converts an integer to a field element.
func feFromInt(i int) (fe, bool) {
	if i < 0 || 32 <= i {
		return 0, false
	}
	return fe(i), true
}

// feFromRune converts a bech32 character to a field element.
func feFromRune(c rune) (fe, bool) {
	if c < 0 || int(c) >= len(invCharsTbl) {
		return 0, false
	}
	e := invCharsTbl[c]
	if e == -1 {
		return 0, false
	}
	return fe(e), true
}

// rune converts the field element to a lowercase bech32 character.
func (e fe) rune() byte {
	// Indexing is fine as we have e in [0, 32) as an invariant.
	return charsLowerTbl[e]
}

func (e fe) String() string {
	return string(rune(e.rune()))
}
