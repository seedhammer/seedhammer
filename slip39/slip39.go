// package slip39 implements the [SLIP-39] specification.
//
// [SLIP39]: https://github.com/satoshilabs/slips/blob/master/slip-0039.md
package slip39

import (
	"sort"
	"strings"
)

//go:generate go run seedhammer.com/cmd/wordlist -pkg slip39

type Word int

type Mnemonic []Word

const NumWords = Word(len(index))

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
