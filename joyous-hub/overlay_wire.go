package main

import (
	"strings"

	"joyous-hub/protocol"
)

// overlayLineFontRole mirrors overlayLineFace's own index->size mapping
// (overlay_template.go) as a wire-safe name instead of a resolved font.Face
// -- index 1 (the "big" line, typically the date) is large, everything
// else (including the default case) is medium.
func overlayLineFontRole(index int) string {
	if index == 1 {
		return "large"
	}
	return "medium"
}

// overlayWireLines resolves the same overlay content and layout the hub
// would bake into an image (see drawWeatherOverlayOnImage) into a flat,
// already-positioned line list a device can draw directly with no layout
// math of its own -- see protocol.OverlaySendBody's comment for why this
// isn't a raw OverlayConfig/WeatherSnapshot handoff like OverlaySendInfo.
// Reuses overlayRenderedLines (text/step content) and
// overlayWeatherOrigin/overlayPhotoNameOrigin (position) -- the exact same
// functions drawWeatherOverlayOnImage/drawPhotoNameCaption call -- so this
// can't silently drift from what's actually baked into a real image.
func overlayWireLines(cfg OverlayConfig, weather WeatherSnapshot, photoName string, portrait bool) (protocol.OverlaySendBody, error) {
	dw, dh := overlayMetricsDimensions(frameW, frameH, portrait)

	lines, err := overlayRenderedLines(cfg, weather, dw, dh)
	if err != nil {
		return protocol.OverlaySendBody{}, err
	}

	style := overlayWeatherStyleBox
	if overlayWeatherUsesOutline(cfg) {
		style = overlayWeatherStyleOutline
	}
	x, y := overlayWeatherOrigin(dw, dh, lines, style)

	var body protocol.OverlaySendBody
	for i, ln := range lines {
		// Strip the rain-cloud private-use rune (overlayIconRainRune,
		// overlay_icons.go) before it ever reaches the wire -- it's a
		// hand-drawn vector icon hub-side (oksvg/rasterx), not a real font
		// glyph, and firmware has no glyph for it either (GFXFF's
		// converted range is plain ASCII 0x20-0x7E) or any icon-drawing
		// code wired up to intercept it -- confirmed on real hardware
		// (2026-08-13): passed through unstripped, it rendered as a
		// visible placeholder/tofu glyph instead of being silently
		// invisible or, worse, actually drawing a cloud. Dropped from the
		// wire format entirely for v1, matching the same "no icon" call
		// already made for the degree glyph -- see Panel::drawDegreeGlyph's
		// own comment in Firmware/include/panel.h.
		text := strings.TrimSpace(strings.ReplaceAll(ln.text, string(overlayIconRainRune), ""))
		if text == "" {
			y += ln.stepPx
			continue
		}
		body.Lines = append(body.Lines, protocol.OverlayWireLine{
			Text: text,
			Font: overlayLineFontRole(i),
			X:    x,
			Y:    y,
		})
		y += ln.stepPx
	}

	if cfg.ShowPhotoName && strings.TrimSpace(photoName) != "" {
		if cx, cy, _, _, ok := overlayPhotoNameOrigin(photoName, cfg.PhotoNamePosition, dw, dh); ok {
			body.Caption = &protocol.OverlayWireLine{
				Text: photoName,
				Font: "caption",
				X:    cx,
				Y:    cy,
			}
		}
	}

	return body, nil
}
