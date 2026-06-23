#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy"]
# ///
"""Generate 1600×1200 (4:3) color-guesses grid for InkJoy P1/P2 calibration.

Outputs:
  color-guesses-1600x1200.png  — flat RGB source (labels in margin only)
  color-guesses-1600x1200.bin  — flat per-pixel snap, no Stucki

Hub behaviour:
  • Upload .bin → served as-is (no conversion).
  • Upload PNG named color-guesses*.png → flat_rgb snap (no LAB/Stucki), same as .bin.
  • Upload a photo → normal LAB + Stucki in PaletteInkJoyDisplay space.

InkJoy only has 6 physical inks — many similar RGB guesses map to the same ink.
That bunching is expected; use color-primaries-1600x1200 for P2 photography.
"""

from __future__ import annotations

import argparse
import math
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw, ImageFont

from gen_color_guesses import GUESSES, ROW_ORDER

WIDTH, HEIGHT = 1600, 1200
LABEL_W = 140
HEADER_H = 40

HI_BYTES = [0x01, 0x02, 0x03, 0x04, 0x06, 0x07]


def load_fonts() -> tuple[ImageFont.FreeTypeFont | ImageFont.ImageFont, ...]:
    try:
        title = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 22)
        label = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 13)
        tiny = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 11)
        return title, label, tiny
    except OSError:
        d = ImageFont.load_default()
        return d, d, d


def render_png() -> Image.Image:
    img = Image.new("RGB", (WIDTH, HEIGHT), (28, 28, 28))
    draw = ImageDraw.Draw(img)
    title_font, label_font, tiny_font = load_fonts()
    n_rows = len(ROW_ORDER)
    body_h = HEIGHT - HEADER_H
    band_h = body_h // n_rows

    draw.rectangle([0, 0, WIDTH, HEADER_H], fill=(16, 16, 16))
    draw.text(
        (10, 8),
        "InkJoy color guesses — flat RGB. Photograph; solid swatches win.",
        fill=(230, 230, 230),
        font=title_font,
    )

    for row_i, family in enumerate(ROW_ORDER):
        guesses = GUESSES[family]
        y0 = HEADER_H + row_i * band_h
        y1 = HEADER_H + (row_i + 1) * band_h
        swatch_top = y0 + 4
        bar_area_w = WIDTH - LABEL_W
        bar_w = bar_area_w // len(guesses)

        draw.rectangle([0, y0, LABEL_W - 4, y1], fill=(40, 40, 40))
        draw.text((8, y0 + 8), family, fill=(240, 240, 240), font=title_font)
        draw.text((8, y0 + 30), f"{len(guesses)}", fill=(160, 160, 160), font=tiny_font)

        for i, (name, r, g, b) in enumerate(guesses):
            x0 = LABEL_W + i * bar_w
            x1 = x0 + bar_w - 2
            draw.rectangle([x0, swatch_top, x1, y1 - 2], fill=(r, g, b))
            # Labels in left column only — avoids antialiased text polluting swatches.
            if i == 0:
                hex_rgb = f"#{r:02X}{g:02X}{b:02X}"
                draw.text((8, y0 + 52), hex_rgb, fill=(180, 180, 180), font=tiny_font)

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
            step = min(30, int(order * 31))
            lo[y, x] = step * 8
    return lo


def flat_snap_bin(img: Image.Image, palette: np.ndarray) -> bytes:
    """Per-pixel nearest ink, no error diffusion — preserves flat swatches."""
    arr = np.array(img.convert("RGB"), dtype=np.float64)
    h, w, _ = arr.shape
    hi = np.zeros((h, w), dtype=np.uint8)
    for y in range(h):
        for x in range(w):
            hi[y, x] = HI_BYTES[nearest_color(arr[y, x], palette)]
    lo = make_clock_wipe(w, h)
    # Bin file is bottom-to-top row order.
    out = np.stack([hi[::-1], lo[::-1]], axis=2).reshape(-1)
    return out.tobytes()


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "-o",
        "--out-dir",
        type=Path,
        default=Path(__file__).resolve().parent,
    )
    args = ap.parse_args()
    out_dir = args.out_dir
    out_dir.mkdir(parents=True, exist_ok=True)

    img = render_png()
    png_path = out_dir / "color-guesses-1600x1200.png"
    img.save(png_path, optimize=True)
    print(f"wrote {png_path}")

    # Flat snap uses source RGB directly (each swatch → nearest hi byte, no Stucki).
    from samsung_palettes import PALETTE_INKJOY_SEND

    bin_path = out_dir / "color-guesses-1600x1200.bin"
    bin_path.write_bytes(flat_snap_bin(img, PALETTE_INKJOY_SEND))
    print(f"wrote {bin_path} ({bin_path.stat().st_size} bytes)")
    print()
    print("InkJoy push (no hub re-dither):")
    print("  1. Hub album → upload color-guesses-1600x1200.bin")
    print("  2. Send to InkJoy frame from Devices tab")
    print("  3. Photograph → uv run calibrate_p2_from_guesses_photo.py photo.png --layout inkjoy")


if __name__ == "__main__":
    main()
