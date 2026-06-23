# PhotoFrames / Joyous Hub

Local-first control for e-ink photo frames on your LAN. **Joyous Hub** (`joyous-hub/`) is a single Go service that hosts a web UI, serves images, and talks to frames directly — no cloud upload for your photos.

| Frame | Protocol | Hub role |
|-------|----------|----------|
| **InkJoy** | MQTT + HTTP `.bin` download | Local MQTT broker, optional cloud bridge, image encode & push |
| **Samsung EM32DX** | MDC (TLS `:1515`) + HTTP pull | Wake/sleep, MDC content push, battery logging |

See [CHANGELOG.md](CHANGELOG.md) for recent hub changes. Album brainstorm: [docs/future-ideas.md](docs/future-ideas.md).

## Joyous Hub at a glance

```
┌─────────────┐     HTTP (album, UI)      ┌──────────────────┐
│   Browser   │◄─────────────────────────►│   Joyous Hub     │
└─────────────┘                           │  :18080 HTTP     │
                                          │  :11883 MQTT     │
┌─────────────┐     MQTT + /images/*.bin    └────────┬─────────┘
│  InkJoy     │◄───────────────────────────────────┤
│  frame      │                                    │
└─────────────┘                                    │ MDC + HTTP
                                                   │ /samsung/…
┌─────────────┐     WoL + magic UDP + MDC          │
│  Samsung    │◄───────────────────────────────────┘
│  E-Paper    │
└─────────────┘
```

**InkJoy:** Point the frame at the hub’s MQTT port (redirect / BLE adopt). The hub can bridge selected traffic to the vendor cloud, intercept OTA and sleep commands, encode album images to `.bin`, and push play URLs.

**Samsung:** The hub discovers frames via SSDP, wakes them over the network when needed, pushes PNGs via MDC (mobile deploy `content.json` URL), then puts the display back to sleep to save battery. Battery level is read on the same MDC session as sleep — no extra wake-only polls.

## Quick start (macOS)

Requires **Go 1.22+**, **libheif** (`brew install libheif`) for HEIC uploads, and **Local Network** permission for SSDP / MDC discovery.

```bash
cd joyous-hub
cp config.yaml.example ~/Library/Application\ Support/Joyous/config.yaml
# edit data_dir, discover_subnets, server_addr as needed

make build
./joyous-hub
# Web UI: http://localhost:18080
```

**Production install** (launchd + `JoyousHub.app`, grants Local Network on macOS):

```bash
cd joyous-hub
./scripts/install-local.sh
# or remote: ./install.sh   (builds, rsyncs to hubhost, runs install-local.sh there)
```

Default ports: HTTP **18080**, MQTT **11883**. Config path:

- macOS: `~/Library/Application Support/Joyous/config.yaml`
- Linux: `~/.config/Joyous/config.yaml`

## Web UI

Open the hub HTTP URL in a browser.

- **Devices** — discovered InkJoy and Samsung frames; send album images, refresh InkJoy display, discover SSDP.
- **Album** — upload JPEG/PNG/HEIC; per-format crops; send to a frame (no background frame polling).
- **InkJoy** — per-frame editor: name, portrait, sleep schedule, display preview, BLE adopt.
- **Samsung** — per-frame editor: wake (moon) / sleep (power), WiFi MAC for remote wake, poll interval, auto-sleep after send, battery history and “since last push” delta.

### Samsung send & power (sleep by default)

The panel should be **asleep almost always**. The hub only wakes it for an image send (or if you tap moon wake). After delivery it sleeps again — battery is read on that same MDC session before sleep.

1. Set **WiFi MAC** from the Samsung E-Paper app (device info) — required for wake-on-send.
2. Enable **Network Standby** on the frame (E-Paper app → power settings). Remote wake does not work when standby is off.
3. Leave **Sleep after send** on; set delay long enough for e-ink refresh (often 30–45s).
4. **Asleep** in the UI is normal. The hub does not MDC-poll Samsung frames in the background.

**On send:** wake → push → wait → battery read → sleep.

If remote wake fails, press the frame **power button** when prompted; the hub keeps retrying MDC for up to 5 minutes.

Battery samples append to `{data_dir}/samsung_battery_history.json` from pre-sleep reads during sends — no wake-only battery polls.

## Configuration

`joyous-hub/config.yaml.example` documents all options. Highlights:

| Setting | Purpose |
|---------|---------|
| `listen_http` / `listen_mqtt` | Hub bind addresses |
| `upstream` | InkJoy cloud broker (`13.39.148.101:1883`); empty = local-only |
| `upstream_allow` / `downstream_allow` | Which MQTT actions cross the bridge |
| `intercept` | Hub-handled cloud→frame actions (`ota`, `wifi_sleep`, `mqtt_config`, …) |
| `data_dir` | `devices.json`, album images, Samsung configs, battery history |
| `server_addr` | Host:port frames use to fetch images (e.g. `hubhost.local:18080`) |
| `discover_subnets` | LAN prefixes for Samsung MDC sweep when SSDP is quiet |

Credentials: `INKJOY_MQTT_USER` / `INKJOY_MQTT_PASSWORD` or `upstream_usr` / `upstream_pwd`.

## Development

```bash
cd joyous-hub
make test          # unit tests
make build         # dev binary
make build-release # needs JOYOUS_SEAL for signed link metadata
```

Docker: `joyous-hub/docker-compose.yml`. Cross-build targets: `linux-arm64`, `linux-arm`, `linux-mips` in the Makefile.

Research notes: `research/firmware-notes.md`, `Samsung/README.md` (APK / MDC reference).

## Legacy InkJoy pipeline

Lower-level tools from before the hub — still useful for router-level interception or offline encoding.

| Path | Purpose |
|------|---------|
| `encode_bin.py` | Image → InkJoy `.bin` (Stucki dither, 6-color palette) |
| `local_push.py` | Serve `.bin` + inject MQTT play via control port |
| `inkjoy-proxy/` | Transparent MQTT proxy on a LAN gateway (iptables REDIRECT) |

### Encode an image

```bash
uv run encode_bin.py photo.jpg /tmp/image.bin
uv run encode_bin.py photo.jpg /tmp/image.bin --portrait --crop-bottom
```

### Router proxy (optional)

```bash
cd inkjoy-proxy
cp .env.example .env   # ROUTER_SSH=user@router
./deploy.sh
```

The proxy forwards MQTT, injects play messages on `:18831`, and drops injected `play_ack` so the cloud never sees local pushes.

**Bin format:** 1600×1200, 2 bytes/pixel (color index + wipe order), bottom-to-top row order — see encoder source for palette and portrait handling.
