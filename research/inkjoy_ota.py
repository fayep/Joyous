#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["requests", "paho-mqtt"]
# ///
"""
InkJoy frame OTA checker/trigger with MQTT observation.

Credentials are read from the macOS Keychain (service "InkJoy", account
user@example.com). Password can also be passed via --password or prompted.

After triggering OTA, connects to the MQTT broker (credentials from
GET /version/serverInfo) and listens on /device/report/{clientId} to capture
the firmware URL the server pushes to the frame.

Usage:
    uv run inkjoy_ota.py                        # check only
    uv run inkjoy_ota.py --trigger              # trigger + listen for firmware URL
    uv run inkjoy_ota.py --trigger --device ID  # target a specific deviceId
"""

import argparse
import hashlib
import hmac
import json
import subprocess
import sys
import time
import uuid

import requests

BASE_URL = "https://app.inkjoyframe.com"
SIGN_KEY = b"REDACTED_SIGN_KEY"
DEFAULT_EMAIL = "user@example.com"
KEYCHAIN_SERVICE = "InkJoy"
MQTT_LISTEN_SECONDS = 60


# ---------------------------------------------------------------------------
# Keychain
# ---------------------------------------------------------------------------

def keychain_password() -> str | None:
    try:
        result = subprocess.run(
            ["security", "find-generic-password", "-s", KEYCHAIN_SERVICE, "-w"],
            capture_output=True, text=True
        )
        if result.returncode == 0:
            return result.stdout.strip()
    except FileNotFoundError:
        pass
    return None


# ---------------------------------------------------------------------------
# Request signing
# ---------------------------------------------------------------------------

def _sha256_hex(text: str) -> str:
    return hashlib.sha256(text.encode()).hexdigest()


def _hmac_sha256_hex(data: str) -> str:
    return hmac.new(SIGN_KEY, data.encode(), hashlib.sha256).hexdigest()


def _sign_headers(method: str, path: str, body: str = "") -> dict:
    timestamp = str(int(time.time() * 1000))
    nonce = uuid.uuid4().hex
    body_hash = _sha256_hex(body) if body else ""
    signature = _hmac_sha256_hex(method + path + timestamp + nonce + body_hash)
    return {"X-Timestamp": timestamp, "X-Nonce": nonce, "X-Signature": signature}


# ---------------------------------------------------------------------------
# API client
# ---------------------------------------------------------------------------

class InkJoyClient:
    def __init__(self):
        self.session = requests.Session()
        self.session.headers.update({"Content-Type": "application/json"})
        self._token: str | None = None
        self._uid: str | None = None

    def _signed_headers(self, method: str, path: str, body: str = "") -> dict:
        h = _sign_headers(method, path, body)
        if self._token:
            h["Authorization"] = f"Bearer {self._token}"
        if self._uid:
            h["uid"] = self._uid
        return h

    def _post(self, path: str, payload: dict | None = None) -> dict:
        body = json.dumps(payload) if payload else ""
        headers = self._signed_headers("POST", path, body)
        resp = self.session.post(BASE_URL + path, data=body or None, headers=headers)
        resp.raise_for_status()
        return resp.json()

    def _get(self, path: str) -> dict:
        headers = self._signed_headers("GET", path)
        resp = self.session.get(BASE_URL + path, headers=headers)
        resp.raise_for_status()
        return resp.json()

    def login(self, email: str, password: str) -> None:
        result = self._post("/inkjoy/api/v1/users/loginByEmail",
                            {"email": email, "password": password})
        if result.get("code") != 0:
            raise RuntimeError(f"Login failed: {result.get('msg')} — {result.get('msgDetail')}")
        data = result["data"]
        self._token = data["token"]
        self._uid = data["uid"]
        print(f"Logged in as: {data['username']}")

    def list_devices(self) -> list[dict]:
        result = self._post("/inkjoy/api/v1/device/list", {"direction": "ASC"})
        if result.get("code") != 0:
            raise RuntimeError(f"Failed to list devices: {result.get('msg')}")
        return result.get("data") or []

    def get_server_info(self) -> dict:
        result = self._get("/inkjoy/api/v1/version/serverInfo")
        if result.get("code") != 0:
            raise RuntimeError(f"Failed to get server info: {result.get('msg')}")
        raw = result.get("data") or "{}"
        return json.loads(raw) if isinstance(raw, str) else raw

    def trigger_ota(self, device_id: str) -> dict:
        path = f"/inkjoy/api/v1/device/ota/{device_id}"
        headers = self._signed_headers("POST", path)
        resp = self.session.post(BASE_URL + path, headers=headers)
        resp.raise_for_status()
        return resp.json()


# ---------------------------------------------------------------------------
# MQTT listener
# ---------------------------------------------------------------------------

