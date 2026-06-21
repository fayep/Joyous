"""Connect to an InkJoy frame via BluFi and send mqtt_config."""
import asyncio, json, time, sys
from bleak import BleakClient, BleakScanner

# Espressif BluFi GATT
BLUFI_SVC  = "0000ffff-0000-1000-8000-00805f9b34fb"
BLUFI_P2E  = "0000ff01-0000-1000-8000-00805f9b34fb"  # phone→ESP (write)
BLUFI_E2P  = "0000ff02-0000-1000-8000-00805f9b34fb"  # ESP→phone (notify)

# BluFi frame type byte: type bits[1:0]=1 (data), subtype bits[7:2]
# Custom data subtype = 15 (0x0f)
BLUFI_TYPE_CUSTOM_DATA = (0x0f << 2) | 0x01  # = 0x3d

seq = 0

def make_blufi_frame(payload: bytes) -> bytes:
    global seq
    type_byte     = BLUFI_TYPE_CUSTOM_DATA
    frame_ctrl    = 0x00   # no frag, no checksum, no encrypt
    seq_byte      = seq & 0xff
    seq += 1
    data_len      = len(payload)
    return bytes([type_byte, frame_ctrl, seq_byte, data_len]) + payload

def mqtt_config_payload(host: str, port: int, usr: str, pwd: str) -> bytes:
    msg = {
        "msgid": str(int(time.time() * 1000)),
        "action": "mqtt_config",
        "data": {"host": host, "port": port, "usr": usr, "pwd": pwd},
    }
    return json.dumps(msg, separators=(",", ":")).encode()

def notification_handler(sender, data):
    print(f"  ← notify: {data.hex()}  ({data!r})")

async def main():
    target_prefix = "IJ_"
    host = sys.argv[1] if len(sys.argv) > 1 else "192.168.1.7"
    port = int(sys.argv[2]) if len(sys.argv) > 2 else 11883
    usr  = sys.argv[3] if len(sys.argv) > 3 else "inkjoy"
    pwd  = sys.argv[4] if len(sys.argv) > 4 else "inkjoy"

    print("Scanning for InkJoy frames...")
    devices = await BleakScanner.discover(timeout=8.0)
    frames = [d for d in devices if d.name and d.name.startswith(target_prefix)]
    if not frames:
        print("No IJ_ devices found.")
        return
    for f in frames:
        print(f"  Found: {f.name}  {f.address}")

    target = frames[0]
    print(f"\nConnecting to {target.name} ({target.address})...")

    async with BleakClient(target.address) as client:
        print(f"Connected: {client.is_connected}")

        # List services
        for svc in client.services:
            print(f"  Service: {svc.uuid}")
            for ch in svc.characteristics:
                print(f"    Char: {ch.uuid}  props={ch.properties}")

        # Subscribe to notify
        await client.start_notify(BLUFI_E2P, notification_handler)

        # Send mqtt_config
        payload = mqtt_config_payload(host, port, usr, pwd)
        frame   = make_blufi_frame(payload)
        print(f"\nSending mqtt_config: {payload.decode()}")
        print(f"BluFi frame ({len(frame)} bytes): {frame.hex()}")
        await client.write_gatt_char(BLUFI_P2E, frame, response=True)
        print("Sent. Waiting 3s for response...")
        await asyncio.sleep(3)

asyncio.run(main())
