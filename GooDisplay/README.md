# GooDisplay S3 e-paper board

New board from GooDisplay: ESP32-S3 + built-in SD, same Spectra6 e-paper connector
(1600x1200) as the 13" Seeed clone. Unlike the Seeed board, no separate SD addon
is needed here — SD is on-board.

**A second unit is on order**: ESP32-133C02-2 (SD firmware variant) paired with
GooDisplay's **7.09" E6 panel** — same 1200x1600 pixel resolution as the 13.3"
panel in a much smaller physical size, so noticeably higher PPI. (The SD GPIO
pin mapping that originally motivated this — this unit's `-3`/WiFi firmware
never touches the SD pins — was resolved a different way, from GooDisplay's
own downloadable board specification; see "GPIO pin mapping" below. Still
worth dumping/comparing once the second unit arrives.)

## Hardware (as dumped 2026-08-07)

- Chip: ESP32-S3 (QFN56, rev v0.2), 8MB embedded PSRAM (AP_3v3), dual core + LP core, 240MHz
- MAC: `a4:cb:8f:c7:b6:84`
- Flash: 16MB, quad SPI (manufacturer 0x46, device 0x4018)
- Connected via `/dev/cu.usbserial-10` (USB-serial adapter, not native USB CDC)
  — 921600 and 460800 baud both dropped the esptool connection mid-read; 115200
  baud completed cleanly (~25 min for the full 16MB).

## Partition table (offset 0x8000)

| type | subtype | offset     | size      | label    |
|------|---------|------------|-----------|----------|
| 1    | 2       | 0x00009000 | 0x5000    | nvs      |
| 1    | 0       | 0x0000e000 | 0x2000    | otadata  |
| 0    | 16      | 0x00010000 | 0x300000  | app0     |
| 1    | 130     | 0x00310000 | 0xe0000   | spiffs   |
| 1    | 3       | 0x003f0000 | 0x10000   | coredump |

Single app slot (no OTA A/B split, unlike the InkJoy H2/H3 units). `spiffs` is
entirely blank (`0xFF`) — unused on this unit.

## Firmware identity

- Built with **Arduino IDE** (not PlatformIO/ESP-IDF directly) — FQBN string found
  in the image: `esp32:esp32:esp32s3:UploadSpeed=921600,USBMode=hwcdc,CDCOnBoot=default,
  MSCOnBoot=default,DFUOnBoot=default,UploadMode=default,CPUFreq=240,FlashMode=qio,
  FlashSize=4M,PartitionScheme=huge_app,DebugLevel=none,PSRAM=opi,LoopCore=1,
  EventsCore=1,EraseFlash=none,JTAGAdapter=default,ZigbeeMode=default`
- ESP-IDF: v5.5-1-gb66b5448e0 (Arduino core's underlying IDF)
- App version string: `afa5cdd` (looks like a git short-hash), compiled Jul 22 2025
- "Project name" reads as `arduino-lib-builder` — that's just the generic Arduino
  core wrapper name, not a real app identifier (expected for any Arduino sketch).

This is a **much simpler, self-hosted reference/demo firmware**, not a cloud-gated
product like the InkJoy units:
- Runs its own HTTP server + WiFi AP config portal (`sta.ssid`/`sta.bssid` NVS keys,
  `<h2>home WiFi network</h2>` setup form)
- Endpoints: `/upload` (POST image data), `/switchToRealTime`, `/switchToSlideShow`
- **All dithering happens client-side in the browser**, then the already-dithered
  pixel data is POSTed to `/upload` — the ESP32 side doesn't compute dithering itself.

## Extracted web UI

`dumps/extracted/webui.html` — full embedded page pulled straight out of `app0.bin`
(`<!DOCTYPE html>` … `</html>`, 34KB, 881 lines). Client-side JS implements 5 dither
algorithms selectable in a dropdown (Chinese-language UI labels, consistent with
GooDisplay being a Dalian-based vendor):
- `floydDither` — standard Floyd-Steinberg
- `floydSerpentineDither` — serpentine-scan variant (default-selected)
- `atkinsonDither`
- `stuckiDither`
- `jjnDither` — Jarvis-Judice-Ninke

Palette constants (matches Spectra6 six-color panel):
```js
const REAL_PALETTE   = [[255,255,255],[0,0,0],[160,32,32],[240,224,80],[80,128,184],[96,128,80]];
const DRIVER_PALETTE = [[255,255,255],[0,0,0],[255,0,0],[255,255,0],[0,0,255],[0,255,0]];
```
`REAL_PALETTE` is the perceptual/display color, `DRIVER_PALETTE` is what actually
gets sent to the panel driver per color index — worth comparing against joyous-hub's
own `color.go` `PaletteReflection` ink RGBs for the InkJoy Spectra6 panel.

## Files in dumps/

- `firmware-a4cb8fc7b684-20260807-123054.full.bin` — full 16MB flash dump (WiFi / -3)
- `app0.bin` — extracted WiFi app partition (3MB)
- `spiffs.bin` — extracted spiffs partition (896KB, all-blank on this unit)
- `extracted/webui.html` — extracted embedded web UI
- `firmware-sd-flash-utility.full.bin` — 4MB flash image of the SD-card firmware variant (was misnamed `sd_app0.bin`; bootloader at `0x0`, same partition table as the WiFi unit)
- `sd_app0.bin` — app0 cut from that image (`0x10000`, 3MB, entry `0x40375f34`)
- `sd_flash_utility_app0_segments/` — loadable segments from that app0

## Not yet done

- No UART/JTAG console log captured yet (only a cold flash read so far, board not
  power-cycled with `screen`/serial monitor attached to see live boot output)
- SD card behavior on this board (mount path, file format) not yet probed
- Haven't compared client-side dither JS output against `joyous-hub/dither.go`'s
  Stucki implementation for algorithmic differences

## Board identity (2026-08-07, resolved via photo + vendor docs)

Board silkscreen: **ESP32-133C02** (Dalian GooDisplay). Confirmed via vendor
product manual (`docs/ESP32-133C02-X_product_manual.pdf`) that GooDisplay ships
**four separate firmware variants for the same physical board** — you must pick
one at purchase time, each is a full separate flash:

- **ESP32-133C02-1**: USB-only (PC "Spectra 6 Tool" pushes images over serial)
- **ESP32-133C02-2**: SD card — parses `image0.bmp`, `image1.bmp`, ... off the
  card, auto-advances ~every 2 min. **This is the variant with real SD code —
  we don't have this firmware, only ever dumped -3.**
- **ESP32-133C02-3**: WiFi web app ("NeoFrame") — **confirmed this is what's on
  your board.** Manual's screenshots of the NeoFrame page ("Upload Image",
  "Switch To Real-time Mode", "Switch To Slideshow Mode") match verbatim
  against strings/JS pulled straight out of the flash dump.
