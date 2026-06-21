#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["paho-mqtt", "requests", "Pillow", "numpy"]
# ///
"""
InkJoy local bin push — bypasses server, hosts bin locally, triggers frame via MQTT.

Two modes:
  1. --capture-play: Watch MQTT and capture what the server sends when you push an image
     from the app (helps us learn the play message format).
  2. --push <bin_file>: Host the bin locally and send a crafted play MQTT message.

Usage:
    # Step 1: capture the server's play message (do a push from the app at the same time)
    uv run local_push.py --capture-play --listen 60

    # Step 2: push a locally-encoded bin directly to the frame
    uv run local_push.py --push /path/to/image.bin --local-ip 192.168.x.x --port 8765
"""

import argparse
import http.server
import json
import os
import socket
import threading
import time
import uuid

import paho.mqtt.client as mqtt

def env(name: str, default: str = "") -> str:
    val = os.environ.get(name, default)
    if not val:
        raise SystemExit(f"Set {name} environment variable")
    return val

CLIENT_ID = os.environ.get("INKJOY_CLIENT_ID", "AABBCCDDEEFF")
MQTT_HOST = os.environ.get("INKJOY_MQTT_HOST", "13.39.148.101")
MQTT_PORT = int(os.environ.get("INKJOY_MQTT_PORT", "1883"))
MQTT_USER = env("INKJOY_MQTT_USER")
MQTT_PASS = env("INKJOY_MQTT_PASSWORD")
TOPIC_FRAME = f"/inkjoyap/{CLIENT_ID}"     # server → frame
TOPIC_RPT   = f"/device/report/{CLIENT_ID}" # frame → server

play_messages = []  # captured play messages


def connect_mqtt(spy_id: str, topics: list[str], on_msg):
    client = mqtt.Client(client_id=spy_id, protocol=mqtt.MQTTv311)
    client.username_pw_set(MQTT_USER, MQTT_PASS)

    def on_connect(c, ud, flags, rc):
        print(f"MQTT connected as {spy_id}")
        for t in topics:
            r, _ = c.subscribe(t, qos=1)
            print(f"  subscribe {t!r}: {'OK' if r == 0 else f'FAILED({r})'}")

    client.on_connect = on_connect
    client.on_message = on_msg
    client.on_disconnect = lambda c, ud, rc: print(f"MQTT disconnect rc={rc}")
    client.connect(MQTT_HOST, MQTT_PORT, keepalive=60)
    return client


def capture_play(listen_secs: int):
    """Subscribe to all frame topics and print everything — find the play message."""
    print(f"Listening for {listen_secs}s for server→frame play messages...")
    print("Now push an image from the InkJoy app to trigger the message.\n")

    def on_msg(c, ud, msg):
        ts = time.strftime("%H:%M:%S")
        payload = msg.payload.decode(errors='replace')
        print(f"\n[{ts}] TOPIC: {msg.topic}")
        try:
            parsed = json.loads(payload)
            print(json.dumps(parsed, indent=2))
            play_messages.append({'topic': msg.topic, 'payload': parsed, 'ts': ts})
        except Exception:
            print(f"  raw: {payload!r}")

    # NOTE: broker ACL only grants QoS 1 on /device/report/{MAC} (frame→server).
    # /inkjoyap/{MAC} (server→frame) and '#' are rejected (QoS 128 = denied).
    # To see server→frame play messages, capture at network level:
    #   sudo bash inkjoy-capture.sh --live   (on the EdgeRouter)
    # then push from the app — play messages arrive unencrypted on port 1883.
    print("NOTE: broker ACL blocks subscription to server→frame topic.")
    print("To capture the play message, run inkjoy-capture.sh --live on the EdgeRouter")
    print("while pushing an image from the app.\n")

    spy = f"inkjoy-spy-{uuid.uuid4().hex[:8]}"
    client = connect_mqtt(spy, [TOPIC_RPT], on_msg)  # only what ACL allows
    client.loop_start()
    try:
        time.sleep(listen_secs)
    except KeyboardInterrupt:
        pass
    client.loop_stop()
    client.disconnect()

    if play_messages:
        print(f"\n\n=== Captured {len(play_messages)} message(s) ===")
        for m in play_messages:
            if 'action' in m['payload'] and m['payload']['action'] in ('play', 'push', 'img', 'display'):
                print(f"\n*** PLAY MESSAGE (topic={m['topic']}) ***")
                print(json.dumps(m['payload'], indent=2))
    else:
        print("\nNo messages captured from allowed topics.")


class BinServer(http.server.BaseHTTPRequestHandler):
    bin_data = b""
    bin_name = "image.bin"

    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("Content-Length", len(self.bin_data))
        self.send_header("Content-Disposition", f'attachment; filename="{self.bin_name}"')
        self.end_headers()
        self.wfile.write(self.bin_data)

    def log_message(self, fmt, *args):
        print(f"  {'HTTPS' if _tls else 'HTTP'}: {fmt % args}")

_tls = False  # set by push_local

def make_self_signed_cert():
    """Generate a temporary self-signed cert+key pair, return (certfile, keyfile)."""
    import subprocess, tempfile
    d = tempfile.mkdtemp()
    cert = os.path.join(d, 'cert.pem')
    key  = os.path.join(d, 'key.pem')
    subprocess.run([
        'openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
        '-keyout', key, '-out', cert, '-days', '1',
        '-subj', '/CN=inkjoy-local'
    ], check=True, capture_output=True)
    return cert, key


