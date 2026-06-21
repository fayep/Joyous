"""
Adopt an InkJoy frame: send WiFi config then mqtt_config via BluFi.
Usage: uv run --with bleak python3 blufi_adopt.py <wifi-ssid> <wifi-pw> <hub-ip> [hub-port] [mqtt-usr] [mqtt-pwd]
"""
import asyncio, json, time, sys
from bleak import BleakClient, BleakScanner

BLUFI_P2E = "0000ff01-0000-1000-8000-00805f9b34fb"
BLUFI_E2P = "0000ff02-0000-1000-8000-00805f9b34fb"

_seq = 0

def _frame(type_byte: int, data: bytes) -> bytes:
    global _seq
    frame_ctrl = 0x00  # no encrypt, no checksum
    pkt = bytes([type_byte, frame_ctrl, _seq & 0xff, len(data)]) + data
    _seq += 1
    return pkt

def _notify(sender, data):
    print(f"  ← {data.hex()}  {data!r}")

async def adopt(wifi_ssid: str, wifi_pw: str, hub_ip: str, hub_port: int, mqtt_usr: str, mqtt_pwd: str):
    print("Scanning for IJ_ frames...")
    devices = await BleakScanner.discover(timeout=8.0)
    frames = [d for d in devices if d.name and d.name.startswith("IJ_")]
    if not frames:
        print("No IJ_ frames found.")
        return False
    for f in frames:
        print(f"  {f.name}  {f.address}")
    target = frames[0]
    print(f"\nConnecting to {target.name}...")

    async with BleakClient(target.address) as client:
        print(f"Connected")
        await client.start_notify(BLUFI_E2P, _notify)
        await asyncio.sleep(0.5)

        async def send(type_byte: int, data: bytes, label: str):
            pkt = _frame(type_byte, data)
            print(f"  → {label}: type=0x{type_byte:02x} len={len(data)}  {pkt.hex()}")
            await client.write_gatt_char(BLUFI_P2E, pkt, response=True)
            await asyncio.sleep(0.3)

        # 1. Set op mode → STA (type 8, data=[1])
        await send(8, bytes([1]), "set-op-mode STA")

        # 2. WiFi SSID (type 9)
        await send(9, wifi_ssid.encode(), f"ssid={wifi_ssid!r}")

        # 3. WiFi password (type 13)
        await send(13, wifi_pw.encode(), "wifi-password")

        # 4. End of WiFi info (type 12, empty)
        await send(12, b"", "wifi-end")

        await asyncio.sleep(1.0)

        # 5. mqtt_config JSON (type 77)
        cfg = {
            "msgid": str(int(time.time() * 1000)),
            "action": "mqtt_config",
            "data": {"host": hub_ip, "port": hub_port, "usr": mqtt_usr, "pwd": mqtt_pwd},
        }
        payload = json.dumps(cfg, separators=(",", ":")).encode()
        await send(77, payload, "mqtt_config")

        print("\nWaiting 5s for response...")
        await asyncio.sleep(5)
    return True

if len(sys.argv) < 4:
    print("Usage: blufi_adopt.py <wifi-ssid> <wifi-pw> <hub-ip> [hub-port=11883] [mqtt-usr=inkjoy] [mqtt-pwd=inkjoy]")
    sys.exit(1)

wifi_ssid = sys.argv[1]
wifi_pw   = sys.argv[2]
hub_ip    = sys.argv[3]
hub_port  = int(sys.argv[4]) if len(sys.argv) > 4 else 11883
mqtt_usr  = sys.argv[5] if len(sys.argv) > 5 else "inkjoy"
mqtt_pwd  = sys.argv[6] if len(sys.argv) > 6 else "inkjoy"

asyncio.run(adopt(wifi_ssid, wifi_pw, hub_ip, hub_port, mqtt_usr, mqtt_pwd))
