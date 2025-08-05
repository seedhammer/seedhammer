// package codex32 is an implementation of the [codex32] scheme.
// [BIP-93] describes the scheme in detail.
//
// [codex32]: https://secretcodex32.com/
// [BIP-93]: https://bips.dev/93/
package codex32

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// String is a codex32 string, containing a valid checksum.
type String struct {
	s string
}

// Alphabet is the bech32 alphabet.
const Alphabet = "QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L"

var (
	errInvalidChecksum     = errors.New("invalid checksum")
	errInvalidLength       = errors.New("invalid length")
	errIncompleteGroup     = errors.New("incomplete group")
	errInvalidShareIndex   = errors.New("invalid share index")
	errInvalidThreshold    = errors.New("invalid threshold")
	errInvalidCase         = errors.New("invalid case")
	errInvalidCharacter    = errors.New("invalid character")
	errInsufficientShares  = errors.New("insufficient shares")
	errMismatchedLength    = errors.New("mismatched length")
	errMismatchedID        = errors.New("mismatched id")
	errMismatchedHRP       = errors.New("mismatched hrp")
	errMismatchedThreshold = errors.New("mismatched threshold")
	errInvalidIDLength     = errors.New("invalid id length")
	errRepeatedIndex       = errors.New("repeated index")
)

const (
	shortCodeMinLength = 48
	shortCodeMaxLength = 93
	longCodeMinLength  = 125
	longCodeMaxLength  = 127
	shortChecksumLen   = 13
	longChecksumLen    = 15
)

func (s String) sanityCheck() error {
	parts, err := partsInner(s)
	if err != nil {
		return err
	}
	incompleteGroup := (len(parts.payload) * 5) % 8
	if incompleteGroup > 4 {
		return errIncompleteGroup
	}
	return nil
}

// fromUnchecksummed constructs a codex32 string from a not-yet-checksummed string.
func fromUnchecksummed(s string) (String, error) {
	// Determine what checksum to use and extend the string.
	var clen int
	var check *engine
	switch {
	case len(s) <= shortCodeMaxLength-shortChecksumLen:
		clen, check = shortChecksumLen, newShortChecksum()
	case len(s) <= longCodeMaxLength-longChecksumLen:
		clen, check = longChecksumLen, newLongChecksum()
	default:
		return String{}, fmt.Errorf("codex32: %w", errInvalidLength)
	}

	r := new(strings.Builder)
	r.Grow(clen)
	r.WriteString(s)
	hrp, res := splitHRP(s)
	// Compute the checksum.
	if err := check.inputHRP(hrp); err != nil {
		return String{}, fmt.Errorf("codex32: %w", err)
	}
	if err := check.inputData(res); err != nil {
		return String{}, fmt.Errorf("codex32: %w", err)
	}
	for _, c := range check.residue {
		r.WriteByte(c.rune())
	}

	ret := String{r.String()}
	if err := ret.sanityCheck(); err != nil {
		return String{}, fmt.Errorf("codex32: %w", err)
	}
	return ret, nil
}

// New constructs a codex32 string from a string.
func New(s string) (String, error) {
	var check *engine
	switch {
	case len(s) >= shortCodeMinLength && len(s) <= shortCodeMaxLength:
		check = newShortChecksum()
	case len(s) >= longCodeMinLength && len(s) <= longCodeMaxLength:
		check = newLongChecksum()
	default:
		return String{}, fmt.Errorf("codex32: %w", errInvalidLength)
	}

	hrp, res := splitHRP(s)
	if err := check.inputHRP(hrp); err != nil {
		return String{}, fmt.Errorf("codex32: %w", err)
	}
	if err := check.inputData(res); err != nil {
		return String{}, fmt.Errorf("codex32: %w", err)
	}
	if !check.isValid() {
		return String{}, fmt.Errorf("codex32: %w", errInvalidChecksum)
	}
	ret := String{s}
	if err := ret.sanityCheck(); err != nil {
		return String{}, fmt.Errorf("codex32: %w", err)
	}
	return ret, nil
}

