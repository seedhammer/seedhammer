package image

import "image"

// Crop returns the bounds of the smallest rectangle
// that contains all the image's pixels where the its
// alpha is non-zero.
func Crop(img image.RGBA64Image) image.Rectangle {
	r := img.Bounds()
	emptyCol := func(x int) bool {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			if img.RGBA64At(x, y).A != 0 {
				return false
			}
		}
		return true
	}
	emptyRow := func(y int) bool {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.RGBA64At(x, y).A != 0 {
				return false
			}
		}
		return true
	}
	// Crop left side.
	for x := r.Min.X; x < r.Max.X; x++ {
		if !emptyCol(x) {
			break
		}
		r.Min.X++
	}
	// Crop right side.
	for x := r.Max.X - 1; x >= r.Min.X; x-- {
		if !emptyCol(x) {
			break
		}
		r.Max.X--
	}
	// Crop top side.
	for y := r.Min.Y; y < r.Max.Y; y++ {
		if !emptyRow(y) {
			break
		}
		r.Min.Y++
	}
	// Crop bottom side.
	for y := r.Max.Y - 1; y >= r.Min.Y; y-- {
		if !emptyRow(y) {
			break
		}
		r.Max.Y--
	}
	return r
}
