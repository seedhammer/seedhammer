package zbar

/*
#cgo CFLAGS: -DENABLE_QRCODE -DNO_STATS -Wno-shift-op-parentheses -Wno-format -Wno-format-security

#include "zbar.h"
#include "binarize.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"image"
	"unsafe"
)

// Scan is a raw wrapper around zbar's scan API. In particular, the backing store
// for the img argument must be allocated outside Go because it is retained across
// Cgo calls (but not after Scan completes).
func Scan(img *image.Gray) ([][]byte, error) {
	_img := img.Pix
	sz := img.Bounds().Size()
	stride := img.Stride
	// Bounds check.
	_img = _img[:stride*sz.Y]
	if len(_img) == 0 {
		return nil, nil
	}

	scanner := C.zbar_image_scanner_create()
	defer C.zbar_image_scanner_destroy(scanner)
	image := C.zbar_image_create()
	defer C.zbar_image_destroy(image)
	C.zbar_image_set_format(image, C.ulong(0x30303859)) // Y800 (grayscale)
	C.zbar_image_set_size(image, C.uint(stride), C.uint(sz.Y))
	C.zbar_image_set_data(image, unsafe.Pointer(&_img[0]), C.ulong(len(_img)), nil)
	C.zbar_image_set_crop(image, 0, 0, C.uint(sz.X), C.uint(sz.Y))

	if res := C.zbar_image_scanner_set_config(scanner, 0, C.ZBAR_CFG_ENABLE, 1); res != 0 {
		return nil, fmt.Errorf("zbar: set_config failed with error code %d", res)
	}
	if res := C.zbar_image_scanner_set_config(scanner, C.ZBAR_QRCODE, C.ZBAR_CFG_BINARY, 1); res != 0 {
		return nil, fmt.Errorf("zbar: set_config(CFG_BINARY) failed with error code %d", res)
	}
	if res := C.zbar_image_scanner_set_config(scanner, 0, C.ZBAR_CFG_TEST_INVERTED, 1); res != 0 {
		return nil, fmt.Errorf("zbar: set_config(ZBAR_CFG_TEST_INVERTED) failed with error code %d", res)
	}

	var results [][]byte
	switch status := C.zbar_scan_image(scanner, image); status {
	case 0:
		return nil, nil
	case -1:
		return nil, errors.New("zbar: scan failed")
	default:
		s := C.zbar_image_first_symbol(image)
		for s != nil {
			dataPtr := C.zbar_symbol_get_data(s)
			n := C.zbar_symbol_get_data_length(s)
			data := unsafe.Slice((*byte)(unsafe.Pointer(dataPtr)), n)
			res := make([]byte, n)
			copy(res, data)
			results = append(results, res)
			s = C.zbar_symbol_next(s)
		}
	}

	return results, nil
}
