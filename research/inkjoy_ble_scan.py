#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["bleak"]
# ///
"""
Scan for InkJoy frame BLE services and check for OTA/DFU endpoints.

Usage:
    uv run inkjoy_ble_scan.py              # scan and immediately probe candidates
    uv run inkjoy_ble_scan.py --scan-only  # just list nearby BLE devices
    uv run inkjoy_ble_scan.py --addr <CoreBluetooth-UUID>
"""

import argparse
import asyncio
from bleak import BleakClient, BleakScanner
from bleak.backends.characteristic import BleakGATTCharacteristic

# ── Known service UUIDs ────────────────────────────────────────────────────

KNOWN_SERVICES = {
    "0000ffff-0000-1000-8000-00805f9b34fb": "BluFi (Espressif provisioning)",
    "0000ff01-0000-1000-8000-00805f9b34fb": "BluFi Write char",
    "0000ff02-0000-1000-8000-00805f9b34fb": "BluFi Notify char",
    "e0010001-0000-4000-8000-00805f9b34fb": "IJ Image Transfer Service",
    "e0010002-0000-4000-8000-00805f9b34fb": "IJ Data (write image chunks)",
    "e0010003-0000-4000-8000-00805f9b34fb": "IJ Notify (img_progress/done/fail)",
    "0000fe59-0000-1000-8000-00805f9b34fb": "*** Nordic Secure DFU Service ***",
    "8ec90001-f315-4f60-9fb8-838830daea50": "*** Nordic DFU Control Point ***",
    "8ec90002-f315-4f60-9fb8-838830daea50": "*** Nordic DFU Packet ***",
    "8ec90003-f315-4f60-9fb8-838830daea50": "*** Nordic Buttonless DFU (with bonds) ***",
    "8ec90004-f315-4f60-9fb8-838830daea50": "*** Nordic Buttonless DFU (bondless) ***",
    "00001530-1212-efde-1523-785feabcd123": "*** Nordic Legacy DFU Service ***",
    "00001531-1212-efde-1523-785feabcd123": "*** Nordic Legacy DFU Control Point ***",
    "00001532-1212-efde-1523-785feabcd123": "*** Nordic Legacy DFU Packet ***",
    "0000ff00-0000-1000-8000-00805f9b34fb": "Espressif custom service",
    "00001800-0000-1000-8000-00805f9b34fb": "Generic Access",
    "00001801-0000-1000-8000-00805f9b34fb": "Generic Attribute",
    "0000180a-0000-1000-8000-00805f9b34fb": "Device Information",
    "0000180f-0000-1000-8000-00805f9b34fb": "Battery Service",
    "00002a00-0000-1000-8000-00805f9b34fb": "Device Name",
    "00002a01-0000-1000-8000-00805f9b34fb": "Appearance",
    "00002a05-0000-1000-8000-00805f9b34fb": "Service Changed",
    "00002a19-0000-1000-8000-00805f9b34fb": "Battery Level",
    "00002a24-0000-1000-8000-00805f9b34fb": "Model Number",
    "00002a25-0000-1000-8000-00805f9b34fb": "Serial Number",
    "00002a26-0000-1000-8000-00805f9b34fb": "Firmware Revision",
    "00002a27-0000-1000-8000-00805f9b34fb": "Hardware Revision",
    "00002a28-0000-1000-8000-00805f9b34fb": "Software Revision",
    "00002a29-0000-1000-8000-00805f9b34fb": "Manufacturer Name",
    "00002902-0000-1000-8000-00805f9b34fb": "CCCD",
}

OTA_KEYWORDS = ["dfu", "ota", "update", "upgrade", "fe59", "1530", "8ec9"]

IGNORE_NAMES = {
    "Faye16", "Bedroom", "Lounge", "AqaraRecorder", "Apple Vision Pro",
    "Faye’s Apple\xa0Watch", "Faye’s AirPods Pro",
    "Faye’s AirPods Pro #2", "Faye’s Mac Studio",
    "[TV] UN65JU650D", "[LG] webOS TV OLED65C4PUA", "M1ni",
    "LAP-V201S-WUS", "NYRHZ", "HEADWIND C1D8", "Aranet4 17463",
    "EF-D5111314", "Petkit_T4", "Petkit_D4", "Aqara-0804-90d",
    "[TV] Samsung 6 Series (50)", "LE-Vox Machina", "LH-FG1D", "net",
    "N01GW",
}
IGNORE_KW = ["airpod", "watch", "mac studio", "iphone", "petkit", "aqara",
             "aranet", "samsung", "oled", "webos", "vox machina", "headwind",
             "apple tv", "vision pro"]


