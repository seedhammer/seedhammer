// Package bip85 implements the [BIP85] specification
//
// [BIP85]: https://bips.dev/85/
package bip85

import (
	"crypto/hmac"
	"crypto/sha512"
)

const PathRoot = 83696968 + 0x80000000

const macKey = "bip-entropy-from-k"

// Entropy derives n bytes of entropy from a private key.
func Entropy(privkey []byte) []byte {
	if len(privkey) != 32 {
		panic("invalid key length")
	}
	mac := hmac.New(sha512.New, []byte(macKey))
	mac.Write(privkey)
	return mac.Sum(nil)
}
