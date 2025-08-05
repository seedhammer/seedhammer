package codex32

import (
	"fmt"
	"slices"
	"unicode"
)

// An engine consumes one GF32 character at a time, and produces
// a residue modulo some generator.
type engine struct {
	_case charCase
	// generator polynomial, as a big-endian (highest powers
	// first) vector of coefficients.
	generator []fe
	residue   []fe
	target    []fe
}

type charCase int

const (
	noCase charCase = iota
	lowerCase
	upperCase
)

// newShortChecksum constructs an engine which computes the normal codex32 checksum.
func newShortChecksum() *engine {
	return &engine{
		generator: []fe{
			feE, feM, fe3, feG, feQ, feE,
			feE, feE, feL, feM, feC, feS,
			feS,
		},
		residue: []fe{
			feQ, feQ, feQ, feQ, feQ, feQ,
			feQ, feQ, feQ, feQ, feQ, feQ,
			feP,
		},
		target: []fe{
			feS, feE, feC, feR, feE, feT,
			feS, feH, feA, feR, feE, fe3,
			fe2,
		},
	}
}

// newLongChecksum produces an engine which computes the "long" codex32 checksum.
func newLongChecksum() *engine {
	return &engine{
		generator: []fe{
			fe0, fe2, feE, fe6, feF, feE,
			fe4, feX, feH, fe4, feX, fe9,
			feK, feY, feH,
		},
		residue: []fe{
			feQ, feQ, feQ, feQ, feQ, feQ,
			feQ, feQ, feQ, feQ, feQ, feQ,
			feQ, feQ, feP,
		},
		target: []fe{
			feS, feE, feC, feR, feE, feT,
			feS, feH, feA, feR, feE, fe3,
			fe2, feE, feX,
		},
	}
}

// isValid reports whether the residue matches the target value
// for the checksum.
func (e *engine) isValid() bool {
	return slices.Equal(e.residue, e.target)
}

// inputHRP initializes the checksum engine by loading an HRP into it.
func (e *engine) inputHRP(hrp string) error {
	for _, c := range hrp {
		if !e.setCase(c) {
			return errInvalidCase
		}
		elem, ok := feFromInt(int(unicode.ToLower(c) >> 5))
		if !ok {
			return errInvalidCharacter
		}
		e.inputFe(elem)
	}
	e.inputFe(feQ)
	for _, c := range hrp {
		elem, ok := feFromInt(int(unicode.ToLower(c) & 0x1f))
		if !ok {
			return fmt.Errorf("invalid character: %c", c)
		}
		e.inputFe(elem)
	}
	return nil
}

// inputChar adds a single character to the checksum engine.
func (e *engine) inputChar(c rune) error {
	if !e.setCase(c) {
		return errInvalidCase
	}
	elem, ok := feFromRune(c)
	if !ok {
		return errInvalidCharacter
	}
	e.inputFe(elem)
	return nil
}

// inputData adds an entire string to the engine, counting each character as a data character
// (not an HRP).
func (e *engine) inputData(s string) error {
	for _, c := range s {
		if err := e.inputChar(c); err != nil {
			return err
		}
	}
	return nil
}

// inputTarget adds the target residue to the end of the input string.
func (e *engine) inputTarget() {
	for _, u := range e.target {
		e.inputFe(u)
	}
}

// setCase sets the case according to c. It returns false if the case is inconsistent
// with the current case.
func (e *engine) setCase(c rune) bool {
	if c < 0 || unicode.MaxASCII < c {
		return false
	}
	if unicode.IsDigit(c) {
		return true
	}
	isLower := c == unicode.ToLower(c)
	switch {
	case e._case == lowerCase && isLower,
		e._case == upperCase && !isLower:
		return true
	case e._case == noCase:
		if isLower {
			e._case = lowerCase
		} else {
			e._case = upperCase
		}
		return true
	}
	return false
}

// inputFe adds a single gf32 element to the checksum engine.
func (e *engine) inputFe(elem fe) {
	n := len(e.residue)
	// Store current coefficient of x^{n-1}, which will become
	// x^n (and get reduced).
	xn := e.residue[0]
	// Shift x^0 through x^{n-1} up one, and set x^0 to the new input.
	for i := 1; i < n; i++ {
		e.residue[i-1] = e.residue[i]
	}
	e.residue[n-1] = elem
	// Then reduce x^n mod the generator.
	for i, r := range e.residue {
		e.residue[i] = r.Add(e.generator[i].Mul(xn))
	}
}
