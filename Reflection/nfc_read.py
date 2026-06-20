#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["pyscard"]
# ///
"""
Dump NFC tag via ACR122U.
Tries NDEF read first, then falls back to raw block/page dumps for
Mifare Classic, Mifare Ultralight, NTAG2xx, and ISO 15693.
"""

from smartcard.System import readers
from smartcard.util import toHexString, toBytes
from smartcard.Exceptions import CardConnectionException
import sys, struct, time


def connect():
    rs = readers()
    if not rs:
        print("No PC/SC readers found.")
        sys.exit(1)
    # prefer ACR122
    r = next((x for x in rs if "ACR122" in str(x)), rs[0])
    print(f"Using reader: {r}")
    conn = r.createConnection()
    conn.connect()
    return conn


def apdu(conn, cmd, label="", retries=3):
    last_exc = None
    for i in range(retries):
        try:
            data, sw1, sw2 = conn.transmit(cmd)
            status = f"{sw1:02X}{sw2:02X}"
            if label:
                print(f"  {label}: [{status}] {toHexString(data) if data else '(no data)'}")
            return data, sw1, sw2
        except Exception as e:
            last_exc = e
            time.sleep(0.1)
    raise last_exc


def get_uid(conn):
    data, sw1, sw2 = apdu(conn, [0xFF, 0xCA, 0x00, 0x00, 0x00], "GET UID")
    if sw1 == 0x90:
        print(f"  UID: {toHexString(data)}")
        time.sleep(0.1)   # let card settle before next command
        return bytes(data)
    return None


def get_atr(conn):
    """Read ATR/ATS to identify card type."""
    try:
        atr = conn.getATR()
        print(f"  ATR: {toHexString(atr)}")
        return bytes(atr)
    except Exception:
        return None


def read_ndef_ultralight(conn):
    """Read all pages of an NTAG2xx / Mifare Ultralight (4 bytes/page)."""
    print("\n--- Ultralight/NTAG page dump ---")
    all_pages = []
    for page in range(0, 64):
        data, sw1, sw2 = conn.transmit([0xFF, 0xB0, 0x00, page, 0x04])
        if sw1 != 0x90:
            print(f"  page {page:02d}: read failed [{sw1:02X}{sw2:02X}] (end of tag)")
            break
        all_pages.append((page, bytes(data)))
        print(f"  page {page:02d}: {toHexString(data)}")
    return all_pages


def read_ndef_classic(conn):
    """Mifare Classic: authenticate sector 0 with default keys then read blocks."""
    print("\n--- Mifare Classic block dump (sector 0, default keys) ---")
    DEFAULT_KEYS = [
        [0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF],  # factory default A
        [0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5],  # MAD key A
        [0xD3, 0xF7, 0xD3, 0xF7, 0xD3, 0xF7],  # NDEF key A
    ]
    # Load key into reader
    for key in DEFAULT_KEYS:
        load = [0xFF, 0x82, 0x00, 0x00, 0x06] + key
        conn.transmit(load)
        # Auth block 0 with key A
        auth = [0xFF, 0x86, 0x00, 0x00, 0x05, 0x01, 0x00, 0x00, 0x60, 0x00]
        d, sw1, sw2 = conn.transmit(auth)
        if sw1 == 0x90:
            print(f"  Authenticated sector 0 with key {toHexString(key)}")
            break
    else:
        print("  Auth failed with all default keys")
        return []

    blocks = []
    for block in range(0, 4):
        data, sw1, sw2 = conn.transmit([0xFF, 0xB0, 0x00, block, 0x10])
        if sw1 == 0x90:
            blocks.append((block, bytes(data)))
            print(f"  block {block:02d}: {toHexString(data)}")
    return blocks


def parse_ndef(data: bytes):
    """Very basic NDEF TLV parser for Type 2 tags (Ultralight/NTAG)."""
    # Skip capability container at page 3 (CC: E1 10 xx xx)
    # NDEF data starts at page 4 = byte offset 16
    payload = data[16:] if len(data) > 16 else data
    i = 0
    while i < len(payload):
        t = payload[i]; i += 1
        if t == 0x00:  # NULL TLV
            continue
        if t == 0xFE:  # Terminator
            break
        if i >= len(payload):
            break
        l = payload[i]; i += 1
        if l == 0xFF:  # 3-byte length
            l = struct.unpack(">H", payload[i:i+2])[0]; i += 2
        v = payload[i:i+l]; i += l
        if t == 0x03:  # NDEF message
            print(f"\n  NDEF TLV found ({l} bytes): {v.hex()}")
            parse_ndef_message(v)
        else:
            print(f"  TLV type=0x{t:02x} len={l}: {v.hex()}")


