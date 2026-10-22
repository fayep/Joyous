package main

import (
	"strings"

	"joyous-hub/protocol"
)

// overlayWireCoord converts a point computed in the "upright" working
// canvas (dw x dh -- swapped to portrait dimensions when portrait=true,
// same convention overlayMetricsDimensions/overlayWeatherOrigin/
// overlayPhotoNameOrigin already use) into the actual frameW x frameH
// coordinate space the device always draws in -- INKJOY_BIN_WIDTH/HEIGHT
// are fixed constants on the firmware side regardless of any given
// play's portrait/landscape content; it's the panel's own fixed physical
// rotation that makes that buffer look right on a tall panel, not a
// different in-buffer coordinate convention per play.
//
// This mirrors the exact rotate90 (compose onto "upright") -> rotate90CCW
// (undo, back to the real wire buffer) round trip drawWeatherOverlay
// applies to actual PIXELS for a portrait send (exif_orientation.go's
// rotate90 forward: dst(h-1-y, x) = src(x, y); dither.go's rotate90CCW
// inverse: dst(y, w-1-x) = src(x, y)) -- overlayWireLines was computing
// coordinates in the "upright" space directly and never applying this
// same transform, so portrait devices got overlay text at the wrong
// (off-panel) position while landscape ones, where dw==frameW and this
// is a no-op, happened to work (confirmed on real hardware, 2026-08-14).
//
// Note this only fixes *position* -- it does not rotate the glyphs
// themselves, unlike the baked-in path, which rotates real pixels (text
// included) as part of the same whole-image transform. Firmware's
// drawString always draws horizontally relative to the buffer's own
// fixed axes, so portrait overlay text landing in the right place but
// still reading sideways is a real, separate possibility this alone
// doesn't address -- needs a real-hardware check.
// nativeDims short-circuits the transform to identity: for a device
// rendering natively (no pixel rotation anywhere, see
// prepareInkJoyFrameFromRaw's own nativeDims branch), the wire buffer IS
// the upright working canvas, so (px,py) needs no further conversion. Kept
// as its own explicit parameter rather than folded into what "portrait"
// means -- conflating "swap the working-canvas dims" with "the wire buffer
// needs de-rotation" is the same ambiguity that appears to cause a
// pre-existing bug in Samsung's drawWeatherOverlay call (portrait Samsung
// panels are already native-shaped, but drawWeatherOverlay's rotate90/
// rotate90CCW round trip still runs unconditionally whenever portrait is
// true) -- not something to replicate here.
func overlayWireCoord(px, py, dw int, portrait, nativeDims bool) (x, y int) {
	if !portrait || nativeDims {
		return px, py
	}
	return py, dw - 1 - px
}

// overlayWireCoordNativize is the inverse of overlayWireCoord's rotate90CCW-
// mirroring transform: given (x,y) already computed in the fixed-landscape
// wire coordinate space (portrait=true, nativeDims=false), it recovers the
// original "upright" working-canvas (px,py) -- i.e. what overlayWireCoord
// would have returned directly had it been called with nativeDims=true in
// the first place. Solving overlayWireCoord's x=py, y=dw-1-px for (px,py)
// gives (px,py) = (dw-1-y, x).
//
// Exists so buildSendImageBody (hardware-agnostic, no build tag) can keep
// always computing OverlaySendBody with nativeDims=false -- it has no way
// to know at that point whether the destination device will turn out to be
// native-dims-eligible, that's decided later, bridge-side, in
// bridgeDeliverInkJoyImage (main_inkjoy_bridge.go) -- which applies this
// correction as a second pass instead. Kept here (hardware-agnostic file)
// rather than inline in the bridge-tagged file specifically so it's
// covered by the normal (non-bridge-tagged) test suite -- this is the
// single riskiest piece of the native-dims feature; see
// TestOverlayWireCoordNativizeMatchesDirectIdentity in overlay_wire_test.go.
func overlayWireCoordNativize(x, y, dw int) (px, py int) {
	return dw - 1 - y, x
}

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
// nativeDims -- see overlayWireCoord's own comment. buildSendImageBody
// (hardware-agnostic, no build tag) always calls this with nativeDims=false
// -- it doesn't know at this point whether the destination device will end
// up native-dims-eligible (that's decided later, bridge-side, in
// bridgeDeliverInkJoyImage). A device that does need nativeDims gets its
// already-built OverlaySendBody coordinate-corrected there instead, via
// overlayWireCoordNativize -- see that function's comment for why this is
// done as a second pass rather than threading nativeDims in here.
func overlayWireLines(cfg OverlayConfig, weather WeatherSnapshot, photoName string, portrait, nativeDims bool) (protocol.OverlaySendBody, error) {
	dw, dh := overlayMetricsDimensions(frameW, frameH, portrait)

	lines, err := overlayRenderedLines(cfg, weather, dw, dh, 1.0)
	if err != nil {
		return protocol.OverlaySendBody{}, err
	}

	style := overlayWeatherStyleBox
	if overlayWeatherUsesOutline(cfg) {
		style = overlayWeatherStyleOutline
	}
	x, y := overlayWeatherOrigin(dw, dh, lines, style, 1.0)

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
		wireX, wireY := overlayWireCoord(x, y, dw, portrait, nativeDims)
		body.Lines = append(body.Lines, protocol.OverlayWireLine{
			Text: text,
			Font: overlayLineFontRole(i),
			X:    wireX,
			Y:    wireY,
		})
		y += ln.stepPx
	}

	if cfg.ShowPhotoName && strings.TrimSpace(photoName) != "" {
		if cx, cy, _, _, ok := overlayPhotoNameOrigin(photoName, cfg.PhotoNamePosition, dw, dh, 1.0); ok {
			wireX, wireY := overlayWireCoord(cx, cy, dw, portrait, nativeDims)
			body.Caption = &protocol.OverlayWireLine{
				Text: photoName,
				Font: "caption",
				X:    wireX,
				Y:    wireY,
			}
		}
	}

	return body, nil
}