def listen_for_ota(server_info: dict, client_ids: list[str], timeout: int) -> None:
    try:
        import paho.mqtt.client as mqtt
    except ImportError:
        print("  (paho-mqtt not available, skipping MQTT listen)")
        return

    mqtt_cfg = server_info.get("mqttServer") or server_info
    host = mqtt_cfg.get("ip") or mqtt_cfg.get("host")
    port = int(mqtt_cfg.get("port", 1883))
    username = mqtt_cfg.get("username", "device")
    password = mqtt_cfg.get("password", "")

    if not host:
        print("  (no MQTT host in serverInfo, skipping listen)")
        return

    topics = [f"/device/report/{cid}" for cid in client_ids]
    print(f"\nConnecting to MQTT broker {host}:{port}")
    print(f"Listening on: {', '.join(topics)}")
    print(f"(waiting up to {timeout}s for firmware URL in OTA response...)\n")

    received = []

    def on_connect(client, userdata, flags, rc):
        if rc == 0:
            for topic in topics:
                client.subscribe(topic, qos=1)
        else:
            print(f"  MQTT connect failed (rc={rc})")

    def on_message(client, userdata, msg):
        payload = msg.payload.decode(errors="replace")
        print(f"[{msg.topic}] {payload}")
        received.append(payload)

    client = mqtt.Client(client_id=f"inkjoy-ota-spy-{uuid.uuid4().hex[:8]}")
    client.username_pw_set(username, password)
    client.on_connect = on_connect
    client.on_message = on_message

    try:
        client.connect(host, port, keepalive=30)
        client.loop_start()
        time.sleep(timeout)
        client.loop_stop()
        client.disconnect()
    except Exception as e:
        print(f"  MQTT error: {e}")

    if not received:
        print("  (no messages received — frame may be offline or OTA already current)")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def print_device(device: dict, index: int) -> None:
    name = device.get("deviceName", "(unnamed)")
    vi = device.get("versionInfo") or {}
    has_update = vi.get("hasNewVersion", False)
    current = vi.get("currentVersion", "unknown")
    newest = vi.get("newVersion", "")
    describe = vi.get("versionDescribe", "")

    update_str = (f"  UPDATE AVAILABLE: {current} -> {newest}" if has_update
                  else f"  Up to date ({current})")
    if describe and has_update:
        update_str += f"\n    Notes: {describe}"

    print(f"\n[{index}] {name}")
    print(f"    deviceId : {device.get('deviceId', '?')}")
    print(f"    MAC      : {device.get('clientId', '?')}")
    print(update_str)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    parser = argparse.ArgumentParser(description="InkJoy OTA checker")
    parser.add_argument("--trigger", action="store_true",
                        help="Trigger OTA and listen for firmware URL via MQTT")
    parser.add_argument("--device", metavar="DEVICE_ID",
                        help="Only act on this specific deviceId")
    parser.add_argument("--email", default=DEFAULT_EMAIL, help="Account email")
    parser.add_argument("--password", help="Account password (falls back to Keychain)")
    parser.add_argument("--mqtt-timeout", type=int, default=MQTT_LISTEN_SECONDS,
                        metavar="SECS", help="How long to listen on MQTT after trigger")
    args = parser.parse_args()

    password = args.password or keychain_password()
    if not password:
        from getpass import getpass
        password = getpass("Password: ")

    client = InkJoyClient()
    try:
        client.login(args.email, password)
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)

    try:
        devices = client.list_devices()
    except Exception as e:
        print(f"Error fetching devices: {e}", file=sys.stderr)
        sys.exit(1)

    if not devices:
        print("No frames found on this account.")
        return

    print(f"\nFound {len(devices)} frame(s):")
    for i, dev in enumerate(devices, 1):
        print_device(dev, i)

    if not args.trigger:
        print("\nRun with --trigger to push OTA and capture the firmware URL.")
        return

    to_update = [
        d for d in devices
        if (d.get("versionInfo") or {}).get("hasNewVersion")
        and (args.device is None or d.get("deviceId") == args.device)
    ]

    if args.device and not to_update:
        matching = [d for d in devices if d.get("deviceId") == args.device]
        if matching:
            print(f"\nNo update flagged for {args.device}, triggering anyway.")
            to_update = matching
        else:
            print(f"\nDevice ID '{args.device}' not found.", file=sys.stderr)
            sys.exit(1)

    if not to_update:
        print("\nNo frames have a pending firmware update.")
        return

    # Fetch MQTT broker creds before triggering so we're subscribed first
    try:
        server_info = client.get_server_info()
    except Exception as e:
        print(f"Warning: couldn't fetch server info for MQTT: {e}")
        server_info = {}

    client_ids = [d["clientId"] for d in to_update if d.get("clientId")]

    # Subscribe first, then trigger — so we don't miss the response
    import threading
    mqtt_thread = threading.Thread(
        target=listen_for_ota,
        args=(server_info, client_ids, args.mqtt_timeout),
        daemon=True
    )
    mqtt_thread.start()
    time.sleep(1)  # give MQTT a moment to connect before the trigger fires

    print(f"\nTriggering OTA on {len(to_update)} frame(s)...")
    for dev in to_update:
        name = dev.get("deviceName", "(unnamed)")
        device_id = dev["deviceId"]
        try:
            result = client.trigger_ota(device_id)
            if result.get("code") == 0:
                print(f"  OK  {name} ({device_id})")
            else:
                print(f"  ERR {name} ({device_id}): {result.get('code')} — {result.get('msg')}")
        except Exception as e:
            print(f"  ERR {name} ({device_id}): {e}")

    mqtt_thread.join()


if __name__ == "__main__":
    main()
