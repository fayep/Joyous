#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["paho-mqtt"]
# ///
"""
Connect to the InkJoy broker as a specific frame MAC address and log every
message the server sends to that device.  Useful for probing what the server
sends to a particular firmware version without needing the physical device.

After subscribing, the client mimics the real frame protocol:
  1. Sends login  → receives login_ack (captures device_id / uid)
  2. Sends heart  → receives heart_ack  (repeats every --heart-interval seconds)
  3. Logs any other server→frame messages (play, ota, update, …)

Usage:
    uv run impersonate_frame.py A1:B2:C3:D4:E5:F6
    uv run impersonate_frame.py A1:B2:C3:D4:E5:F6 --ver "M H:2 F:4.4.0"
    uv run impersonate_frame.py --mac A1:B2:C3:D4:E5:F6 --duration 120
"""

import argparse
import json
import random
import sys
import threading
import time

import paho.mqtt.client as mqtt

MQTT_HOST = "13.39.148.101"
MQTT_PORT = 1883
MQTT_USER = "REDACTED_MQTT_USER"
MQTT_PASS = "REDACTED_MQTT_PASSWORD"

DEFAULT_VER = "M H:2 F:0.5.6"   # login ver  ("M H:<fpga_ver> F:<esp32_fw>")
DEFAULT_FW  = "0.5.6"            # heart version (fw only)


def normalise_mac(mac: str) -> tuple[str, str]:
    """Return (with_colons, without_colons)."""
    raw = mac.upper().replace(":", "").replace("-", "")
    if len(raw) != 12 or not all(c in "0123456789ABCDEF" for c in raw):
        raise ValueError(f"Invalid MAC address: {mac!r}")
    return ":".join(raw[i:i+2] for i in range(0, 12, 2)), raw


def fw_from_ver(ver: str) -> str:
    """Extract bare fw string from 'M H:2 F:4.4.0' → '4.4.0'."""
    for part in ver.split():
        if part.startswith("F:"):
            return part[2:]
    return ver


