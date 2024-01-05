// Package otp provides access to the one-time-programmable
// memory on the rp2350 microcontroller.
package otp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode"
	"unsafe"
)

const (
	NumBootKeySlots = 4

	// Predefined OTP rows.
	CHIPID0              = 0x000
	RANDID0              = 0x004
	BOOTKEY0_0           = 0x080
	CRIT1                = 0x040
	BOOT_FLAGS1          = 0x04b
	USB_BOOT_FLAGS       = 0x059
	USB_WHITE_LABEL_ADDR = 0x05c

	WHITE_LABEL_ADDR_VALID = 0b1 << 22

	// USB entries.
	INDEX_USB_DEVICE_VID_VALUE                   = 0x0000
	INDEX_USB_DEVICE_PID_VALUE                   = 0x0001
	INDEX_USB_DEVICE_BCD_DEVICE_VALUE            = 0x0002
	INDEX_USB_DEVICE_LANG_ID_VALUE               = 0x0003
	INDEX_USB_DEVICE_MANUFACTURER_STRDEF         = 0x0004
	INDEX_USB_DEVICE_PRODUCT_STRDEF              = 0x0005
	INDEX_USB_DEVICE_SERIAL_NUMBER_STRDEF        = 0x0006
	INDEX_USB_CONFIG_ATTRIBUTES_MAX_POWER_VALUES = 0x0007
	INDEX_VOLUME_LABEL_STRDEF                    = 0x0008
	INDEX_SCSI_INQUIRY_VENDOR_STRDEF             = 0x0009
	INDEX_SCSI_INQUIRY_PRODUCT_STRDEF            = 0x000a
	INDEX_SCSI_INQUIRY_VERSION_STRDEF            = 0x000b
	INDEX_INDEX_HTM_REDIRECT_URL_STRDEF          = 0x000c
	INDEX_INDEX_HTM_REDIRECT_NAME_STRDEF         = 0x000d
	INDEX_INFO_UF2_TXT_MODEL_STRDEF              = 0x000e
	INDEX_INFO_UF2_TXT_BOARD_ID_STRDEF           = 0x000f

	// Flags.
	_IS_WRITE = 0x1
	_IS_ECC   = 0x2

	// Return codes.
	_BOOTROM_OK                             = 0
	_BOOTROM_ERROR_NOT_PERMITTED            = -4
	_BOOTROM_ERROR_BAD_ALIGNMENT            = -11
	_BOOTROM_ERROR_UNSUPPORTED_MODIFICATION = -18

	FirstUserRow = 0x0c0
	LastUserRow  = 0xf3f
	numRows      = 4096
)

type bootromError struct {
	errCode int
}

func (b *bootromError) Error() string {
	switch b.errCode {
	case _BOOTROM_ERROR_NOT_PERMITTED:
		return "otp: not permitted"
	case _BOOTROM_ERROR_UNSUPPORTED_MODIFICATION:
		return "otp: unsupported modification"
	case _BOOTROM_ERROR_BAD_ALIGNMENT:
		return "otp: bad alignment"
	default:
		return fmt.Sprintf("otp: unknown error: %d", b.errCode)
	}
}

func readECC(buf []byte, row uint16) error {
	return otpAccess(buf, row, _IS_ECC)
}

func writeECC(buf []uint8, row uint16) error {
	return otpAccess(buf, row, _IS_ECC|_IS_WRITE)
}

func read(buf []byte, row uint16) error {
	return otpAccess(buf, row, 0)
}

func write(buf []uint8, row uint16) error {
	return otpAccess(buf, row, _IS_WRITE)
}

func EnableSecureBoot() error {
	return writeOrRow(CRIT1, 8, 0b1)
}

func IsSecureBootEnabled() (bool, error) {
	crit1, err := readOrRow(CRIT1, 8)
	return crit1&0b1 != 0, err
}

