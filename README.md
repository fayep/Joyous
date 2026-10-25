# Joyous

Local-first control for e-ink photo frames. Joyous keeps your photo library on your own machine, renders each photo for the panel it's going to (dithering against that panel's real inks), and sends it to the frame over your LAN. No cloud upload for your photos.

| Frame | How Joyous talks to it |
|---|---|
| **InkJoy** 10" and 28.5" | MQTT and HTTP. Joyous runs the broker the frame connects to and can relay to InkJoy's cloud. |
| **Seeed XIAO EE02 / GooDisplay ESP32-133C02** e-paper boards | Same as InkJoy, using the custom firmware in [`Firmware/`](Firmware/) |
| **Samsung E-Paper** (EM32DX) | MDC over TLS plus HTTP pull: wake, push, then sleep again |
| **Nixplay** | Nixplay's own cloud: Joyous uploads to your Nixplay galleries |

## Install

Download the archive for your platform from the [latest release](https://github.com/fayep/Joyous/releases/latest). Each archive has an `INSTALL.md` with the steps for that platform:

- **macOS (Apple silicon):** `joyous-<version>-darwin-arm64.tar.gz`. It includes an installer that sets Joyous up as login services.
- **Linux (amd64, arm64):** `joyous-<version>-linux-<arch>.tar.gz`. A Docker image is also published: `ghcr.io/fayep/joyous-hub`.
- **Windows (amd64):** `joyous-<version>-windows-amd64.zip`

`SHA256SUMS` in the release lets you check the download.

Joyous is four programs. Run the hub plus the bridges for the frames you have:

| Program | Does |
|---|---|
| `joyous-hub` | Web UI, photo library, rendering, frame list |
| `inkjoy-bridge` | InkJoy frames and the custom-firmware boards (also does their Bluetooth setup) |
| `samsung-bridge` | Samsung E-Paper frames |
| `nixplay-bridge` | Nixplay |

Then open `http://<host>:18080`, and follow [Frame setup](docs/frame-setup.md) to connect each frame.

### Ports

| Port | Used by |
|---|---|
| 18080 (TCP) | Web UI and API; frames download their images here |
| 1883 (TCP) | Hub MQTT broker: the bridges connect to it (local only) |
| 11883 (TCP) | InkJoy bridge MQTT broker: InkJoy frames connect to it |
| 1900 (UDP) | SSDP discovery of Samsung frames |

Frames must be able to reach the host on 18080 and 11883.

### Configuration

Each program reads a YAML file from your config folder:

- **macOS:** `~/Library/Application Support/Joyous/`
- **Linux:** `~/.config/Joyous/`
- **Windows:** `%AppData%\Joyous\`

The files are `config.yaml` (hub), `inkjoy-config.yaml`, `samsung-config.yaml` and `nixplay-config.yaml`. Commented examples are in [`joyous-hub/`](joyous-hub/) and in every release archive. A missing file means defaults; `-config <path>` points a program at another file.

The settings most people need:

| File | Setting | What it's for |
|---|---|---|
| `config.yaml` | `data_dir` | Where the photo library and frame list live |
| `config.yaml` | `server_addr` | `host:18080` as the frames should reach it (required) |
| `inkjoy-config.yaml` | `upstream` | InkJoy's cloud broker, relayed to so the InkJoy app keeps working; `""` keeps frames local-only |
| `samsung-config.yaml` | `discover_subnets` | Your LAN (e.g. `192.168.1.0/24`), searched when SSDP discovery is quiet |
| `nixplay-config.yaml` | `keychain_account` | Your Nixplay email; store the password with `nixplay-bridge -set-password` |

## Using Joyous

- **Album:** upload JPEG, PNG, HEIC or AVIF photos. Tag them, build smart albums, set a crop per aspect ratio, and send to a frame.
- **Devices:** every frame, with:
  - **Send image** and **Scheduled sends**.
  - **Color calibration:** send a colour chart, then save the panel's measured inks.
  - The refresh **Transition** (InkJoy 10" and 28.5").
- **Overlays:** optional weather and photo-name overlay drawn onto sent photos.
- **InkJoy** and **Nixplay:** the bridges' own pages, including InkJoy Bluetooth setup.
- **Samsung:** wake and sleep, Wi-Fi MAC for remote wake, polling, inactive hours, battery history.

Each photo is rendered for its frame:
- sharpened on lightness only;
- fitted to the panel's black-to-white range;
- dithered against that panel's own measured inks.

Per-frame colour calibration corrects panels whose inks differ from the standard set.

## License

Joyous is free software under the [GNU Affero General Public License v3](LICENSE). If you run a modified version for others over a network, you must offer them its source. The binaries include third-party components under their own licences, listed in [THIRD_PARTY_NOTICES.md](joyous-hub/THIRD_PARTY_NOTICES.md), among them libde265 (LGPL-3.0) for HEIC decoding.

## Development

Requires Go 1.27 and a C/C++ compiler (CGO builds the HEIC decoder and, on macOS, the Bluetooth bridge).

```bash
cd joyous-hub
make test          # unit tests
make build-all     # hub + the three bridges
./install.sh       # macOS: build, rsync to a remote Mac, install as launchd services
```

Architecture: [BRIDGES.md](joyous-hub/BRIDGES.md). Image catalog and smart albums: [ADR 0001](docs/adr/0001-sqlite-catalog-smart-albums.md). Ideas: [docs/future-ideas.md](docs/future-ideas.md). Research notes: `research/firmware-notes.md`, `Samsung/README.md`.

### Releases

Pushing a `v*` tag (e.g. `git tag v1.0.0 && git push origin v1.0.0`) runs `.github/workflows/release.yml`. It does three things:

- Builds the hub and the three bridges natively for macOS (Apple silicon), Linux (amd64, arm64) and Windows (amd64), each with HEIC support.
- Publishes them as a GitHub release with `SHA256SUMS`.
- Pushes the multi-arch hub image to `ghcr.io/fayep/joyous-hub`.

To pack one platform locally, run `joyous-hub/scripts/package-release.sh <goos> <goarch> <version> <out-dir>`. After changing dependencies, regenerate the notices with `joyous-hub/scripts/gen-notices.sh`.

### Standalone InkJoy tools

Older scripts from before the hub, still handy for offline work:

| Script | Does |
|---|---|
| `encode_bin.py` | Image → InkJoy `.bin` (`uv run encode_bin.py photo.jpg out.bin [--portrait]`) |
| `decode_bin.py` | InkJoy `.bin` → PNG |
| `local_push.py` | Host a `.bin` locally and tell a frame to play it over MQTT |
| `mqtt_watch.py` | Watch InkJoy MQTT traffic |
| `ble_scan.py` | Scan for Samsung E-Paper frames over Bluetooth |

**`.bin` format:** 1600×1200, 2 bytes per pixel (ink index, then wipe order), rows bottom to top.
