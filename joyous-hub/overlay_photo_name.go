package main

import (
	"image"
	"path/filepath"
	"strings"

	"golang.org/x/image/font"
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

// overlayPhotoNameOrigin computes the top-left point the photo-name caption
// actually starts drawing from, and the resolved font/size used to measure
// it. Shared by drawPhotoNameCaption and overlayWireLines (overlay_wire.go)
// for the same reason overlayWeatherOrigin is shared -- see its comment.
func overlayPhotoNameOrigin(text, position string, dw, dh int, boost float64) (x, y int, face font.Face, fontSize float64, ok bool) {
	if text == "" {
		return 0, 0, nil, 0, false
	}
	fontSize = overlayPhotoNameFontSize(dw, dh, boost)
	face = overlayCaveatFace(fontSize)
	if face == nil {
		face = overlayFacePt(overlayFontSmallPt, dw, dh, boost)
	}
	if face == nil {
		return 0, 0, nil, 0, false
	}
	marginX := overlayPadForDimension(dw, dw, dh, boost)
	marginY := overlayPadForDimension(dh, dw, dh, boost)
	textW := fontMeasureString(face, text)
	ascent := face.Metrics().Ascent.Ceil()
	descent := face.Metrics().Descent.Ceil()
	lineH := ascent + descent
	yTop := dh - marginY - lineH
	x = dw - marginX - textW
	if normalizePhotoNamePosition(position) == overlayPhotoNameBottomCenter {
		x = (dw - textW) / 2
	}
	return x, yTop, face, fontSize, true
}

func drawPhotoNameCaption(dst *image.RGBA, text, position string, dw, dh int, boost float64) {
	x, yTop, face, fontSize, ok := overlayPhotoNameOrigin(text, position, dw, dh, boost)
	if !ok {
		return
	}
	drawBorderedOverlayText(dst, text, x, yTop, face, fontSize)
}
