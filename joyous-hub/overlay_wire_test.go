package main

import "testing"

// TestOverlayWireCoordNativizeMatchesDirectIdentity is the load-bearing test
// for the native-dims overlay-text feature (see the plan this implements:
// docs/future-ideas.md's "On-device overlay text" + the native-orientation
// rendering work). buildSendImageBody always computes OverlaySendBody in
// the fixed-landscape wire space (portrait=true, nativeDims=false, via
// overlayWireCoord); bridgeDeliverInkJoyImage then corrects it for
// native-dims devices via overlayWireCoordNativize. This must produce
// exactly the same coordinates as calling overlayWireCoord directly with
// nativeDims=true (the identity case) would have -- the two-step path and
// the direct path must agree, or overlay text lands in the wrong place on
// a native-dims device.
func TestOverlayWireCoordNativizeMatchesDirectIdentity(t *testing.T) {
	dw := frameH // swapped working-canvas width for portrait content, matches overlayMetricsDimensions(frameW, frameH, true)

	cases := []struct{ px, py int }{
		{0, 0},
		{1, 1},
		{84, 1050},
		{599, 1599},
		{dw - 1, 0},
		{0, dw - 1},
		{300, 800},
	}
	for _, c := range cases {
		wireX, wireY := overlayWireCoord(c.px, c.py, dw, true, false)
		gotX, gotY := overlayWireCoordNativize(wireX, wireY, dw)
		wantX, wantY := overlayWireCoord(c.px, c.py, dw, true, true)
		if gotX != wantX || gotY != wantY {
			t.Errorf("px=%d py=%d: nativize(wire(px,py))=(%d,%d), want direct identity (%d,%d)",
				c.px, c.py, gotX, gotY, wantX, wantY)
		}
		if gotX != c.px || gotY != c.py {
			t.Errorf("px=%d py=%d: round trip gave (%d,%d), want original (px,py)", c.px, c.py, gotX, gotY)
		}
	}
}

// TestOverlayWireCoordLandscapeUnaffected guards against nativeDims
// accidentally changing landscape (portrait=false) behavior, which must
// stay a pure passthrough regardless of nativeDims.
func TestOverlayWireCoordLandscapeUnaffected(t *testing.T) {
	for _, nativeDims := range []bool{false, true} {
		x, y := overlayWireCoord(123, 456, frameW, false, nativeDims)
		if x != 123 || y != 456 {
			t.Errorf("nativeDims=%v: landscape overlayWireCoord(123,456)=(%d,%d), want passthrough", nativeDims, x, y)
		}
	}
}
