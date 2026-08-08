# SD content-addressed cache + offline fallback

## Context

**Primary target: GooDisplay** (`GooDisplay/`, ESP32-S3 + built-in SD, 7.09"
panel on order in the SD-firmware variant) — this is the frame going to
Faye's parents, so it needs to keep showing real content even when the
gateway is unreachable, not just work in the demo case. Seeed
(`Seeed/`, ESP32-S3 clone + SD addon) is the **secondary/reference**
target: its firmware already exists and already has a working, real-
hardware-validated local-playlist mechanism (`Playlist`/`SdCard` modules,
a port of the real InkJoy firmware's own reverse-engineered schema), so
it's the fastest place to build and validate this design *today* — then
port the validated logic to GooDisplay once that board (and its real SD
pin mapping) is in hand.

Today, Seeed has two independent content paths: live MQTT `play` pushes,
and a manually-populated `playlist.json`. Nothing keeps the SD card in
sync with what the hub actually wants shown. GooDisplay has neither yet —
no MQTT client, no playlist support, nothing — its current firmware is
GooDisplay's own self-hosted WiFi demo (`-3` variant, dumped and analyzed
earlier this session), not anything of ours.

Goal: the hub keeps each frame's SD card populated with its current photo
set automatically, so it can keep cycling real content locally whenever
the MQTT gateway is unreachable. Builds directly on this session's
P2-calibration-at-login work (`Device.DeviceReportedP2`) — cache content
must be dithered with each device's own calibrated palette, same
precedence already used for live sends.

## Two hardware families, two on-disk/wire formats — this is real, not incidental

- **Seeed**: panel format is 2 bytes/pixel (hi=color index via `hiBytes[]`
  in `joyous-hub/dither.go`, lo=wipe byte) — joyous-hub's own existing
  InkJoy `.bin` format. PNG's compression advantage doesn't apply here and
  isn't worth the embedded complexity (new dependency, new render path to
  validate). Seeed keeps `.bin` end-to-end, on SD and over the wire.
- **GooDisplay**: panel format is 4bpp-packed (2px/byte, matches its QSPI
  `pindefine.h` driver — `SPI_CS0=18/SPI_CS1=17/SPI_CLK=9/SPI_Data0-3=41/40/39/38`
  from the vendor's USB-variant source). This is exactly where 4-bit
  indexed PNG pays off — validated earlier this session
  (`joyous-hub/png4bit_experiment_test.go`): 1.97x compression vs. raw
  packed 4bpp, exactly lossless round-trip, because PNG's 4-bit indexed
  color lines up bit-for-bit with this format. GooDisplay firmware
  genuinely needs a PNG decoder (`lodepng` — single-file C, handles 4-bit
  indexed PNG natively) once real firmware work starts there.

The **hub-side logic is fully hardware-agnostic** and should be built once:
dither with the device's own P2 palette → produce the on-disk artifact
(`.bin` for Seeed, indexed-PNG for GooDisplay, selected by `Device.Type`/
`HardwareModel`) → hash *that artifact's bytes* (post-dither, so two
devices with different calibrations never collide) → sync by hash. This
part is worth building now regardless of which physical board ships first.

## Design

