# Joyous bridge architecture

Joyous Hub is split into a **core hub** plus optional **vendor bridges**. Users
who do not own InkJoy or Samsung frames do not need to run the corresponding
bridge.

The InkJoy bridge migration is **complete**. Samsung is **in progress**: discovery
is delegated; image encode, frame pull serving, and most UI APIs still run on
the hub. See [Samsung migration](#samsung-migration) for the target design.

Nixplay is a fourth bridge kind, cloud-only (no LAN device, no frame pull —
Nixplay's own backend syncs uploaded photos out to frames). See
[Nixplay bridge](#nixplay-bridge) below.

## Processes

| Binary | Build | Role |
|--------|-------|------|
| `joyous-hub` | `make build` | Albums, UI, image pipeline, Joyous MQTT broker, frame content cache |
| `inkjoy-bridge` | `make build-inkjoy-bridge` | InkJoy frame MQTT + cloud upstream + encode |
| `samsung-bridge` | `make build-samsung-bridge` | Samsung SSDP/MDC + encode + push (target) |
| `nixplay-bridge` | `make build-nixplay-bridge` | Nixplay mobile API sign-in + gallery list + S3 upload |

Build all: `make build-all`

Default ports (macOS install):

| Service | Port | Notes |
|---------|------|-------|
| Hub HTTP | `:18080` | Albums, `/images/…`, frame content cache |
| Hub MQTT | `:1883` | Bridges connect here |
| InkJoy frame MQTT | `:11883` | Frames connect to inkjoy-bridge |
| Samsung bridge HTTP | `:18082` | Legacy/direct; **target:** hub `:18080` serves frame pulls |

## Design principles

These rules came out of the InkJoy migration and apply to Samsung too.

1. **Hub owns album assets; bridge owns vendor encode.** The hub sends
   `send.image` with `hub_base_url`, crops, and optional overlay data. The bridge
   fetches `/images/{id}/original`, encodes, and writes frame-ready bytes to disk.

2. **Frame downloads are served from hub disk, never the MQTT HTTP tunnel.**
   Large payloads (InkJoy `.bin`, Samsung `.png`) must hit the hub HTTP server
   directly. The tunnel is for small UI/API traffic (~20–45s timeout).

3. **Shared `hub_data_dir`.** Bridge `hub_data_dir` must match hub `config.yaml`
   `data_dir` exactly (case-sensitive volumes matter). The bridge writes cache
   files; the hub serves them.

4. **Long-running bridge commands run off the MQTT thread.** `send.image`,
   BLE scan/adopt, and similar work run in a goroutine on the bridge side so
   tunneled UI HTTP and other MQTT traffic stay responsive.

5. **Delivery tracking crosses the bridge boundary via MQTT.** Hub
   `Register()` creates a send id → bridge binds vendor-specific keys → bridge
   publishes `send.complete` (with optional `phase`) → hub updates UI.

6. **Bridge registers devices; hub merges snapshots.** Each bridge publishes
   `devices.sync` and `device.update`; the hub Devices tab is vendor-neutral.

## Joyous MQTT protocol

Bridges connect to the hub broker (`listen_mqtt`, default `:1883`).

### Topics

```
joyous/bridge/{bridge_id}/presence   bridge → hub (retained hello)
joyous/bridge/{bridge_id}/devices    bridge → hub (retained snapshot)
joyous/bridge/{bridge_id}/device       bridge → hub (single delta)
joyous/bridge/{bridge_id}/event        bridge → hub (ephemeral)
joyous/bridge/{bridge_id}/ui           bridge → hub (retained tab state, HTTP responses)
joyous/hub/{bridge_id}/cmd             hub → bridge (commands)
joyous/hub/{bridge_id}/ui              hub → bridge (UI actions, HTTP requests)
```

`bridge_id` is the bridge kind: `inkjoy` or `samsung`.

Types and payloads live in `joyous-hub/protocol/`.

### Message types

| Type | Direction | Purpose |
|------|-----------|---------|
| `bridge.hello` | bridge → hub | Announce kind, capabilities, listen addresses |
| `devices.sync` | bridge → hub | Full device list |
| `device.update` | bridge → hub | Single device delta |
| `device.remove` | bridge → hub | Remove device |
| `bridge.event` | bridge → hub | Named event (logging, future hooks) |
| `ui.state` | bridge → hub | Bridge-owned tab JSON state |
| `ui.http.request` / `ui.http.response` | hub ↔ bridge | HTTP-over-MQTT proxy |
| `bridge.cmd` | hub → bridge | Command envelope (`CmdPayload`) |
| `ui.action` | hub → bridge | Structured UI user action |
| `send.complete` | bridge → hub | Send delivery progress/completion |
| `mqtt.logs` | bridge → hub | Optional frame MQTT log stream |

### Commands (`bridge.cmd`)

| Command | InkJoy | Samsung (target) | Notes |
|---------|--------|------------------|-------|
| `discover` | — | ✓ | SSDP + MDC LAN sweep |
| `send.image` | ✓ | ✓ (target) | Async; see [Image sends](#image-sends) |
| `display.refresh` | ✓ | — | InkJoy image refresh MQTT |
| `sleep.set` | ✓ | — | InkJoy wifi sleep |
| `mqtt.redirect` | ✓ | — | Push mqtt_config to frame |
| `samsung.push` | — | ✓ (target) | MDC content download after encode |
| `samsung.wake` | — | ✓ (target) | Magic wake + MDC |
| `samsung.sleep` | — | ✓ (target) | MDC standby |
| `samsung.config` | — | ✓ (target) | Per-frame config write |
| `ble.scan` / `ble.adopt` | ✓ | — | InkJoy provisioning |

Capabilities (in `bridge.hello`): `config.ui` means the bridge serves a vendor
configuration page tunneled at `/{bridge_id}/`.

## HTTP route ownership

Routes are registered on the hub mux in this order: album/API routes → **frame
content cache** → `/{bridge_id}/…` MQTT proxy.

| Prefix | Served by | Tunnel? | Examples |
|--------|-----------|---------|----------|
| `/images/…` | joyous-hub | — | originals, previews, web UI |
| `/inkjoy/{mac}/*.bin` | joyous-hub disk | **never** | frame album/cloud play downloads |
| `/inkjoy/api/…`, `/inkjoy/` UI | inkjoy-bridge | yes (~45s) | config UI, frame MQTT logs |
| `/samsung/{frameId}/content.json` | joyous-hub disk (target) | **never** | frame poll manifest |
| `/samsung/{frameId}/image`, `…/status`, `/{frameId}.png` | joyous-hub disk (target) | **never** | frame image pull |
| `/api/samsung/…` | samsung-bridge (target) | yes (~20s) | UI list, push, wake, config |
| `/samsung/…` (today, bridge :18082) | samsung-bridge | — | **legacy** direct serve during migration |

Hub-only APIs (not proxied):

- `GET /api/bridges` — online bridges and capabilities
- `GET/POST /api/bridge/{kind}/ui` — bridge tab state and actions
- `GET /api/send/{send_id}` — send delivery status (with optional `?wait=`)

Proxy timeouts (`bridge_routes.go`): default 20s; InkJoy `/inkjoy/api/` 45s;
BLE routes 90s.

## Image sends

The hub does **not** vendor-encode before sending to a bridge. Flow:

```
Hub UI send
  → hub sendDelivery.Register()           # creates send_id
  → MQTT bridge.cmd send.image            # async goroutine on bridge
  → bridge: GET hub /images/{id}/original
  → bridge: encode (crop, overlay, color pipeline)
  → bridge: fsync write to {hub_data_dir}/{vendor}/…
  → bridge: HEAD/GET probe hub cache URL
  → bridge: vendor-specific notify frame
  → frame: HTTP GET hub cache
  → bridge: send.complete (phases) → hub UI
```

### `send.image` body (`SendImageBody`)

```json
{
  "image_id": "…",
  "hub_base_url": "http://hubhost:18080",
  "crops": { "4:3": { "x": 0, "y": 0.1, "w": 1, "h": 0.8 } },
  "overlay_token": "…",
  "overlay": { "config": {…}, "weather": {…} },
  "send_id": "…"
}
```

`device_id` is on the command envelope; the bridge picks crop/orientation from
its device record.

### Delivery tracking

| Vendor | Bridge binds | Downloading signal | Delivered signal |
|--------|--------------|--------------------|------------------|
| InkJoy | play `msgid` → `BindInkJoy` | `play_ack` progress → `phase=downloading` | `play_ack` 113 → `phase=delivered` |
| Samsung (target) | `frame_id` + PNG `etag` → `BindSamsung` | frame GET `content.json` → `phase=downloading` | frame GET `.png`/`/image` → `phase=delivered` |

`BindInkJoy` / `BindSamsung` work even when the hub did not pre-register on the
bridge — the bridge creates the binding from `send_id` in the command body.

Hub `GET /api/send/{id}?wait=60` blocks until `delivered`, `failed`, or timeout.

---

## InkJoy bridge (complete)

Runs the frame-facing MQTT broker (default `:11883`) and upstream cloud bridges.
Only this process needs `INKJOY_MQTT_*` credentials and upstream allow/intercept
lists.

```bash
./inkjoy-bridge -config ~/Library/Application\ Support/Joyous/inkjoy-config.yaml
```

Default config path: `~/Library/Application Support/Joyous/inkjoy-config.yaml`
(macOS) or `~/.config/Joyous/inkjoy-config.yaml` (Linux). See
`inkjoy-config.yaml.example`.

Frames connect to the bridge on **port 11883**, not the Joyous hub broker.

### InkJoy send flow (reference)

```
Hub sendInkJoyImage()
  → bridge.cmd send.image
  → bridgeEncodeInkJoy() → cache.Save(mac, name, bin)
  → ProbeHubURL(http://hub:18080/inkjoy/{mac}/{name}.bin)
  → MQTT play → frame
  → frame GET hub /inkjoy/{mac}/{name}.bin
  → play_ack → send.complete phase=downloading|delivered
```

Cloud/app play commands use the same cache path: bridge downloads remote `.bin`,
writes to hub cache, rewrites play URL to hub.

### InkJoy config highlights

| Key | Purpose |
|-----|---------|
| `hub_mqtt` | Joyous broker (`tcp://127.0.0.1:1883`) |
| `hub_http` | Hub base for album fetch + cache URLs |
| `listen_mqtt` | Frame broker (`:11883`) |
| `upstream` / `upstream_usr` / `upstream_pwd` | InkJoy cloud |
| `upstream_allow` / `downstream_allow` / `intercept` | Cloud relay rules |
| `data_dir` | Bridge-local registry, captures |
| `hub_data_dir` | Must match hub `data_dir` — cache root `{hub_data_dir}/inkjoy/` |

### InkJoy UI

When an InkJoy bridge is online with `config.ui`, the hub shows the InkJoy tab;
the iframe loads `/inkjoy/` (MQTT proxy). Frame MQTT logs:
`GET /inkjoy/api/mqtt/logs`. Hub **MQTT** tab shows Joyous broker traffic only
(`GET /api/mqtt/logs`). Without a running InkJoy bridge, the InkJoy tab is hidden.

### InkJoy troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| Send stuck at 106→104, UI "Downloading…" | `.bin` hit MQTT proxy instead of hub cache; old hub binary; path mismatch |
| `bridge HTTP timeout` during send | Encoding ran on MQTT thread (fixed: async `send.image`) |
| Hub cache 404 after bridge write | `hub_data_dir` ≠ hub `data_dir`; hub not restarted after deploy |
| Play messages missing in UI | MQTT log ring buffer evicted them (fixed: smart eviction) |

Verify cache: `curl -I http://hub:18080/inkjoy/{mac}/{file}.bin` → 200,
`X-Joyous-Inkjoy-Cache: 1`.

---

## Samsung bridge

Samsung EM32DX frames poll HTTP: `content.json` → `image` (PNG). The hub pushes
updates via Samsung MDC (`SET_CONTENT_DOWNLOAD`) over TCP `:1515`.

```bash
./samsung-bridge -config ~/Library/Application\ Support/Joyous/samsung-config.yaml
```

See `samsung-config.yaml.example` for the target configuration shape.

### Current state (partial migration)

| Feature | Where today | Notes |
|---------|-------------|-------|
| SSDP/MDC discover | samsung-bridge | Hub delegates `POST /api/devices/discover` |
| Device sync | samsung-bridge | `devices.sync` every 10s |
| PNG encode on album send | **samsung-bridge** | `send.image` → encode → hub cache → MDC push |
| Frame pull routes | **hub** `:18080` | Never via MQTT tunnel; `X-Joyous-Samsung-Cache` |
| MDC push (album send) | **samsung-bridge** | After hub cache probe |
| Wake / sleep / push / config UI | **hub** (still) | Next: proxy `/api/samsung/…` |
| Overnight / daily refresh | bridge + hub | Scheduler runs on bridge process |
| Samsung tab UI | **hub** | Inline in `web.go`, not bridge iframe yet |
| Send delivery | hub BindSamsung via `phase=bound` | Frame pull completes on hub PNG handler |

### Samsung migration (target)

Mirror the InkJoy split:

```
┌─────────────────┐     MQTT :1883      ┌──────────────────┐
│   joyous-hub    │◄───────────────────►│  samsung-bridge  │
│  albums/images  │                     │  SSDP/MDC        │
│  /samsung/* GET │◄── hub_data_dir ───►│  encode + push   │
│  UI proxy       │                     │  schedulers      │
└────────┬────────┘                     └────────┬─────────┘
         │ :18080                                │ MDC :1515
         ▼                                       ▼
    Samsung frame polls content.json / image
```

#### Target responsibilities

**Hub keeps:**

- Albums, tags, overlay config, color presets (shared)
- `GET /images/…` originals for bridge fetch
- Frame pull routes from `{data_dir}/samsung/` on `:18080`
- Send registration and `GET /api/send/{id}`
- Devices tab merge from `devices.sync`
- Proxy `/api/samsung/…` → samsung-bridge when online

**Bridge takes:**

- SSDP discovery + MDC LAN sweep (`discover` cmd)
- `send.image`: fetch original, `prepareSamsungPNG`, write
  `{hub_data_dir}/samsung/{frameId}.png`, bind etag, MDC push
- Wake, sleep, push, config, calibration, daily refresh, overnight scheduler
- Per-frame config under bridge `data_dir` (synced via `ui.state` or hub proxy)
- `send.complete` with `phase=downloading|delivered|failed`
- Optional `config.ui` capability + Samsung tab iframe (like InkJoy)

#### Target send flow

```
Hub sendSamsungImage()  [refactor: delegate like InkJoy]
  → bridge.cmd send.image
  → bridge: GET hub /images/{id}/original
  → bridge: prepareSamsungPNG (crop, overlay, color pipeline)
  → bridge: write {hub_data_dir}/samsung/{frameId}.png + etag
  → bridge: probe GET http://hub:18080/samsung/{frameId}/content.json
  → bridge: pushSamsungFrame (MDC SET_CONTENT_DOWNLOAD → hub content URL)
  → frame: GET hub /samsung/{frameId}/content.json  → send.complete downloading
  → frame: GET hub /samsung/{frameId}/image         → send.complete delivered
```

MDC content URLs must use **hub** `server_addr` (`host:18080`), not bridge
`:18082`. `publicHTTPHost()` already picks the outbound LAN IP toward the frame.

#### Target HTTP split

Register on hub **before** the `/{bridge_id}/…` proxy (like InkJoy cache):

| Route | Owner | Tunnel |
|-------|-------|--------|
| `GET /samsung/{frameId}/content.json` | hub disk | never |
| `GET /samsung/{frameId}/image` | hub disk | never |
| `GET /samsung/{frameId}/status` | hub disk | never |
| `GET /samsung/{frameId}.png` | hub disk | never |
| `GET /samsung/{frameId}.lock` | hub disk | never |
| `GET /api/samsung` | samsung-bridge | yes |
| `POST /api/samsung/{frameId}/push` | samsung-bridge | yes |
| `POST /api/samsung/{frameId}/wake|sleep` | samsung-bridge | yes |
| `PUT /api/samsung/{frameId}/config` | samsung-bridge | yes |
| `GET/PUT /api/samsung/{frameId}/daily-refresh` | samsung-bridge | yes |
| `POST /api/samsung/{frameId}/calibration` | samsung-bridge | yes |
| `GET /api/samsung/{frameId}/preview` | samsung-bridge | yes |

Reject frame-pull paths from the `/samsung/{path…}` MQTT proxy (same pattern as
`isInkJoyFrameBinProxyPath`).

#### Migration checklist

1. ✅ Add `samsung_cache.go` — hub disk serve headers + proxy rejection + startup self-check
2. ✅ Add `hub_data_dir` reconciliation to samsung-bridge
3. ✅ Move `sendSamsungImage` encode/push to bridge `send.image` handler (async)
4. ✅ Bridge publishes `send.complete` phase=`bound`; hub BindSamsung; PNG handlers complete delivery
5. ⬜ Move overnight scheduler + battery tracking ownership cleanup (scheduler already on bridge)
6. ⬜ Proxy `/api/samsung/…` only when bridge online; fallback or hide tab when offline
7. ✅ Update `install-local.sh`: `SAMSUNG_SERVER_ADDR` → hub `:18080`; samsung-config.yaml
8. ⬜ Remove Samsung control routes from hub once UI is proxied
9. ✅ Tests: cache serve, proxy rejection, send delivery bound phase

#### Samsung commands (target mapping)

Existing protocol constants map to current hub HTTP handlers:

| MQTT cmd | Replaces |
|----------|----------|
| `discover` | `POST /api/devices/discover` delegation ✓ |
| `send.image` | album send encode + push |
| `samsung.push` | `POST /api/samsung/{id}/push` |
| `samsung.wake` | `POST /api/samsung/{id}/wake` |
| `samsung.sleep` | `POST /api/samsung/{id}/sleep` |
| `samsung.config` | `PUT /api/samsung/{id}/config` |

---

## Nixplay bridge

Cloud-only: there's no LAN device and no frame pull. A "device" in the hub
Devices tab is actually a Nixplay **playlist** (what the mobile app calls a
gallery) — its ID is the Nixplay `playlistId`, not a MAC/IP. `send.image`
uploads the album original straight to Nixplay's S3 bucket via the
reverse-engineered mobile API (see `Nixplay/docs/mobile_api.md` in the repo
root); Nixplay's own backend then syncs it out to any frame assigned to that
playlist over its own protocol (see `Nixplay/docs/socketio_protocol.md`).

- No crop/overlay is applied — Nixplay resizes per frame model server-side.
- Credentials come from the macOS Keychain (Passwords app), never from
  `nixplay-config.yaml`: `security add-generic-password -a <email> -s
  joyous-hub-nixplay -w` (run interactively so the password is never typed
  into a file or chat).
- `discover` / periodic refresh re-lists playlists (`GET /v6/playlists/`) and
  republishes them as `devices.sync`.
- This integration is reverse-engineered from decompiled app bytecode, not
  confirmed against live traffic — validate with a real test upload before
  relying on it.

## Configuration files

| File | Process | Purpose |
|------|---------|---------|
| `config.yaml` | joyous-hub | `data_dir`, `listen_http`, `listen_mqtt`, `server_addr`, … |
| `inkjoy-config.yaml` | inkjoy-bridge | Frame MQTT, cloud upstream, `hub_data_dir` |
| `samsung-config.yaml` | samsung-bridge | `hub_mqtt`, `hub_http`, `hub_data_dir`, `discover_subnets` |
| `nixplay-config.yaml` | nixplay-bridge | `hub_mqtt`, `hub_http`, `keychain_service`, `keychain_account` |

CLI flags override YAML values.

## Upgrading from monolithic hub

The monolithic hub served InkJoy frames on **`listen_mqtt` (11883)** and Samsung
content on hub HTTP. After the bridge split:

| Traffic | Before | After |
|---------|--------|-------|
| InkJoy frame MQTT | hub :11883 | inkjoy-bridge :11883 |
| Joyous bridge MQTT | (same broker) | hub :1883 |
| InkJoy frame `.bin` | hub or cloud | hub cache :18080 |
| Samsung frame HTTP | hub :18080 | hub :18080 (target; was :18082 during partial migration) |

InkJoy upgrade steps:

1. Set `listen_mqtt: ":1883"` in hub `config.yaml`
2. Reinstall with `--with-inkjoy`; inkjoy-bridge binds :11883, hub :1883
3. Restart inkjoy-bridge first, then hub
4. Frames keep mqtt_config host:**11883** — no re-provision needed

Samsung upgrade (when migration lands): point MDC content URLs at hub
`server_addr` (:18080). Frames poll the same paths; only the encode/push owner
changes.

## Build tags

| Tag | Binary | Shared code |
|-----|--------|-------------|
| (default) | joyous-hub | Full hub minus bridge mains |
| `inkjoybridge` | inkjoy-bridge | InkJoy + bridgehub + encode |
| `samsungbridge` | samsung-bridge | Samsung + bridgehub (partial) |
| `nixplaybridge` | nixplay-bridge | Nixplay mobile API client + bridgehub |

Hub `config.yaml` no longer starts an InkJoy frame broker or cloud upstream.
InkJoy settings live in `inkjoy-config.yaml`. Samsung-specific scheduler and MDC
settings will move to `samsung-config.yaml` as migration completes.
