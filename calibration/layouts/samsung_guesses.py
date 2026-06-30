#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow"]
# ///
"""Generate 2560×1440 flat RGB guess grid for Samsung dither probing.

Each row is one ink family (black → green). Swatches are solid sRGB — no hub dither.
Photograph on the frame; the ones that look solid (not stippled) are candidates for
PaletteSamsung.

Push without re-dither:
  cp color-guesses-2560x1440.png …/data/samsung/<frame-id>.png
  POST /api/samsung/<frame-id>/push
"""

from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

WIDTH, HEIGHT = 2560, 1440
LABEL_W = 200
HEADER_H = 44

# (short label, R, G, B) — flat fills only
GUESSES: dict[str, list[tuple[str, int, int, int]]] = {
    "black": [
        ("hub #000", 0, 0, 0),
        ("refl #800", 8, 0, 0),
        ("ink #1E1", 30, 30, 30),
        ("#050505", 5, 5, 5),
        ("#0A0A0A", 10, 10, 10),
        ("#101010", 16, 16, 16),
        ("#1A1A1A", 26, 26, 26),
        ("#120800", 18, 8, 0),
        ("#080808", 8, 8, 8),
        ("#020000", 2, 0, 0),
    ],
    "white": [
        ("hub #FFF", 255, 255, 255),
        ("refl #EFF", 239, 255, 255),
        ("ink #95A", 149, 162, 165),
        ("#F0F0F0", 240, 240, 240),
        ("#E8E8E8", 232, 232, 232),
        ("#FAFAFA", 250, 250, 250),
        ("#EFFFFF", 239, 255, 255),
    ],
    "yellow": [
        ("hub #FEB", 255, 235, 0),
        ("#FFE800", 255, 232, 0),
        ("#FFFF00", 255, 255, 0),
        ("refl #FD7", 255, 215, 0),
        ("#FFF200", 255, 242, 0),
        ("#FFEA00", 255, 234, 0),
        ("ink #A65", 166, 165, 17),
        ("#FFD700", 255, 215, 0),
        ("#E6D000", 230, 208, 0),
        ("#F5E100", 245, 225, 0),
        ("#D4C000", 212, 192, 0),
        ("#FFF44F", 255, 244, 79),
        ("#FFE066", 255, 224, 102),
        ("#C9B800", 201, 184, 0),
    ],
    "red": [
        ("#FF0000", 255, 0, 0),
        ("hub #9A0", 154, 0, 0),
        ("refl #860", 134, 0, 0),
        ("#FF2222", 255, 34, 34),
        ("#EE0000", 238, 0, 0),
        ("#CC0000", 204, 0, 0),
        ("#FF4444", 255, 68, 68),
    ],
    "blue": [
        ("hub #024", 0, 36, 154),
        ("refl #01C", 0, 28, 138),
        ("#0000FF", 0, 0, 255),
        ("#0044FF", 0, 68, 255),
        ("#0066FF", 0, 102, 255),
        ("#0088FF", 0, 136, 255),
        ("#3366FF", 51, 102, 255),
        ("#2266EE", 34, 102, 238),
        ("#1E90FF", 30, 144, 255),
        ("#0055FF", 0, 85, 255),
        ("#0099FF", 0, 153, 255),
        ("#0033DD", 0, 51, 221),
    ],
    "green": [
        ("hub #145", 20, 85, 16),
        ("refl #145", 20, 93, 20),
        ("#00FF00", 0, 255, 0),
        ("#00EE00", 0, 238, 0),
        ("#33FF33", 51, 255, 51),
        ("#00FF44", 0, 255, 68),
        ("#7CFC00", 124, 252, 0),
        ("#ADFF2F", 173, 255, 47),
        ("#32CD32", 50, 205, 50),
        ("#55FF55", 85, 255, 85),
        ("#6CFF00", 108, 255, 0),
        ("#00DD00", 0, 221, 0),
    ],
}

ROW_ORDER = ["black", "white", "yellow", "red", "blue", "green"]


def load_fonts() -> tuple[ImageFont.FreeTypeFont | ImageFont.ImageFont, ...]:
    try:
        title = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 26)
        label = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 15)
        tiny = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 13)
        return title, label, tiny
    except OSError:
        d = ImageFont.load_default()
        return d, d, d


def render(out_path: Path) -> None:
    img = Image.new("RGB", (WIDTH, HEIGHT), (28, 28, 28))
    draw = ImageDraw.Draw(img)
    title_font, label_font, tiny_font = load_fonts()

    n_rows = len(ROW_ORDER)
    body_h = HEIGHT - HEADER_H
    band_h = body_h // n_rows

    draw.rectangle([0, 0, WIDTH, HEADER_H], fill=(16, 16, 16))
    draw.text(
        (12, 8),
        "Color guesses — flat RGB, no dither. Photograph; solid swatches win.",
        fill=(230, 230, 230),
        font=title_font,
    )

    for row_i, family in enumerate(ROW_ORDER):
        guesses = GUESSES[family]
        y0 = HEADER_H + row_i * band_h
        y1 = HEADER_H + (row_i + 1) * band_h

        swatch_top = y0 + 6
        swatch_h = y1 - swatch_top - 4
        bar_area_w = WIDTH - LABEL_W
        bar_w = bar_area_w // len(guesses)

        draw.rectangle([0, y0, LABEL_W - 4, y1], fill=(40, 40, 40))
        draw.text((10, y0 + 10), family, fill=(240, 240, 240), font=title_font)
        draw.text(
            (10, y0 + 40),
            f"{len(guesses)} guesses",
            fill=(160, 160, 160),
            font=tiny_font,
        )

        for i, (name, r, g, b) in enumerate(guesses):
            x0 = LABEL_W + i * bar_w
            x1 = x0 + bar_w - 2
            draw.rectangle([x0, swatch_top, x1, y1 - 2], fill=(r, g, b))
            hex_rgb = f"#{r:02X}{g:02X}{b:02X}"
            lum = 0.299 * r + 0.587 * g + 0.114 * b
            fg = (0, 0, 0) if lum > 130 else (255, 255, 255)
            draw.text((x0 + 6, swatch_top + 6), name, fill=fg, font=label_font)
            draw.text((x0 + 6, swatch_top + 24), hex_rgb, fill=fg, font=tiny_font)

    img.save(out_path, optimize=True)
    print(f"wrote {out_path}")
    print()
    print("Push (no hub re-dither):")
    print("  cp color-guesses-2560x1440.png …/data/samsung/<frame-id>.png")
    print("  POST /api/samsung/<frame-id>/push")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "-o",
        "--out",
        type=Path,
        default=Path(__file__).resolve().parent / "color-guesses-2560x1440.png",
    )
    args = ap.parse_args()
    render(args.out)


if __name__ == "__main__":
    main()
