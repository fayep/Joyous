#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy"]
# ///
"""Six full-width P1 primary swatches at 2560×1440 for Samsung P2 calibration.

Outputs:
  color-primaries-2560x1440.png  — flat RGB, no hub dither

Push without re-dither:
  cp color-primaries-2560x1440.png …/data/samsung/<frame-id>.png
  POST /api/samsung/<frame-id>/push

Or upload via Samsung tab (filename color-primaries* is stored flat).

Photograph after push; run:
  uv run calibrate_p2_from_guesses_photo.py photo.png --layout samsung-primaries --frame white
"""

from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

from samsung_palettes import COLOR_NAMES, PALETTE_SAMSUNG_SEND

WIDTH, HEIGHT = 2560, 1440
LABEL_W = 220
HEADER_H = 52

GUESSES: dict[str, list[tuple[str, int, int, int]]] = {
    name: [
        (
            f"#{int(PALETTE_SAMSUNG_SEND[i, 0]):02X}{int(PALETTE_SAMSUNG_SEND[i, 1]):02X}{int(PALETTE_SAMSUNG_SEND[i, 2]):02X}",
            int(PALETTE_SAMSUNG_SEND[i, 0]),
            int(PALETTE_SAMSUNG_SEND[i, 1]),
            int(PALETTE_SAMSUNG_SEND[i, 2]),
        )
    ]
    for i, name in enumerate(COLOR_NAMES)
}

ROW_ORDER = list(COLOR_NAMES)


def load_fonts():
    try:
        title = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 28)
        label = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 22)
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
        (14, 12),
        "Samsung P1 primaries — flat RGB for P2 photo",
        fill=(230, 230, 230),
        font=title_font,
    )

    for row_i, family in enumerate(ROW_ORDER):
        name, r, g, b = GUESSES[family][0]
        y0 = HEADER_H + row_i * band_h
        y1 = HEADER_H + (row_i + 1) * band_h
        draw.rectangle([0, y0, LABEL_W - 4, y1], fill=(36, 36, 36))
        draw.text((12, y0 + 16), family, fill=(240, 240, 240), font=label_font)
        draw.text((12, y0 + 48), name, fill=(200, 200, 200), font=label_font)
        draw.rectangle([LABEL_W, y0 + 4, WIDTH - 4, y1 - 4], fill=(r, g, b))

    return img


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("-o", "--out-dir", type=Path, default=Path(__file__).resolve().parent)
    args = ap.parse_args()
    out_dir = args.out_dir
    out_dir.mkdir(parents=True, exist_ok=True)

    img = render_png()
    png_path = out_dir / "color-primaries-2560x1440.png"
    img.save(png_path, optimize=True)
    print(f"wrote {png_path}")
    print()
    print("Push (no hub re-dither):")
    print("  cp color-primaries-2560x1440.png …/data/samsung/<frame-id>.png")
    print("  POST /api/samsung/<frame-id>/push")
    print("  or Samsung tab → upload → Push to display")

if __name__ == "__main__":
    main()
