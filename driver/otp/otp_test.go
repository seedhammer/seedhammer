package otp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"runtime"
	"testing"
	"unsafe"
)

func TestReadWrite(t *testing.T) {
	resetOTP()
	v, err := hex.DecodeString("deadbeef")
	if err != nil {
		panic(err)
	}
	if err := writeECC(v, RANDID0); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(v))
	if err := readECC(got, RANDID0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(v, got) {
		t.Errorf("wrote %x, got %x", v, got)
	}
	// Test that impossible OTP writes are caught.
	v[0] = 0xdc
	if err := writeECC(v, RANDID0); err == nil {
		t.Fatal("impossible OTP write accepted")
	}
}

func TestWriteBootKey(t *testing.T) {
	resetOTP()
	keyHash := make([]byte, 32)
	_, err := io.ReadFull(rand.Reader, keyHash[:])
	if err != nil {
		t.Fatal(err)
	}
	// Ensure the first byte is 0x00 to be able to test
	// partially written keys.
	keyHash[0] = 0x00

	slot0Valid, err := IsBootKeyValid(0)
	if err != nil {
		t.Fatal(err)
	}
	if slot0Valid {
		t.Fatal("empty slot 0 was reported as valid")
	}
	slot, err := AddBootKey(keyHash)
	if err != nil {
		t.Fatal(err)
	}
	if slot != 0 {
		t.Fatalf("key written to slot %d, not 0", slot)
	}
	buf := make([]byte, 64)
	slot, err = AddBootKey(keyHash)
	if err != nil {
		t.Fatal(err)
	}
	if slot != 0 {
		t.Fatalf("key re-written to another slot %d, not 0", slot)
	}
	slot0Valid, err = IsBootKeyValid(0)
	if err != nil {
		t.Fatal(err)
	}
	if !slot0Valid {
		t.Fatal("slot 0 was reported as invalid")
	}
	// Mark the slot as invalid.
	if err := writeOrRow(BOOT_FLAGS1, 3, 0b1<<(8+slot)); err != nil {
		t.Fatal(err)
	}
	slot0Valid, err = IsBootKeyValid(0)
	if err != nil {
		t.Fatal(err)
	}
	if slot0Valid {
		t.Fatal("invalid marked slot 0 was reported as valid")
	}
	// Write it again.
	slot, err = AddBootKey(keyHash)
	if err != nil {
		t.Fatal(err)
	}
	if slot != 1 {
		t.Fatalf("key re-written to another slot %d, not 1", slot)
	}
	if err := readECC(buf[:64], BOOTKEY0_0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf[:32], keyHash) || !bytes.Equal(buf[32:], keyHash) {
		t.Fatalf("keys not written properly")
	}
	// Test that a different, but compatible key won't overwrite a key marked valid.
	keyHash[0] = 0x01
	slot, err = AddBootKey(keyHash)
	if err != nil {
		t.Fatal(err)
	}
	if slot != 2 {
		t.Fatalf("key overwrite valid slot %d", slot)
	}
	// Fill remaining slot 3 with a partially written key.
	const wantSlot = 3
	if err := writeECC(keyHash, BOOTKEY0_0+uint16(wantSlot*16)); err != nil {
		t.Fatal(err)
	}
	// An incompatible key must not fit.
	keyHash[0] = 0x02
	slot, err = AddBootKey(keyHash)
	if err == nil {
		t.Fatalf("key was successfully written to already filled slot %d", slot)
	}
	// A compatible key must fit.
	keyHash[0] = 0x03
	slot, err = AddBootKey(keyHash)
	if err != nil {
		t.Fatal(err)
	}
	if slot != wantSlot {
		t.Fatalf("key wasn't written to slot %d, not its partial matching slot %d", slot, wantSlot)
	}
}

func TestEnableSecureBoot(t *testing.T) {
	resetOTP()
	if err := EnableSecureBoot(); err != nil {
		t.Fatal(err)
	}
	enabled, err := IsSecureBootEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Fatal("failed to enable secure boot")
	}
}

func TestWhiteLabel(t *testing.T) {
	resetOTP()
	if err := WriteWhiteLabelString(INDEX_INFO_UF2_TXT_BOARD_ID_STRDEF, "test"); err == nil {
		t.Error("write succeeded before address initialization")
	}
	if err := WriteWhiteLabelAddr(FirstUserRow); err != nil {
		t.Fatal(err)
	}
	const want = "model"
	if err := WriteWhiteLabelString(INDEX_INFO_UF2_TXT_MODEL_STRDEF, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadWhiteLabelString(INDEX_INFO_UF2_TXT_MODEL_STRDEF)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("read label %q, expected %q", got, want)
	}
	// Rewrite should succeed.
	if err := WriteWhiteLabelString(INDEX_INFO_UF2_TXT_MODEL_STRDEF, want); err != nil {
		t.Error(err)
	}
	// But not to another value.
	if err := WriteWhiteLabelString(INDEX_INFO_UF2_TXT_MODEL_STRDEF, "anothermodel"); err == nil {
		t.Error("re-write succeeded")
	}
}

func resetOTP() {
	mem := make([]byte, numRows*3)
	otp_access = func(bufPtr *uint8, buf_len, row_and_flags uint32) int {
		isECC := row_and_flags&(_IS_ECC<<16) != 0
		// Pin the pointer just like C would, so the alignment can
		// be verified.
		var pinner runtime.Pinner
		pinner.Pin(bufPtr)
		defer pinner.Unpin()
		align := uintptr(4)
		if isECC {
			align = 2
		}
		if uintptr(unsafe.Pointer(bufPtr))%align != 0 {
			panic("unaligned access")
		}
		if uintptr(buf_len)%align != 0 {
			panic("unaligned length")
		}
		buf := unsafe.Slice(bufPtr, buf_len)
		startRow := int(row_and_flags & 0xffff)
		for i := range buf {
			row := i / 4
			off := i % 4
			if isECC {
				row = i / 2
				off = i % 2
			} else if off == 3 {
				// Rows are 24 bits wide.
				continue
			}
			idx := (startRow+row)*3 + off
			if row_and_flags&(_IS_WRITE<<16) != 0 {
				b := buf[i]
				if mem[idx]&^b != 0 {
					return _BOOTROM_ERROR_UNSUPPORTED_MODIFICATION
				}
				mem[idx] = b
			} else {
				buf[i] = mem[idx]
			}
		}
		return _BOOTROM_OK
	}
}
