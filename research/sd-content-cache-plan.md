# SD content-addressed cache + offline fallback

## Context

**Primary target: GooDisplay** (`GooDisplay/`, ESP32-S3 + built-in SD) —
this is the frame going to Faye's parents, so it needs to keep showing
real content even when the gateway is unreachable, not just work in the
demo case.

Both Seeed (EE02 clone + SD addon) and GooDisplay (ESP32-133C02, built-in
SD) now run the **same shared firmware** (`Firmware/`, one PlatformIO
project, board-gated by `config.h` → `config_seeed.h`/`config_goodisplay.h`)
— this is a change from when this doc was first written, when GooDisplay
had no firmware of ours at all (its board was still running GooDisplay's
own `-3`/WiFi demo firmware) and Seeed/GooDisplay were assumed to be
headed toward divergent implementations. As of this session, GooDisplay's
panel driver, MQTT client, SD mount, and playlist code are all the same
code Seeed uses — `main.cpp`/`playlist.cpp`/`sd_card.cpp` are
board-agnostic already, gated only by `ENABLE_SD_CACHE` and per-board pin
macros. Both boards' SD pins are now known (`config_seeed.h`/
`config_goodisplay.h`), and GooDisplay's SD mount is confirmed on real
hardware (a 256GB SDXC card, reformatted to FAT32). **Seeed's SD/playlist
path is implemented and code-reviewed but has never been tested against
real Seeed SD addon hardware** — that hardware isn't currently in hand.
The *schema* (`playlist.json`) was validated separately, against real
InkJoy firmware, during reverse-engineering; that's solid. The C++ port of
it running on Seeed specifically is not.

Given both boards share one codebase now, this doc's original premise —
build and validate against Seeed first as a "reference," then port to
GooDisplay once it exists — has flipped: GooDisplay is not only the
priority board, it's also the one with actual real-hardware SD validation
right now. Whatever gets built here should target the shared codebase
directly, not a per-board port.

Today, live MQTT `play` pushes and a manually-populated `playlist.json`
are independent paths on both boards. Nothing keeps the SD card in sync
with what the hub actually wants shown — that's the gap this doc closes.

Goal: the hub keeps each frame's SD card populated with its current photo
set automatically, so it can keep cycling real content locally whenever
the MQTT gateway is unreachable. Builds directly on this session's
P2-calibration-at-login work (`Device.DeviceReportedP2`) — cache content
must be dithered with each device's own calibrated palette, same
precedence already used for live sends.

## On-disk/wire format: unified indexed PNG for both boards

Both boards' real on-panel pixel format is the **same**: both are driven
by the identical vendored `Seeed_GFX` `EPaper` class (GDEP133C02 and
Seeed's T133A01 are the same driver silicon), and `T133A01_Defines.h`
declares `EPD_COLOR_DEPTH 4` unconditionally — 4bpp-packed (2px/byte) for
both. There's no genuine panel-format difference between them. (An earlier
version of this doc claimed there was, and proposed keeping Seeed on the
InkJoy wire `.bin` format while only building GooDisplay against indexed
PNG. That framing predated both boards being consolidated onto the same
driver, and was really "Seeed's *existing* cache code happens to store
`.bin`" dressed up as a hardware distinction.)

Since the shared codebase now serves both boards identically, and Seeed's
`.bin`-based path was never hardware-validated anyway (see above), there's
little to lose and a real gain (one shared, cross-board-tested
implementation instead of two divergent untested ones) from unifying on
**indexed PNG for both boards**:

- 4-bit indexed PNG lines up bit-for-bit with the panel's real 4bpp
  format — validated earlier this session
  (`joyous-hub/png4bit_experiment_test.go`, since deleted): 1.97x
  compression vs. raw packed 4bpp, exactly lossless round-trip.
- One decode path (`lodepng` — single-file C, handles 4-bit indexed PNG
  natively) replaces the existing `.bin`-reading code in `playlist.cpp`,
  used by both boards.
- Testing this on whichever board is available (currently GooDisplay, with
  confirmed-working real SD hardware) validates the same code the other
  board runs — the only genuinely per-board pieces left are the SD pin
  macros (`SD_SPI_*_PIN`/`SD_CS_PIN`) and the low-level panel CS pins,
  both already handled by the existing `config_seeed.h`/
  `config_goodisplay.h` split.

The **hub-side logic is fully hardware-agnostic** and should be built
once: dither with the device's own P2 palette → produce the indexed-PNG
on-disk artifact → hash *that artifact's bytes* (post-dither, so two
devices with different calibrations never collide) → sync by hash.

## Design

