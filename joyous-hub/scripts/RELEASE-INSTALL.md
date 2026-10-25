# Installing Joyous

This archive has the hub and its three bridges in `bin/`:

| Binary | Does |
|---|---|
| `joyous-hub` | Web UI (`http://<host>:18080`), photo library, rendering, frame registry |
| `inkjoy-bridge` | InkJoy frames and the Seeed/GooDisplay clones (MQTT; Bluetooth for setup) |
| `samsung-bridge` | Samsung E-Paper frames |
| `nixplay-bridge` | Nixplay frames |

Run the hub plus the bridges for the frames you have.

Each binary reads its settings from your config folder:

- **macOS:** `~/Library/Application Support/Joyous/`
- **Linux:** `~/.config/Joyous/`
- **Windows:** `%AppData%\Joyous\`

The settings files are `config.yaml` (hub), `inkjoy-config.yaml`, `samsung-config.yaml` and `nixplay-config.yaml`. Start from the matching `*.yaml.example` in this archive. A missing file means defaults; `-config <path>` points a binary at another file.

## macOS (Apple silicon)

1. Unpack the archive to `~/joyous-hub` and clear the download quarantine:
   ```
   xattr -dr com.apple.quarantine ~/joyous-hub
   ```
2. Install the hub, plus any bridges you need, as login services:
   ```
   cd ~/joyous-hub
   SKIP_BUILD=1 WITH_INKJOY=1 WITH_SAMSUNG=1 ./scripts/install-local.sh
   ```
   Options:
   - `WITH_NIXPLAY=1` adds the Nixplay bridge.
   - `DATA_DIR=...` moves the photo library. The default is `~/Library/Application Support/Joyous/data`.
   - `DISCOVER_SUBNETS=192.168.1` sets your LAN prefix for finding Samsung frames.

   The installer wraps each binary in an app bundle, so macOS can ask for Local Network and Bluetooth access. It also starts each one as a login service.

   `./scripts/uninstall-local.sh` removes them.

## Linux

```
mkdir -p ~/.config/Joyous
cp config.yaml.example ~/.config/Joyous/config.yaml   # then edit data_dir, server_addr
./bin/joyous-hub &
./bin/inkjoy-bridge &      # and/or samsung-bridge, nixplay-bridge
```

For a long-running install, give each binary a systemd user service.

Bluetooth uses BlueZ. Samsung discovery uses SSDP multicast, so in Docker use the host network. The hub is also published as an image:

```
docker run -d --network host -v /path/to/library:/data \
  ghcr.io/fayep/joyous-hub:latest --data-dir=/data --listen-http=:18080
```

## Windows

Copy `config.yaml.example` to `%AppData%\Joyous\config.yaml` and edit it. Then run `bin\joyous-hub.exe` and the bridges you need.

Allow them through Windows Firewall when asked: frames connect in to MQTT and HTTP.
