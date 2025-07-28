// package address derives recieve and change addresses from
// output descriptors.
package address

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"seedhammer.com/bip380"
)

func Change(desc *bip380.Descriptor, index uint32) (string, error) {
	return address(desc, index, true)
}

func Receive(desc *bip380.Descriptor, index uint32) (string, error) {
	return address(desc, index, false)
}

func Supported(desc *bip380.Descriptor) bool {
	_, err := Receive(desc, 0)
	return !errors.Is(err, errUnsupported)
}

var errUnsupported = errors.New("unsupported descriptor")

func address(desc *bip380.Descriptor, index uint32, change bool) (string, error) {
	var addr btcutil.Address
	var network *chaincfg.Params
	switch desc.Type {
	case bip380.SortedMulti:
		var keys []*btcutil.AddressPubKey
		for _, k := range desc.Keys {
			pub, err := derivePubKey(k, index, change)
			if err != nil {
				return "", fmt.Errorf("address: %w", err)
			}
			if network != nil && k.Network != network {
				return "", fmt.Errorf("address: multisig descriptor mixes networks: %w", errUnsupported)
			}
			network = k.Network
			addrPub, err := btcutil.NewAddressPubKey(pub.SerializeCompressed(), network)
			if err != nil {
				return "", fmt.Errorf("address: %w", err)
			}
			keys = append(keys, addrPub)
		}
		slices.SortFunc(keys, func(addr1, addr2 *btcutil.AddressPubKey) int {
			return bytes.Compare(addr1.PubKey().SerializeCompressed(), addr2.PubKey().SerializeCompressed())
		})
		script, err := txscript.MultiSigScript(keys, desc.Threshold)
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
		switch desc.Script {
		case bip380.P2SH:
			addr, err = btcutil.NewAddressScriptHash(script, network)
		case bip380.P2WSH, bip380.P2SH_P2WSH:
			hash := sha256.Sum256(script)
			addr, err = btcutil.NewAddressWitnessScriptHash(hash[:], network)
		default:
			return "", fmt.Errorf("address: multisig script: %s: %w", desc.Script, errUnsupported)
		}
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
	case bip380.Singlesig:
		k := desc.Keys[0]
		network = k.Network
		pub, err := derivePubKey(k, index, change)
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
		switch desc.Script {
		case bip380.P2PKH:
			pkHash := btcutil.Hash160(pub.SerializeCompressed())
			addr, err = btcutil.NewAddressPubKeyHash(pkHash, network)
		case bip380.P2WPKH, bip380.P2SH_P2WPKH:
			pkHash := btcutil.Hash160(pub.SerializeCompressed())
			addr, err = btcutil.NewAddressWitnessPubKeyHash(pkHash, network)
		case bip380.P2TR:
			tkey := txscript.ComputeTaprootKeyNoScript(pub)
			addr, err = btcutil.NewAddressTaproot(schnorr.SerializePubKey(tkey), network)
		default:
			return "", fmt.Errorf("address: singlesig script: %s: %w", desc.Script, errUnsupported)
		}
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
	default:
		return "", fmt.Errorf("address: descriptor: %w", errUnsupported)
	}
	// Derive wrapped address types.
	switch desc.Script {
	case bip380.P2SH_P2WPKH, bip380.P2SH_P2WSH:
		script, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
		addr, err = btcutil.NewAddressScriptHash(script, network)
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
	}
	return addr.String(), nil
}

func derivePubKey(k bip380.Key, index uint32, change bool) (*secp256k1.PublicKey, error) {
	children := k.Children
	if len(children) == 0 {
		// Default to <0;1>/*.
		children = append(children,
			bip380.Derivation{
				Type:  bip380.RangeDerivation,
				Index: 0,
				End:   1,
			},
			bip380.Derivation{
				Type: bip380.WildcardDerivation,
			},
		)
	}
	xpub := k.ExtendedKey()
	for _, c := range children {
		var id uint32
		switch c.Type {
		case bip380.ChildDerivation:
			id = c.Index
		case bip380.RangeDerivation:
			if c.End != c.Index+1 {
				return nil, errors.New("unsupported range path element")
			}
			id = c.Index
			if change {
				id = c.End
			}
		case bip380.WildcardDerivation:
			id = index
		default:
			return nil, errors.New("unsupported path element")
		}
		child, err := xpub.Derive(id)
		if err != nil {
			return nil, err
		}
		xpub = child
	}
	return xpub.ECPubKey()
}