def parse_ndef_message(msg: bytes):
    i = 0
    while i < len(msg):
        if i >= len(msg): break
        header = msg[i]; i += 1
        # MB = bit7, ME = bit6, CF = bit5, SR = bit4, IL = bit3, TNF = bits2:0
        mb  = (header >> 7) & 1
        me  = (header >> 6) & 1
        sr  = (header >> 4) & 1
        il  = (header >> 3) & 1
        tnf = header & 0x07
        if i >= len(msg): break
        type_len = msg[i]; i += 1
        if sr:
            if i >= len(msg): break
            payload_len = msg[i]; i += 1
        else:
            if i+4 > len(msg): break
            payload_len = struct.unpack(">I", msg[i:i+4])[0]; i += 4
        id_len = 0
        if il:
            if i >= len(msg): break
            id_len = msg[i]; i += 1
        rec_type = msg[i:i+type_len]; i += type_len
        rec_id   = msg[i:i+id_len];   i += id_len
        payload  = msg[i:i+payload_len]; i += payload_len

        tnf_names = {0:"Empty",1:"Well-Known",2:"MIME",3:"Absolute URI",
                     4:"External",5:"Unknown",6:"Unchanged",7:"Reserved"}
        print(f"  NDEF Record: TNF={tnf_names.get(tnf,tnf)} type={rec_type!r}")
        if tnf == 1 and rec_type == b'T':  # Text record
            enc = 'utf-16' if (payload[0] & 0x80) else 'utf-8'
            lang_len = payload[0] & 0x3F
            text = payload[1+lang_len:].decode(enc, errors='replace')
            print(f"    Text: {text!r}")
        elif tnf == 1 and rec_type == b'U':  # URI record
            prefixes = {0x00:'',0x01:'http://www.',0x02:'https://www.',
                        0x03:'http://',0x04:'https://',0x05:'tel:',
                        0x06:'mailto:',0x23:'urn:nfc:'}
            uri = prefixes.get(payload[0], '') + payload[1:].decode('utf-8', errors='replace')
            print(f"    URI: {uri!r}")
        elif tnf == 2:  # MIME
            mime = rec_type.decode('ascii', errors='replace')
            print(f"    MIME: {mime}")
            print(f"    Data: {payload.hex()}")
            try: print(f"    Text: {payload.decode('utf-8', errors='replace')!r}")
            except: pass
        elif tnf == 4:  # External type
            print(f"    External: {payload.hex()}")
            try: print(f"    Text: {payload.decode('utf-8', errors='replace')!r}")
            except: pass
        else:
            print(f"    Payload ({payload_len}B): {payload.hex()}")
            try: print(f"    As UTF-8: {payload.decode('utf-8', errors='replace')!r}")
            except: pass
        if me:
            break


NDEF_AID = [0xD2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01]

def read_type4(conn):
    """ISO 7816-4 NDEF read for Type 4 tags.
    Fast path: skip CC re-read, use known file ID 0x0001 from prior CC dump.
    """
    print("\n--- Type 4 NDEF read ---")
    # SELECT NDEF Application
    data, sw1, sw2 = apdu(conn,
        [0x00, 0xA4, 0x04, 0x00, len(NDEF_AID)] + NDEF_AID,
        "SELECT NDEF App")
    if sw1 not in (0x90, 0x61):
        print("  Not a Type 4 tag or app not found")
        return False

    # Fast path: known file ID 0x0001, max NDEF 512 bytes (from prior CC read)
    ndef_file_id = [0x00, 0x01]
    max_size = 512

    # SELECT NDEF file directly
    apdu(conn, [0x00, 0xA4, 0x00, 0x0C, 0x02] + ndef_file_id, "SELECT NDEF file")

    # READ NDEF length
    d2, sw1, sw2 = conn.transmit([0x00, 0xB0, 0x00, 0x00, 0x02])
    if sw1 != 0x90:
        print(f"  Length read failed [{sw1:02X}{sw2:02X}]")
        return False
    nlen = (d2[0] << 8) | d2[1]
    print(f"  NDEF length: {nlen} bytes")

    # READ NDEF message in chunks
    msg = b''
    offset = 2
    while len(msg) < nlen:
        chunk = min(0xF0, nlen - len(msg))
        d3, sw1, sw2 = conn.transmit([0x00, 0xB0, offset >> 8, offset & 0xFF, chunk])
        if sw1 != 0x90:
            print(f"  Read error at offset {offset}: [{sw1:02X}{sw2:02X}]")
            break
        msg += bytes(d3)
        offset += len(d3)

    print(f"  Raw NDEF ({len(msg)}B): {msg.hex()}")
    print("\n=== NDEF Parse ===")
    parse_ndef_message(msg)
    return True


def main():
    import time
    # Try connecting immediately (card already present), then poll
    for attempt in range(30):
        try:
            conn = connect()
            break
        except Exception:
            if attempt == 0:
                print("Place NFC tag on reader (waiting up to 15s)...")
            time.sleep(0.5)
    else:
        print("No card detected within 15s.")
        return

    print("\n=== Card Info ===")
    atr = get_atr(conn)
    # Skip UID read — dynamic tag disappears fast; go straight to NDEF

    # ATR 3B 80 80 01 01 = ISO 14443-4 T=1 card → try Type 4 NDEF first
    if not read_type4(conn):
        # Fallback: try Ultralight/NTAG
        try:
            pages = read_ndef_ultralight(conn)
            if pages:
                raw = b''.join(p for _, p in pages)
                print("\n=== NDEF Parse ===")
                parse_ndef(raw)
        except Exception as e:
            print(f"Ultralight read error: {e}")
            try:
                read_ndef_classic(conn)
            except Exception as e2:
                print(f"Classic read error: {e2}")

    conn.disconnect()


if __name__ == "__main__":
    main()
