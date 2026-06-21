#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["paho-mqtt"]
# ///
"""
Watch all InkJoy MQTT traffic we can see.
Subscribe to every topic the broker ACL permits and print everything.

Requires INKJOY_MQTT_USER and INKJOY_MQTT_PASSWORD environment variables.
"""

import json
import os
import time
import uuid

import paho.mqtt.client as mqtt


def env(name: str) -> str:
    val = os.environ.get(name)
    if not val:
        raise SystemExit(f"Set {name} environment variable")
    return val


MQTT_HOST = os.environ.get("INKJOY_MQTT_HOST", "13.39.148.101")
MQTT_PORT = int(os.environ.get("INKJOY_MQTT_PORT", "1883"))
MQTT_USER = env("INKJOY_MQTT_USER")
MQTT_PASS = env("INKJOY_MQTT_PASSWORD")

TOPICS = [
    ("#", 1),
    ("/device/report/+", 1),
    ("/device/report/#", 1),
    ("/inkjoyap/+", 1),
]

def on_connect(client, userdata, flags, rc):
    print(f"Connected (rc={rc})")
    for topic, qos in TOPICS:
        r, _ = client.subscribe(topic, qos=qos)
        status = "OK" if r == 0 else f"FAILED({r})"
        print(f"  subscribe {topic!r}: {status}")

def on_subscribe(client, userdata, mid, granted_qos):
    for i, q in enumerate(granted_qos):
        if q == 128:
            print(f"  [ACL DENIED] mid={mid} index={i}")

def on_message(client, userdata, msg):
    ts = time.strftime("%H:%M:%S")
    payload = msg.payload.decode(errors='replace')
    print(f"\n[{ts}] {msg.topic}")
    try:
        parsed = json.loads(payload)
        print(json.dumps(parsed, indent=2))
    except Exception:
        print(f"  {payload!r}")

def on_disconnect(client, userdata, rc):
    print(f"Disconnected rc={rc}")

spy_id = f"ij-watch-{uuid.uuid4().hex[:8]}"
client = mqtt.Client(client_id=spy_id, protocol=mqtt.MQTTv311)
client.username_pw_set(MQTT_USER, MQTT_PASS)
client.on_connect    = on_connect
client.on_subscribe  = on_subscribe
client.on_message    = on_message
client.on_disconnect = on_disconnect

print(f"Connecting as {spy_id}…")
client.connect(MQTT_HOST, MQTT_PORT, keepalive=60)
print("Watching — Ctrl-C to stop.\n")
try:
    client.loop_forever()
except KeyboardInterrupt:
    pass
client.disconnect()