def label(uuid: str) -> str:
    u = uuid.lower()
    known = KNOWN_SERVICES.get(u)
    if known:
        return known
    for kw in OTA_KEYWORDS:
        if kw in u:
            return f"*** possible OTA *** {uuid}"
    return uuid


def prop_str(char: BleakGATTCharacteristic) -> str:
    return ",".join(char.properties)


def is_inkjoy(dev, adv) -> bool:
    """Only match on definitive InkJoy advertisement UUIDs."""
    uuids = [str(u).lower() for u in (adv.service_uuids or [])]
    return any("e0010001" in u or "0000ffff" in u for u in uuids)


async def enumerate_services(address: str):
    print(f"\nConnecting to {address} ...")
    async with BleakClient(address, timeout=15.0) as client:
        print(f"Connected. MTU={client.mtu_size}")
        print(f"\n{'='*70}")

        ota_found = []

        for service in client.services:
            svc_uuid = service.uuid.lower()
            svc_label = label(svc_uuid)
            is_ota = any(kw in svc_uuid for kw in OTA_KEYWORDS) or "***" in svc_label
            marker = " <<<<" if is_ota else ""
            print(f"\nSERVICE  {svc_uuid}  [{svc_label}]{marker}")

            for char in service.characteristics:
                char_uuid = char.uuid.lower()
                char_label = label(char_uuid)
                is_ota_char = any(kw in char_uuid for kw in OTA_KEYWORDS) or "***" in char_label
                marker2 = " <<<<" if is_ota_char else ""
                print(f"  CHAR   {char_uuid}  [{char_label}]  props=[{prop_str(char)}]{marker2}")

                if is_ota or is_ota_char:
                    ota_found.append((service.uuid, char.uuid, prop_str(char)))

                if "read" in char.properties:
                    if any(x in svc_uuid for x in ["180a", "e0010001"]) or \
                       any(x in char_uuid for x in ["2a24","2a25","2a26","2a27","2a28","2a29","e0010002"]):
                        try:
                            val = await client.read_gatt_char(char)
                            try:
                                decoded = val.decode("utf-8").strip()
                            except Exception:
                                decoded = val.hex()
                            print(f"         => {decoded!r}")
                        except Exception as e:
                            print(f"         => (read error: {e})")

                for desc in char.descriptors:
                    print(f"    DESC {desc.uuid}  [{label(desc.uuid.lower())}]")

        print(f"\n{'='*70}")
        if ota_found:
            print("OTA/DFU CHARACTERISTICS FOUND:")
            for svc, ch, props in ota_found:
                print(f"  svc={svc}  char={ch}  props=[{props}]")
        else:
            print("No standard OTA/DFU service found.")


async def scan_and_probe(scan_duration: float = 15.0):
    """Scan continuously; connect to each candidate as soon as it appears."""
    seen = set()
    probed = set()
    queue: asyncio.Queue = asyncio.Queue()

    print(f"Scanning for up to {scan_duration}s — connecting to candidates immediately.")
    print("Press the frame button NOW.\n")

    def on_detect(device, adv):
        addr = device.address
        if addr in seen:
            return
        seen.add(addr)
        name = device.name or ""
        uuids = [str(u).lower() for u in (adv.service_uuids or [])]
        flag = " <-- InkJoy!" if any("e0010001" in u or "0000ffff" in u for u in uuids) else ""
        print(f"  {addr}  {name!r:30s}  RSSI={adv.rssi}{flag}")
        if uuids:
            for u in uuids:
                print(f"      adv: {label(u)}")
        if is_inkjoy(device, adv):
            queue.put_nowait((device, adv))

    scanner = BleakScanner(detection_callback=on_detect)
    await scanner.start()
    await asyncio.sleep(scan_duration)
    await scanner.stop()

    if queue.empty():
        print("\nNo candidates found. Try pressing the frame button and running again.")
        return

    candidates = []
    while not queue.empty():
        candidates.append(queue.get_nowait())
    candidates.sort(key=lambda x: x[1].rssi, reverse=True)

    print(f"\nProbing {len(candidates)} candidate(s)...")
    for dev, adv in candidates:
        if dev.address in probed:
            continue
        probed.add(dev.address)
        print(f"\n── {dev.name or '(unnamed)'}  {dev.address}  RSSI={adv.rssi} ──")
        try:
            await enumerate_services(dev.address)
        except Exception as e:
            print(f"  Could not connect: {e}")


