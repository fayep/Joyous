# InkJoy Local Pipeline

Push images to an InkJoy e-ink photo frame without uploading anything to the cloud.

The frame communicates over MQTT and downloads images via HTTP(S). This pipeline intercepts that traffic at the router level and injects locally-served images, so the server never sees your photos.

## Components

| File | Purpose |
|------|---------|
| `encode_bin.py` | Encode any image to the InkJoy `.bin` format (Stucki dithering, 6-color e-ink palette) |
| `local_push.py` | Serve a `.bin` over HTTP and push a play message directly to the frame via MQTT |
| `inkjoy-proxy/` | Transparent MQTT proxy that runs on the EdgeRouter X, intercepts broker traffic, and injects play messages covertly |

## Requirements

- **Mac**: Python 3.11+, [uv](https://github.com/astral-sh/uv)
- **Router**: EdgeRouter X (or similar); SSH access as `ubnt@192.168.1.1`
- **Frame**: InkJoy e-ink frame on the LAN (tested with device MAC `AA:BB:CC:DD:EE:FF`)

## Quick start

### 1. Encode an image

```bash
# Landscape frame
uv run encode_bin.py photo.jpg /tmp/image.bin

# Portrait frame — crops bottom 3:4 and pre-rotates for correct display
uv run encode_bin.py photo.jpg /tmp/image.bin --portrait --crop-bottom
```

Options:
- `--portrait` — frame is mounted vertically (rotates content 90° CCW into bin)
- `--crop-bottom` — crop the bottom portion before encoding (aspect matches `--portrait`)
- `--lo-template ref.bin` — copy the wipe-order pattern from an existing bin instead of generating a clock wipe

### 2. Deploy the proxy (first time only)

```bash
cd inkjoy-proxy
./deploy.sh
```

This builds a MIPS32LE binary, copies it to the router, installs an iptables REDIRECT rule, and starts the proxy in the background. Logs stream to the control port; nothing is written to disk on the router.

To stop: `./deploy.sh --stop`

### 3. Serve the bin and inject

```bash
# Start HTTP server on your Mac
python3 -m http.server --bind 192.168.1.100 8080 --directory /tmp

# Inject a play message — fires on the frame's next MQTT heartbeat
echo '{"url":"http://192.168.1.100:8080/image.bin"}' | nc 192.168.1.1 18831

# Watch live proxy logs
nc 192.168.1.1 18831
```

The proxy suppresses the `play_ack` that the frame sends back, so the server never learns an image was displayed.

## How it works

### Encoder (`encode_bin.py`)

Reverse-engineered from `ISFR-lite.exe` (the vendor's offline encoder) via Binary Ninja static analysis.

**Algorithm:** Stucki error diffusion in RGB space  
**Palette:** 6 ink colors — black, white, yellow, red, blue, green  
**Bin format:** 1600×1200, row-major, bottom-to-top, 2 bytes/pixel  
- Hi byte: color index (0x01=black, 0x02=white, 0x03=yellow, 0x04=red, 0x06=blue, 0x07=green)  
- Lo byte: refresh wipe order (0–248 in 31 steps of 8)

**Portrait mode:** The frame maps bin X→display Y. Pass `--portrait` to pre-rotate the image 90° CCW so content appears upright.

### Proxy (`inkjoy-proxy/`)

A transparent TCP proxy deployed on the EdgeRouter X. An iptables PREROUTING rule redirects all traffic destined for the broker IP (`13.39.148.101:1883`) through the proxy regardless of which LAN port the frame is on.

The proxy:
- Forwards all MQTT traffic unchanged in both directions
- Queues inject commands received on the control port (`192.168.1.1:18831`)
- Fires the injected play message on the next broker→frame packet
- Tracks injected message IDs and drops matching `play_ack` replies so the server never sees them
- Fans all log output to every connected control-port client in real time

**Cross-compile for EdgeRouter X:**
```bash
GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -o inkjoy-proxy-mipsle .
```

## Bin file format detail

```
offset 0:        pixel (row=1199, col=0)   ← bottom-left (bin is bottom-to-top)
offset 1:        wipe order byte
offset 2:        pixel (row=1199, col=1)
...
offset 3839998:  pixel (row=0, col=1599)   ← top-right
offset 3839999:  wipe order byte
```

Total: 1600 × 1200 × 2 = 3,840,000 bytes
