# Joyous bridge architecture

Joyous Hub is split into a **core hub** plus optional **vendor bridges**. Users
who do not own InkJoy or Samsung frames do not need to run the corresponding
bridge.

## Processes

| Binary | Build | Role |
|--------|-------|------|
| `joyous-hub` | `make build` | Albums, UI, image pipeline, Joyous MQTT broker |
| `inkjoy-bridge` | `make build-inkjoy-bridge` | InkJoy frame MQTT + cloud upstream |
| `samsung-bridge` | `make build-samsung-bridge` | Samsung SSDP/MDC + `/samsung/*` HTTP |

Build all three: `make build-all`

## Joyous MQTT protocol

Bridges connect to the hub broker (`listen_mqtt`, default `:11883`) and use
topics under `joyous/bridge/{id}/…` and `joyous/hub/{id}/…`.

Message types live in `joyous-hub/protocol/`. Key flows:

- **Bridge → hub**: `bridge.hello`, `devices.sync`, `device.update`, `ui.state`
- **Hub → bridge**: `bridge.cmd` (send image, discover, refresh, …), `ui.action`

The unified **Devices** tab merges `devices.sync` snapshots from all bridges.

## InkJoy bridge

Runs the frame-facing MQTT broker (default `:1883`) and upstream cloud bridges.
Only this process needs `INKJOY_MQTT_*` credentials and upstream allow/intercept
lists.

```bash
./inkjoy-bridge -hub-mqtt tcp://127.0.0.1:11883 -listen-mqtt :1883
```

Frames point at the bridge MQTT host, not the hub.

## Samsung bridge

Runs Samsung frame HTTP pull routes and discovery. When the hub receives
`POST /api/devices/discover` and the Samsung bridge is online, discovery is
delegated via MQTT.

```bash
./samsung-bridge -hub-mqtt tcp://127.0.0.1:11883 -listen-http :18082
```

Point frames at the bridge HTTP host for `/samsung/{frameId}/…`.

## Hub API for bridge UI

Bridge-owned tab state is available at:

- `GET /api/bridge/inkjoy/ui`
- `GET /api/bridge/samsung/ui`
- `POST /api/bridge/{kind}/ui` — forward user actions to the bridge

The embedded SPA still uses legacy `/api/samsung` and device APIs; migrating
InkJoy/Samsung tabs to the bridge UI endpoints is in progress.

## Image sends

The hub does **not** vendor-encode before sending to a bridge. A `send.image`
command carries:

- `image_id` — album image
- `hub_base_url` — where the bridge fetches `/images/{id}/original`
- `crops` — all saved album crops (e.g. `"4:3"`, `"16:9"`, `"3:4"`)
- `overlay_token` — when compositing weather/name overlay before encode
- `device_id` on the command envelope (bridge picks crop/orientation from its device record)

The bridge pulls the source asset from the hub, selects the appropriate crop for
the destination frame type, then runs InkJoy/Samsung encode using shared
joyous-hub libraries. Encoded output is served from the bridge HTTP host.


- Hub `config.yaml` no longer starts an InkJoy frame broker or cloud upstream.
  Move `upstream`, `upstream_allow`, `downstream_allow`, and `intercept` to
  inkjoy-bridge flags or its own config file (future).
- Seeed firmware and InkJoy sleep handling landed on `main` before this branch
  (`2f69903`, `ebd9df0`); the refactor rebases on top of those commits.
