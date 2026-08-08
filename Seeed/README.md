# Seeed EE02 (ESP32-S3) — hardware notes

Physical hardware notes for the Seeed XIAO ESP32-S3 + EE02 driver board
(13.3" Spectra-6 / T133A01 panel). The firmware itself now lives in
`Firmware/` (a shared PlatformIO project also targeting GooDisplay's
ESP32-133C02 board — see `Firmware/README.md`), built as env `xiao_esp32s3`
with `-DBOARD_SEEED`. This directory is docs-only, mirroring how
`GooDisplay/` holds that board's own hardware notes.

## microSD addon board

A snap-on addon board taps a second SPI bus (schematic name `SPI1`) already
broken out on the B2B connector alongside the onboard flash chip and the
unpopulated font-chip footprint (see below), plus one extra GPIO for chip
select, to add a microSD slot. See `SD_SPI_*_PIN`/`SD_CS_PIN` in
`Firmware/include/config_seeed.h` for the exact pins and the reasoning:

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

## Panel driver

Uses Seeed's `Seeed_Arduino_LCD` library (`EPaper` class, `T133A01_DRIVER`,
`BOARD_SCREEN_COMBO 510` — see `Firmware/include/driver.h`). Streams the
InkJoy `.bin` (1600×1200, 2 bytes/pixel: hi=color index, lo=wipe order,
bottom-to-top rows) row-by-row and draws it straight onto the panel via
`drawPixel`, so peak RAM use is one row buffer (3.2 KB) plus the library's
own ~960 KB 4bpp frame buffer (needs the board's PSRAM).

The `lo` (wipe order) byte is InkJoy-FPGA-specific animation data and is
ignored; this driver's own e-paper controller handles its own refresh
waveform.

Status/setup screens use Seeed_GFX's bundled GFXFF vector fonts
(`FreeSansBold24pt7b` / `FreeSans12pt7b` via `setFreeFont()`) rather than the
classic GLCD font scaled up with `setTextSize()` — real glyphs at their
point size instead of blown-up 8px bitmaps. See the font-chip note below for
why this is software-only.

## Known unverified-against-hardware items

- **Panel orientation** (`PANEL_ROTATION` in `config_seeed.h`): the bin is
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
  drive; the panel driver uses Seeed_GFX's bundled GFXFF vector fonts
  instead, which is the best text quality available without that chip.

## Build verification

Verified against the actual `Seeed_Arduino_LCD` library source (cloned and
inspected directly — `EPaper` class API, `T133A01_Defines.h` color constants,
4bpp pixel-packing behavior in `drawPixel`) rather than guessed from docs.
`pio run -e xiao_esp32s3` (from `Firmware/`) builds clean end-to-end.
