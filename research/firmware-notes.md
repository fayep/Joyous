# InkJoy Frame — Firmware & Protocol Notes

## Overview

The InkJoy photo frame is an ESP32-C5 based e-paper display. The ESP32-C5 is a
RISC-V chip (not the older Xtensa core) with WiFi 6 and BLE 5. The e-paper panel
is driven by a separate FPGA or dedicated controller, not the ESP32 directly — the
two have independent firmware and must version-match at runtime.

**Device under study:** `AA:BB:CC:DD:EE:FF` (clientId `AABBCCDDEEFF`)  
**Firmware version:** v0.5.6, built May 11 2026 08:23:11  
**ESP-IDF version:** v5.5.2  
**Flash:** 16 MB, 80 MHz DIO  
**Project name:** `ij_epd`

### Hardware ICs (from boot log)
- `DA215S` — Dalian DA215S 3-axis accelerometer (drives `orientation` field in heart)
- `ETA9002` — battery charger IC
- SPIFFS: 8.4 MB total, 3.8 MB used (FPGA bitstream lives here)

### Boot identity string
Boot log prints: `PID:264 H:2 F:0.5.6`
- `PID` — likely screen size in mm (264 = 10"). Unconfirmed; 13.3" frame would be PID:338.
- `H` — hardware model: H:2 = 10", H:3 = 13.3" (confirmed via YouTube unboxing of 13.3" frame showing H:3 F:0.3.3)
- `F` — ESP32 firmware semver

---

## How We Got the Firmware

The OTA image is delivered over HTTP from the broker host after a REST trigger.
The full acquisition flow:

1. Run `impersonate_frame.py` with an old firmware version string to pose as an
   out-of-date device.
2. The REST endpoint `POST /inkjoy/api/v1/device/ota/{device_id}` triggers the
   server to queue an update.
3. The broker pushes an `ota` action to `/inkjoyap/{MAC}`:

```json
{
  "action": "ota",
  "data": { "host": "13.39.148.101", "port": 8080, "path": "/inkjoy/ota/{MAC}" },
  "msgid": "...",
  "stamac": "AA:BB:CC:DD:EE:FF"
}
```

4. Fetch immediately — the URL appears to be session-scoped:

```
curl http://13.39.148.101:8080/inkjoy/ota/AABBCCDDEEFF -o firmware.bin
```

The resulting file is a raw ESP32-C5 app partition image (not a full flash dump).
It does **not** include the bootloader, partition table, SPIFFS filesystem, or
FPGA bitstream — those are separate flash regions.

Inspect with:
```
esptool image-info firmware.bin
```

---

## Binary Structure

```
Image type : ESP32-C5 app partition
Size       : 1,838,208 bytes (~1.75 MB)
Entry point: 0x408002be
Checksum   : valid
Secure boot: none (image is unencrypted)
```

### Segments

| # | Load address | Size   | Type              | Notes                     |
|---|-------------|--------|-------------------|---------------------------|
| 0 | 0x42160020  | 295 KB | DROM / IROM       | Read-only data, log strings |
| 1 | 0x40800000  |  28 KB | DRAM / IRAM       | Initialised data + IRAM code |
| 2 | 0x42000020  | 1.3 MB | DROM / IROM       | Main code (largest segment) |
| 3 | 0x40807160  | 103 KB | DRAM / IRAM       | More data/code             |
| 4 | 0x40820d80  |  17 KB | DRAM / IRAM       |                           |
| 5 | 0x50000000  | 140 B  | RTC_IRAM/DRAM     | Deep-sleep stub            |
| 6 | 0x500000d4  |  32 B  | RTC_IRAM/DRAM     |                           |

Segment 0 (DROM) contains all log format strings and is the best source for
understanding the runtime. Extract it for analysis:

```python
import struct
data = open("firmware.bin","rb").read()
off = 0x18
for i in range(7):
    la, sl = struct.unpack_from('<II', data, off)
    if i == 0:
        open("seg0.bin","wb").write(data[off+8:off+8+sl])
    off = off + 8 + sl
```

Then:
```
strings -n 8 seg0.bin | grep '^\[0;3'   # all ESP_LOG lines
strings -n 6 seg0.bin | grep -v '^\['   # symbol names, paths, literals
```

---

## Runtime Architecture

These are ESP-IDF log tags (the `%s` in `I (%lu) %s: ...`), which correspond to
FreeRTOS tasks or component modules — **not** MQTT topic strings despite similar
naming:

| Tag                  | Purpose                                         |
|---------------------|-------------------------------------------------|
| `IJ_MAIN`           | Top-level application init and boot state machine |
| `IJ_NETMGR`         | WiFi + MQTT lifecycle manager                   |
| `IJ_BLE`            | BLE / BluFi provisioning                        |
| `IJ_POWER`          | Power key handling, deep/light sleep            |
| `IJ_STOR`           | SPIFFS filesystem, image storage                |
| `IJ_COMPAT`         | Backwards compatibility shims                   |
| `mqtt_play_inkjoy`  | Handles inbound `play` actions (image display)  |
| `mqtt_ota_inkjoy`   | Handles inbound `ota` actions (ESP32 firmware)  |
| `mqtt_fpga_ota_inkjoy` | Handles inbound `fpga_ota` actions (panel FW) |
| `app_init`          | Application information printed at boot         |
| `efuse_init`        | eFuse / chip revision checks                    |

---

## Message Schema Reference

Field names and types are extracted from firmware string constants and printf/scanf
format strings. `[C]` = confirmed from live capture, `[F]` = firmware strings only.

### Envelope (all messages)

```
{
  "action":   string          // dispatch key — see table below
  "clientid": string          // MAC without colons (frame-sent only)
  "stamac":   string          // MAC without colons in frame→broker; broker mirrors the same format back
  "msgid":    string          // unix milliseconds as string  [%lld]
}
```

### broker→frame actions

| Action | Data fields | Notes |
|--------|-------------|-------|
| `login_ack` | `ack_msgid` str, `device_id` str, `uid` str | [C] |
| `heart_ack` | _(unknown — frame just acks)_ | |
| `play` | `host` str, `port` int, `imgs` array, `mode` int, `strategy` int | [C] `imgs[].imgid` str, `imgs[].imgurl` str |
| `ota` | `host` str, `port` int, `path` str | [C] |
| `fpga` | `host`(?), `port`(?), `path`(?) | [F] action confirmed; fields assumed same as `ota` |
| `mqtt_config` | `bins` array(?) | [F] `bins` is the only field name found nearby |
| `wifimode` | _(unknown)_ | [F] |
| `strategy_bin` | `file` str, `maclist` str, `user` str, `pwd` str, `updatetype` str, `updatedays` str, `updatetimelist` str, `begintime` str `[%2[^:]:%2s]`, `endtime` str, `intervalminutes` int | [F] WiFi-MAC-filtered scheduled update |
| `strategy_stop` | _(unknown)_ | [F] |
| `down_int_img` | `width` int, `height` int, `imgcount` int | [F] downloads SPIFFS status images |
| `play_tf` | _(likely same as `play`)_ | [F] plays from TF card slot |
| `clean_tf` | _(unknown)_ | [F] |
| `image_refresh` | _(unknown)_ | [F] |
| `wifi_sleep` | `beginTime` str `[%d:%d]`, `endTime` str | [F] sleep window config |
| `device_config` | `batchKey` str, `uid` str, `deviceId` str | [F] pushes device credentials to NVS |
| `updev` | _(unknown)_ | [F] likely triggers a device info update |
| `live` | `timingTime` str `[%d-%d-%d %d:%d]` | [F] server timestamp / keep-alive |

### frame→broker actions

| Action | Data fields | Notes |
|--------|-------------|-------|
| `login` | `inkjoy` bool, `ver` str `["M H:%d F:%d.%d.%d"]` (H=hardware model, F=ESP32 fw), `statype` int, `sleep_mode` int, `sleep_begin_time` str `[%02d:%02d]`, `sleep_end_time` str | [C] |
| `heart` | `type` int, `ack` int, `wifi` str, `wifi_name` str, `ble` str, `tf` str `["absent"\|"present"]`, `tfsize` int, `tfused` int, `orientation` int, `battery` int, `wifi_listen_iv` int, `wifi_rssi` int, `wifi_ch` int, `ble_rssi` int, `version` str `[%d.%d.%d]` | [C] |
| `sleep` | `ack` int, `battery` int, `reason` int | [C] sent before power-off; **correction:** an earlier pass at this doc guessed the action name was `shutdown` with no `clientid` — a live capture (2026-07) shows the real action is `sleep`, and it *does* include `clientid`/`stamac` like other frame→broker messages. `reason` enum not fully mapped; `2` observed at 47% battery (nightly wifi_sleep window, not obviously low-battery-triggered). Triggers an ack (name unconfirmed — see note below).
| `play_ack` | `ack_msgid` str, `result` int | [C+F] bitfield — see **Ack result bitfield** below |
| `ota_ack` | `ack_msgid` str, `result` int | [F] same encoding as `play_ack` |
| `fpga_ota_ack` | _(unknown)_ | [F] wire reply to `"fpga"` action |
| `mqtt_config_ack` | `ack_msgid` str, `result` int | [F] |
| `strategy_bin_ack` | `ack_msgid` str, `result` int | [F] |
| `strategy_stop_ack` | _(unknown)_ | [F] |
| `down_int_img_ack` | `ack_msgid` str, `result` int | [F] |
| `image_refresh_ack` | _(unknown)_ | [F] |
| `wifimode_ack` | _(unknown)_ | [F] |
| `shutdown_ack` | `ack_msgid` str | [C] broker acknowledges frame power-off — **name unconfirmed**: captured when we assumed the frame→broker action was `shutdown`; now that the real action is known to be `sleep` (see above), the ack name may actually be `sleep_ack` instead. Needs re-checking against a live capture. |
| `image_refresh_ack` | `ack_msgid` str, `result` int | [C] result 113 = success (same complete composite as play done) |
| `wifi_sleep_ack` | `ack_msgid` str, `result` int | [C] result 106 = accepted (one-shot) |
| `device_config_ack` | _(unknown)_ | [F] |
| `strategy_ack` | _(unknown)_ | [F] |
| `clean_tf_ack` | _(unknown)_ | [F] |

### Format string key

| Pattern | Meaning |
|---------|---------|
| `"M H:%d F:%d.%d.%d"` | `ver` field in login — **H**=hardware model (compile-time constant, likely 1=13" / 2=10"), **F**=ESP32 firmware major.minor.patch |
| `"%d.%d.%d"` | `version` field in heart — F (ESP32 firmware) only; H is static so omitted after login |
| `"%02d:%02d"` | `sleep_begin_time` / `sleep_end_time` in login — HH:MM |
| `"%d:%d"` | `beginTime` / `endTime` in wifi_sleep — H:MM (no zero-pad) |
| `"%d-%d-%d %d:%d"` | `timingTime` in live — YYYY-M-D H:MM |
| `"%2[^:]:%2s"` | `begintime` / `endtime` scanf in strategy_bin — parses "HH:MM" |
| `"%d.%d.%d.%d"` | IPv4 address (seen in execmdtask / cmdserver context) |
| `"i%.2x%.2x%.2x%.2x%.2x%.2x"` | BLE advertising name — `i` + lowercase MAC hex |
| `"%02X%c%02X%c%02X%c%02X%c%02X%c%02X"` | MAC address with separator char |

---

## MQTT Protocol

### Credentials

Credentials come from the REST API (`GET /inkjoy/api/v1/version/serverInfo`), which
returns an AES-GCM encrypted blob. The decryption key is `uid[:16]` (first 16 chars
of the user's uid, which is returned by the login API).

Observed pattern (values are account-specific — do not commit real credentials):

```
host:     13.39.148.101
port:     1883
username: <from serverInfo>
password: <username + uid[:6]>
```

Credentials are per-account, not per-device.

The broker ACL restricts subscriptions to devices registered under the authenticated
account. Subscribing to an unregistered device's topic returns QoS 128 (denied).

### CONNECT behaviour

The real frame connects with:
- `clientID`: MAC without colons (`AABBCCDDEEFF`)
- `clean_session`: 0 (persistent session)
- `keepalive`: 120 s

### Topics

| Direction       | Topic                         | Actions                                              |
|----------------|-------------------------------|------------------------------------------------------|
| frame → broker | `/device/report/{MAC}`        | `login`, `heart`, `play_ack`, `fpga_ota_ack`        |
| broker → frame | `/inkjoyap/{MAC}`             | `login_ack`, `heart_ack`, `play`, `ota`, `fpga_ota`, `mqtt_config`, `mqtt_config_ack` |

### Message formats

**login** (frame → broker, on subscribe):
```json
{
  "action": "login", "clientid": "AABBCCDDEEFF", "stamac": "AABBCCDDEEFF",
  "msgid": "<unix_ms>",
  "data": { "inkjoy": true, "ver": "M H:2 F:0.5.6", "statype": 3,
            "sleep_mode": 2, "sleep_begin_time": "07:00", "sleep_end_time": "13:00" }
}
```

**login_ack** (broker → frame):
```json
{
  "action": "login_ack",
  "data": { "ack_msgid": "<login_msgid>", "device_id": "<uuid>", "uid": "<uuid>" },
  "msgid": "<unix_ms>", "stamac": "AABBCCDDEEFF"
}
```

**heart** (frame → broker, every 600 s / 10 min; `mqtt defaults: keepalive=120, heart=600`):
```json
{
  "action": "heart", "clientid": "AABBCCDDEEFF", "stamac": "AABBCCDDEEFF",
  "msgid": "<unix_ms>",
  "data": { "type": 3, "ack": 1, "wifi": "on", "wifi_name": "Novac",
            "ble": "off", "tf": "absent", "tfsize": 0, "tfused": 0,
            "orientation": 0, "battery": 73, "wifi_listen_iv": 50,
            "wifi_rssi": -58, "wifi_ch": 1, "ble_rssi": 0, "version": "0.5.6" }
}
```

Note: `ver` in `login` uses the full `"M H:<hw> F:<fw>"` format; `version` in
`heart` is the bare firmware number only.

**play** (broker → frame — image display):
```json
{
  "action": "play",
  "data": { "host": "ink-ufile.s3.eu-west-3.amazonaws.com", "port": 443,
            "imgs": [{"imgid": "...", "imgurl": "/88/xxxx.bin"}],
            "mode": 2, "strategy": 1 },
  "msgid": "...", "stamac": "AA:BB:CC:DD:EE:FF"
}
```

Image URLs point to S3. The frame downloads the `.bin` file, which is in the
native 2-bytes-per-pixel palette format (see `encode_bin.py`).

play_ack / ota_ack **result bitfield** (low byte):

| Bit | Val | Meaning |
|-----|-----|---------|
| 7 | 128 | Progress phase (download streaming) |
| 6 | 64 | Base flag (all observed acks) |
| 5 | 32 | Base flag |
| 4 | 16 | Complete phase |
| 3 | 8 | Accepted / work queued |
| 2 | 4 | Set during progress steps |
| 1 | 2 | Accepted / work queued |
| 0 | 1 | Success / done |

Composites (confirmed v0.5.6 + InkJoy Android app):

| Result | Bits | Meaning |
|--------|------|---------|
| 104 | 64+32+8 (106−2) | Play interrupted — second `play_ack` on same `ack_msgid`, frame drops soon after [C] |
| 106 | 64+32+8+2 | Accepted — work started |
| 182, 184, 186, 188 | 128+… (+2 steps) | Download 20%, 40%, 60%, 80% |
| 113 | 64+32+16+1 | Finished OK (play/ota/image_refresh complete) |

Accepted → complete: clear 8+2, set 16+1 (106 + 7 = 113). One-shot acks stop at 106.

Failed play (same `ack_msgid`): 106 (started) → 104 (106 − 2, bit 2 cleared) → MQTT drop,
with no 182+ progress and no 113. Seen before disconnect without DISCONNECT (sleep/battery/crash).

Hub intercept of `ota`/`fpga`: does not forward to frame; sends synthetic `ota_ack` /
`fpga_ota_ack` with the same 106 → 104 pair so cloud knows the push was not applied
(artifact still captured locally). See `buildBlockedOTAAcks` in `joyous-hub/inkjoy_ack.go`.
Progress formula: `percent = 20 + (result − 182) / 2 × 20` for the 182–188 series.
Hub helpers: `joyous-hub/inkjoy_ack.go` (`inkjoyProgressResult`, `inkjoyProgressPercent`).

Some older notes mention 255 (0xFF) as done; current app and captures use 113.

---

## Boot & Provisioning Sequence

### Normal boot (configured device)

```
power key → parainitcheck (NVS has config?)
  → YES: STA mode
      → WiFi connect (saved BSSID/SSID, dual-band probe)
      → MQTT connect → subscribe /inkjoyap/{MAC}
      → login → login_ack
      → heart loop (every 600 s)
```

### First boot / factory reset (no NVS config)

```
parainitcheck → no config found
  → scan for SMTap01..SMTap06 (factory WiFi SSIDs, RSSI threshold check)
      ├─ FOUND: factory mode
      │     → connect (password: 12345678) → cmdserver receives broker URL, clientId, mqtt creds
      │     → epdDisplayFactoryInfo (factory test image on panel)
      │     → FACTORY AUTO TEST 1..N (END)
      │     → write config to NVS → reboot → normal boot
      └─ NOT FOUND: user provisioning mode
            → epdDisplayMacInfo: render MAC barcode onto /spiffs/img_set.bin
            → BluFi advertising as "_InkJoy" (BLE scan response: "HV:%d FV:%d.%d.%d")
            → user opens InkJoy app → Add Device → scan barcode → account binding
            → BluFi custom_data: broker URL + clientId + mqtt creds (hypothesis)
            → BluFi WiFi config: SSID + password
            → WiFi connect → MQTT connect → normal boot
```

### Power key behaviour

- Short press: boot up from sleep
- Hold ~300 ms: enter provisioning mode (re-advertise BluFi)
- `server_try_times > 3`: device calls `enter_reset()` after repeated MQTT failures

### Provisioning barcode

The frame displays a **Code 128 1D barcode** (not a QR code) encoding the
`clientId` (MAC without colons). The InkJoy app's `ScanQRCodeActivity` uses ZXing
to scan it, then calls `POST /inkjoy/api/v1/device/...` to bind the device to the
account. `make_device_barcode.py` in this repo generates this screen for any MAC.

---

## FPGA Device

The e-paper panel controller is a **Gowin** FPGA running **e-paper display soft IP**
(likely [Caster](https://github.com/Modos-Labs/Caster) or a derivative). Gowin does
not offer a dedicated e-paper controller chip; instead developers implement display
controller logic directly in the FPGA fabric, using the FPGA's embedded PSRAM as a
frame buffer. This is a well-established pattern, with the **GW1NR-9** (as found on
the Tang Nano 9K) being the most common vehicle.

**Why GW1NR-9 specifically:**
- 8,640 LUTs — enough headroom for Caster plus supporting logic
- Embedded 8 MB PSRAM (the "R" suffix) — critical, since a 1600×1200 6-color panel
  needs ~960 KB per frame buffer (2 bits/pixel × 2 buffers); discrete RAM would add
  cost and board space
- IDCODE `0x0100681B` — will be confirmed when `fpga` OTA bitstream is captured
- CS0 / CS1 dual chip-selects match a dual-die panel where each Gowin drives one
  horizontal half (800×1200 each)

**Programming evidence from firmware strings:**
- `GOWIN EMBEDDED PROGRAMMING DEMO Base on ESP32-C5` — Gowin's own reference
  implementation, dated `Updated At 2025.11`
- Bitbang **JTAG** from ESP32-C5 GPIO (`Invalid FPGA bitstream data for JTAG
  programming`, tap-walking test, verify-after-program)
- Status register values `0x15421` / `0x35421` (pre/post erase) are Gowin GW1N
  family programming state machine values
- `Xpage:%d Ypage:%lu`, timing constants `6us_coude` / `15us_coude` / `30us_coude`
  are Gowin-specific flash programming pulse widths
- IDCODE read dynamically at programming time (`code_dev:%lx code_fsfile:%lx`) and
  compared against `fscode` embedded in the bitstream header — not hardcoded in the
  ESP32 app image

**Runtime interface:**
`FPGA_SPI` is a separate SPI bus used to stream **image data** to the FPGA at
runtime — distinct from the JTAG path used only during OTA programming. The
`epdDisplayFactColorBar` factory test and the `play` image pipeline both route
through this SPI interface.

---

## OTA System

There are **two independent OTA channels**:

### ESP32 app OTA (`mqtt_ota_inkjoy`)

- v0.5.6 has `secure_version: 0` — anti-rollback eFuse not yet armed. If 0.7.0
  ships with `secure_version: 1`, installing it burns the eFuse and the bootloader
  will permanently reject 0.5.6. The proxy auto-downloads the binary before the
  frame installs it, but to *stay* on 0.5.6 the `ota` push would need to be blocked
  at the proxy (`--block-unknown` with `ota` removed from `--allow-actions`).
- Triggered by `POST /inkjoy/api/v1/device/ota/{device_id}`
- Broker pushes `ota` action with HTTP URL to `/inkjoyap/{MAC}`
- Frame downloads from `http://13.39.148.101:8080/inkjoy/ota/{MAC}`
- **No authentication on the download** — plain HTTP, no headers required. The URL
  is the only gate; fetch it directly with curl. Appears session-scoped so fetch
  immediately after the MQTT push (the proxy auto-download handles this).
- Result is a standard ESP32-C5 app partition image

### FPGA / panel controller OTA (`mqtt_fpga_ota_inkjoy`)

- Broker→frame action is **`"fpga"`** (not `"fpga_ota"`); frame replies with `"fpga_ota_ack"`
- The FPGA bitstream is stored in the SPIFFS filesystem (not in the app image)
- If the SPIFFS bitstream version doesn't match the ESP32 app expectation,
  the device panics: `---!!!fs file and fpga device mismatching---`
- The FPGA bitstream OTA has not been captured yet

### SPIFFS image OTA

The provisioning/status screen images in SPIFFS are also independently updatable
via HTTP (`int_img_http_get_file`, `down_int_img_ack`). Version is tracked in
`/spiffs/intimg_config.json`.

---

## SPIFFS Filesystem

The SPIFFS partition is not included in the app image. Known files from firmware
log strings:

| Path                         | Purpose                                    |
|-----------------------------|--------------------------------------------|
| `/spiffs/img_set.bin`        | Background for provisioning/setup screen   |
| `/spiffs/img_connected.bin`  | Shown on successful WiFi connection        |
| `/spiffs/img_no_wifi.bin`    | Shown when WiFi cannot be reached          |
| `/spiffs/img_low_batt.bin`   | Low battery warning                        |
| `/spiffs/img_low_power_off.bin` | Powering off screen                    |
| `/spiffs/img_factory.bin`    | Factory test image (SMTap flow)            |
| `/spiffs/intimg_config.json` | Version tracking for updatable images      |

All `*.bin` files are in the same 2-bytes-per-pixel palette format as play images.
The provisioning screen (`img_set.bin`) has the MAC address dynamically rendered on
top by `epdDisplayMacInfo` at runtime.

---

## REST API

Base URL: `https://app.inkjoyframe.com`

All requests require HMAC-SHA256 signing:
```
sig = HMAC-SHA256(key=<sign-key-from-app-binary>,
                  msg=method+path+timestamp+nonce+sha256(body))
headers: X-Timestamp, X-Nonce, X-Signature
```

Authenticated endpoints also need `Authorization: Bearer {token}` and `uid: {uid}`.

| Method | Path                                    | Purpose                        |
|--------|-----------------------------------------|--------------------------------|
| POST   | `/inkjoy/api/v1/users/loginByEmail`     | Get token + uid                |
| GET    | `/inkjoy/api/v1/version/serverInfo`     | MQTT broker config (encrypted) |
| POST   | `/inkjoy/api/v1/device/list`            | List devices with version info |
| POST   | `/inkjoy/api/v1/device/ota/{device_id}` | Trigger OTA push               |

`serverInfo` response is AES-GCM encrypted: `key = uid[:16].encode()`,
`nonce = data[:12]`, `ciphertext = data[12:-16]`, `tag = data[-16:]`.

---

## Tools in This Repo

| File                    | Purpose                                                       |
|------------------------|---------------------------------------------------------------|
| `impersonate_frame.py` | Connect as a specific MAC, send login+heart, capture OTA push |
| `inkjoy-proxy/`        | Go MITM proxy (EdgeRouter X); intercepts all MQTT traffic     |
| `encode_bin.py`        | Convert images to native `.bin` palette format                |
| `decode_bin.py`        | Decode `.bin` files back to PNG                               |
| `samsung_serve.py`     | Samsung EM32DX MDC push (different device, different protocol)|
| `make_device_barcode.py` | Generate provisioning barcode screen for any MAC            |
| `research/inkjoy_ota.py` | REST API client with OTA trigger + MQTT listener            |
| `research/inkjoy_mqtt.py` | MQTT explorer / frame spoofer with full API auth           |

---

## Investigations Done

**MQTT credential derivation** — password = username + uid[:6] (account-scoped).
Confirmed by comparing captured CONNECT packet to login_ack uid field.

**OTA trigger** — spoofing an old firmware version in `login`/`heart` messages does
not alone trigger an OTA push; the REST endpoint must also be called. The server
tracks the reported version but the push is manual/triggered.

**Firmware acquisition** — successfully captured v0.5.6 app image via the OTA flow.
Image is unencrypted, passes checksum validation.

**Boot sequence reconstruction** — derived entirely from ESP_LOG format strings in
the firmware binary without running the device. Factory SMTap SSIDs (SMTap01–06)
and provisioning barcode format discovered this way.

**FPGA OTA** — existence confirmed from firmware strings (`mqtt_fpga_ota_inkjoy`,
`fpga_ota_ack`, filesystem mismatch panic). Bitstream not yet captured.

**Factory WiFi password** — SMTap01–SMTap06 all use WPA2 password `12345678`, baked
into the firmware as string literals alongside the SSID list. Any device on the factory
floor with a phone can join the factory network and interact with the cmdserver flow.

**FPGA OTA wire action** — the broker sends `{"action":"fpga",...}`, not `"fpga_ota"`.
The frame task and ack are named `mqtt_fpga_ota_inkjoy` / `fpga_ota_ack`, but the
on-wire JSON action key is just `"fpga"`. Data field names are likely the same as `ota`
(`host`/`port`/`path`) based on shared download infrastructure, but not confirmed —
`host`, `port`, `path` don't appear as standalone string constants in the binary.

**BluFi custom_data contents** — the broker URL, clientId, and MQTT credentials
delivered to the device during provisioning come through `send_custom_data` in the
BluFi exchange. Exact byte format not yet captured (requires BLE sniffer during
initial pairing of a fresh device).

---

## Open Questions

- **BluFi custom_data format** — what exactly gets written to NVS during
  provisioning. A BLE sniffer during first pairing of the larger frame (arriving
  later) will answer this.

- **FPGA bitstream and `fpga` message shape** — data fields are assumed to be
  `host`/`port`/`path` (same as `ota`) but not confirmed. The proxy will capture
  the first `fpga` push to `/tmp/ij-ota/*.fs`. The bitstream header will contain
  the device IDCODE — predicted `0x0100681B` (GW1NR-9). If confirmed, the soft IP
  is almost certainly Caster or a fork.

- **SPIFFS image format** — assumed to be the same 2-bytes-per-pixel palette format
  as play images, but not confirmed. Capturing `img_set.bin` from the SPIFFS
  partition would confirm this and let us replace the provisioning screen image.

- **cmdserver protocol** — what the factory SMTap flow sends. Probably a simple
  JSON blob over TCP, but the port and format are unknown.

- **`mqtt_config` action** — the server can push new broker config to a running
  device. Format and trigger conditions unknown.

- **`wifi_sleep` schedule** — the device has a configurable deep-sleep window
  (`sleep_begin_time`, `sleep_end_time`, `sleep_mode`). How `updatetype`,
  `updatedays`, `updatetimelist` interact with this is not yet mapped.

- **`sleep` ack name and `reason` enum** — a 2026-07 capture confirmed the
  power-off action is `sleep` (not the previously guessed `shutdown`), with
  `data: {ack, battery, reason}` and normal `clientid`/`stamac` fields. Only
  `reason: 2` has been observed (at 47% battery — plausibly the scheduled
  wifi_sleep window). The ack action name is still whatever was captured
  under the old `shutdown`/`shutdown_ack` assumption and needs
  re-confirming as `sleep_ack` (or whatever it actually is) against a fresh
  capture.
