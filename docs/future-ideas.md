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

## Phone app

A native iOS (or cross-platform) companion for Joyous Hub on the LAN — upload from the camera roll, pick frames, push photos, maybe glance at frame status (battery, last image, asleep vs deep sleep).

**Notes:**

- Phone **cannot** replace a Mac PhotoKit bridge for the full Apple Photos library on the desktop; it could upload **selected** photos/albums from the device’s own library via standard iOS photo picker APIs.
- Hub already speaks HTTP + JSON; app is mostly a tailored client for album upload, send-to-frame, and Samsung/InkJoy status — no cloud required if home Wi‑Fi reachable (Tailscale or similar for away-from-home later).
- Share extension (“Send to Joyous”) would pair nicely with the polaroid album flow.