- **ESP32-133C02-4**: fixed demo/test image cycling, no network or storage

So your reading that "it looks kinda opaque, they may not have included SD
code" was exactly right — SD support is real hardware + real vendor firmware,
just a different SKU/flash than what shipped on this specific unit.

### Physical SD slot
Labeled **CARD1** on the board silkscreen, top-left corner next to buttons
SW1/SW2/SW4 (see `docs/board_photos/sd_slot_zoom.jpg`). Push-push microSD
socket, 9-pad row visible (8 signal pins + mechanical card-detect switch pin,
standard for this socket type). An unmarked 8-pin SOIC sits nearby along with
a block of ~10 small resistors (R43/44/46/47/49/50/51/52/106/107) — consistent
with series/pull-up resistors on the SD lines, but the IC's top marking wasn't
legible at photo resolution.

### GPIO pin mapping — resolved (2026-08-11)
The vendor's own official board specification
(`docs/esp32_133c02_specification.pdf`, downloaded from the product page —
https://www.good-display.com/product/574.html — not something previously
pulled from a firmware dump or continuity trace) documents the complete
GPIO pinout directly, Section 3:

```c
// SD card (Section 3.2)
#define SD_CS    15
#define SD_MOSI   3
#define SD_MISO   5
#define SD_SCK    8

// EPD (Section 3.3)
#define EPD_MOSI 41
#define EPD_MISO 40
#define EPD_CS_M 18
#define EPD_CS_S 17
#define EPD_RST   6
#define EPD_DC    2   // matches the independently hand-traced + firmware-
                      // disassembly-confirmed finding in Firmware/include/
                      // tft_setup_goodisplay.h -- three independent methods,
                      // same answer
#define EPD_BUSY  7
#define EPD_SCK   9

// Function buttons (Section 3.1) -- pin numbers known, but not which
// button maps to which InkJoy-side function (orient vs. factory reset)
#define SW2 12
#define SW3 13
#define SW4 14
```
Wired into `Firmware/include/config_goodisplay.h` (`ENABLE_SD_CACHE=1`,
`SD_SPI_*_PIN`/`SD_CS_PIN`). Buttons still unwired -- pin numbers known now,
but the semantic mapping isn't documented, so assigning them to
`BTN_ORIENT_PIN`/`BTN_RESET_PIN` would be guessing at behavior, not just a
pin number.

The same spec (Section 4.5, "Dual-IC Programming Analysis") also directly
confirms the CS0(M)/CS1(S) split is left/right by column (each half
diagrammed as `X (0-599)`, full `Y (0-1599)` height) -- matching what
`Firmware/src/panel_goodisplay.cpp`'s bring-up determined empirically via
`rulerTest()` before this document was found.
