#pragma once

// GooDisplay ESP32-133C02 pin mapping — confirmed from the vendor's own
// USB-variant example source (GooDisplay/docs/vendor_code_example_usb_variant/
// pindefine.h), not guessed. Panel driven via Seeed_GFX's existing
// T133A01_DRIVER backend (see Firmware/include/tft_setup_goodisplay.h) —
// GDEP133C02 and T133A01 are the same underlying driver silicon, confirmed
// by an identical register map. A from-scratch ESP-IDF driver
// (panel_goodisplay.cpp, still in the tree but excluded from this build —
// see platformio.ini's goodisplay_c02 build_src_filter) was built and fully
// debugged first: real QSPI wasn't actually needed (single-line SPI, like
// Seeed_GFX uses), the CS0/CS1 halves split left/right by column not
// top/bottom, and it confirmed a working, correctly-rotated render on real
// hardware. Once that confirmed the panel needed no genuinely custom
// protocol, retrying Seeed_GFX (this time with tft_setup_goodisplay.h's
// TFT_DC/TFT_RST pin-aliasing bug fixed -- the real reason the first
// Seeed_GFX attempt produced no output) worked immediately, including
// correct rotation with zero manual transform math (T133A01_Rotation.h
// already handles it) — so Seeed_GFX became the real implementation
// (2026-08-08) and the custom driver was retired to reference-only.
#define EPD_CS0    18
#define EPD_CS1    17
#define SPI_CLK     9
#define SPI_Data0  41
#define SPI_Data1  40
#define EPD_BUSY    7
#define EPD_RST     6
#define LOAD_SW    45

// InkJoy bin format is landscape 1600x1200; the T133A01/GDEP133C02 panel is
// wired portrait (TFT_WIDTH=1200, TFT_HEIGHT=1600 in tft_setup_goodisplay.h).
// Rotation 0/2 keep the sprite's reported width()/height() in the panel's
// native portrait shape (1200x1600); 1/3 swap it to landscape (1600x1200),
// which is what this shared panel code (panel_tftespi.cpp) assumes --
// InkJoy images and layout math are written for a landscape canvas, so
// content only fills part of the panel under 0/2. Confirmed on real
// hardware (2026-08-08) across all four values: 0 was upright but only
// filled ~2/3 of the panel (wrong canvas shape), 3 was upside down, 1 --
// same as Seeed's own default, despite this being different silicon-adjacent
// hardware with its own independent mount -- is correct: full landscape
// canvas, right side up.
#define PANEL_ROTATION 1

// Per-panel calibration register overrides for Seeed_GFX's T133A01 driver
// (vendored in Firmware/lib/Seeed_GFX/ -- see T133A01_Defines.h). Seeed's
// EE02 and this board are the same driver silicon (T133A01 == GDEP133C02)
// but different vendor calibration profiles -- these are GooDisplay's own
// values from their GDEP133C02.c reference (GooDisplay/docs/
// vendor_code_example_usb_variant/GDEP133C02.c), not Seeed's. Confirmed
// working on real hardware with Seeed_GFX (2026-08-08) -- no visible
// difference from Seeed's own (also confirmed-working) defaults, but this
// board now runs its own vendor-matched tuning rather than borrowed values.
#define T133A01_CDI_V     { 0xF7 }
#define T133A01_BTST_P_V  { 0xE8, 0x28 }
#define T133A01_BTST_N_V  { 0xE8, 0x28 }
#define T133A01_AN_TM_V   { 0xC0, 0x1E, 0x1E, 0xCE, 0xCE, 0xCE, 0x15, 0x15, 0x55 }

#define INKJOY_BIN_WIDTH  1600
#define INKJOY_BIN_HEIGHT 1200

// "H" in the login ver string ("M H:%d F:...") — distinct from Seeed's 9 so
// joyous-hub can tell the two clone hardware types apart (see
// inkjoybridge.HardwareModelGooDisplay).
#define HW_MODEL 10
#define FW_VERSION "1.0.0"

// No known SD hardware on this firmware slice — the SD-variant board (and
// its real GPIO pin mapping) hasn't arrived yet. See GooDisplay/README.md.
#define ENABLE_SD_CACHE 0

// Battery ADC and button pins are not yet traced for this board (no
// schematic beyond the panel/SD-slot region examined so far). -1 sentinels
// (matching the existing PANEL_ENABLE_PIN/BATTERY_ADC_PIN convention in
// power.cpp) disable the corresponding code paths at compile time rather
// than guessing pin numbers — buttonHeld()/pollOrientationButton()/
// pollResetButton() become no-ops, batteryMillivolts() returns -1, and
// handleBatteryLevel() already treats a negative percent as "unknown, don't
// act on it."
#define BTN_ORIENT_PIN -1
#define BTN_RESET_PIN -1
#define BTN_RESET_HOLD_MS 3000
#define PANEL_ENABLE_PIN -1
#define BATTERY_ADC_PIN -1
#define BATTERY_ADC_EN_PIN -1
#define BATTERY_DIVIDER_RATIO 2
#define BATTERY_LOW_PCT 10
#define BATTERY_CRITICAL_PCT 5
#define BATTERY_LOW_RECHECK_SECONDS 3600UL

// InkJoy cloud orientation enum: 0=portrait, 90=landscape-left. No physical
// orientation button on this board yet (BTN_ORIENT_PIN above) — kept for
// parity with the shared heartOrientation()/toggleOrientation() code.
#define ORIENT_PORTRAIT 0
#define ORIENT_LANDSCAPE 90

#define HEART_INTERVAL_MS (600UL * 1000UL)
#define MQTT_KEEPALIVE_SEC (HEART_INTERVAL_MS / 1000UL)
#define LOGIN_SETTLE_MS 2000UL

#define CPU_MHZ_IDLE 80
#define CPU_MHZ_PERF 240
