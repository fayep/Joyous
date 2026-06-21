#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["requests", "paho-mqtt", "pycryptodome"]
# ///
"""
InkJoy MQTT explorer / frame spoofer.

Protocol (from pcap):
  Frame → Server  publish to /device/report/{MAC}
    login:  {"action":"login","clientid":MAC,"msgid":TS,"stamac":MAC,
             "data":{"ver":"M H:2 F:0.5.6","statype":3,...}}
    heart:  {"action":"heart","clientid":MAC,"msgid":TS,"stamac":MAC,
             "data":{"type":3,"ack":1,"wifi":"on",...,"version":"0.5.6"}}

  Server → Frame  publish to /inkjoyap/{MAC}
    login_ack, heart_ack, and OTA push (unknown format — this is what we're after)

Usage:
    uv run inkjoy_mqtt.py --no-auth             # listen only, no account needed
    uv run inkjoy_mqtt.py --no-auth --spoof     # spoof login+heart as frame
    uv run inkjoy_mqtt.py --no-auth --spoof --fake-version 0.1.0  # old fw → trigger autoOta
    uv run inkjoy_mqtt.py --trigger             # login + trigger OTA via REST, then listen
"""

import argparse
import hashlib
import hmac
import json
import os
import subprocess
import sys
import time
import uuid

import requests
import paho.mqtt.client as mqtt

BASE_URL = "https://app.inkjoyframe.com"
KEYCHAIN_SERVICE = "InkJoy"

CLIENT_ID = os.environ.get("INKJOY_CLIENT_ID", "AABBCCDDEEFF")

# Broker host is public; credentials come from serverInfo or env.
MQTT_HOST = os.environ.get("INKJOY_MQTT_HOST", "13.39.148.101")
MQTT_PORT = int(os.environ.get("INKJOY_MQTT_PORT", "1883"))


def env(name: str, default: str = "") -> str:
    val = os.environ.get(name, default)
    if not val:
        raise SystemExit(f"Set {name} environment variable")
    return val


def sign_key() -> bytes:
    return env("INKJOY_SIGN_KEY").encode()


def mqtt_user() -> str:
    return env("INKJOY_MQTT_USER")


def mqtt_pass() -> str:
    return env("INKJOY_MQTT_PASSWORD")

TOPIC_REPORT  = f"/device/report/{CLIENT_ID}"   # frame → server
TOPIC_INKJOY  = f"/inkjoyap/{CLIENT_ID}"         # server → frame (discovered via pcap)


def keychain_password():
    r = subprocess.run(["security", "find-generic-password", "-s", KEYCHAIN_SERVICE, "-w"],
                       capture_output=True, text=True)
    return r.stdout.strip() if r.returncode == 0 else None


def sign(method, path, body=""):
    ts = str(int(time.time() * 1000))
    nonce = uuid.uuid4().hex
    bh = hashlib.sha256(body.encode()).hexdigest() if body else ""
    sig = hmac.new(sign_key(), (method + path + ts + nonce + bh).encode(), hashlib.sha256).hexdigest()
    return {"X-Timestamp": ts, "X-Nonce": nonce, "X-Signature": sig}


def api_login(email, password):
    path = "/inkjoy/api/v1/users/loginByEmail"
    body = json.dumps({"email": email, "password": password})
    headers = sign("POST", path, body)
    headers["Content-Type"] = "application/json"
    r = requests.post(BASE_URL + path, data=body, headers=headers)
    r.raise_for_status()
    resp = r.json()
    assert resp["code"] == 0, f"Login failed: {resp}"
    return resp["data"]["token"], resp["data"]["uid"]


def device_id() -> str:
    return os.environ.get("INKJOY_DEVICE_ID", CLIENT_ID)


def api_trigger_ota(token, uid):
    path = f"/inkjoy/api/v1/device/ota/{device_id()}"
    headers = sign("POST", path)
    headers.update({"Authorization": f"Bearer {token}", "uid": uid,
                    "Content-Type": "application/json"})
    r = requests.post(BASE_URL + path, headers=headers)
    r.raise_for_status()
    return r.json()


def api_server_info(token, uid):
    path = "/inkjoy/api/v1/version/serverInfo"
    headers = sign("GET", path)
    headers.update({"Authorization": f"Bearer {token}", "uid": uid})
    r = requests.get(BASE_URL + path, headers=headers)
    r.raise_for_status()
    resp = r.json()
    assert resp["code"] == 0, f"serverInfo failed: {resp}"
    raw = resp.get("data")
    if not raw:
        return resp
    if isinstance(raw, str) and not raw.strip().startswith("{"):
        return _decrypt_server_info(raw, uid)
    return json.loads(raw) if isinstance(raw, str) else raw


def _decrypt_server_info(ciphertext_b64: str, uid: str) -> dict:
    from Crypto.Cipher import AES
    import base64
    key = uid[:16].encode("utf-8")
    data = base64.b64decode(ciphertext_b64)
    cipher = AES.new(key, AES.MODE_GCM, nonce=data[:12])
    plaintext = cipher.decrypt_and_verify(data[12:-16], data[-16:])
    return json.loads(plaintext)


# ── Frame-mimicking payloads (exact format from pcap) ─────────────────────

def make_login(fake_version=None):
    fw = fake_version or "0.5.6"
    return json.dumps({
        "action": "login",
        "clientid": CLIENT_ID,
        "msgid": str(int(time.time() * 1000)),
        "stamac": CLIENT_ID,
        "data": {
            "ver": f"M H:2 F:{fw}",
            "statype": 3,
            "sleep_mode": 2,
            "sleep_begin_time": "07:00",
            "sleep_end_time": "13:00",
            "inkjoy": True,
        },
    })


