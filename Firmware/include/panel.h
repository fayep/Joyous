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
// existing default behavior.
void showMode(bool offline);

// Confirmation before reboot into BLE provisioning (KEY3 factory reset).
void showFactoryReset();

// Shown once after BLE adopt when Wi‑Fi/MQTT are up.
void showConnected();

// Shown before the critical-battery deep sleep (see BATTERY_LOW_PCT in
// config.h) — the last thing on the panel until it's charged and woken.
void showLowBattery();

// Fetches an InkJoy .bin image over HTTP (host:port + path) and renders it.
// Calls onProgress(20/40/60/80) as rows are consumed, then update()s the
// panel on success. Returns false on any HTTP/stream error (nothing is
// drawn to the physical panel in that case — caller sends a failed ack).
bool renderBinFromHttp(const String &host, int port, const String &path,
                        void (*onProgress)(int percent));

// Same shape as renderBinFromHttp but for the indexed-PNG format: the
// whole file is buffered in PSRAM first (PNG can't be decoded row-by-row
// as it streams in, unlike .bin), then handed to renderPngBytes. Used
// whenever a play's imgurl is .png but there's no usable on-device cache
// to route it through ContentCache (ENABLE_SD_CACHE off, or the SD addon
// isn't physically present even though the hub thinks this firmware is
// cache-capable — see main.cpp's handlePlay fallback). No onProgress here;
// a whole-buffer fetch has no natural row-count milestones to report.
// Returns false on any HTTP/decode error.
bool renderPngFromHttp(const String &host, int port, const String &path);

// Same InkJoy .bin format, read from a local file on the SD addon instead
// of over HTTP (see SdCard::begin() / include/playlist.h). Also accepts
// indexed-PNG files (see ContentCache) -- dispatches on `path`'s extension
// (".png" vs anything else, treated as legacy .bin), so Playlist's existing
// .bin frames keep working unchanged during the hub's format cutover.
// Returns false on any open/read/decode error -- nothing is drawn to the
// panel in that case.
bool renderBinFromFile(const String &path);

// Renders raw InkJoy .bin-format bytes already fully loaded in memory
// (hi/lo per pixel, exactly INKJOY_BIN_WIDTH*INKJOY_BIN_HEIGHT*2 bytes).
// For callers holding a whole file/fetch in a buffer already (e.g.
// ContentCache) rather than streaming row-by-row like renderBinFromHttp/File
// do. Returns false if `len` doesn't match the expected size.
bool renderBinBytes(const uint8_t *bytes, size_t len);

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

// Renders indexed-PNG-format bytes already fully loaded in memory (4-bit
// palette, INKJOY_BIN_WIDTH x INKJOY_BIN_HEIGHT, palette index 0-5 mapped
// to black/white/yellow/red/blue/green -- a contract with the hub-side
// encoder, not derived from the file). Returns false on any decode error
// or if the decoded image doesn't match the expected dimensions/format.
bool renderPngBytes(const uint8_t *bytes, size_t len);

} // namespace Panel
