#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["python-barcode", "Pillow"]
# ///
"""
Generate the InkJoy frame provisioning barcode screen.

The InkJoy app's ScanQRCodeActivity scans a Code 128 1D barcode that encodes
the device clientId (MAC without colons, e.g. AABBCCDDEEFF).  The frame
displays this during first-boot provisioning so the user can scan it to bind
the device to their account before WiFi credentials are sent via BluFi.

Outputs a 1600×1200 PNG (and optionally a .bin via encode_bin.py) ready to
push to the display.

Usage:
    uv run make_device_barcode.py AA:BB:CC:DD:EE:FF
    uv run make_device_barcode.py AABBCCDDEEFF --out provisioning.png
    uv run make_device_barcode.py AABBCCDDEEFF --encode  # also produce .bin
"""

import argparse
import io
import subprocess
import sys
from pathlib import Path

import barcode
from barcode.writer import ImageWriter
from PIL import Image, ImageDraw, ImageFont

FRAME_W, FRAME_H = 1600, 1200
BARCODE_W_FRAC = 0.55   # barcode occupies 55 % of frame width
BARCODE_H_PX   = 260    # barcode stripe height in pixels
LABEL_MARGIN   = 30     # gap between barcode and MAC text label


def normalise_mac(mac: str) -> tuple[str, str]:
    raw = mac.upper().replace(":", "").replace("-", "")
    if len(raw) != 12 or not all(c in "0123456789ABCDEF" for c in raw):
        raise ValueError(f"Invalid MAC: {mac!r}")
    colons = ":".join(raw[i:i+2] for i in range(0, 12, 2))
    return colons, raw


def make_barcode_image(client_id: str, target_w: int, target_h: int) -> Image.Image:
    """Render a Code 128 barcode for client_id into a PIL image of target_w×target_h."""
    CODE128 = barcode.get_barcode_class("code128")
    buf = io.BytesIO()
    writer = ImageWriter()
    CODE128(client_id, writer=writer).write(buf, options={
        "module_width":  0.6,
        "module_height": 20.0,   # mm — will be resized anyway
        "font_size":     0,      # suppress built-in text label
        "text_distance": 0,
        "quiet_zone":    2.0,
        "dpi":           300,
        "write_text":    False,
    })
    buf.seek(0)
    bar_img = Image.open(buf).convert("L")   # greyscale
    # Resize to desired pixel dimensions, preserving aspect ratio via width
    aspect = bar_img.height / bar_img.width
    bar_w = int(FRAME_W * BARCODE_W_FRAC)
    bar_h = max(BARCODE_H_PX, int(bar_w * aspect))
    bar_img = bar_img.resize((bar_w, bar_h), Image.LANCZOS)
    # Threshold to pure black/white
    bar_img = bar_img.point(lambda p: 0 if p < 128 else 255, "L")
    return bar_img


def make_frame_image(mac_colons: str, client_id: str) -> Image.Image:
    canvas = Image.new("L", (FRAME_W, FRAME_H), 255)   # white background
    draw = ImageDraw.Draw(canvas)

    bar_img = make_barcode_image(client_id, FRAME_W, FRAME_H)
    bar_x = (FRAME_W - bar_img.width) // 2
    bar_y = (FRAME_H - bar_img.height) // 2 - 60   # shift up a little for label
    canvas.paste(bar_img, (bar_x, bar_y))

    # MAC address label below barcode
    font_size = 52
    try:
        font = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", font_size)
    except OSError:
        font = ImageFont.load_default()

    label = mac_colons
    bbox = draw.textbbox((0, 0), label, font=font)
    text_w = bbox[2] - bbox[0]
    text_x = (FRAME_W - text_w) // 2
    text_y = bar_y + bar_img.height + LABEL_MARGIN
    draw.text((text_x, text_y), label, fill=0, font=font)

    # Small header
    header = "Scan to add this frame"
    try:
        hfont = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 36)
    except OSError:
        hfont = ImageFont.load_default()
    hbbox = draw.textbbox((0, 0), header, font=hfont)
    draw.text(((FRAME_W - (hbbox[2]-hbbox[0])) // 2, bar_y - 70), header, fill=0, font=hfont)

    return canvas.convert("RGB")


def main():
    parser = argparse.ArgumentParser(description="Generate InkJoy provisioning barcode image")
    parser.add_argument("mac", help="Frame MAC address (any format)")
    parser.add_argument("--out", default=None, help="Output PNG path (default: <clientId>_barcode.png)")
    parser.add_argument("--encode", action="store_true", help="Also run encode_bin.py to produce a .bin")
    args = parser.parse_args()

    try:
        mac_colons, client_id = normalise_mac(args.mac)
    except ValueError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)

    out_png = Path(args.out or f"{client_id}_barcode.png")
    img = make_frame_image(mac_colons, client_id)
    img.save(out_png)
    print(f"Saved {out_png}  ({FRAME_W}×{FRAME_H})")

    if args.encode:
        out_bin = out_png.with_suffix(".bin")
        encode = Path(__file__).parent / "encode_bin.py"
        result = subprocess.run(
            ["uv", "run", str(encode), str(out_png), str(out_bin)],
            capture_output=True, text=True
        )
        if result.returncode == 0:
            print(f"Encoded  {out_bin}")
        else:
            print(f"encode_bin.py failed:\n{result.stderr}", file=sys.stderr)


if __name__ == "__main__":
    main()