BASELINE_FILE = "/Volumes/CaseSensitive/InkJoy/ble_baseline.txt"


async def build_baseline(duration: float = 60.0):
    seen = {}
    def on_detect(device, adv):
        seen[device.address] = device.name or ""

    print(f"Building baseline — scanning for {duration}s. Frame should NOT be advertising.")
    scanner = BleakScanner(detection_callback=on_detect)
    await scanner.start()
    await asyncio.sleep(duration)
    await scanner.stop()

    with open(BASELINE_FILE, "w") as f:
        for addr, name in sorted(seen.items()):
            f.write(f"{addr}\t{name}\n")
    print(f"Saved {len(seen)} devices to {BASELINE_FILE}")
    for addr, name in sorted(seen.items()):
        print(f"  {addr}  {name!r}")


async def scan_for_new(duration: float = 20.0):
    baseline = set()
    try:
        with open(BASELINE_FILE) as f:
            for line in f:
                addr = line.split("\t")[0].strip()
                if addr:
                    baseline.add(addr)
        print(f"Loaded {len(baseline)} baseline devices to ignore.")
    except FileNotFoundError:
        print("No baseline file found — run with --baseline first.")
        return

    found_new = {}

    def on_detect(device, adv):
        addr = device.address
        if addr in baseline or addr in found_new:
            return
        found_new[addr] = (device, adv)
        name = device.name or "(unnamed)"
        uuids = [str(u).lower() for u in (adv.service_uuids or [])]
        print(f"\n  NEW: {addr}  {name!r}  RSSI={adv.rssi}")
        for u in uuids:
            print(f"       adv: {label(u)}")

    print(f"Scanning for {duration}s — press the frame button NOW...\n")
    scanner = BleakScanner(detection_callback=on_detect)
    await scanner.start()
    await asyncio.sleep(duration)
    await scanner.stop()

    if not found_new:
        print("\nNo new devices appeared.")
        return

    print(f"\n{len(found_new)} new device(s) appeared. Connecting to each...")
    for addr, (dev, adv) in sorted(found_new.items(), key=lambda x: x[1][1].rssi, reverse=True):
        print(f"\n── {dev.name or '(unnamed)'}  {addr}  RSSI={adv.rssi} ──")
        try:
            await enumerate_services(addr)
        except Exception as e:
            print(f"  Could not connect: {e}")


async def main():
    parser = argparse.ArgumentParser(description="InkJoy BLE GATT enumerator")
    parser.add_argument("--addr", help="CoreBluetooth UUID to connect to directly")
    parser.add_argument("--scan-only", action="store_true")
    parser.add_argument("--baseline", action="store_true", help="60s scan to record all non-frame devices")
    parser.add_argument("--scan-time", type=float, default=20.0)
    args = parser.parse_args()

    if args.addr:
        await enumerate_services(args.addr)
        return

    if args.baseline:
        await build_baseline(60.0)
        return

    if args.scan_only:
        print(f"Scanning for {args.scan_time}s...")
        devices = await BleakScanner.discover(timeout=args.scan_time, return_adv=True)
        for dev, adv in sorted(devices.values(), key=lambda x: x[1].rssi, reverse=True):
            uuids = [str(u).lower() for u in (adv.service_uuids or [])]
            flag = " <-- InkJoy!" if any("e0010001" in u or "0000ffff" in u for u in uuids) else ""
            print(f"  {dev.address}  {(dev.name or '')!r:30s}  RSSI={adv.rssi}{flag}")
            for u in uuids:
                print(f"      adv: {label(u)}")
        return

    await scan_for_new(args.scan_time)


asyncio.run(main())
