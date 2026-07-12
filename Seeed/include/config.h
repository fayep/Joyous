#pragma once

// InkJoy bin format is landscape 1600x1200; the T133A01 panel is wired portrait
// (TFT_WIDTH=1200, TFT_HEIGHT=1600 in Setup510). setRotation(1) or (3) swaps
// EPaper's reported width()/height() to landscape so bin pixels can be drawn
// at their native (x,y) with no manual transform — but which of 1 vs 3 is
// "right side up" depends on how the panel ends up mounted in the enclosure.
// Not verified against real hardware yet; flip to 3 if the first render
// comes out upside down or mirrored.
#define PANEL_ROTATION 1

// Battery voltage divider ADC pin, if/when one is wired in (EE02 itself only
// exposes charge status via LED, not a fuel-gauge readout). -1 = not wired;
// heart reports a placeholder battery value.
#define BATTERY_ADC_PIN -1

#define INKJOY_BIN_WIDTH  1600
#define INKJOY_BIN_HEIGHT 1200

#define HEART_INTERVAL_MS (600UL * 1000UL)

// "H" in the login ver string ("M H:%d F:...") identifies hardware model on
// real frames (2=10", 3=13.3"). This board isn't a real InkJoy model, so use
// a value outside that range — purely informational, the hub doesn't branch
// on it today.
#define HW_MODEL 9
#define FW_VERSION "1.0.0"