func AddBootKey(keyHash []byte) (int, error) {
	if len(keyHash) != 32 {
		return -1, errors.New("otp: invalid key hash length")
	}
	// Read valid and invalid key sets.
	bf1, err := readOrRow(BOOT_FLAGS1, 3)
	if err != nil {
		return -1, err
	}
	validKeys := bf1 & 0xf
	invalidKeys := (bf1 >> 8) & 0xf
	buf := make([]byte, len(keyHash))
	// Determine most fitting slot. Prefer an exact match,
	// then the longest unused partial match.
	keyIdx := -1
	length := 0
slots:
	for i := range NumBootKeySlots {
		if err := ReadBootKey(buf, i); err != nil {
			return -1, err
		}
		// Skip invalid slots.
		if invalidKeys&(0b1<<i) != 0 {
			continue
		}
		n := 0
		for i, b := range buf {
			khb := keyHash[i]
			if b&^khb != 0 {
				// Writing keyHash to this requires an
				// incompatible OTP flip of a 1 to a 0.
				continue slots
			}
			if khb == b {
				n++
			}
		}
		if valid := validKeys&(0b1<<i) != 0; valid {
			if n == len(keyHash) {
				// Key exists and is marked valid.
				return i, nil
			}
			// Skip occupied slots that doesn't contain the key.
			continue
		}
		if keyIdx == -1 || n > length {
			length = n
			keyIdx = i
		}
	}
	if keyIdx == -1 {
		return -1, errors.New("otp: no available key slot")
	}
	err = WriteBootKey(keyHash, keyIdx)
	return keyIdx, err
}

func WriteBootKey(keyHash []byte, slot int) error {
	if slot < 0 || NumBootKeySlots <= slot {
		return errors.New("otp: key slot out of range")
	}
	if len(keyHash) != 32 {
		return errors.New("otp: invalid key hash length")
	}
	if err := writeECC(keyHash, BOOTKEY0_0+uint16(slot*16)); err != nil {
		return err
	}
	return writeOrRow(BOOT_FLAGS1, 3, 0b1<<slot)
}

func WriteWhiteLabelAddr(row uint16) error {
	return writeECCRow(USB_WHITE_LABEL_ADDR, row)
}

func ReadWhiteLabelString(idx uint8) (string, error) {
	tblRow, err := readECCRow(USB_WHITE_LABEL_ADDR)
	if err != nil || tblRow == 0 {
		return "", err
	}
	flags, err := readOrRow(USB_BOOT_FLAGS, 3)
	if err != nil {
		return "", err
	}
	if flags&WHITE_LABEL_ADDR_VALID == 0 || flags|(0b1<<idx) == 0 {
		return "", nil
	}
	entry, err := readECCRow(tblRow + uint16(idx))
	if err != nil {
		return "", err
	}
	if utf16 := entry&0x80 != 0; utf16 {
		return "", errors.New("otp: UTF-16 label not supported")
	}
	n := entry & 0x7f
	row := entry >> 8
	buf := make([]byte, n)
	if err := readECC(buf, row+tblRow); err != nil {
		return "", err
	}
	return string(buf), nil
}

