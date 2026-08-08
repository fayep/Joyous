#pragma once
#include <Arduino.h>

// P2 dither-target calibration: the RGB each ink actually looks like on this
// panel, used by joyous-hub to pick the nearest ink per pixel during Stucki
// dithering. Stored in NVS so a fresh device reports a real value at login
// (see InkJoy::buildLogin's "p2" field) instead of the hub falling back to
// the real-InkJoy-panel default, which this clone's panel doesn't match.

struct P2Ink {
    uint8_t r, g, b;
};

// Compiled-in fallback used until NVS holds a real calibration. Currently
// just mirrors joyous-hub's PaletteInkJoyDisplay (the real InkJoy panel) —
// TODO: replace with a real photographed-swatch calibration for this panel
// (see joyous-hub web.go's per-device color calibration UI / the "IMG_0158
// primaries, click calibration" process already used for InkJoyDisplay).
extern const P2Ink kDefaultP2Calibration[6];

// Fills out[6] from NVS ("ijcfg"/"p2cal"), or kDefaultP2Calibration if unset.
void loadP2Calibration(P2Ink out[6]);

// Persists a new calibration to NVS. Not yet called anywhere — for a future
// on-device or companion-app calibration flow.
void saveP2Calibration(const P2Ink in[6]);
