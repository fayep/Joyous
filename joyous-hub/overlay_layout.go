package main

import (
	"image"
	"math"
)

// Overlay typography is tuned on Samsung landscape (2560×1440). Smaller displays
// (InkJoy 1600×1200) scale up proportionally by diagonal so physical size stays consistent.
const (
	overlayFontRefWidth  = 2560
	overlayFontRefHeight = 1440
	overlayFontLargePt   = 56
	overlayFontMediumPt  = 40
	overlayFontSmallPt   = 30
	overlayPadMinPt      = 24
	overlayLineStepPt    = 36
	overlayDateStepPt    = 64
	overlayPhotoNamePt   = 52
)

func overlayFontScale(w, h int) float64 {
	if w <= 0 || h <= 0 {
		return 1
	}
	ref := math.Hypot(float64(overlayFontRefWidth), float64(overlayFontRefHeight))
	diag := math.Hypot(float64(w), float64(h))
	return ref / diag
}

func overlayScaledFontPt(base float64, w, h int) float64 {
	return base * overlayFontScale(w, h)
}

func overlayScaledPx(base int, w, h int) int {
	return overlayInt(float64(base) * overlayFontScale(w, h))
}

func overlayPadForDimension(dim, w, h int) int {
	pad := overlayScaledPx(dim/40, w, h)
	min := overlayScaledPx(overlayPadMinPt, w, h)
	if pad < min {
		pad = min
	}
	return pad
}

func overlayMetricsDimensions(width, height int, portrait bool) (w, h int) {
	w, h = width, height
	if portrait {
		w, h = h, w
	}
	return w, h
}

func overlayPhotoNameFontSize(w, h int) float64 {
	return overlayScaledFontPt(overlayPhotoNamePt, w, h)
}

func overlayInt(v float64) int {
	if v < 1 {
		return 1
	}
	return int(v + 0.5)
}

func overlayLineFontSizePx(index, w, h int) int {
	switch index {
	case 0:
		return overlayScaledPx(overlayFontMediumPt, w, h)
	case 1:
		return overlayScaledPx(overlayFontLargePt, w, h)
	default:
		return overlayScaledPx(overlayFontMediumPt, w, h)
	}
}

func overlayLineStepAfter(index, w, h int) int {
	if index == 1 {
		return overlayScaledPx(overlayDateStepPt, w, h)
	}
	return overlayScaledPx(overlayLineStepPt, w, h)
}

func overlayDrawDimensions(src image.Image, portrait bool) (w, h int) {
	if src == nil {
		return overlayFontRefWidth, overlayFontRefHeight
	}
	b := src.Bounds()
	w, h = b.Dx(), b.Dy()
	if portrait {
		w, h = h, w
	}
	return w, h
}