func WriteWhiteLabelString(idx uint8, s string) error {
	existing, err := ReadWhiteLabelString(idx)
	if err != nil || existing == s {
		return err
	}
	switch idx {
	case INDEX_USB_DEVICE_MANUFACTURER_STRDEF,
		INDEX_USB_DEVICE_PRODUCT_STRDEF,
		INDEX_USB_DEVICE_SERIAL_NUMBER_STRDEF,
		INDEX_VOLUME_LABEL_STRDEF,
		INDEX_SCSI_INQUIRY_VENDOR_STRDEF,
		INDEX_SCSI_INQUIRY_PRODUCT_STRDEF,
		INDEX_SCSI_INQUIRY_VERSION_STRDEF,
		INDEX_INDEX_HTM_REDIRECT_URL_STRDEF,
		INDEX_INDEX_HTM_REDIRECT_NAME_STRDEF,
		INDEX_INFO_UF2_TXT_MODEL_STRDEF,
		INDEX_INFO_UF2_TXT_BOARD_ID_STRDEF:
	default:
		return fmt.Errorf("invalid white-label index %d", idx)
	}
	if len(s) > 0x7f {
		return fmt.Errorf("otp: white label string %q too long", s)
	}
	len16 := uint16(len(s))
	// Some of the string fields support UTF-16 text, but we
	// don't.
	for _, r := range s {
		if r > unicode.MaxASCII {
			return fmt.Errorf("otp: white label string %q contains non-ascii character '%c'", s, r)
		}
	}
	tblRow, err := readECCRow(USB_WHITE_LABEL_ADDR)
	if err != nil {
		return err
	}
	if tblRow == 0 {
		return errors.New("otp: white label address is not set")
	}
	const whiteLabelTableSize = 16
	// Find space for the string, starting at the end of the
	// table.
	rowOffset := uint16(whiteLabelTableSize)
	for i := range whiteLabelTableSize {
		entry, err := readECCRow(tblRow + uint16(i))
		if err != nil {
			return err
		}
		n := entry & 0x7f
		if utf16 := entry&0x80 != 0; utf16 {
			switch idx {
			case INDEX_USB_DEVICE_PRODUCT_STRDEF,
				INDEX_USB_DEVICE_SERIAL_NUMBER_STRDEF,
				INDEX_USB_DEVICE_MANUFACTURER_STRDEF:
				n *= 2
			}
		}
		row := entry >> 8
		end := row + (n+1)/2
		rowOffset = max(rowOffset, end)
	}
	startRow := tblRow + rowOffset
	nrows := (len16 + 1) / 2
	if startRow < FirstUserRow || LastUserRow <= startRow+nrows {
		return errors.New("otp: no space for white label string")
	}
	entry := rowOffset<<8 | len16
	if err := writeECCRow(tblRow+uint16(idx), entry); err != nil {
		return err
	}
	if err := writeECC([]byte(s), startRow); err != nil {
		return err
	}
	return writeOrRow(USB_BOOT_FLAGS, 3, 0b1<<idx|WHITE_LABEL_ADDR_VALID)
}

func ReadBootKey(buf []byte, slot int) error {
	if slot < 0 || NumBootKeySlots <= slot {
		return errors.New("otp: key slot out of range")
	}
	if len(buf) != 32 {
		return errors.New("otp: invalid key hash length")
	}
	return readECC(buf, BOOTKEY0_0+uint16(slot*16))
}

func IsBootKeyValid(slot int) (bool, error) {
	bf1, err := readOrRow(BOOT_FLAGS1, 3)
	if err != nil {
		return false, err
	}
	bit := uint32(0b1 << slot)
	return bf1&bit != 0 && bf1&(bit<<8) == 0, nil
}

func readOrRow(row, redundancy uint16) (uint32, error) {
	var v uint32
	for i := range redundancy {
		rv, err := readRow(row + i)
		if err != nil {
			return 0, err
		}
		v |= rv
	}
	return v, nil
}

func writeOrRow(row, redundancy uint16, val uint32) error {
	for i := range redundancy {
		old, err := readRow(row + i)
		if err != nil {
			return err
		}
		if err := writeRow(row+i, old|val); err != nil {
			return err
		}
	}
	return nil
}

func writeRow(row uint16, val uint32) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], val)
	return write(buf[:], row)
}

func writeECCRow(row, val uint16) error {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], val)
	return writeECC(buf[:], row)
}

func readRow(row uint16) (uint32, error) {
	var buf [4]byte
	if err := read(buf[:4], row); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

func readECCRow(row uint16) (uint16, error) {
	var buf [2]byte
	if err := readECC(buf[:2], row); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(buf[:]), nil
}

func otpAccess(buf []byte, row uint16, flags int) error {
	var aligned []byte
	rowAndFlags := (uint32(flags) << 16) | uint32(row)
	if isECC := rowAndFlags&(_IS_ECC<<16) != 0; isECC {
		buf16 := make([]uint16, (len(buf)+1)/2)
		ptr := (*byte)(unsafe.Pointer(unsafe.SliceData(buf16)))
		aligned = unsafe.Slice(ptr, len(buf16)*2)
	} else {
		buf32 := make([]uint32, (len(buf)+3)/4)
		ptr := (*byte)(unsafe.Pointer(unsafe.SliceData(buf32)))
		aligned = unsafe.Slice(ptr, len(buf32)*4)
	}
	copy(aligned, buf)
	res := otp_access(unsafe.SliceData(aligned), uint32(len(aligned)), rowAndFlags)
	copy(buf, aligned)
	return toErr(int(res))
}

var otp_access func(buf *uint8, buf_len, row_and_flags uint32) int

func toErr(res int) error {
	if res == 0 {
		return nil
	}
	return &bootromError{res}
}