def make_heart(fake_version=None):
    fw = fake_version or "0.5.6"
    return json.dumps({
        "action": "heart",
        "clientid": CLIENT_ID,
        "msgid": str(int(time.time() * 1000)),
        "stamac": CLIENT_ID,
        "data": {
            "type": 3,
            "ack": 1,
            "wifi": "on",
            "wifi_name": "ExampleWiFi",
            "ble": "off",
            "tf": "absent",
            "tfsize": 0,
            "tfused": 0,
            "orientation": 0,
            "battery": 73,
            "wifi_listen_iv": 50,
            "wifi_rssi": -45,
            "wifi_ch": 1,
            "ble_rssi": 0,
            "version": fw,
        },
    })


# ── Main ───────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--email", default=os.environ.get("INKJOY_EMAIL", ""))
    parser.add_argument("--password")
    parser.add_argument("--no-auth", action="store_true",
                        help="Skip API login; use INKJOY_MQTT_USER/PASSWORD env vars")
    parser.add_argument("--spoof", action="store_true",
                        help="Send login+heart messages as the frame")
    parser.add_argument("--fake-version", default=None, metavar="VER",
                        help="Firmware version to report (default: 0.5.6 current)")
    parser.add_argument("--listen", type=int, default=120, metavar="SECS")
    parser.add_argument("--trigger", action="store_true",
                        help="Trigger OTA via REST API after connecting (requires auth)")
    parser.add_argument("--trigger-delay", type=int, default=5, metavar="SECS")
    args = parser.parse_args()

    token = uid = None
    if args.no_auth:
        host, port, username, password_mqtt = MQTT_HOST, MQTT_PORT, mqtt_user(), mqtt_pass()
        print("Using MQTT credentials from environment (no API login).")
        if args.trigger:
            print("ERROR: --trigger requires auth; remove --no-auth")
            sys.exit(1)
    else:
        if not args.email:
            raise SystemExit("Set --email or INKJOY_EMAIL for API login")
        password = args.password or keychain_password()
        if not password:
            import getpass
            password = getpass.getpass("Password: ")
        print("Logging in...")
        token, uid = api_login(args.email, password)
        print(f"  uid={uid}")
        print("Fetching MQTT broker info...")
        info = api_server_info(token, uid)
        cfg = info.get("mqttServer") or info
        host = cfg.get("ip") or cfg.get("host")
        port = int(cfg.get("port", 1883))
        username = cfg.get("username", "device")
        password_mqtt = cfg.get("password", "")
        if not host:
            print(f"No MQTT host in serverInfo: {json.dumps(info, indent=2)}")
            sys.exit(1)

    print(f"\nBroker: {host}:{port}  user={username}")
    print(f"Subscribing to: {TOPIC_INKJOY}  (server→frame)")
    print(f"                {TOPIC_REPORT}  (frame→server, to see our own publishes)")

    received = []

    def on_connect(client, userdata, flags, rc):
        if rc != 0:
            print(f"MQTT connect failed rc={rc}")
            return
        print("Connected.")
        for t in [TOPIC_INKJOY, TOPIC_REPORT]:
            result, mid = client.subscribe(t, qos=1)
            status = "OK" if result == mqtt.MQTT_ERR_SUCCESS else f"FAILED({result})"
            print(f"  subscribe {t!r} → {status}")

    def on_subscribe(client, userdata, mid, granted_qos):
        print(f"  confirmed mid={mid} qos={granted_qos}")

    def on_message(client, userdata, msg):
        ts = time.strftime("%H:%M:%S")
        try:
            payload = msg.payload.decode(errors="replace")
        except Exception:
            payload = msg.payload.hex()
        print(f"\n[{ts}] TOPIC: {msg.topic}")
        try:
            print(json.dumps(json.loads(payload), indent=2))
        except Exception:
            print(f"  raw: {payload!r}")
        received.append((msg.topic, payload))

    def on_disconnect(client, userdata, rc):
        print(f"Disconnected rc={rc}")

    client_id = f"InkJoyAndroid_{uuid.uuid4().hex[:8]}"
    client = mqtt.Client(client_id=client_id, protocol=mqtt.MQTTv311)
    client.username_pw_set(username, password_mqtt)
    client.on_connect = on_connect
    client.on_subscribe = on_subscribe
    client.on_message = on_message
    client.on_disconnect = on_disconnect

    print(f"\nConnecting as {client_id} ...")
    client.connect(host, port, keepalive=60)
    client.loop_start()
    time.sleep(2)

    if args.spoof:
        login_msg = make_login(args.fake_version)
        print(f"\nPublishing login → {TOPIC_REPORT}")
        print(f"  {login_msg}")
        client.publish(TOPIC_REPORT, login_msg, qos=1)
        time.sleep(1)
        heart_msg = make_heart(args.fake_version)
        print(f"\nPublishing heart → {TOPIC_REPORT}")
        print(f"  {heart_msg}")
        client.publish(TOPIC_REPORT, heart_msg, qos=1)

    if args.trigger:
        print(f"\nWaiting {args.trigger_delay}s before triggering OTA...")
        time.sleep(args.trigger_delay)
        print(f"Triggering OTA via REST for device {device_id()}...")
        resp = api_trigger_ota(token, uid)
        print(f"  response: {resp}")

    print(f"\nListening for {args.listen}s... (Ctrl-C to stop early)\n")
    try:
        time.sleep(args.listen)
    except KeyboardInterrupt:
        pass

    client.loop_stop()
    client.disconnect()

    print(f"\n── Summary: {len(received)} message(s) received ──")
    for topic, payload in received:
        print(f"  [{topic}] {payload[:120]}")


if __name__ == "__main__":
    main()
