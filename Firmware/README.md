# Frame firmware — InkJoy protocol, shared across boards

Custom firmware speaking the InkJoy MQTT protocol against **joyous-hub**'s
local broker, so a frame receives photos the same way a real InkJoy frame
does. One shared PlatformIO project, board selection and SD capability
gated by compile-time flags in `include/config.h` — see
`platformio.ini`'s two `[env:...]` sections. Origin: the real InkJoy
frame's ESP32-C5 can't be ported to (different ISA — RISC-V vs. these
boards' Xtensa), and its panel is driven by a proprietary Gowin FPGA we
don't have firmware for, so this is a clean-room reimplementation of just
the wire protocol, targeting each board's own standard e-paper driver IC.

Daytime networking uses ESP-IDF **esp-mqtt** (event-driven; the Arduino loop
blocks on a FreeRTOS event/timeout instead of busy-polling) plus
`WIFI_PS_MIN_MODEM` so the radio can nap between AP beacons while staying
MQTT-reachable for `play`.

Protocol reference: `research/firmware-notes.md` at the repo root has the full
reverse-engineered writeup this firmware implements against.

## Boards

| Env | Board | Hardware notes | SD |
|---|---|---|---|
| `xiao_esp32s3` | Seeed XIAO ESP32-S3 + EE02 (13.3" Spectra-6) | `Seeed/README.md` | On (addon board) |
| `goodisplay_c02` | GooDisplay ESP32-133C02 | `GooDisplay/README.md` | Off (pins unknown — SD-variant board not yet in hand) |

`include/config.h` dispatches to `config_seeed.h` or `config_goodisplay.h`
based on which board's `-DBOARD_SEEED`/`-DBOARD_GOODISPLAY` build flag is
set. `ENABLE_SD_CACHE` (also per-board) compiles the whole SD/playlist
subsystem (`sd_card.cpp`/`playlist.cpp`, `main.cpp`'s call sites) in or out
entirely — not just a runtime "no card found," an actually SD-free build.

The panel driver is the one piece that's genuinely different per board:
`panel_seeed.cpp` (Seeed_GFX's `EPaper` class) and `panel_goodisplay.cpp`
(direct ESP-IDF `driver/spi_master.h`/`driver/gpio.h` calls, ported from
GooDisplay's own vendor reference source) both implement the same shared
`Panel::` interface (`include/panel.h`) — everything else (MQTT protocol,
BLE provisioning, sleep/battery logic, P2 calibration) is one shared
implementation, board-agnostic.

## Scope

Implemented: `login`, `heart`, `play` (+ `play_ack` progress/done/fail),
`wifi_sleep` (+ `wifi_sleep_ack`), `shutdown`, BLE provisioning (matching
joyous-hub's existing adopt flow — see Setup below), a critical-battery
cutoff where the board has known battery ADC pins (see
`BATTERY_LOW_PCT`/`BATTERY_CRITICAL_PCT` in each board's config header):
at or below 10% the frame shows a low-battery panel message, sends a
`sleep` with a low-battery reason so the hub knows why it went dark, and
deep-sleeps an hour before rechecking; at or below 5% it stops self-waking
entirely and needs a button or power cycle to come back. Deliberately
**not** implemented: OTA (ESP32 or FPGA), factory mode, and the real
InkJoy app/cloud's own provisioning path — none of that applies to a frame
that only ever talks to your own hub.

## SD card / local playlist (where `ENABLE_SD_CACHE` is on)

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
HTTP stream). See `research/sd-content-cache-plan.md` for the planned
content-addressed hub-driven sync on top of this.

Known gap, not yet resolved: the real firmware requires each frame file's
size to exactly match a value computed from `screen.panel`/`width`/`height`.
This firmware doesn't enforce or use that field at all; a frame that's the
wrong byte count for what `Panel::renderBinFromFile` expects
(`INKJOY_BIN_WIDTH`/`INKJOY_BIN_HEIGHT`) will just read garbage rows rather
than being rejected up front.

## Setup

1. `cp include/secrets.h.example include/secrets.h`. Leave `NTP_TZ_INFO` at
   `"UTC0"` — joyous-hub converts sleep-window times you enter (local) to UTC
   before sending `wifi_sleep` over the wire (see `localHHMMToUTC` in
   `inkjoy_bridge_ui.go`), so the frame's own clock needs to stay plain UTC for
   the comparison to land on the right real-world time. Setting this to your
   real timezone will make the sleep window fire at the wrong time of day.
   WiFi/`HUB_HOST` fields in there are only a compile-time fallback (see
   below) — safe to leave as placeholders.
2. `pio run -e xiao_esp32s3 -t upload` or `pio run -e goodisplay_c02 -t upload`
   (or select the environment in PlatformIO IDE / VS Code extension).
3. On first boot (no saved config), the board advertises over BLE as
   `IJ_<MAC>` and shows a setup screen with that name on the panel. From
   joyous-hub's web UI, use the existing "scan for IJ_ frames" / "adopt"
   controls (`POST /api/inkjoy/ble/scan`, `/api/inkjoy/ble/adopt`) — the same
   ones already used to onboard real InkJoy frames — pick this board's name
   from the list, enter the WiFi network it should join, and adopt it. It
   saves the WiFi + hub connection info to NVS and reconnects normally from
   then on. No InkJoy app or account involved anywhere in this flow.

No changes needed on the hub side — it already speaks both the frame's MQTT
protocol (`joyous-hub/inkjoybridge/`) and the BLE provisioning wire format
this firmware implements, so it treats either board like any other InkJoy
device once it's adopted and logs in. `inkjoybridge.HardwareModelSeeed`
(9) / `HardwareModelGooDisplay` (10) tell the two apart where it matters
(P2 calibration pre-fill).

## GooDisplay panel driver — not yet hardware-verified

`panel_goodisplay.cpp` ports GooDisplay's own working ESP-IDF reference
driver (`GooDisplay/docs/vendor_code_example_usb_variant/comm.c` +
`GDEP133C02.c`) directly — same register sequences, same SPI transaction
shape. What's *not* yet confirmed against real hardware: the row/column
remap from the landscape InkJoy `.bin` into this panel's portrait CS0/CS1
(M/S) halves (`GOODISPLAY_ROTATE_CW` in `panel_goodisplay.cpp` — same
category of item as Seeed's own `PANEL_ROTATION`, below). Status/setup
screens are solid-color placeholders (no text-rendering pipeline built for
this raw driver yet, unlike Seeed's GFXFF fonts). Battery ADC and button
GPIOs aren't known for this board yet (`config_goodisplay.h`) — those code
paths are compiled out via `-1` pin sentinels rather than guessed.

## Known unverified-against-hardware items (Seeed)

See `Seeed/README.md` for the Seeed-specific hardware notes (panel
orientation, battery telemetry, FPC pinout, the absent font chip).

## Build verification

`pio run -e xiao_esp32s3` and `pio run -e goodisplay_c02` both build clean
end-to-end. Seeed's build verified against the actual `Seeed_Arduino_LCD`
library source (cloned and inspected directly — `EPaper` class API,
`T133A01_Defines.h` color constants, 4bpp pixel-packing behavior in
`drawPixel`) rather than guessed from docs. GooDisplay's build compiles
clean but hasn't been flashed to real hardware yet.
