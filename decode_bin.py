#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy", "requests"]
# ///
"""
InkJoy .bin → PNG decoder.

Format (1600×1200, bottom-to-top row order):
  2 bytes per pixel: hi=color index, lo=wipe order
  Color indices: 0x01=black 0x02=white 0x03=yellow 0x04=red 0x06=blue 0x07=green
  Wipe order: 0-248 (31 quantized steps), clock wipe from center outward

Usage:
    uv run decode_bin.py input.bin output.png
    uv run decode_bin.py https://…/foo.bin output.png
    uv run decode_bin.py input.bin output.png --transition wipe.png
"""

import argparse
import sys
import numpy as np
from PIL import Image

# Palette order matches HI_BYTES = [0x01, 0x02, 0x03, 0x04, 0x06, 0x07]
# InkJoy: physical pigment colors (reverse-engineered from ISFR-lite.exe)
PALETTE_INKJOY = {
    0x01: ( 30,  30,  30),  # black
    0x02: (149, 162, 165),  # white
    0x03: (166, 165,  17),  # yellow
    0x04: (121,  23,  17),  # red
    0x06: (  0,  76, 136),  # blue
    0x07: ( 46,  91,  65),  # green
}
# Samsung EM32DX: sRGB values from official spec sheet
PALETTE_SAMSUNG = {
    0x01: (  0,   0,   0),  # black
    0x02: (255, 255, 255),  # white
    0x03: (255, 235,   0),  # yellow
    0x04: (154,   0,   0),  # red
    0x06: (  0,  36, 154),  # blue
    0x07: ( 20,  85,  16),  # green
}
FALLBACK = (128, 128, 128)  # unknown index → grey

WIDTH, HEIGHT = 1600, 1200


def _bin_to_hi_lo(data: bytes):
    expected = WIDTH * HEIGHT * 2
    if len(data) != expected:
        print(f"Warning: expected {expected} bytes, got {len(data)}", file=sys.stderr)
    raw = np.frombuffer(data, dtype=np.uint8).reshape(HEIGHT, WIDTH, 2)
    # stored bottom-to-top; flip to top-to-bottom
    return raw[::-1, :, 0], raw[::-1, :, 1]


def decode_transition(data: bytes) -> Image.Image:
    _, lo = _bin_to_hi_lo(data)
    return Image.fromarray(lo, "L")


def decode(data: bytes, palette: dict) -> Image.Image:
    hi, _ = _bin_to_hi_lo(data)
    rgb = np.full((HEIGHT, WIDTH, 3), FALLBACK, dtype=np.uint8)
    for idx, color in palette.items():
        rgb[hi == idx] = color
    return Image.fromarray(rgb, "RGB")


def load(path: str) -> bytes:
    if path.startswith("http://") or path.startswith("https://"):
        import requests
        r = requests.get(path, timeout=30)
        r.raise_for_status()
        return r.content
    with open(path, "rb") as f:
        return f.read()


def main():
    parser = argparse.ArgumentParser(description="Decode InkJoy .bin to PNG")
    parser.add_argument("input", help=".bin file path or URL")
    parser.add_argument("output", help="Output PNG path")
    parser.add_argument("--palette", choices=["inkjoy", "samsung"], default="inkjoy",
                        help="Color palette (default: inkjoy)")
    parser.add_argument("--transition", metavar="PATH",
                        help="Also save lo-byte wipe order as greyscale PNG")
    args = parser.parse_args()

    palette = PALETTE_SAMSUNG if args.palette == "samsung" else PALETTE_INKJOY
    print(f"Loading {args.input}…", file=sys.stderr)
    data = load(args.input)
    print(f"Decoding {len(data)} bytes ({args.palette} palette)…", file=sys.stderr)
    img = decode(data, palette)
    img.save(args.output, format="PNG")
    print(f"Saved {args.output} ({WIDTH}×{HEIGHT})", file=sys.stderr)
    if args.transition:
        t = decode_transition(data)
        t.save(args.transition, format="PNG")
        print(f"Saved {args.transition} (transition map)", file=sys.stderr)


if __name__ == "__main__":
    main()
