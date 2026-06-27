package main

import (
	"image"
	"image/color"
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
	col := overlayContrastingColor(dst, x, yTop, textW, lineH)
	fontSize := overlayPhotoNameFontSize(w)
	drawOutlinedOverlayText(dst, text, x, yTop, face, col, overlayOutlineColor(col), fontSize)
}

func overlayContrastingColor(dst *image.RGBA, x0, y0, w, h int) color.Color {
	b := dst.Bounds()
	if w <= 0 || h <= 0 {
		return color.RGBA{255, 255, 255, 255}
	}
	var sum float64
	var n int
	for y := y0; y < y0+h; y += 2 {
		for x := x0; x < x0+w; x += 2 {
			if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
				continue
			}
			i := dst.PixOffset(x, y)
			r := float64(dst.Pix[i])
			g := float64(dst.Pix[i+1])
			bv := float64(dst.Pix[i+2])
			sum += 0.299*r + 0.587*g + 0.114*bv
			n++
		}
	}
	if n == 0 {
		return color.RGBA{255, 255, 255, 255}
	}
	if sum/float64(n) >= 128 {
		return color.RGBA{0, 0, 0, 255}
	}
	return color.RGBA{255, 255, 255, 255}
}
