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

func drawPhotoNameCaption(dst *image.RGBA, text, position string, dw, dh int) {
	if text == "" {
		return
	}
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	fontSize := overlayPhotoNameFontSize(dw, dh)
	face := overlayCaveatFace(fontSize)
	if face == nil {
		face = overlayFacePt(overlayFontSmallPt, dw, dh)
	}
	if face == nil {
		return
	}
	marginX := overlayPadForDimension(w, dw, dh)
	marginY := overlayPadForDimension(h, dw, dh)
	textW := fontMeasureString(face, text)
	ascent := face.Metrics().Ascent.Ceil()
	descent := face.Metrics().Descent.Ceil()
	lineH := ascent + descent
	yTop := b.Max.Y - marginY - lineH
	x := b.Max.X - marginX - textW
	if normalizePhotoNamePosition(position) == overlayPhotoNameBottomCenter {
		x = b.Min.X + (w-textW)/2
	}
	drawBorderedOverlayText(dst, text, x, yTop, face, fontSize)
}
