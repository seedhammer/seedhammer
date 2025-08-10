// package bip39 represents and converts bitcoin bip39 mnemonic phrases.
package bip39

//go:generate go run gen.go

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

type Word int

type Mnemonic []Word

type Roll [5]int

const NumWords = Word(len(index))

const wordBits = 11

var ErrInvalidChecksum = errors.New("bip39: invalid checksum")

// DiceToWord converts a dice roll to its bip39 word index. It returns
// false if the roll doesn't have a word defined.
func DiceToWord(roll Roll) (Word, bool) {
	// Map 1-6 to 0-5; fail if out of bounds.
	for i, digit := range roll {
		digit--
		if digit < 0 || 5 < digit {
			return -1, false
		}
		// Last digit is a coin toss.
		if i == len(roll)-1 {
			if digit < 3 {
				digit = 0
			} else {
				digit = 1
			}
		}
		roll[i] = digit
	}
	const rowsPerSubcolumn = 5 * 16
	const rowsPerPage = 13 * 16
	const wordsPerPage = 2 * rowsPerPage
	page := roll[0]
	subcol := roll[len(roll)-1]
	row := 0
	exp := 1
	// Compute row from middle dice.
	for i := len(roll) - 1 - 1; i >= 1; i-- {
		row += roll[i] * exp
		exp *= 6
	}
	if row >= rowsPerPage {
		return -1, false
	}
	column := row / rowsPerSubcolumn
	word := column*2*rowsPerSubcolumn + row%rowsPerSubcolumn
	subrows := (rowsPerPage - column*rowsPerSubcolumn)
	if subrows > rowsPerSubcolumn {
		subrows = rowsPerSubcolumn
	}
	word += subrows * subcol
	word += page * 2 * rowsPerPage
	w := Word(word)
	if !w.valid() {
		return -1, false
	}
	return w, true
}

func LabelFor(w Word) string {
	if !w.valid() {
		return ""
	}
	start := index[w]
	end := uint16(len(words))
	if int(w+1) < len(index) {
		end = index[w+1]
	}
	return words[start:end]
}

func (w Word) valid() bool {
	return w >= 0 && int(w) < len(index)
}

func ClosestWord(word string) (Word, bool) {
	i := sort.Search(len(index), func(i int) bool {
		return LabelFor(Word(i)) >= word
	})
	if i == len(index) {
		return -1, false
	}
	match := LabelFor(Word(i))
	return Word(i), strings.HasPrefix(match, word)
}

// Valid reports whether the mnemonic checksum is correct.
func (m Mnemonic) Valid() bool {
	// Panics in splitMnemonic.
	if len(m)%3 != 0 {
		return false
	}
	ent, _ := splitMnemonic(m)
	last := m[len(m)-1]
	return ChecksumWord(ent) == last
}

// FixChecksum returns a copy of the mnemonic with a correct checksum.
// This method defeats the purpose of the bip39 checksum, so it should
// only be used for generating new mnemonics.
func (m Mnemonic) FixChecksum() Mnemonic {
	m2 := make(Mnemonic, len(m))
	copy(m2, m)
	ent, _ := splitMnemonic(m2)
	m2[len(m2)-1] = ChecksumWord(ent)
	return m2
}

// Entropy returns the entropy represented by the mnemonic. It
// panics if the mnemonic is invalid.
func (m Mnemonic) Entropy() []byte {
	if !m.Valid() {
		panic("invalid mnemonic")
	}
	ent, _ := splitMnemonic(m)
	return ent
}

func (m Mnemonic) String() string {
	s := new(strings.Builder)
	for _, w := range m {
		if s.Len() > 0 {
			s.WriteByte(' ')
		}
		s.WriteString(LabelFor(w))
	}
	return s.String()
}

