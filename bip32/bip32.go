// package bip32 contains helper functions for operating on bitcoin bip32
// extended keys.
package bip32

import (
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"seedhammer.com/bc/urtypes"
)

func Derive(mk *hdkeychain.ExtendedKey, path urtypes.Path) (mfp uint32, xpub *hdkeychain.ExtendedKey, err error) {
	key := mk
	for i, p := range path {
		key, err = key.Derive(p)
		if err != nil {
			return
		}
		if i == 0 {
			mfp = key.ParentFingerprint()
		}
	}
	xpub, err = key.Neuter()
	return
}
