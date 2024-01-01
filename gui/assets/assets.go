package assets

import (
	"embed"
	"image"
	"image/png"

	"seedhammer.com/image/ninepatch"
)

var (
	CircleFilled = mustLoad("circle-filled.png")
	Circle       = mustLoad("circle.png")

	ArrowLeft  = mustLoad("arrow-left.png")
	ArrowRight = mustLoad("arrow-right.png")

	Hammer = mustLoad("hammer.png")

	NavBtnPrimary   = mustLoad("nav-btn-primary.png")
	NavBtnSecondary = mustLoad("nav-btn-secondary.png")

	IconCheckmark = mustLoad("icon-checkmark.png")
	IconBack      = mustLoad("icon-back.png")
	IconFlip      = mustLoad("icon-flip.png")
	IconLeft      = mustLoad("icon-left.png")
	IconBackspace = mustLoad("icon-backspace.png")
	IconDot       = mustLoad("icon-dot.png")
	IconProgress  = mustLoad("icon-progress.png")
	IconEdit      = mustLoad("icon-edit.png")
	IconDiscard   = mustLoad("icon-discard.png")
	IconRight     = mustLoad("icon-right.png")
	IconInfo      = mustLoad("icon-info.png")
	IconHammer    = mustLoad("icon-hammer.png")

	SH01 = mustLoad("sh01.png")
	SH02 = mustLoad("sh02.png")
	SH03 = mustLoad("sh03.png")

	LogoSmall = mustLoad("logo-small.png")

	ProgressCircle = mustLoad("progress-circle.png")
	CameraCorners  = ninepatch.New(mustLoad("camera-corners.9.png"))

	ButtonFocused = ninepatch.New(mustLoad("button-focused.9.png"))

	Key          = ninepatch.New(mustLoad("key.9.png"))
	KeyActive    = ninepatch.New(mustLoad("key-active.9.png"))
	KeyBackspace = mustLoad("key-backspace.png")

	WarningBoxBg     = ninepatch.New(mustLoad("warning-box-bg.9.png"))
	WarningBoxBorder = ninepatch.New(mustLoad("warning-box-border.9.png"))
)

func mustLoad(name string) image.RGBA64Image {
	f, err := images.Open(name)
	if err != nil {
		panic(err)
	}
	img, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	return img.(image.RGBA64Image)
}

//go:embed *.png
var images embed.FS
