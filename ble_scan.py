#!/usr/bin/env python3
"""
BLE scanner for Samsung ePaper display.
Looks for manufacturer data (company ID 0x0075 = 117) and dumps it.
Also tries to read the PIN characteristic via GATT.

Samsung ePaper BLE:
  Manufacturer ID: 117 (0x75)
  bArr[1] == 0x22 (34) identifies ePaper device
  Service UUID: 53564344-6576-6963-6553-657474696E67
  Characteristic (setting):   UUID_CHARACTERISTIC_SETTING
  Characteristic (pairing):   UUID_CHARACTERISTIC_CONTROL_PAIRING

Run with: .venv/bin/python ble_scan.py
"""

import asyncio
import sys
from bleak import BleakScanner, BleakClient

TARGET_MAC = "b0:f2:f6:57:d5:cd"  # Samsung EM32DX
SAMSUNG_COMPANY_ID = 0x0075        # 117

# UUIDs from BLEBlessedDataSourceImpKt.java
UUID_SERVICE_SETTING   = "53564344-6576-6963-6553-657474696e67"
UUID_CHARACTERISTIC_SETTING       = None  # filled by scan
UUID_CHARACTERISTIC_CONTROL_PAIRING = None


def decode_manuf(data: bytes) -> str:
    return " ".join(f"{b:02x}" for b in data)


async def scan():
    print("Scanning for BLE devices (10s)...", flush=True)
    found = {}

    def callback(device, adv):
        mfr = adv.manufacturer_data
        if SAMSUNG_COMPANY_ID in mfr:
            data = mfr[SAMSUNG_COMPANY_ID]
            found[device.address] = (device, adv, data)

    async with BleakScanner(callback):
        await asyncio.sleep(10)

    if not found:
        print("No Samsung ePaper devices found (manufacturer ID 0x0075).")
        return

    print(f"\nFound {len(found)} device(s):")
    for addr, (dev, adv, data) in found.items():
        print(f"  {dev.name or '?':30s}  {addr}  rssi={adv.rssi}")
        print(f"    manufacturer[0x75]: [{decode_manuf(data)}]  len={len(data)}")
        if len(data) > 0:
            print(f"    data[0]={data[0]:02x}  ", end="")
            if len(data) > 1:
                print(f"data[1]={data[1]:02x}  ", end="")
                if data[1] == 0x22:
                    print("← ePaper device marker (0x22=34)", end="")
            print()

        # Try to decode PIN-like bytes
        try:
            text = data.decode("utf-8", errors="replace")
            print(f"    as ASCII: {repr(text)}")
        except Exception:
            pass

    # Try GATT connect to target
    target = None
    for addr, (dev, adv, data) in found.items():
        if addr.lower() == TARGET_MAC.lower():
            target = dev
            break

    if target is None:
        print(f"\nTarget {TARGET_MAC} not in scan results. Try moving closer or power-cycling the display.")
        return

    print(f"\nConnecting to {target.address} via GATT...")
    try:
        async with BleakClient(target) as client:
            print(f"  Connected: {client.is_connected}")
            print("  Services:")
            for svc in client.services:
                print(f"    {svc.uuid}  ({svc.description})")
                for char in svc.characteristics:
                    print(f"      {char.uuid}  props={char.properties}")
                    if "read" in char.properties:
                        try:
                            val = await client.read_gatt_char(char.uuid)
                            print(f"        value: {val.hex()} = {repr(val)}")
                        except Exception as e:
                            print(f"        read error: {e}")
    except Exception as e:
        print(f"  GATT connection failed: {e}")


if __name__ == "__main__":
    asyncio.run(scan())
