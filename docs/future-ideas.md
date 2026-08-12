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

## On-device overlay text (separate raw photo from overlay in the SD cache)

Right now the SD content cache (`ContentCache`, offline-mode slideshow) stores whatever the hub delivers — a photo with any date/weather/caption overlay already baked in, frozen at whatever it said when that image was last fetched. Idea: cache the **raw** photo (no overlay) separately from overlay text, and have the firmware draw the overlay itself at display time. Decided (2026-08-12) to leave this for later — not worth it yet, see below — but capturing what it'd actually take.

**Status quo, decided against for now:**

- **Weather is a hard no.** Requires a live network fetch, which directly fights offline mode's whole reason for existing — `goOffline()` turns WiFi off specifically to save battery. Reintroducing periodic fetches just for weather text undermines that.
- **Date/time isn't worth it either, for a different reason:** this is a 6-color e-paper panel (GDEP133C02/T133A01), and every real update — even a small one — goes through the same multi-second PON→DRF→POF waveform sequence regardless of how much of the panel changes. There's no way to make a clock-style frequent update cheap on this hardware, so a "live" date is off the table too.

**What it would actually take, if revisited:**

- **Hub side:** split raw-photo caching from overlay compositing — device fetches/caches the plain photo once, hub (or device) supplies overlay text separately, composited at render time instead of once at encode time.
- **Firmware side:** port the outline-stroke text technique from `joyous-hub/overlay_render.go`'s `drawBorderedOverlayText` (8-direction stroke + fill), not a soft drop shadow — confirmed this matters: the hub's own semi-transparent shadow already gets crushed to a hard edge by `pinOverlayPixels`' nearest-palette-ink snap before it ever reaches a device, since there's no true alpha blending on a 6-ink display. An outline guarantees contrast on every side of a glyph against an unpredictable dithered photo background; a single offset shadow only protects one side. Font infra for this already exists (`GoRegular23pt7b`/`GoRegular30pt7b`/`Caveat39pt7b`, `Panel::drawDegreeGlyph` — see `panel_tftespi.cpp`).
- **A real, narrower-scope path if only occasional (not live) date refreshes are wanted later:** the panel hardware does support partial-window updates — confirmed in GooDisplay's own vendor reference code (`GooDisplay/docs/vendor_code_example_usb_variant/GDEP133C02.c`, `partialWindowUpdateWithImageData`, `PTLW` register) — which constrains a refresh to a rectangular sub-window (e.g. just a date strip) instead of the whole panel, leaving the rest of the displayed photo undisturbed. Two real caveats: it still calls the same `epdDisplay()` waveform sequence internally (same multi-second wait — this saves *area*, not *time*), and the window coordinates have real alignment constraints (`HRST` a multiple of 8, `HRED-7` a multiple of 8, `xStart≤584`, `xPixel≤600`, `yStart+yLine` even, etc.) that would need real firmware work to satisfy, not just picking a rectangle.

## Phone app

A native iOS (or cross-platform) companion for Joyous Hub on the LAN — upload from the camera roll, pick frames, push photos, maybe glance at frame status (battery, last image, asleep vs deep sleep).

**Notes:**

- Phone **cannot** replace a Mac PhotoKit bridge for the full Apple Photos library on the desktop; it could upload **selected** photos/albums from the device’s own library via standard iOS photo picker APIs.
- Hub already speaks HTTP + JSON; app is mostly a tailored client for album upload, send-to-frame, and Samsung/InkJoy status — no cloud required if home Wi‑Fi reachable (Tailscale or similar for away-from-home later).
- Share extension (“Send to Joyous”) would pair nicely with the polaroid album flow.
