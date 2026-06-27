package main

// Large-display overlay matches InkJoy typography; box size fits stacked content.
const (
	overlayFontLarge  = 56
	overlayFontMedium = 40
	overlayFontSmall  = 30
	overlayPadMin     = 24
	overlayLineStep   = 36
	overlayDateStep   = 64
)

func overlayPadForWidth(w int) int {
	pad := w / 40
	if pad < overlayPadMin {
		pad = overlayPadMin
	}
	return pad
}

func overlayInt(v float64) int {
	if v < 1 {
		return 1
	}
	return int(v + 0.5)
}
