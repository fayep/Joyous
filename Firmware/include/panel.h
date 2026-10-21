#pragma once
#include <Arduino.h>

namespace Panel {

void begin();

// Puts the panel controller into its own low-power sleep state before deep
// sleep. Board-specific: Seeed's EPaper class has its own sleep(); GooDisplay
// sends the driver's POF (power-off) sequence.
void sleep();

// Draws a plain-text setup screen showing the frame's BLE provisioning name
// ("IJ_<clientId>") so whoever is running the hub's scan/adopt tooling can
// tell this physical unit apart from any other frame in provisioning mode.
void showProvisioning(const String &clientId);

// Brief on-panel confirmation after toggling portrait/landscape preference.
void showOrientation(bool portrait);

// Brief on-panel confirmation after toggling offline/online playback mode
// (SW3/KEY2 -- see BTN_MODE_PIN). offline=true means "driven by the SD
// playlist"; offline=false means "driven by MQTT play messages", the
// existing default behavior. portrait -- see showOrientation()'s own
// comment on this pattern; every status screen draws upright in the
// device's current orientation preference, not always the fixed mount
// rotation.
void showMode(bool offline, bool portrait);

// Confirmation before reboot into BLE provisioning (KEY3 factory reset).
void showFactoryReset(bool portrait);

// Shown once after BLE adopt when Wi‑Fi/MQTT are up.
void showConnected(bool portrait);

// Shown before the critical-battery deep sleep (see BATTERY_LOW_PCT in
// config.h) — the last thing on the panel until it's charged and woken.
void showLowBattery(bool portrait);

// One already-positioned line of overlay text. Deliberately a Panel-local
// type, not InkJoy::OverlayLine (inkjoy_protocol.h) -- this header has no
// dependency on the protocol layer today and shouldn't gain one just for
// this; main.cpp translates InkJoy::OverlayMsg into this at the call site.
// x/y are absolute pixel coordinates (top-left, same convention drawString
// already uses -- see drawOverlayLines()'s own comment in panel_tftespi.cpp).
struct OverlayLine {
    String text;
    String font; // "large" | "medium" | "caption"
    int x = 0;
    int y = 0;
};

// Every render*() function below takes the same trailing overlay
// parameters and draws overlay text into the same buffer as the image,
// before the one and only epaper.update() call that function makes --
// there's no separate "render, then separately draw+refresh overlay"
// step anywhere. lines/count/caption default to none/0/nullptr, which is
// a complete no-op (plain image, immediate refresh, identical to how
// these functions behaved before overlay support existed) -- every
// existing caller, offline-mode SD cycling included, needs zero changes
// and simply gets an empty overlay. Confirmed on real hardware
// (2026-08-13): rendering the image and then separately calling a
// standalone overlay-draw step -- two full e-paper waveform refreshes
// back to back -- was genuinely, unacceptably slow (~1 minute per play).
// One render call, one overlay (possibly empty), one refresh.

// Fetches an InkJoy .bin image over HTTP (host:port + path) and renders
// it. Calls onProgress(20/40/60/80) as rows are consumed. Returns false
// on any HTTP/stream error (nothing is drawn to the physical panel in
// that case — caller sends a failed ack).
bool renderBinFromHttp(const String &host, int port, const String &path,
                        void (*onProgress)(int percent),
                        const OverlayLine *lines = nullptr, int lineCount = 0,
                        const OverlayLine *caption = nullptr);

// Same shape as renderBinFromHttp but for the indexed-PNG format: the
// whole file is buffered in PSRAM first (PNG can't be decoded row-by-row
// as it streams in, unlike .bin), then handed to renderPngBytes. Used
// whenever a play's imgurl is .png but there's no usable on-device cache
// to route it through ContentCache (ENABLE_SD_CACHE off, or the SD addon
// isn't physically present even though the hub thinks this firmware is
// cache-capable — see main.cpp's handlePlay fallback). No onProgress here;
// a whole-buffer fetch has no natural row-count milestones to report.
// Returns false on any HTTP/decode error.
bool renderPngFromHttp(const String &host, int port, const String &path,
                        const OverlayLine *lines = nullptr, int lineCount = 0,
                        const OverlayLine *caption = nullptr);

// Same InkJoy .bin format, read from a local file on the SD addon instead
// of over HTTP (see SdCard::begin() / include/playlist.h). Also accepts
// indexed-PNG files (see ContentCache) -- dispatches on `path`'s extension
// (".png" vs anything else, treated as legacy .bin), so Playlist's existing
// .bin frames keep working unchanged during the hub's format cutover.
// Returns false on any open/read/decode error -- nothing is drawn to the
// panel in that case.
bool renderBinFromFile(const String &path,
                        const OverlayLine *lines = nullptr, int lineCount = 0,
                        const OverlayLine *caption = nullptr);

// Renders raw InkJoy .bin-format bytes already fully loaded in memory
// (hi/lo per pixel, exactly INKJOY_BIN_WIDTH*INKJOY_BIN_HEIGHT*2 bytes).
// For callers holding a whole file/fetch in a buffer already (e.g.
// ContentCache) rather than streaming row-by-row like renderBinFromHttp/File
// do. Returns false if `len` doesn't match the expected size.
bool renderBinBytes(const uint8_t *bytes, size_t len,
                     const OverlayLine *lines = nullptr, int lineCount = 0,
                     const OverlayLine *caption = nullptr);

// Renders indexed-PNG-format bytes already fully loaded in memory (4-bit
// palette, INKJOY_BIN_WIDTH x INKJOY_BIN_HEIGHT, palette index 0-5 mapped
// to black/white/yellow/red/blue/green -- a contract with the hub-side
// encoder, not derived from the file). Returns false on any decode error
// or if the decoded image doesn't match the expected dimensions/format.
bool renderPngBytes(const uint8_t *bytes, size_t len,
                     const OverlayLine *lines = nullptr, int lineCount = 0,
                     const OverlayLine *caption = nullptr);

// Draws a degree glyph ('°') as a small stroked circle at (x, y), scaled to
// sit correctly against text drawn at the given point size. Not a font
// glyph -- Go Regular's GFXFF conversion only covers ASCII 0x20-0x7E (see
// include/fonts/GoRegular*.h), and widening that range for one rarely-used
// character isn't worth the added flash -- same reasoning joyous-hub
// already applies to its own rain-cloud weather icon (a private-use rune
// intercepted before font lookup and hand-drawn instead of rasterized from
// a font; see joyous-hub/overlay_icons.go). Staged for on-device weather
// text, not yet called from anywhere.
void drawDegreeGlyph(int x, int y, int fontPt, uint16_t color);

// Draws overlay text lines directly on top of whatever the panel is
// currently showing, with its own immediate refresh -- does NOT clear the
// panel first, unlike showProvisioning()/showMode()/etc above. `caption`
// may be nullptr. Returns false (and draws nothing) if any line's `font`
// isn't a recognized role. Used standalone only by the overlay
// sizing/position test message (joyous-hub's CmdSendOverlay) -- a real
// play never calls this directly, it passes its overlay into whichever
// render*() function above it's already calling instead, for one combined
// refresh (see this file's own note above the render*() declarations).
bool drawOverlayLines(const OverlayLine *lines, int count, const OverlayLine *caption);

} // namespace Panel
