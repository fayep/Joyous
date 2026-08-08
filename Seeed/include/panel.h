#pragma once
#include <Arduino.h>

namespace Panel {

void begin();

// Draws a plain-text setup screen showing the frame's BLE provisioning name
// ("IJ_<clientId>") so whoever is running the hub's scan/adopt tooling can
// tell this physical unit apart from any other frame in provisioning mode.
void showProvisioning(const String &clientId);

// Brief on-panel confirmation after toggling portrait/landscape preference.
void showOrientation(bool portrait);

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

// Same InkJoy .bin format, read from a local file on the SD addon instead
// of over HTTP (see SdCard::begin() / include/playlist.h). Returns false
// on any open/read error -- nothing is drawn to the panel in that case.
bool renderBinFromFile(const String &path);

} // namespace Panel
