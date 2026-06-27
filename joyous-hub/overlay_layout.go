package main

// Large-display overlay matches InkJoy typography; box size fits stacked content.
const (
	overlayFontLarge  = 56
	overlayFontMedium = 40
	overlayFontSmall  = 30
	overlayPadMin     = 24
	overlayLineStep   = 36
	overlayDateStep   = 64

	// Photo-name caption uses Caveat (album UI); scaled from InkJoy landscape width.
	overlayPhotoNameFontRefWidth  = 1600
	overlayPhotoNameFontSizeAtRef = 52
)

func overlayPadForWidth(w int) int {
	pad := w / 40
	if pad < overlayPadMin {
		pad = overlayPadMin
	}
	return pad
}

func overlayPhotoNameFontSize(frameWidth int) float64 {
	size := float64(frameWidth) * overlayPhotoNameFontSizeAtRef / overlayPhotoNameFontRefWidth
	if size < 36 {
		return 36
	}
	if size > 80 {
		return 80
	}
	return size
}

func overlayInt(v float64) int {
	if v < 1 {
		return 1
	}
	return int(v + 0.5)
}
