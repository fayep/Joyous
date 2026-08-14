# Future ideas (brainstorm)

Rough album-wall improvements — not committed to a timeline, just things that would be fun to build.

## Edit names in place

Captions are display names, not the stored filename. Tap or click to edit inline (Caveat handwriting style preserved), save to image metadata without renaming the file on disk.

## Emoji and stickers

Decorations anchored to a photo — relative positioning (on top, beside, tucked in a corner, overlapping the edge). The polaroid stack layout is begging for a bit of playful clutter; placement should feel physical, not grid-snapped.

## Themes / stacks

Group photos into themed stacks that fan out when opened, with an animated title drifting over the top of the pile — like labeled bundles on the wall.

## Post-it notes

Little handwritten notes pinned to the wall — two surfaces:

- **Album wall** — notes fixed to the left or right of a row (or tucked against a stack), like reminders on a real pinboard. Caveat or similar; draggable, maybe colour-coded.
- **On the frame** — send a note with an image push; it composites on top of the photo on the e-ink display (position, size, rotation). A message from home without replacing the picture underneath.

## Filtering

Narrow the wall by:

- **Date** — when the photo was taken or uploaded
- **Shape** — aspect ratio or any saved crop format (e.g. all 4:3, all portrait 3:4)
- **Keywords** — tags in metadata, filename search, or future caption text

## Apple Photos import (macOS)

Browse and import from the system Photos library — albums, smart albums, favorites, people, media types — without routing pictures through the cloud.

**Feasibility notes:**

- Apple exposes the library via **PhotoKit** (Swift/ObjC) or **Photos.app scripting**, not from the browser or pure Go. User must grant **Photos** privacy access once.
- **Works well:** user albums, shared albums, smart albums, favorites, people collections, screenshots/panoramas/etc. via asset metadata, export/copy into the hub image store.
- **Partial:** places (GPS on assets, not the full Places UI tree); built-in sidebar items (Years, Recents) via queries, not always 1:1 with the Photos app.
- **Weak / skip:** Memories as the app shows them; unofficial SQLite scraping (`osxphotos`) — fragile on OS updates.
- **Likely shape:** small macOS helper (CLI or companion) lists collections → JSON for the hub; user picks albums → helper exports new/changed assets → hub ingests with album/tags. Fits the themes/stacks idea.
- **Open choices:** one-shot import vs ongoing sync; copy into `data/images` (simple, matches today) vs referencing files in the library (harder).

## On-device overlay text

**Update (2026-08-14): done for live plays.** A `play` message now carries its own overlay data (`SendImageBody.OverlayWire`/`data.overlay`), and the frame renders it itself — outline-stroke text (`Panel::drawOverlayLines`, 8-direction stroke + white fill, matching hub's own `drawBorderedOverlayText` technique — a soft shadow doesn't survive this device's nearest-palette-ink snap anyway, no true alpha blending on a 6-ink display, so outline is what actually works here), one combined refresh with the image (see `panel.h`'s render*() functions), real photo-name captions resolved hub-side. Baked-in overlay compositing is now skipped for our own Seeed/GooDisplay clones specifically (gated in `bridgeDeliverInkJoyImage`, `main_inkjoy_bridge.go`) — real InkJoy vendor frames and Samsung still always get it baked in, unchanged, since they can't render text themselves.

**What's still open: offline-mode captions.** The SD content cache (`ContentCache`, offline-mode slideshow) still cycles plain photos with no overlay at all — `renderFrame()`/the `loop()` cycling path never pass overlay data, by design (see the render*() functions' own doc comment: defaults are a deliberate no-op so offline cycling needed zero code changes). This remains genuinely unaddressed, not because the rendering can't do it (it now can, trivially — just pass real `Panel::OverlayLine`s instead of the current `nullptr, 0, nullptr` defaults) but because of two separate content questions:

- **Weather is still a hard no.** Requires a live network fetch, which directly fights offline mode's whole reason for existing — `goOffline()` turns WiFi off specifically to save battery.
- **Date is more feasible than originally thought, though.** Originally written off here as "not worth it" because every panel update — even a tiny one — goes through the same multi-second PON→DRF→POF waveform regardless of how little changed, so a *live* clock-style refresh was never going to be cheap on this hardware (still true, unchanged). But the device already maintains a real GMT clock, synced via the login/heart round-trip (`Power::syncClockFromRoundTrip`) — so once it's been online at least once, it has a genuinely reasonable date available even fully offline, with no network dependency. That just means date-in-offline-cycling is now a "worth revisiting" question rather than a hard no — the refresh-cost tradeoff (showing a slightly-off date at whatever interval `kCacheCycleIntervalMs` already cycles on, vs. not showing one at all) is a judgment call, not a technical blocker.
- **Real InkJoy hardware's own partial-window refresh** (`PTLW` register, confirmed in GooDisplay's vendor reference code, `GDEP133C02.c`'s `partialWindowUpdateWithImageData`) is still the only way to make a date-only update touch less of the panel than a full photo redraw — it saves *area*, not *time* (same waveform wait either way), and has real coordinate-alignment constraints (`HRST` a multiple of 8, `xStart≤584`, etc.) that would need real firmware work. Not yet explored further than this.
- **Captions need a name-recovery mechanism that doesn't exist yet.** `ContentCache` keys files purely by content hash (`<hash>.png`) — nothing today records what a cached file was originally called, unlike a live play, which always gets a fresh caption in its own overlay data. Two options if this gets built: a sidecar file (e.g. `<hash>.name`) written alongside the cached PNG, or embedding the name in the PNG's own EXIF (`tEXt`/`iTXt` chunks). Sidecar is the better bet — plain text I/O via the same `SD_MMC` APIs already used everywhere in this codebase, vs. EXIF requiring lodepng's ancillary-chunk parsing to be compiled in and exercised on a decode path that's deliberately minimal today (`color_convert=0`, straight to indexed 4-bit, no chunk parsing at all). The real cost either way is keeping a sidecar paired with its PNG — `ContentCache::evictIfNeeded` would need to delete both together, and the fetch/write path would need to write both.

## Phone app

A native iOS (or cross-platform) companion for Joyous Hub on the LAN — upload from the camera roll, pick frames, push photos, maybe glance at frame status (battery, last image, asleep vs deep sleep).

**Notes:**

- Phone **cannot** replace a Mac PhotoKit bridge for the full Apple Photos library on the desktop; it could upload **selected** photos/albums from the device’s own library via standard iOS photo picker APIs.
- Hub already speaks HTTP + JSON; app is mostly a tailored client for album upload, send-to-frame, and Samsung/InkJoy status — no cloud required if home Wi‑Fi reachable (Tailscale or similar for away-from-home later).
- Share extension (“Send to Joyous”) would pair nicely with the polaroid album flow.
