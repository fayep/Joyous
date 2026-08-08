# Seeed EE02 (ESP32-S3) — InkJoy protocol firmware

Custom firmware for a Seeed XIAO ESP32-S3 + EE02 driver board (13.3" Spectra-6
/ T133A01 panel), speaking the InkJoy MQTT protocol against **joyous-hub**'s
local broker so it can receive photos the same way a real InkJoy frame does.
Origin: the real InkJoy frame's ESP32-C5 can't be ported to (different ISA —
RISC-V vs. this board's Xtensa), and its panel is driven by a proprietary
Gowin FPGA we don't have firmware for, so this is a clean-room reimplementation
of just the wire protocol, targeting a standard e-paper driver IC (T133A01)
instead.

Daytime networking uses ESP-IDF **esp-mqtt** (event-driven; the Arduino loop
blocks on a FreeRTOS event/timeout instead of busy-polling) plus
`WIFI_PS_MIN_MODEM` so the radio can nap between AP beacons while staying
MQTT-reachable for `play`.

Protocol reference: `research/firmware-notes.md` at the repo root has the full
reverse-engineered writeup this firmware implements against.

## Scope (see repo conversation / TODO for the decision)

Implemented: `login`, `heart`, `play` (+ `play_ack` progress/done/fail),
`wifi_sleep` (+ `wifi_sleep_ack`), `shutdown`, BLE provisioning (matching
joyous-hub's existing adopt flow — see Setup below), KEY1 toggles
portrait/landscape preference (NVS + `heart.orientation` 0/90 so the hub
updates its Portrait crop), and a critical-battery cutoff (see
`BATTERY_LOW_PCT`/`BATTERY_CRITICAL_PCT` in `include/config.h`): at or below
10% the frame shows a "Connect USB" panel message, sends a `sleep` with a
low-battery reason so the hub knows why it went dark, and deep-sleeps an hour
before rechecking; at or below 5% it stops self-waking entirely and needs
KEY1/KEY3 or a power cycle to come back. Deliberately **not** implemented:
OTA (ESP32 or FPGA), factory mode, and the real InkJoy app/cloud's own
provisioning path — none of that applies to a frame that only ever talks to
your own hub.

## microSD addon board

A snap-on addon board taps a second SPI bus (schematic name `SPI1`) already
broken out on the B2B connector alongside the onboard flash chip and the
unpopulated font-chip footprint (see below), plus one extra GPIO for chip
select, to add a microSD slot. See `SD_SPI_*_PIN`/`SD_CS_PIN` in
`include/config.h` for the exact pins and the reasoning:

- SCLK/MISO/MOSI are `SPI1`'s own dedicated pins (GPIO11/12/13).
- CS reuses `SPI1_CS1` (GPIO40) — confirmed dead everywhere except the
  never-populated font chip, so safe to repurpose.
- **No card-detect line**: every other GPIO on this connector is already
  committed (panel SPI0, buttons, battery sensing), so there's no spare pin
  left for CD. Presence (`SdCard::present()`) is inferred purely from
  whether `SD.begin()` succeeds — an absent addon board and an addon board
  with an empty slot currently look identical. A future hardware rev would
  need to free up a GPIO to tell them apart.
- Runs on its own SPI host (`FSPI`, distinct from the panel's `HSPI`), so it
  never contends with panel refreshes for the same peripheral.

`SdCard::begin()` is called once at boot (`main.cpp`) and mounts the card if
present. `Playlist::begin()` (`include/playlist.h`) then loads and validates
`playlist.json` from it — schema reverse-engineered from the real InkJoy
firmware (`ij_epd` v0.7.10, confirmed on real hardware 2026-08-02; full
writeup in `~/Source/gerald/decoded/ij_epd_h2_v0.7.10/` and that project's
`project_inkjoy_playlist_json.md` memory), so a playlist authored for a real
InkJoy frame's SD card should work here too, sizing aside. Prefers
`/ij_play/playlist.json`, falls back to `/playlist.json` at the root,
matching the real firmware's own preference order. Only `encrypt.enabled:
false` is supported — an encrypted playlist (real per-recipient
ECIES-P256/AES-GCM) is rejected outright rather than silently ignored, since
that scheme isn't implemented here. `loop()` cycles frames per
`schedule.interval_min`, independent of MQTT `play` (the InkJoy `.bin` format
itself is unchanged — `Panel::renderBinFromFile()` is the same row-streaming
decoder as `renderBinFromHttp()`, just reading a local file instead of an
HTTP stream).

Known gap, not yet resolved: the real firmware requires each frame file's
size to exactly match a value computed from `screen.panel`/`width`/`height`
(seemingly `width × height × bytes-per-pixel` for that panel — traced from
`ij_epd`'s own validator). This firmware doesn't enforce or use that field at
all; a frame that's the wrong byte count for what `Panel::renderBinFromFile`
expects (`INKJOY_BIN_WIDTH`/`INKJOY_BIN_HEIGHT` in `config.h`) will just read
garbage rows rather than being rejected up front.

## Setup

1. `cp include/secrets.h.example include/secrets.h`. Leave `NTP_TZ_INFO` at
   `"UTC0"` — joyous-hub converts sleep-window times you enter (local) to UTC
   before sending `wifi_sleep` over the wire (see `localHHMMToUTC` in
   `inkjoy_bridge_ui.go`), so the frame's own clock needs to stay plain UTC for
   the comparison to land on the right real-world time. Setting this to your
   real timezone will make the sleep window fire at the wrong time of day.
   WiFi/`HUB_HOST` fields in there are only a compile-time fallback (see
   below) — safe to leave as placeholders.
2. `pio run -t upload` (or open in PlatformIO IDE / VS Code extension).
3. On first boot (no saved config), the board advertises over BLE as
   `IJ_<MAC>` and shows a setup screen with that name on the panel. From
   joyous-hub's web UI, use the existing "scan for IJ_ frames" / "adopt"
   controls (`POST /api/inkjoy/ble/scan`, `/api/inkjoy/ble/adopt`) — the same
   ones already used to onboard real InkJoy frames — pick this board's name
   from the list, enter the WiFi network it should join, and adopt it. It
   saves the WiFi + hub connection info to NVS and reconnects normally from
   then on. No InkJoy app or account involved anywhere in this flow.

No changes needed on the hub side — it already speaks both the frame's MQTT
protocol (`joyous-hub/broker.go`, `play_relay.go`) and the BLE provisioning
wire format this board now implements (`joyous-hub/ble.go`), so it treats
this board like any other InkJoy device once it's adopted and logs in.

## Panel driver

Uses Seeed's `Seeed_Arduino_LCD` library (`EPaper` class, `T133A01_DRIVER`,
`BOARD_SCREEN_COMBO 510` — see `include/driver.h`). `panel.cpp` streams the
InkJoy `.bin` (1600×1200, 2 bytes/pixel: hi=color index, lo=wipe order,
bottom-to-top rows — see `encode_bin.py` at the repo root) row-by-row over
HTTP and draws it straight onto the panel via `drawPixel`, so peak RAM use is
one row buffer (3.2 KB) plus the library's own ~960 KB 4bpp frame buffer
(needs the board's PSRAM — `platformio.ini` enables it).

The `lo` (wipe order) byte is InkJoy-FPGA-specific animation data and is
ignored; this driver's own e-paper controller handles its own refresh
waveform.

Status/setup screens (`showProvisioning`, `showOrientation`,
`showFactoryReset`, `showConnected`) use Seeed_GFX's bundled GFXFF vector
fonts (`FreeSansBold24pt7b` / `FreeSans12pt7b` via `setFreeFont()`) rather
than the classic GLCD font scaled up with `setTextSize()` — real glyphs at
their point size instead of blown-up 8px bitmaps. See the font-chip note
under Known unverified-against-hardware items for why this is software-only.

## Known unverified-against-hardware items

- **Panel orientation** (`include/config.h`, `PANEL_ROTATION`): the bin is
  landscape 1600×1200, the panel is wired portrait 1200×1600. `setRotation(1)`
  should give the right landscape mapping, but which of 1 vs. 3 is "right side
  up" (vs. upside down / mirrored) depends on how the panel ends up mounted —
  flip it after the first real render.
- **Battery telemetry**: EE02 has no fuel-gauge IC. Pack voltage is read via
  A0/GPIO1 through an onboard ÷2 divider gated by D5/GPIO6, then mapped to a
  rough SoC for InkJoy `heart`/`sleep` `battery`. With USB plugged in and no
  pack, the charger biases the sense node (~full); trust readings most on
  battery-only power. Charge LED still indicates charging / no-battery.
- **60-pin FPC pinout match**: confirm the panel you have is electrically the
  same T133A01-family part EE02 expects before connecting it — same pin count
  doesn't guarantee same pinout.
- **GT32L32S0140 font chip absent**: Seeed's EE02 schematic shows a font/glyph
  ROM chip (U6, GT32L32S0140, SPI1 alongside the ZB25VQ32DSJG flash) with a
  full ASCII + GB2312 dot-matrix character set — genuine font hardware, not
  just a raster panel. But the schematic marks U6, its pull-up (R39), and its
  decoupling cap (C63) as not populated, and inspection of a physical board
  confirmed only the flash chip is present at that SPI1 footprint — the font
  chip was cost-cut from this board rev. So there's no on-board glyph ROM to
  drive; `panel.cpp`'s status screens use Seeed_GFX's bundled GFXFF vector
  fonts instead (see Panel driver below), which is the best text quality
  available without that chip.

## Build verification

Verified against the actual `Seeed_Arduino_LCD` library source (cloned and
inspected directly — `EPaper` class API, `T133A01_Defines.h` color constants,
4bpp pixel-packing behavior in `drawPixel`) rather than guessed from docs.
`pio run` builds clean end-to-end (RAM 17.8%, flash 36.3%) — the earlier
PlatformIO package-postinstall crash blocking a full build isn't reproducing
in this environment anymore.
