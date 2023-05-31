// package address derives recieve and change addresses from
// output descriptors.
package address

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"seedhammer.com/bc/urtypes"
)

func Change(net *chaincfg.Params, desc urtypes.OutputDescriptor, index uint32) (string, error) {
	return address(net, desc, index, true)
}

func Receive(net *chaincfg.Params, desc urtypes.OutputDescriptor, index uint32) (string, error) {
	return address(net, desc, index, false)
}

func address(net *chaincfg.Params, desc urtypes.OutputDescriptor, index uint32, change bool) (string, error) {
	var addr btcutil.Address
	switch desc.Type {
	case urtypes.Multi, urtypes.SortedMulti:
		var keys []*btcutil.AddressPubKey
		for _, k := range desc.Keys {
			pub, err := derivePubKey(net, k, index, change)
			if err != nil {
				return "", fmt.Errorf("address: %w", err)
			}
			addrPub, err := btcutil.NewAddressPubKey(pub.SerializeCompressed(), net)
			if err != nil {
				return "", fmt.Errorf("address: %w", err)
			}
			keys = append(keys, addrPub)
		}
		if desc.Type == urtypes.SortedMulti {
			sort.Slice(keys, func(i, j int) bool {
				return bytes.Compare(keys[i].PubKey().SerializeCompressed(), keys[j].PubKey().SerializeCompressed()) == -1
			})
		}
		script, err := txscript.MultiSigScript(keys, desc.Threshold)
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
		switch desc.Script {
		case urtypes.P2SH:
			addr, err = btcutil.NewAddressScriptHash(script, net)
		case urtypes.P2WSH, urtypes.P2SH_P2WSH:
			hash := sha256.Sum256(script)
			addr, err = btcutil.NewAddressWitnessScriptHash(hash[:], net)
		default:
			return "", fmt.Errorf("address: unsupported multisig script: %s", desc.Script)
		}
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
	case urtypes.Singlesig:
		pub, err := derivePubKey(net, desc.Keys[0], index, change)
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
		switch desc.Script {
		case urtypes.P2PKH:
			pkHash := btcutil.Hash160(pub.SerializeCompressed())
			addr, err = btcutil.NewAddressPubKeyHash(pkHash, net)
		case urtypes.P2WPKH, urtypes.P2SH_P2WPKH:
			pkHash := btcutil.Hash160(pub.SerializeCompressed())
			addr, err = btcutil.NewAddressWitnessPubKeyHash(pkHash, net)
		case urtypes.P2TR:
			tkey := txscript.ComputeTaprootKeyNoScript(pub)
			addr, err = btcutil.NewAddressTaproot(schnorr.SerializePubKey(tkey), net)
		default:
			return "", fmt.Errorf("address: unsupported singlesig script: %s", desc.Script)
		}
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
	default:
		return "", errors.New("address: unsupported descriptor")
	}
	// Derive wrapped address types.
	switch desc.Script {
	case urtypes.P2SH_P2WPKH, urtypes.P2SH_P2WSH:
		script, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return "", fmt.Errorf("address: %w", err)
		}
		addr, err = btcutil.NewAddressScriptHash(script, net)
	}
	return addr.String(), nil
}

func derivePubKey(net *chaincfg.Params, k urtypes.KeyDescriptor, index uint32, change bool) (*secp256k1.PublicKey, error) {
	children := k.Children
	if len(children) == 0 {
		// Default to <0;1>/*.
		children = append(children,
			urtypes.Derivation{
				Type:  urtypes.RangeDerivation,
				Index: 0,
				End:   1,
			},
			urtypes.Derivation{
				Type: urtypes.WildcardDerivation,
			},
		)
	}
	xpub := k.ExtendedKey()
	for _, c := range children {
		var id uint32
		switch c.Type {
		case urtypes.ChildDerivation:
			id = c.Index
		case urtypes.RangeDerivation:
			if c.End != c.Index+1 {
				return nil, errors.New("unsupported range path element")
			}
			id = c.Index
			if change {
				id = c.End
			}
		case urtypes.WildcardDerivation:
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
