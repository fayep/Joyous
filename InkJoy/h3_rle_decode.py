"""H3 EPD plane decoder recovered from ij_epd (ESP32-C5) firmware-h3.bin.

RLE function: IROM VA 0x4201c766 (see disasm_h3_rle.s)

Plane size:
  PIC_SIZE = 1200 * 800 = 960000
  (= one CS0/CS1 half of the 1200x1600 panel)

Inner payload (after the device has parsed its 21-byte image header):
  flag at header+0x10 == 1  -> RLE
  otherwise                 -> raw copy of the remaining bytes

RLE stream (starts at payload offset 0x15):
  repeated (count: u8, value: u8) pairs
  emit `value`, `count` times (byte-wise — not 16-bit)
  (0, 0) logs "RLE err: zero-length run" and skips the pair
  running length must stay <= PIC_SIZE or "RLE overflow" is logged

Outer "new-format" path (separate, VA ~0x4201d238):
  4-byte header with bytes [1..3] == {0xB0, 0x06, 0x40}, then
  memcpy of PIC_SIZE raw bytes from offset +4 (not RLE).
"""

from __future__ import annotations

PIC_SIZE = 1200 * 800  # 960000
RLE_PAYLOAD_OFF = 0x15


def decode_rle_plane(blob: bytes, out: bytearray | None = None) -> bytearray:
    """Decode an RLE plane from an image blob (header + stream)."""
    if out is None:
        out = bytearray(PIC_SIZE)
    pcnt = 0
    i = RLE_PAYLOAD_OFF
    while pcnt < PIC_SIZE and i + 1 < len(blob):
        count, value = blob[i], blob[i + 1]
        i += 2
        if count == 0 and value == 0:
            # Firmware logs and continues; treat as hard error for hosts.
            raise ValueError(f"RLE zero-length run at pcnt={pcnt}")
        pcnt += count
        if pcnt > PIC_SIZE:
            raise ValueError(f"RLE overflow: pcnt={pcnt} > PIC_SIZE={PIC_SIZE}")
        start = pcnt - count
        out[start:pcnt] = bytes([value]) * count
    return out


def looks_like_new_format(hdr4: bytes) -> bool:
    return len(hdr4) >= 4 and hdr4[1:4] == bytes([0xB0, 0x06, 0x40])
