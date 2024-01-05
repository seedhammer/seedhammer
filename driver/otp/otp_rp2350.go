//go:build tinygo && rp2350

package otp

/*
typedef unsigned char uint8_t;
typedef unsigned short uint16_t;
typedef unsigned long uint32_t;
typedef unsigned long size_t;
typedef unsigned long uintptr_t;
typedef long int intptr_t;

#define RT_FLAG_FUNC_ARM_SEC    0x0004
#define RT_FLAG_FUNC_ARM_NONSEC 0x0010

#define BOOTROM_FUNC_TABLE_OFFSET 0x14

#define BOOTROM_WELL_KNOWN_PTR_SIZE 2

#define BOOTROM_VTABLE_OFFSET 0x00
#define BOOTROM_TABLE_LOOKUP_OFFSET     (BOOTROM_FUNC_TABLE_OFFSET + BOOTROM_WELL_KNOWN_PTR_SIZE)

#define ROM_TABLE_CODE(c1, c2) ((c1) | ((c2) << 8))

#define ROM_FUNC_OTP_ACCESS                     ROM_TABLE_CODE('O', 'A')

typedef void *(*rom_table_lookup_fn)(uint32_t code, uint32_t mask);
typedef int (*otp_access_fn)(uint8_t *buf, uint32_t buf_len, uint32_t row_and_flags);

static int *rom_func_lookup(uint32_t code) {
    rom_table_lookup_fn rom_table_lookup = (rom_table_lookup_fn)(uintptr_t)*(uint16_t*)(BOOTROM_TABLE_LOOKUP_OFFSET);
    return rom_table_lookup(code, RT_FLAG_FUNC_ARM_SEC);
}

int otp_access(uint8_t *buf, uint32_t buf_len, uint32_t row_and_flags) {
    otp_access_fn f = (otp_access_fn)rom_func_lookup(ROM_FUNC_OTP_ACCESS);
    return f(buf, buf_len, row_and_flags);
}
*/
import "C"

func init() {
	otp_access = func(buf *uint8, buf_len, row_and_flags uint32) int {
		return int(C.otp_access(buf, C.uint32_t(buf_len), C.uint32_t(row_and_flags)))
	}
}
