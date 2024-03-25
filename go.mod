module seedhammer.com

go 1.21

require (
	github.com/btcsuite/btcd v0.23.0
	github.com/btcsuite/btcd/btcec/v2 v2.1.3
	github.com/btcsuite/btcd/btcutil v1.1.3
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.3.0
	github.com/fxamacker/cbor/v2 v2.4.0
	github.com/kortschak/qr v0.3.0
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef
	github.com/tarm/serial v0.0.0-20180830185346-98f6abe2eb07
	golang.org/x/crypto v0.7.0
	golang.org/x/image v0.6.0
	golang.org/x/sys v0.6.0
	periph.io/x/conn/v3 v3.7.0
	periph.io/x/host/v3 v3.8.2
)

require (
	github.com/btcsuite/btcd/chaincfg/chainhash v1.0.1 // indirect
	github.com/btcsuite/btclog v0.0.0-20170628155309-84c8d2346e9f // indirect
	github.com/decred/dcrd/crypto/blake256 v1.0.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
)

replace github.com/kortschak/qr => github.com/seedhammer/kortschak-qr v0.0.0-20240113235555-375796488df0
