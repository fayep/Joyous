#pragma once
#include <Arduino.h>
#include "panel.h"

// Content-addressed SD cache for InkJoy images, shared by both boards.
//
// Design (research/sd-content-cache-plan.md): reactive, not a hub-pushed
// sync/manifest scheme. On a `play`, the hub sends a content hash alongside
// the usual host/port/path; the device checks its SD cache for that hash
// first and renders locally if present (skips the network entirely),
// otherwise fetches, saves, and renders. Files live at
// /ij_play/cache/<portrait|landscape>/<hash>.png, indexed PNG (4-bit
// palette, lines up bit-for-bit with the panel's native 4bpp format -- no
// RGBA expansion needed). Hash is caller-supplied (computed hub-side,
// post-dither) -- the device never computes or verifies it itself, it's
// just a cache key.
//
// Split into a separate cache per orientation (portrait/landscape, pass
// the same values as ORIENT_PORTRAIT/ORIENT_LANDSCAPE): every live `play`
// naturally caches into whichever orientation the device is currently in,
// so real, contextually-appropriate content accumulates in both buckets
// over ordinary use -- no proactive dual-orientation generation needed
// hub-side. This is also what the offline-fallback loop cycles through
// (see frameCount()/renderFrame() below) when no playlist.json exists to
// drive it instead, so flipping orientation switches to an already-
// populated, correctly-shaped set immediately instead of showing wrong-
// oriented cached content or an empty cache.
//
// Writes use write-to-.tmp-then-rename, so an interrupted fetch (power
// loss, dropped connection) can never leave a corrupted file at the real
// cache path -- the rename only happens after the full write succeeds.
namespace ContentCache {

// Ensures /ij_play/cache/portrait and /ij_play/cache/landscape exist,
// creating the full parent chain if needed (SD.mkdir() isn't recursive --
// see content_cache.cpp's ensureDir()). Call once at boot, after
// SdCard::begin() succeeds -- takes directory creation off a play's
// critical path and surfaces a real mkdir failure in the boot log instead
// of only showing up as a mysterious cache-write failure later. Safe to
// skip (fetchAndRender() creates its own directory on demand too) but
// wasteful not to call, since every cache-eligible play would otherwise
// pay this cost on its first write.
void begin();

// True if /ij_play/cache/<orientation>/<hash>.png already exists on SD.
// `orientation` is ORIENT_PORTRAIT or ORIENT_LANDSCAPE.
bool has(const String &hash, int orientation);

// Decodes and renders the cached PNG for `hash` (from the given
// orientation's cache) directly to the panel, drawing lines/caption into
// the same buffer before the one refresh this makes -- same overlay
// contract as Panel's render*() functions (see panel.h's own note), and
// for the same reason: one play, one refresh. Defaults to none, a no-op
// for offline-mode cycling (see renderFrame() below, which never passes
// any). Returns false on any open/decode error -- nothing is drawn to the
// panel in that case.
bool render(const String &hash, int orientation,
            const Panel::OverlayLine *lines = nullptr, int lineCount = 0,
            const Panel::OverlayLine *caption = nullptr);

// Fetches image bytes from host:port+path over HTTP, saves to the given
// orientation's cache under `hash` (write-to-.tmp-then-rename), then
// renders it (same overlay contract as render() above). Returns false on
// any HTTP/write/decode error; a failed fetch never touches an existing
// valid cache entry for a different hash.
bool fetchAndRender(const String &host, int port, const String &path,
                     const String &hash, int orientation,
                     const Panel::OverlayLine *lines = nullptr, int lineCount = 0,
                     const Panel::OverlayLine *caption = nullptr);

// Deletes oldest-by-mtime cache files (within this orientation's cache
// only -- each orientation has its own independent cap, so heavy use of
// one orientation can't evict the other's accumulated content) until
// under the file-count/byte-size caps. Call after a successful
// fetchAndRender() -- not on every render, since render() alone doesn't
// grow the cache.
void evictIfNeeded(int orientation);

// Number of cached files for this orientation -- for offline-mode
// cycling (see main.cpp's loop()) when no playlist.json exists to drive
// it instead. 0 if the directory is empty, missing, or unmounted.
size_t frameCount(int orientation);

// Renders the Nth cached file (0-indexed, sorted by filename for a
// stable cycle order across boots/reinsertion -- not mtime/insertion
// order, which would reshuffle every time a new file gets cached).
// Returns false if index is out of range or the open/decode fails.
bool renderFrame(size_t index, int orientation);

} // namespace ContentCache