**Trigger**: a new periodic scheduler (`joyous-hub/inkjoy_cache_sync.go`,
mirroring `scheduled_sends.go`'s ticker shape), running per eligible
connected device. For Seeed clones today that's `Type == DeviceTypeInkJoy
&& HardwareModel == inkjoybridge.HardwareModelSeeed` (real InkJoy stock
firmware doesn't understand `cache_sync`); GooDisplay gets its own
`HardwareModel`/`DeviceType` once its firmware exists and reports one at
login (mirroring the P2-report pattern already built for Seeed). Live
`play` sends stay on their existing hot path, untouched.

**Sync**: for each eligible device, build its target image set (capped at
100 for Seeed — see `Playlist`'s `frames[100]` limit; GooDisplay's cap TBD
once its own local-manifest format is designed), dither each through
`StuckiDither` using that device's `DeviceReportedP2`/`ColorOverride`
precedence (reuse `bridge_send.go`'s existing palette-resolution logic),
encode to the device's on-disk format, hash, save into a per-MAC content
cache — skipping anything already present (stat-before-write). Then
publish a `cache_sync` MQTT action carrying the hash→URL entry list and a
ready-to-write local manifest (device doesn't need to derive one).

### Seeed track (buildable now)

Reuses joyous-hub's existing per-device-MAC cache
(`joyous-hub/inkjoy_cache.go`'s `InkJoyCache`, served at
`/inkjoy/{mac}/{file}`) unmodified — just name entries by content hash
(`sha256(binBytes)`, truncated to 16 hex chars) instead of the existing
`(imageID, overlayToken, portrait)` scheme. No new HTTP route needed.

```json
{
  "action": "cache_sync",
  "data": {
    "host": "<hub host>", "port": 8080,
    "entries": [{"hash": "abcd1234...", "path": "/inkjoy/<MAC>/abcd1234....bin", "bytes": 3840000}],
    "playlist": {
      "version": 1, "boot_policy": "exclusive",
      "play": {"mode": "sequence", "loop": true},
      "schedule": {"type": "interval", "interval_min": 15},
      "encrypt": {"enabled": false},
      "frames": [{"file": "cache/abcd1234....bin"}]
    }
  }
}
```

Firmware (`Seeed/include/cache_sync.h` + `src/cache_sync.cpp`, new):
handles `cache_sync` — checks which `entries[]` are already on SD (by
filename+size), fetches missing ones over HTTP straight to
`/ij_play/cache/<hash>.bin` (new small fetch-to-file helper, distinct from
`Panel::renderBinFromHttp` which renders instead of saving), deletes any
file under `/ij_play/cache/` not in the received set (**hub-authoritative
eviction, no on-device LRU** — matches how this codebase already treats
the device as dumb everywhere else: `ColorOverride`, scheduled sends,
`wifi_sleep` windows are all hub-owned). Writes the received manifest to a
temp file, renames over `/ij_play/playlist.json` (atomic-ish), calls
`Playlist::begin()` to reload — already safely re-callable (resets its own
statics at the top; today just never called more than once).

`Playlist` itself needs zero changes — hash-derived filenames trivially
pass `validFilePath()`, and at 100 frames the generated `playlist.json`
lands around 9KB, well under its 32KB cap.

**Gating**: today the playlist render loop in `main.cpp` ticks
unconditionally on its own interval, never reset by a live `play` — a
freshly-pushed photo could get silently overwritten by the next scheduled
tick even while connected. Fix: track `lastLivePlayMs` (set on a
successful live `play`), only let the playlist loop render when
`!MqttHub::connected() || millis() - lastLivePlayMs >= DISCONNECT_GRACE_MS`.

### GooDisplay track (blocked on board arrival + real SD pin mapping)

Not started — no firmware exists to modify yet. When the SD-variant board
arrives: dump its vendor `-2` firmware the same way the `-3` unit was
dumped this session (this time it should contain real SD/FATFS code,
finally resolving the CARD1 GPIO pins we couldn't get from the `-3` dump
or public schematics). From there, real firmware work is needed from
scratch, informed by the Seeed implementation as a template:
- Port the InkJoy-style MQTT client (login/heart/play) — this is the
  "our InkJoy MQTT style" firmware the parents-frame idea started from.
- Port `SdCard`/`Playlist`-equivalent modules, adapted for whatever local
  manifest format makes sense here (could literally reuse the same
  `playlist.json` envelope for protocol consistency, or something
  simpler — worth deciding once the board's real constraints are known).
- Add the P2-calibration-at-login mechanism (mirrors `Seeed/include/calibration.h`
  exactly — NVS-backed, reported in `login`), with GooDisplay's own real
  calibration (the vendor web UI's `REAL_PALETTE` swatch values are a
  reasonable starting candidate, not yet swatch-verified against a real
  physical unit).
- Add `lodepng` and a PNG→4bpp-packed decode step feeding the existing
  QSPI panel write path.
- Add the same `cache_sync` handler logic as Seeed, adapted to this
  format.

## Files

**joyous-hub (new):** `inkjoy_cache_sync.go` — scheduler + `checkCacheSync`
+ target-set builder + `syncDeviceCache` (dither/hash/save/publish),
written to dispatch on-disk format by device type from the start even
though only the Seeed `.bin` path has a real consumer today.
**joyous-hub (changed):** `main_inkjoy_bridge.go` (wire up the scheduler
alongside `startScheduledSendScheduler`); possibly a small helper in
`inkjoy_cache.go`; confirm `InkJoyCache.Save` is safe to call concurrently
with live-send saves (check its locking).

**Seeed (new):** `include/cache_sync.h` + `src/cache_sync.cpp`.
**Seeed (changed):** `main.cpp` (new `cache_sync` action dispatch branch,
`lastLivePlayMs` tracking, gate the existing playlist-render block);
`config.h` (`DISCONNECT_GRACE_MS`).

**GooDisplay:** no files yet — firmware doesn't exist. First real file
will be whatever bootstraps its MQTT client, once the board arrives.

## Build/verification order

1. Hub-side dither/hash/cache-population, unit-tested in isolation (fake
   `Device` + images; assert hash names land correctly, assert two devices
   sharing a palette converge on identical hashes, assert differing
   palettes diverge). Format-dispatch by device type, but only exercise
   the `.bin` path for now.
2. Wire-format payload builder + a worst-case 100-frame JSON size check.
3. Seeed firmware SD-write/eviction/playlist-swap logic in isolation —
   hand-place a fake `cache_sync`-shaped `playlist.json` + pre-populated
   `/ij_play/cache/*.bin` files and confirm `Playlist::begin()` picks it up.
4. Seeed firmware HTTP-fetch-to-SD-file against the hub's already-working
   `/inkjoy/{mac}/{file}` route.
5. Seeed end-to-end on real hardware: empty→populated sync, no-op re-sync,
   eviction on album change, live-play-not-clobbered-by-tick (grace
   period), WiFi pull mid-sync doesn't corrupt `playlist.json`.
6. **Once the GooDisplay SD board arrives**: dump its `-2` firmware, get
   real SD pins, then repeat steps 3-5 against GooDisplay using the Seeed
   implementation as the validated reference for protocol/logic — the new
   work there is almost entirely the MQTT client + PNG decode + panel
   write path, not the cache-sync design itself.
