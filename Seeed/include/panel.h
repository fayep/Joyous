#pragma once
#include <Arduino.h>

namespace Panel {

void begin();

// Draws a plain-text setup screen showing the frame's BLE provisioning name
// ("IJ_<clientId>") so whoever is running the hub's scan/adopt tooling can
// tell this physical unit apart from any other frame in provisioning mode.
void showProvisioning(const String &clientId);

// Fetches an InkJoy .bin image over HTTP (host:port + path) and renders it.
// Calls onProgress(20/40/60/80) as rows are consumed, then update()s the
// panel on success. Returns false on any HTTP/stream error (nothing is
// drawn to the physical panel in that case — caller sends a failed ack).
bool renderBinFromHttp(const String &host, int port, const String &path,
                        void (*onProgress)(int percent));

} // namespace Panel