// partsInner breaks the string up into its constituent parts.
func partsInner(s String) (*parts, error) {
	hrp, res := splitHRP(string(s.s))
	checkLen := longChecksumLen
	if len(s.s) <= shortCodeMaxLength {
		checkLen = shortChecksumLen
	}
	var thres int
	switch t := res[0]; t {
	case '0':
		thres = 0
	case '2':
		thres = 2
	case '3':
		thres = 3
	case '4':
		thres = 4
	case '5':
		thres = 5
	case '6':
		thres = 6
	case '7':
		thres = 7
	case '8':
		thres = 8
	case '9':
		thres = 9
	default:
		return nil, errInvalidThreshold
	}
	si := rune(res[5])
	shareIdx, ok := feFromRune(si)
	if !ok {
		panic("unreacable")
	}
	ret := &parts{
		hrp:       hrp,
		threshold: thres,
		id:        res[1:5],
		shareIdx:  shareIdx,
		payload:   string(res[6 : len(res)-checkLen]),
		checksum:  string(res[len(res)-checkLen:]),
	}
	if ret.threshold == 0 && ret.shareIdx != feS {
		return nil, errInvalidShareIndex
	}
	return ret, nil
}

// parts break the string up into its constituent parts.
func (s String) parts() *parts {
	p, err := partsInner(s)
	if err != nil {
		// OK since we validated the input on parse.
		panic("unreachable")
	}
	return p
}

// Interpolate a set of shares to derive a share at a specific index.
//
// Using the index 'S' will recover the master seed.
func Interpolate(shares []String, index rune) (String, error) {
	// Collect indices and sanity check.
	if len(shares) == 0 {
		return String{}, errInsufficientShares
	}
	target, ok := feFromRune(index)
	if !ok {
		return String{}, errInvalidShareIndex
	}
	indices := make([]fe, len(shares))
	s0Parts := shares[0].parts()
	for i, share := range shares {
		parts := share.parts()
		if len(shares[0].s) != len(share.s) {
			return String{}, errMismatchedLength
		}
		if s0Parts.hrp != parts.hrp {
			return String{}, errMismatchedHRP
		}
		if s0Parts.threshold != parts.threshold {
			return String{}, errMismatchedThreshold
		}
		if s0Parts.id != parts.id {
			return String{}, errMismatchedID
		}
		indices[i] = parts.shareIdx
	}

	// Do lagrange interpolation.
	mult := feP
	for i, idx := range indices {
		if idx == target {
			// If we're trying to output an input share, just output it directly.
			// Naive Lagrange multiplication would otherwise multiply by 0.
			return shares[i], nil
		}

		mult = mult.Mul(idx.Add(target))
	}

	if s0Parts.threshold > len(shares) {
		return String{}, errInsufficientShares
	}
	payloadLen := 6 + len(s0Parts.payload) + len(s0Parts.checksum)
	hrpLen := len(shares[0].s) - payloadLen
	result := make([]fe, payloadLen)

	for i, idxi := range indices {
		inv := feP
		for j, idxj := range indices {
			m := target
			if i != j {
				// If there is a repeated index, just call this an error. Technically
				// speaking, we could reject the other one and re-do the threshold
				// check in case we had enough unique ones .. but easier to just make
				// it the user's responsibility to provide unique indices to begin with.
				if idxi == idxj {
					return String{}, errRepeatedIndex
				}
				m = idxi
			}
			inv = inv.Mul(idxj.Add(m))
		}

		for j, r := range result {
			chAtI, ok := feFromRune(rune(shares[i].s[hrpLen+j]))
			if !ok {
				panic("unreachable")
			}
			result[j] = r.Add(mult.Div(inv).Mul(chAtI))
		}
	}

	s := new(strings.Builder)
	s.WriteString(s0Parts.hrp)
	s.WriteByte('1')
	isUpper := true
	for _, c := range s0Parts.hrp {
		isUpper = isUpper && unicode.IsUpper(c)
	}
	for _, e := range result {
		c := e.rune()
		if isUpper {
			c = byte(unicode.ToUpper(rune(c)))
		}
		s.WriteByte(c)
	}
	return String{s.String()}, nil
}

