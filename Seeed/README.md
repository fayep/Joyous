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
`wifi_sleep` (+ `wifi_sleep_ack`), `shutdown`, and BLE provisioning (matching
joyous-hub's existing adopt flow — see Setup below). Deliberately **not**
implemented: OTA (ESP32 or FPGA), TF card, factory mode, and the real InkJoy
app/cloud's own provisioning path — none of that applies to a frame that only
ever talks to your own hub.

## Setup

1. `cp include/secrets.h.example include/secrets.h` and fill in your
   timezone's POSIX TZ string for `NTP_TZ_INFO`. WiFi/`HUB_HOST` fields in
   there are only a compile-time fallback (see below) — safe to leave as
   placeholders.
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

## Known unverified-against-hardware items

- **Panel orientation** (`include/config.h`, `PANEL_ROTATION`): the bin is
  landscape 1600×1200, the panel is wired portrait 1200×1600. `setRotation(1)`
  should give the right landscape mapping, but which of 1 vs. 3 is "right side
  up" (vs. upside down / mirrored) depends on how the panel ends up mounted —
  flip it after the first real render.
- **Battery telemetry** (`include/config.h`, `BATTERY_ADC_PIN`): EE02 has
  on-board LiPo charging (2-pin JST) but no fuel-gauge readout documented.
  `heart` reports a placeholder 100% until an ADC pin + divider are wired and
  characterized against real battery voltages.
- **60-pin FPC pinout match**: confirm the panel you have is electrically the
  same T133A01-family part EE02 expects before connecting it — same pin count
  doesn't guarantee same pinout.

## Build verification

Verified against the actual `Seeed_Arduino_LCD` library source (cloned and
inspected directly — `EPaper` class API, `T133A01_Defines.h` color constants,
4bpp pixel-packing behavior in `drawPixel`) rather than guessed from docs.
A full `pio run` toolchain build was attempted but blocked by a PlatformIO
package-postinstall crash in this environment — hasn't been confirmed to
compile end-to-end yet. Try `pio run` locally before flashing; report back if
anything doesn't compile so the protocol/panel code (not the toolchain
plumbing) can be fixed.