func splitMnemonic(m Mnemonic) (entropy []byte, checksum byte) {
	ent := big.NewInt(0)
	shift11 := big.NewInt(1 << wordBits)
	for _, w := range m {
		ent.Mul(ent, shift11)
		ent.Or(ent, big.NewInt(int64(w)))
	}
	if len(m)%3 != 0 {
		panic("mnemonic length not divisible with 3")
	}
	checkBits := len(m) / 3
	check := big.NewInt(0).And(ent, big.NewInt(1<<checkBits-1)).Int64()
	ent.Div(ent, big.NewInt(1<<checkBits))
	// Pad entropy bytes because BIP39 checksum is sensitive to
	// leading zeros.
	entBits := len(m)*wordBits - checkBits
	entBytes := ent.Bytes()
	padding := bytes.Repeat([]byte{0}, entBits/8-len(entBytes))
	entBytes = append(padding, entBytes...)
	return entBytes, byte(check)
}

func checksum(entropy []byte) byte {
	h := sha256.New()
	h.Write(entropy)
	check := h.Sum(nil)[0]
	checkBits := len(entropy) / 4
	if checkBits > 8 {
		panic("entropy too long")
	}
	return check >> (8 - checkBits)
}

func ChecksumWord(entropy []byte) Word {
	checkBits := len(entropy) / 4
	last := entropy[len(entropy)-1]
	w := Word(last)<<checkBits | Word(checksum(entropy))
	return w % Word(len(index))
}

func MnemonicSeed(m Mnemonic, password string) []byte {
	var sentence strings.Builder
	for i, w := range m {
		sentence.WriteString(LabelFor(w))
		if i < len(m)-1 {
			sentence.WriteByte(' ')
		}
	}
	return pbkdf2.Key([]byte(sentence.String()), []byte("mnemonic"+password), 2048, 64, sha512.New)
}

func New(entropy []byte) Mnemonic {
	if len(entropy) < 16 || 32 < len(entropy) {
		panic("invalid entropy length")
	}
	if len(entropy)%4 != 0 {
		panic("odd entropy length")
	}
	ent := big.NewInt(0).SetBytes(entropy)
	check := checksum(entropy)
	// Shift entropy and append checksum bits.
	checkBits := len(entropy) / 4
	ent.Mul(ent, big.NewInt(1<<checkBits))
	ent.Or(ent, big.NewInt(int64(check)))
	shift11 := big.NewInt(1 << wordBits)
	mask := big.NewInt(0).Add(shift11, big.NewInt(-1))
	w := big.NewInt(0)
	m := make(Mnemonic, (len(entropy)*8+checkBits)/wordBits)
	for i := range m {
		w.And(ent, mask)
		ent.Div(ent, shift11)
		idx := w.Int64()
		m[len(m)-1-i] = Word(idx)
	}
	if !m.Valid() {
		panic("unreachable")
	}
	return m
}

func Parse(buf []byte) (Mnemonic, error) {
	var m Mnemonic
	for w := range bytes.SplitSeq(buf, []byte(" ")) {
		if len(m) == 24 {
			return nil, fmt.Errorf("bip39: parse: mnemonic too long")
		}
		closest, valid := ClosestWord(string(w))
		if !valid || len(w) < 3 ||
			!bytes.HasPrefix([]byte(LabelFor(closest)), w) {
			return nil, fmt.Errorf("bip39: parse: unknown word: %q", w)
		}
		m = append(m, closest)
	}
	if !m.Valid() {
		return nil, ErrInvalidChecksum
	}
	return m, nil
}

func ParseMnemonic(mnemonic string) (Mnemonic, error) {
	words := strings.Split(mnemonic, " ")
	m := make(Mnemonic, len(words))
	for i, w := range words {
		closest, valid := ClosestWord(w)
		if !valid || LabelFor(closest) != w {
			return nil, fmt.Errorf("bip39: unknown word: %q", w)
		}
		m[i] = closest
	}
	if !m.Valid() {
		return nil, ErrInvalidChecksum
	}
	return m, nil
}

func RandomWord() Word {
	var u16 [2]byte
	if _, err := rand.Read(u16[:]); err != nil {
		panic(err)
	}
	// Modulo reduction of a random number ok because the reduced
	// range (2^11) divides the full range (2^16). But be paranoid.
	const n = len(index)
	if math.MaxUint16%n != n-1 {
		panic("biased random distribution")
	}
	return Word(binary.BigEndian.Uint16(u16[:])) % Word(n)
}
