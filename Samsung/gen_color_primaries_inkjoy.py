#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy"]
# ///
"""Six full-width P1 primary swatches at 1600×1200 for InkJoy P2 calibration.

Outputs:
  color-primaries-1600x1200.png
  color-primaries-1600x1200.bin  — flat snap, no Stucki

Photograph after send; run:
  uv run calibrate_p2_from_guesses_photo.py photo.png --layout inkjoy-primaries --frame wood
"""

from __future__ import annotations

import argparse
import math
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFont

from samsung_palettes import COLOR_NAMES, PALETTE_INKJOY_SEND

WIDTH, HEIGHT = 1600, 1200
LABEL_W = 160
HEADER_H = 44

# One P1 primary per row (matches samsung_palettes / PaletteInkJoySend).
GUESSES: dict[str, list[tuple[str, int, int, int]]] = {
    name: [
        (
            f"#{int(PALETTE_INKJOY_SEND[i, 0]):02X}{int(PALETTE_INKJOY_SEND[i, 1]):02X}{int(PALETTE_INKJOY_SEND[i, 2]):02X}",
            int(PALETTE_INKJOY_SEND[i, 0]),
            int(PALETTE_INKJOY_SEND[i, 1]),
            int(PALETTE_INKJOY_SEND[i, 2]),
        )
    ]
    for i, name in enumerate(COLOR_NAMES)
}

ROW_ORDER = list(COLOR_NAMES)

HI_BYTES = [0x01, 0x02, 0x03, 0x04, 0x06, 0x07]


def load_fonts():
    try:
        title = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 22)
        label = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 18)
        return title, label
    except OSError:
        d = ImageFont.load_default()
        return d, d


def render_png() -> Image.Image:
    img = Image.new("RGB", (WIDTH, HEIGHT), (24, 24, 24))
    draw = ImageDraw.Draw(img)
    title_font, label_font = load_fonts()
    n_rows = len(ROW_ORDER)
    body_h = HEIGHT - HEADER_H
    band_h = body_h // n_rows

    draw.rectangle([0, 0, WIDTH, HEADER_H], fill=(12, 12, 12))
    draw.text(
        (10, 10),
        "InkJoy P1 primaries — flat RGB for P2 photo",
        fill=(230, 230, 230),
        font=title_font,
    )

    for row_i, family in enumerate(ROW_ORDER):
        name, r, g, b = GUESSES[family][0]
        y0 = HEADER_H + row_i * band_h
        y1 = HEADER_H + (row_i + 1) * band_h
        draw.rectangle([0, y0, LABEL_W - 2, y1], fill=(36, 36, 36))
        draw.text((8, y0 + 12), family, fill=(240, 240, 240), font=label_font)
        draw.text((8, y0 + 36), name, fill=(200, 200, 200), font=label_font)
        draw.rectangle([LABEL_W, y0 + 2, WIDTH - 2, y1 - 2], fill=(r, g, b))

    return img


def nearest_color(rgb: np.ndarray, palette: np.ndarray) -> int:
    return int(np.argmin(np.sum((palette - rgb) ** 2, axis=1)))


def make_clock_wipe(cols: int, rows: int) -> np.ndarray:
    cy, cx = (rows - 1) / 2.0, (cols - 1) / 2.0
    lo = np.zeros((rows, cols), dtype=np.uint8)
    max_d = math.sqrt(cy**2 + cx**2)
    for y in range(rows):
        dy = y - cy
        for x in range(cols):
            dx = x - cx
            angle = (math.atan2(dx, -dy) + 2 * math.pi) % (2 * math.pi)
            dist = math.sqrt(dy * dy + dx * dx) / max_d
            order = (angle / (2 * math.pi) + dist * 0.01) % 1.0
            lo[y, x] = min(30, int(order * 31)) * 8
    return lo


def flat_snap_bin(img: Image.Image, palette: np.ndarray) -> bytes:
    arr = np.array(img.convert("RGB"), dtype=np.float64)
    h, w, _ = arr.shape
    hi = np.zeros((h, w), dtype=np.uint8)
    for y in range(h):
        for x in range(w):
            hi[y, x] = HI_BYTES[nearest_color(arr[y, x], palette)]
    lo = make_clock_wipe(w, h)
    return np.stack([hi[::-1], lo[::-1]], axis=2).reshape(-1).tobytes()


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("-o", "--out-dir", type=Path, default=Path(__file__).resolve().parent)
    args = ap.parse_args()
    out_dir = args.out_dir
    out_dir.mkdir(parents=True, exist_ok=True)

    img = render_png()
    png_path = out_dir / "color-primaries-1600x1200.png"
    img.save(png_path, optimize=True)
    print(f"wrote {png_path}")

    bin_path = out_dir / "color-primaries-1600x1200.bin"
    bin_path.write_bytes(flat_snap_bin(img, PALETTE_INKJOY_SEND))
    print(f"wrote {bin_path}")
    print()
    print("Upload PNG or .bin to hub (filename triggers flat_rgb snap for PNG).")


if __name__ == "__main__":
    main()
