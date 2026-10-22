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

// overlayFontScale returns the base-point→pixel scale for a display of size
// (w,h), relative to the Samsung reference diagonal. boost (>1) enlarges the
// whole overlay for a given panel without moving its corner anchors (callers
// keep positioning on the real dimensions); boost<=0 is treated as 1.
func overlayFontScale(w, h int, boost float64) float64 {
	if w <= 0 || h <= 0 {
		return 1
	}
	if boost <= 0 {
		boost = 1
	}
	ref := math.Hypot(float64(overlayFontRefWidth), float64(overlayFontRefHeight))
	diag := math.Hypot(float64(w), float64(h))
	return boost * ref / diag
}

func overlayScaledFontPt(base float64, w, h int, boost float64) float64 {
	return base * overlayFontScale(w, h, boost)
}

func overlayScaledPx(base int, w, h int, boost float64) int {
	return overlayInt(float64(base) * overlayFontScale(w, h, boost))
}

func overlayPadForDimension(dim, w, h int, boost float64) int {
	pad := overlayScaledPx(dim/40, w, h, boost)
	min := overlayScaledPx(overlayPadMinPt, w, h, boost)
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

func overlayPhotoNameFontSize(w, h int, boost float64) float64 {
	return overlayScaledFontPt(overlayPhotoNamePt, w, h, boost)
}

func overlayInt(v float64) int {
	if v < 1 {
		return 1
	}
	return int(v + 0.5)
}

func overlayLineFontSizePx(index, w, h int, boost float64) int {
	switch index {
	case 0:
		return overlayScaledPx(overlayFontMediumPt, w, h, boost)
	case 1:
		return overlayScaledPx(overlayFontLargePt, w, h, boost)
	default:
		return overlayScaledPx(overlayFontMediumPt, w, h, boost)
	}
}

func overlayLineStepAfter(index, w, h int, boost float64) int {
	if index == 1 {
		return overlayScaledPx(overlayDateStepPt, w, h, boost)
	}
	return overlayScaledPx(overlayLineStepPt, w, h, boost)
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