// NewSeed creates a share from secret data. The share index 'S' denotes an unshared secret.
func NewSeed(hrp string, threshold int, id string, shareIdx rune, data []byte) (String, error) {
	if len(id) != 4 {
		return String{}, errInvalidIDLength
	}
	si, ok := feFromRune(shareIdx)
	if !ok {
		return String{}, errInvalidShareIndex
	}

	payloadLen := (len(data)*8 + 4) / 5
	ret := new(strings.Builder)
	ret.Grow(len(hrp) + 6 + payloadLen)
	ret.WriteString(hrp)
	ret.WriteByte('1')
	var k fe
	switch threshold {
	case 0:
		k = fe0
	case 2:
		k = fe2
	case 3:
		k = fe3
	case 4:
		k = fe4
	case 5:
		k = fe5
	case 6:
		k = fe6
	case 7:
		k = fe7
	case 8:
		k = fe8
	case 9:
		k = fe9
	default:
		return String{}, errInvalidThreshold
	}
	// FIXME correct case to match HRP.
	ret.WriteByte(k.rune())
	ret.WriteString(id)
	ret.WriteByte(si.rune())

	// Convert byte data to base 32.
	nextU5 := byte(0)
	rem := 0
	for _, b := range data {
		// Each byte provides at least one u5. Push that.
		u5 := (nextU5 << (5 - rem)) | b>>(3+rem)
		e, ok := feFromInt(int(u5))
		if !ok {
			panic("unreachable")
		}
		ret.WriteByte(e.rune())
		nextU5 = b & ((1 << (3 + rem)) - 1)
		// If there were 2 or more bits from the last iteration, then
		// this iteration will push *two* u5s.
		if rem >= 2 {
			e, ok := feFromInt(int(nextU5 >> (rem - 2)))
			if !ok {
				panic("unreachable")
			}
			ret.WriteByte(e.rune())
			nextU5 &= (1 << (rem - 2)) - 1
		}
		rem = (rem + 8) % 5
	}
	if rem > 0 {
		e, ok := feFromInt(int(nextU5 << (5 - rem)))
		if !ok {
			panic("unreachable")
		}
		ret.WriteByte(e.rune())
	}

	// Initialize checksum engine with HRP and header.
	var check *engine
	if payloadLen <= shortCodeMaxLength-shortChecksumLen {
		check = newShortChecksum()
	} else {
		check = newLongChecksum()
	}
	if err := check.inputHRP(hrp); err != nil {
		return String{}, err
	}
	payload := ret.String()
	if err := check.inputData(payload[len(hrp)+1:]); err != nil {
		return String{}, err
	}
	// Now, to compute the checksum, we stick the target residue onto the end
	// of the input string, then take the resulting residue as the checksum.
	check.inputTarget()
	for _, e := range check.residue {
		ret.WriteByte(e.rune())
	}

	payload = ret.String()
	check = newShortChecksum()
	if err := check.inputHRP(hrp); err != nil {
		return String{}, err
	}
	if err := check.inputData(payload[len(hrp)+1:]); err != nil {
		return String{}, err
	}
	return String{payload}, nil
}

// Seed extracts the seed.
func (s String) Seed() []byte {
	return s.parts().data()
}

func (s String) String() string {
	return s.s
}

func (s String) Split() (id string, threshold int, idx rune) {
	p := s.parts()
	t := p.threshold
	if t == 0 {
		t = 1
	}
	return p.id, t, rune(p.shareIdx.rune())
}

// parts is a codex32 string, split into its constituent parts.
type parts struct {
	hrp       string
	threshold int
	id        string
	shareIdx  fe
	payload   string
	checksum  string
}

// data extract the binary data from a checksummed string.
//
// If the string does not have a multiple-of-8 number of bits, right-pad the
// final byte with 0s.
func (p *parts) data() []byte {
	ret := make([]byte, 0, (len(p.payload)*5+7)/8)

	nextByte := byte(0)
	rem := 0
	for _, c := range p.payload {
		e, ok := feFromRune(c)
		// ok since string is valid bech32.
		if !ok {
			panic("unreachable")
		}
		switch {
		case rem < 3:
			// If we are within 3 bits of the start we can fit the whole next char in
			nextByte |= byte(e) << (3 - rem)
		case rem == 3:
			// If we are exactly 3 bits from the start then this char fills in the byte
			ret = append(ret, nextByte|byte(e))
			nextByte = 0
		default:
			// Otherwise we have to break it in two.
			overshoot := rem - 3
			if overshoot <= 0 {
				panic("assert")
			}
			ret = append(ret, nextByte|(byte(e)>>overshoot))
			nextByte = byte(e) << (8 - overshoot)
		}
		rem = (rem + 5) % 8
	}
	if rem > 4 {
		panic("assert")
	}
	return ret
}

func splitHRP(s string) (string, string) {
	p1, p2, ok := strings.Cut(s, "1")
	if !ok {
		return "", p1
	}
	return p1, p2
}
