package main

import (
	"image"
	"path/filepath"
	"strings"
)

const (
	overlayPhotoNameBottomRight  = "bottom_right"
	overlayPhotoNameBottomCenter = "bottom_center"
)

func normalizePhotoNamePosition(p string) string {
	switch strings.TrimSpace(p) {
	case overlayPhotoNameBottomCenter:
		return overlayPhotoNameBottomCenter
	default:
		return overlayPhotoNameBottomRight
	}
}

func overlayPhotoNameFromFilename(name string) string {
	base := strings.TrimSpace(filepath.Base(name))
	if base == "" || base == "." {
		return ""
	}
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return strings.TrimSpace(base)
}

func overlayNeedsWeather(cfg OverlayConfig) bool {
	if t := strings.TrimSpace(cfg.Template); t != "" {
		return true
	}
	return cfg.ShowCity || cfg.ShowDate || cfg.ShowTemp || cfg.ShowCondition
}

func drawPhotoNameCaption(dst *image.RGBA, text, position string) {
	if text == "" {
		return
	}
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	face := overlayCaveatFace(overlayPhotoNameFontSize(w))
	if face == nil {
		face = overlayFace(overlayFontSmall)
	}
	if face == nil {
		return
	}
	margin := overlayPadForWidth(w)
	if marginY := overlayPadForWidth(h); marginY < overlayPadMin {
		if margin < marginY {
			margin = marginY
		}
	}
	textW := fontMeasureString(face, text)
	ascent := face.Metrics().Ascent.Ceil()
	descent := face.Metrics().Descent.Ceil()
	lineH := ascent + descent
	yTop := b.Max.Y - margin - lineH
	x := b.Max.X - margin - textW
	if normalizePhotoNamePosition(position) == overlayPhotoNameBottomCenter {
		x = b.Min.X + (w-textW)/2
	}
	drawBorderedOverlayText(dst, text, x, yTop, face, overlayPhotoNameFontSize(w))
}
