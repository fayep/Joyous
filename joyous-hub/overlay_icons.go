package main

import (
	"image"
	"image/color"
	"strings"

	"golang.org/x/image/font"
)

const overlayIconRainRune = '\uE000'

// overlayRainGlyph is the template value for {{.Rain}}; the renderer draws strokes for it.
func overlayRainGlyph() string {
	return string(overlayIconRainRune)
}

type overlayLineSegment struct {
	text string
	rain bool
}

func overlayLineSegments(s string) []overlayLineSegment {
	if s == "" {
		return nil
	}
	var segs []overlayLineSegment
	var buf strings.Builder
	flushText := func() {
		if buf.Len() == 0 {
			return
		}
		segs = append(segs, overlayLineSegment{text: buf.String()})
		buf.Reset()
	}
	for _, r := range s {
		if r == overlayIconRainRune {
			flushText()
			segs = append(segs, overlayLineSegment{rain: true})
			continue
		}
		buf.WriteRune(r)
	}
	flushText()
	return segs
}

func overlayIconSize(fontPx int) int {
	if fontPx < 14 {
		return 14
	}
	return fontPx
}

func overlayIconGap(fontPx int) int {
	gap := fontPx / 6
	if gap < 2 {
		return 2
	}
	return gap
}

func overlayLineContentWidth(ln overlayLine) int {
	if ln.text == "" {
		return 0
	}
	w := 0
	iconW := overlayIconSize(ln.fontPx)
	gap := overlayIconGap(ln.fontPx)
	for _, seg := range overlayLineSegments(ln.text) {
		if seg.rain {
			w += iconW + gap
			continue
		}
		if ln.face != nil && seg.text != "" {
			w += font.MeasureString(ln.face, seg.text).Ceil()
		}
	}
	if w > gap {
		w -= gap
	}
	return w
}

func drawOverlayLineContent(dst *image.RGBA, x, y int, ln overlayLine, col color.Color, bordered bool) int {
	if ln.text == "" {
		return x
	}
	iconW := overlayIconSize(ln.fontPx)
	gap := overlayIconGap(ln.fontPx)
	for _, seg := range overlayLineSegments(ln.text) {
		if seg.rain {
			drawOverlayRainIcon(dst, x, y, iconW, ln.fontPx, col, bordered)
			x += iconW + gap
			continue
		}
		if seg.text == "" {
			continue
		}
		if bordered {
			drawBorderedOverlayText(dst, seg.text, x, y, ln.face, float64(ln.fontPx))
		} else {
			drawPlainOverlayText(dst, seg.text, x, y, ln.face, col)
		}
		if ln.face != nil {
			x += font.MeasureString(ln.face, seg.text).Ceil()
		}
	}
	return x
}

func drawOverlayRainIcon(dst *image.RGBA, x, y, width, fontPx int, col color.Color, bordered bool) {
	iconH := overlayRainIconHeight(width)
	top := y
	if fontPx > 0 && iconH < fontPx {
		top = y + (fontPx-iconH)/4
	}
	if bordered {
		drawBorderedRainIcon(dst, x, top, width)
		return
	}
	drawRainCloudIcon(dst, x, top, width, col, 2)
}

func drawBorderedRainIcon(dst *image.RGBA, x, y, width int) {
	drawRainCloudIcon(dst, x+1, y+1, width, overlayBorderedShadow, 2)
	drawRainCloudIcon(dst, x, y, width, overlayBorderedOutline, 3)
	drawRainCloudIcon(dst, x, y, width, overlayBorderedFill, 2)
}
