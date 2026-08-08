#pragma once

// InkJoy bin format is landscape 1600x1200; the T133A01 panel is wired portrait
// (TFT_WIDTH=1200, TFT_HEIGHT=1600 in Setup510). setRotation(1) or (3) swaps
// EPaper's reported width()/height() to landscape so bin pixels can be drawn
// at their native (x,y) with no manual transform — but which of 1 vs 3 is
// "right side up" depends on how the panel ends up mounted in the enclosure.
// Not verified against real hardware yet; flip to 3 if the first render
// comes out upside down or mirrored.
#define PANEL_ROTATION 1

// EE02 battery sense: A0 (GPIO1) via onboard ÷2 divider, gated by D5 (GPIO6).
// No fuel-gauge IC — heart.battery is a Li-ion voltage-curve estimate.
// Enable the divider only while sampling (see Power::batteryPercent).
#define BATTERY_ADC_PIN 1
#define BATTERY_ADC_EN_PIN 6
#define BATTERY_DIVIDER_RATIO 2

// Panel power rail (TFT_ENABLE on EE02 pin map). Held LOW in deep sleep.
#define PANEL_ENABLE_PIN 43

// EE02 KEY1..KEY3 (same GPIOs across EE0x).
#define BTN_ORIENT_PIN 2  // KEY1 — toggle portrait/landscape preference
#define BTN_RESET_PIN  5  // KEY3 — long-press clears NVS and re-enters BLE adopt
#define BTN_RESET_HOLD_MS 3000
#define BTN_WAKE_MASK ((1ULL << BTN_ORIENT_PIN) | (1ULL << BTN_RESET_PIN))

// InkJoy cloud orientation enum: 0=portrait, 90=landscape-left.
#define ORIENT_PORTRAIT 0
#define ORIENT_LANDSCAPE 90

#define INKJOY_BIN_WIDTH  1600
#define INKJOY_BIN_HEIGHT 1200

#define HEART_INTERVAL_MS (600UL * 1000UL)
// Align MQTT keepalive with heart so PINGREQ isn't an extra radio wake —
// a published heart resets the broker idle timer.
#define MQTT_KEEPALIVE_SEC (HEART_INTERVAL_MS / 1000UL)

// Grace period after login before the sleep-window check is allowed to act.
// The hub may republish a corrective wifi_sleep if our reported schedule
// didn't match what it expects (e.g. RTC reset by a reflash — see
// joyous-hub's UpdateLogin) — without this, we could re-enter deep sleep
// under the OLD (wrong) schedule before that correction ever arrives.
#define LOGIN_SETTLE_MS 2000UL

#define CPU_MHZ_IDLE 80
#define CPU_MHZ_PERF 240

// Critical battery handling (Seeed-specific — no real InkJoy behavior to
// match here). At/below BATTERY_LOW_PCT the frame warns on-panel, tells the
// hub why, and deep-sleeps to recheck later. At/below BATTERY_CRITICAL_PCT it
// stops self-waking entirely — only KEY1/KEY3 or a power cycle brings it back
// (a plugged-in charger biases the ADC sense node upward, so at these low
// readings the pack is very unlikely to still be off a charger and about to
// die again in an hour; better to just wait for the user).
#define BATTERY_LOW_PCT 10
#define BATTERY_CRITICAL_PCT 5
#define BATTERY_LOW_RECHECK_SECONDS 3600UL

// Idle path uses WiFi modem-sleep only (CPU stays schedulable for MQTT).
// Full chip light-sleep was tried and dropped inbound play (TCP never ACKed).

// "H" in the login ver string ("M H:%d F:...") identifies hardware model on
// real frames (2=10", 3=13.3"). This board isn't a real InkJoy model — use 9
// so joyous-hub can recognize Seeed (Portrait KEY1 sync) and inkjoy-bridge
// keeps the session local (never forwards login/heart to InkJoy cloud).
#define HW_MODEL 9
#define FW_VERSION "1.0.0"

// microSD addon board (snap-on, tapped off the "SPI1" bus already broken out
// on the B2B connector alongside the onboard flash / unpopulated font-chip
// footprint -- see README). SCLK/MISO/MOSI are that bus's own dedicated
// pins. CS reuses SPI1_CS1 (GPIO40), the net that only ever went to the
// never-populated GT32L32S0140 font chip (U6/R39/C63 confirmed DNP) -- dead
// there, so it's free for the SD card. Every other GPIO on this connector
// is already committed (panel SPI0, buttons, battery sensing), so there is
// no spare pin anywhere for a card-detect line: presence is inferred purely
// from whether SD.begin() succeeds. That means "no addon board" and "no
// card" read identically for now -- a real CD pin would need a hardware
// rev with a spare GPIO.
#define SD_SPI_SCLK_PIN 11
#define SD_SPI_MISO_PIN 12
#define SD_SPI_MOSI_PIN 13
#define SD_CS_PIN 40