def main():
    parser = argparse.ArgumentParser(description="Impersonate an InkJoy frame on MQTT")
    parser.add_argument("mac", nargs="?", help="Device MAC address (any format)")
    parser.add_argument("--mac", dest="mac_opt", help="Device MAC address (alternative)")
    parser.add_argument("--ver", default=DEFAULT_VER,
                        help=f'login ver string (default: "{DEFAULT_VER}")')
    parser.add_argument("--heart-interval", type=float, default=20,
                        help="Seconds between heart messages (default: 20)")
    parser.add_argument("--duration", type=float, default=300,
                        help="How long to run in seconds (default: 300)")
    parser.add_argument("--host", default=MQTT_HOST)
    parser.add_argument("--port", type=int, default=MQTT_PORT)
    args = parser.parse_args()

    raw_mac = args.mac or args.mac_opt
    if not raw_mac:
        parser.error("MAC address required")

    mac_colons, mac_plain = normalise_mac(raw_mac)
    fw_ver      = fw_from_ver(args.ver)
    ap_topic    = f"/inkjoyap/{mac_plain}"
    report_topic = f"/device/report/{mac_plain}"

    print(f"Connecting as {mac_plain} ({mac_colons})")
    print(f"  login ver : {args.ver!r}   heart version: {fw_ver!r}")
    print(f"  ap topic  : {ap_topic}")
    print()

    received: list[dict] = []
    session: dict = {}   # populated from login_ack: device_id, uid
    stop_event = threading.Event()

    # ── Payload builders ────────────────────────────────────────────────────

    def make_login() -> str:
        return json.dumps({
            "action":   "login",
            "clientid": mac_plain,
            "stamac":   mac_plain,
            "msgid":    str(int(time.time() * 1000)),
            "data": {
                "inkjoy":           True,
                "ver":              args.ver,
                "statype":          3,
                "sleep_mode":       2,
                "sleep_begin_time": "07:00",
                "sleep_end_time":   "13:00",
            },
        })

    def make_heart() -> str:
        return json.dumps({
            "action":   "heart",
            "clientid": mac_plain,
            "stamac":   mac_plain,
            "msgid":    str(int(time.time() * 1000)),
            "data": {
                "type":            3,
                "ack":             1,
                "wifi":            "on",
                "wifi_name":       "Novac",
                "ble":             "off",
                "tf":              "absent",
                "tfsize":          0,
                "tfused":          0,
                "orientation":     0,
                "battery":         73,
                "wifi_listen_iv":  50,
                "wifi_rssi":       -58 + random.randint(-3, 3),
                "wifi_ch":         1,
                "ble_rssi":        0,
                "version":         fw_ver,
            },
        })

    # ── MQTT callbacks ───────────────────────────────────────────────────────

    def on_connect(client, userdata, flags, rc):
        if rc != 0:
            print(f"Connection refused (rc={rc})", file=sys.stderr)
            stop_event.set()
            return
        ts = time.strftime("%H:%M:%S")
        print(f"[{ts}] Connected")
        client.subscribe(ap_topic, qos=1)

    def on_subscribe(client, userdata, mid, granted_qos):
        for q in granted_qos:
            if q == 128:
                print("  [ACL DENIED] — broker rejected subscription", file=sys.stderr)
                stop_event.set()
                return
        # Subscription live — fire login
        payload = make_login()
        ts = time.strftime("%H:%M:%S")
        print(f"[{ts}] → login   {report_topic}")
        client.publish(report_topic, payload, qos=1)

    def on_message(client, userdata, msg):
        ts = time.strftime("%H:%M:%S")
        payload_str = msg.payload.decode(errors="replace")
        try:
            parsed = json.loads(payload_str)
        except Exception:
            parsed = None

        action = parsed.get("action", "") if parsed else ""

        if action == "login_ack":
            data = parsed.get("data", {})
            session["device_id"] = data.get("device_id", "")
            session["uid"]       = data.get("uid", "")
            print(f"[{ts}] ← login_ack   device_id={session['device_id']}  uid={session['uid']}")
            threading.Thread(target=heart_loop, args=(client,), daemon=True).start()

        elif action == "heart_ack":
            # Print full content on first heart_ack, then just tick in place
            if not session.get("seen_heart_ack"):
                session["seen_heart_ack"] = True
                print(f"[{ts}] ← heart_ack (first)")
                if parsed:
                    print(json.dumps(parsed, indent=2))
            else:
                print(f"[{ts}] ← heart_ack", end="\r")

        else:
            # Anything outside the heartbeat loop — print prominently
            print(f"\n[{ts}] *** ← {action or '?'}   {msg.topic}")
            if parsed:
                print(json.dumps(parsed, indent=2))
            else:
                print(f"  {payload_str!r}")
            print()

        received.append({"ts": ts, "topic": msg.topic, "action": action, "payload": parsed or payload_str})

    def on_disconnect(client, userdata, rc):
        print(f"\nDisconnected (rc={rc})")

    # ── Heartbeat thread ─────────────────────────────────────────────────────

    def heart_loop(client):
        while not stop_event.wait(timeout=args.heart_interval):
            payload = make_heart()
            ts = time.strftime("%H:%M:%S")
            print(f"[{ts}] → heart   {report_topic}")
            client.publish(report_topic, payload, qos=1)

    # ── Connect and run ──────────────────────────────────────────────────────

    client = mqtt.Client(client_id=mac_plain, protocol=mqtt.MQTTv311)
    client.username_pw_set(MQTT_USER, MQTT_PASS)
    client.on_connect    = on_connect
    client.on_subscribe  = on_subscribe
    client.on_message    = on_message
    client.on_disconnect = on_disconnect

    client.connect(args.host, args.port, keepalive=60)

    print(f"Running for {args.duration:.0f}s — Ctrl-C to stop early\n")
    try:
        client.loop_start()
        stop_event.wait(timeout=args.duration)
    except KeyboardInterrupt:
        pass
    finally:
        stop_event.set()
        client.loop_stop()
        client.disconnect()

    notable = [m for m in received if m["action"] not in ("login_ack", "heart_ack")]
    hearts  = sum(1 for m in received if m["action"] == "heart_ack")
    print(f"\n--- {hearts} heart_ack(s), {len(notable)} notable message(s) ---")
    for m in notable:
        print(f"  [{m['ts']}] {m['action'] or '?':12s}  {m['topic']}")
        if isinstance(m["payload"], dict):
            print(json.dumps(m["payload"], indent=4))


if __name__ == "__main__":
    main()