def native_to_bin(png_path: str) -> bytes:
    """Convert a native paletted PNG (R=hi byte, G=lo byte in palette) to .bin wire format."""
    import numpy as np
    from PIL import Image
    img = Image.open(png_path)
    idx = np.array(img)
    pal = np.array(img.getpalette(), dtype=np.uint8).reshape(256, 3)
    hi, lo = pal[idx, 0], pal[idx, 1]
    return np.stack([hi[::-1], lo[::-1]], axis=2).reshape(-1).tobytes()


def push_local(bin_path: str, local_ip: str, port: int, use_tls: bool = False, play_json: dict | None = None, native: bool = False):
    """Host the bin and send a crafted play message to the frame."""
    if native:
        print(f"Converting native PNG {bin_path} → .bin…")
        BinServer.bin_data = native_to_bin(bin_path)
        BinServer.bin_name = os.path.splitext(os.path.basename(bin_path))[0] + '.bin'
    else:
        BinServer.bin_data = open(bin_path, 'rb').read()
        BinServer.bin_name = os.path.basename(bin_path)

    global _tls
    _tls = use_tls
    server = http.server.HTTPServer(('0.0.0.0', port), BinServer)
    if use_tls:
        import ssl
        cert, key = make_self_signed_cert()
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(cert, key)
        server.socket = ctx.wrap_socket(server.socket, server_side=True)
        scheme = 'https'
        print(f"TLS: self-signed cert (frame likely won't verify — IoT devices rarely do)")
    else:
        scheme = 'http'
    url = f"{scheme}://{local_ip}:{port}/{BinServer.bin_name}"
    print(f"Serving {bin_path} ({len(BinServer.bin_data)} bytes) at {url}")
    t = threading.Thread(target=server.serve_forever, daemon=True)
    t.start()

    # Craft play message — exact format captured from proxy:
    # broker→frame PUBLISH /inkjoyap/{MAC}
    # {"action":"play","data":{"host":"...","port":443,"imgs":[{"imgid":"...","imgurl":"/path.bin"}],"mode":2,"strategy":1},"msgid":"...","stamac":"AA:BB:CC:DD:EE:FF"}
    if play_json:
        msg = play_json.copy()
    else:
        from urllib.parse import urlparse
        parsed = urlparse(url)
        host = parsed.hostname
        port = parsed.port or (443 if parsed.scheme == 'https' else 80)
        path = parsed.path or '/'
        msg = {
            "action": "play",
            "msgid": str(int(time.time() * 1000)),
            "stamac": ':'.join(CLIENT_ID[i:i+2] for i in range(0, 12, 2)),
            "data": {
                "host": host,
                "port": port,
                "imgs": [{"imgid": "local-0", "imgurl": path}],
                "mode": 2,
                "strategy": 1,
            }
        }

    payload = json.dumps(msg)
    print(f"\nPublishing to {TOPIC_FRAME}:")
    print(json.dumps(msg, indent=2))

    spy = f"inkjoy-push-{uuid.uuid4().hex[:8]}"
    received = []

    def on_msg(c, ud, m):
        payload_str = m.payload.decode(errors='replace')
        print(f"\n[{time.strftime('%H:%M:%S')}] FRAME REPLY on {m.topic}:")
        try:
            print(json.dumps(json.loads(payload_str), indent=2))
        except Exception:
            print(f"  raw: {payload_str!r}")
        received.append(payload_str)

    client = connect_mqtt(spy, [TOPIC_FRAME, TOPIC_RPT], on_msg)
    client.loop_start()
    time.sleep(1)

    client.publish(TOPIC_FRAME, payload, qos=1)
    print(f"\nMessage sent. Waiting 30s for frame response...")
    time.sleep(30)

    client.loop_stop()
    client.disconnect()
    server.shutdown()

    print(f"\nDone. {len(received)} response(s) received.")


def get_local_ip():
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
        s.close()
        return ip
    except Exception:
        return "127.0.0.1"


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest='cmd')

    cap = sub.add_parser('capture-play', help='Capture server play messages')
    cap.add_argument('--listen', type=int, default=60)

    push = sub.add_parser('push', help='Push local bin to frame')
    push.add_argument('bin_file', help='.bin file or native LA PNG (with --native)')
    push.add_argument('--native', action='store_true',
                      help='Input is a native LA PNG; convert to .bin on the fly')
    push.add_argument('--local-ip', default=None)
    push.add_argument('--port', type=int, default=8080)
    push.add_argument('--tls', action='store_true',
                      help='Serve over HTTPS with a self-signed cert (try if plain HTTP fails)')
    push.add_argument('--play-json', default=None,
                      help='Path to JSON file with play message template')

    args = parser.parse_args()

    if args.cmd == 'capture-play':
        capture_play(args.listen)
    elif args.cmd == 'push':
        ip = args.local_ip or get_local_ip()
        tmpl = json.load(open(args.play_json)) if args.play_json else None
        push_local(args.bin_file, ip, args.port, args.tls, tmpl, args.native)
    else:
        parser.print_help()


if __name__ == '__main__':
    main()
