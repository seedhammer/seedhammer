package picobin

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/hex"
	"io"
	"slices"
	"testing"
)

//go:embed testdata/signed.bin.gz
var signedGZ []byte

//go:embed testdata/hashed.bin.gz
var hashedGZ []byte

var (
	signedImage = mustGunzip(signedGZ)
	hashedImage = mustGunzip(hashedGZ)
)

func TestSignature(t *testing.T) {
	img := bytes.NewReader(signedImage)
	finfo, err := NewImage(img)
	if err != nil {
		t.Fatal(err)
	}
	pkey, sig, err := finfo.Signature()
	if err != nil {
		t.Fatal(err)
	}
	wantSig := unhex("28510e83c2ab21039c023c4c10967405d6efd7ec15bcd8ed92b92bd029a9da4721404e4bd1424bb2fbc1faf102dd63d1797f54be4c872c53c1a63a6ad4305281")
	wantPubKey := unhex("e4894ee23471084e88852dea63f6d8bad35ef6db802f0cf2946cfa67572fd49eb65f5ac02c35534bc45159783cd3a7403eea91e55f482e35e446a0e7089de6ff")
	if !slices.Equal(sig, wantSig) || !slices.Equal(pkey, wantPubKey) {
		t.Errorf("signature mismatch: got\npublic key: %x\nsignature %x\nexpected\npublic key %x\nsignature %x", pkey, sig, wantPubKey, wantSig)
	}
}

func TestSign(t *testing.T) {
	newKey := bytes.Repeat([]byte{0xde, 0xad}, 32)
	newSig := bytes.Repeat([]byte{0xbe, 0xef}, 32)
	for _, img := range [][]byte{signedImage, hashedImage} {
		img, err := NewImage(bytes.NewReader(img))
		if err != nil {
			t.Fatal(err)
		}
		resigned := new(bytes.Buffer)
		if err := img.Sign(resigned, newKey, newSig); err != nil {
			t.Fatal(err)
		}
		r := bytes.NewReader(resigned.Bytes())
		finfo, err := NewImage(r)
		if err != nil {
			t.Fatal(err)
		}
		pkey, sig, err := finfo.Signature()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(sig, newSig) || !slices.Equal(pkey, newKey) {
			t.Errorf("signature mismatch: got\npublic key: %x\nsignature %x\nexpected\npublic key %x\nsignature %x", pkey, sig, newKey, newSig)
		}
	}
}

func TestHashData(t *testing.T) {
	img := bytes.NewReader(signedImage)
	finfo, err := NewImage(img)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := finfo.HashData(img, 0x10000000)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := unhex("15cf016da39866e8d1c0dff1aaa29fd0429876f9b55a290c1fc6fce819783557")
	if !slices.Equal(hash[:], wantHash) {
		t.Errorf("hash mismatch: got\n%x\nexpected\n%x", hash, wantHash)
	}
}

func TestHash(t *testing.T) {
	img := bytes.NewReader(hashedImage)
	finfo, err := NewImage(img)
	if err != nil {
		t.Fatal(err)
	}
	h, err := finfo.Hash()
	if err != nil {
		t.Fatal(err)
	}
	wantHash := unhex("0ef02dfd453b87629fb168b35f76ad6095e40a50a7c2650cd09fa27424a92bd7")
	if !slices.Equal(h, wantHash) {
		t.Errorf("got hash %x, expected %x", h, wantHash)
	}
}

func unhex(h string) []byte {
	v, err := hex.DecodeString(h)
	if err != nil {
		panic(err)
	}
	return v
}

func mustGunzip(f []byte) []byte {
	r, err := gzip.NewReader(bytes.NewReader(f))
	if err != nil {
		panic(err)
	}
	d, err := io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	return d
}
