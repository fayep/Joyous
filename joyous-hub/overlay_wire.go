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
		if ln.text == "" {
			y += ln.stepPx
			continue
		}
		body.Lines = append(body.Lines, protocol.OverlayWireLine{
			Text: ln.text,
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