**Trigger**: a new periodic scheduler (`joyous-hub/inkjoy_cache_sync.go`,
mirroring `scheduled_sends.go`'s ticker shape), running per eligible
connected device — any `Type == DeviceTypeInkJoy` with
`HardwareModel == HardwareModelSeeed || HardwareModelGooDisplay` (real
InkJoy stock firmware doesn't understand `cache_sync`). Live `play` sends
stay on their existing hot path, untouched.

**Sync**: for each eligible device, build its target image set (capped at
100 — see `Playlist`'s `frames[100]` limit), dither each through
`StuckiDither` using that device's `DeviceReportedP2`/`ColorOverride`
precedence (reuse `bridge_send.go`'s existing palette-resolution logic),
encode to indexed PNG, hash, save into a per-MAC content cache — skipping
anything already present (stat-before-write). Then publish a `cache_sync`
MQTT action carrying the hash→URL entry list and a ready-to-write local
manifest (device doesn't need to derive one).

Reuses joyous-hub's existing per-device-MAC cache
(`joyous-hub/inkjoy_cache.go`'s `InkJoyCache`, served at
`/inkjoy/{mac}/{file}`) unmodified — just name entries by content hash
(`sha256(pngBytes)`, truncated to 16 hex chars) instead of the existing
`(imageID, overlayToken, portrait)` scheme. No new HTTP route needed.

```json
{
  "action": "cache_sync",
  "data": {
    "host": "<hub host>", "port": 8080,
    "entries": [{"hash": "abcd1234...", "path": "/inkjoy/<MAC>/abcd1234....png", "bytes": 1950000}],
    "playlist": {
      "version": 1, "boot_policy": "exclusive",
      "play": {"mode": "sequence", "loop": true},
      "schedule": {"type": "interval", "interval_min": 15},
      "encrypt": {"enabled": false},
      "frames": [{"file": "cache/abcd1234....png"}]
    }
  }
}
```

Firmware (`Firmware/include/cache_sync.h` + `src/cache_sync.cpp`, new,
board-agnostic): handles `cache_sync` — checks which `entries[]` are
already on SD (by filename+size), fetches missing ones over HTTP straight
to `/ij_play/cache/<hash>.png` (new small fetch-to-file helper, distinct
from `Panel::renderBinFromHttp` which renders instead of saving), deletes
any file under `/ij_play/cache/` not in the received set
(**hub-authoritative eviction, no on-device LRU** — matches how this
codebase already treats the device as dumb everywhere else:
`ColorOverride`, scheduled sends, `wifi_sleep` windows are all hub-owned).
Writes the received manifest to a temp file, renames over
`/ij_play/playlist.json` (atomic-ish), calls `Playlist::begin()` to reload
— already safely re-callable (resets its own statics at the top; today
just never called more than once).

`Playlist` needs one real change: its current frame-render path
(`panel_tftespi.cpp`'s `renderBinFromFile`) reads raw `.bin`. Add a PNG
decode step (`lodepng` → 4bpp-packed buffer → existing
`EPaper::drawBufferPixel`-equivalent write path) alongside/replacing it.
Hash-derived filenames trivially pass `validFilePath()`, and at 100 frames
the generated `playlist.json` lands around 9KB, well under its 32KB cap.

**Gating**: today the playlist render loop in `main.cpp` ticks
unconditionally on its own interval, never reset by a live `play` — a
freshly-pushed photo could get silently overwritten by the next scheduled
tick even while connected. Fix: track `lastLivePlayMs` (set on a
successful live `play`), only let the playlist loop render when
`!MqttHub::connected() || millis() - lastLivePlayMs >= DISCONNECT_GRACE_MS`.

## Files

**joyous-hub (new):** `inkjoy_cache_sync.go` — scheduler + `checkCacheSync`
+ target-set builder + `syncDeviceCache` (dither/hash/save-as-PNG/publish).
**joyous-hub (changed):** `main_inkjoy_bridge.go` (wire up the scheduler
alongside `startScheduledSendScheduler`); possibly a small helper in
`inkjoy_cache.go`; confirm `InkJoyCache.Save` is safe to call concurrently
with live-send saves (check its locking).

**Firmware (new, shared, both boards):** `include/cache_sync.h` +
`src/cache_sync.cpp`; `lodepng` as a lib dependency; PNG decode step in
the playlist render path.
**Firmware (changed, shared):** `main.cpp` (new `cache_sync` action
dispatch branch, `lastLivePlayMs` tracking, gate the existing
playlist-render block); `config_seeed.h`/`config_goodisplay.h`
(`DISCONNECT_GRACE_MS`); `playlist.cpp` (PNG instead of raw `.bin` read).

## Build/verification order

1. Hub-side dither/hash/cache-population, unit-tested in isolation (fake
   `Device` + images; assert hash names land correctly, assert two devices
   sharing a palette converge on identical hashes, assert differing
   palettes diverge).
2. Wire-format payload builder + a worst-case 100-frame JSON size check.
3. `lodepng` integration + PNG-decode-to-4bpp-buffer, tested against a
   real GooDisplay render (real SD hardware already confirmed working this
   session) before wiring into the full cache-sync flow.
4. Firmware SD-write/eviction/playlist-swap logic in isolation — hand-place
   a fake `cache_sync`-shaped `playlist.json` + pre-populated
   `/ij_play/cache/*.png` files and confirm `Playlist::begin()` picks it up.
5. Firmware HTTP-fetch-to-SD-file against the hub's already-working
   `/inkjoy/{mac}/{file}` route.
6. GooDisplay end-to-end on real hardware: empty→populated sync, no-op
   re-sync, eviction on album change, live-play-not-clobbered-by-tick
   (grace period), WiFi pull mid-sync doesn't corrupt `playlist.json`.
7. Once real Seeed SD addon hardware is available: repeat step 6 there —
   should need zero code changes given the shared implementation, just a
   real hardware confirmation that's currently missing.
