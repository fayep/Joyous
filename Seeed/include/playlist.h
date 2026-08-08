#pragma once
#include <Arduino.h>

// SD-card playlist.json support, matching the schema reverse-engineered
// from the real InkJoy firmware (ij_epd v0.7.10) -- see
// ~/Source/gerald/decoded/ij_epd_h2_v0.7.10/ for the full reconstruction
// this is based on, and gerald's project_inkjoy_playlist_json.md memory
// for the confirmed-on-real-hardware schema.
//
// Only `encrypt.enabled: false` is supported -- the real firmware's
// per-recipient ECIES-P256/AES-GCM scheme isn't implemented here, so an
// encrypted playlist is rejected outright rather than silently ignored.
namespace Playlist {

// Loads and validates playlist.json from the SD card. Prefers
// /ij_play/playlist.json, falls back to /playlist.json at the root --
// matching the real firmware's own preference order. Requires
// SdCard::present(). Returns false on no card, no playlist.json at
// either candidate path, any schema violation, or encrypt.enabled=true.
bool begin();

bool loaded();
size_t frameCount();

// Current frame's file field (relative path) joined against whichever
// source directory playlist.json was found in -- ready to pass to
// Panel::renderBinFromFile().
String currentFramePath();

bool loopEnabled();

// schedule.interval_min converted to milliseconds.
uint32_t intervalMs();

// Advances to the next frame, wrapping if loopEnabled(). No-op past the
// last frame if not looping.
void advance();

} // namespace Playlist
