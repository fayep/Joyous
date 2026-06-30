#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy"]
# ///
"""Generate 2560×1440 palette comparison PNG for Samsung EM32DX (16:9).

Three horizontal bands — InkJoy, Samsung spec, Reflection Spectra 6 — each with
six labeled swatches (black, white, yellow, red, blue, green).

Outputs:
  palette-bars-2560x1440.png       — exact source palette RGB (bypass hub dither)
  palette-bars-hub-samsung.png    — hub two-palette Stucki (P2 dither → P1 send RGB)

Copy the source PNG directly to data/samsung/{frame-id}.png to avoid re-dither on upload.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

_CAL_DIR = Path(__file__).resolve().parent
if str(_CAL_DIR) not in sys.path:
    sys.path.insert(0, str(_CAL_DIR))

import numpy as np
from PIL import Image, ImageDraw, ImageFont

from palettes import (
    COLOR_NAMES,
    PALETTE_SAMSUNG_DISPLAY,
    PALETTE_SAMSUNG_SEND,
    PALETTES,
)

WIDTH, HEIGHT = 2560, 1440
LABEL_W = 220
HEADER_H = 56


def nearest_color(rgb: np.ndarray, palette: np.ndarray) -> int:
    d = np.sum((palette - rgb) ** 2, axis=1)
    return int(np.argmin(d))


def stucki_dither(img: np.ndarray, palette: np.ndarray) -> np.ndarray:
    """Stucki error diffusion (matches joyous-hub/dither.go weights)."""
    h, w, _ = img.shape
    work = img.astype(np.float64).copy()
    out = np.zeros((h, w), dtype=np.uint8)
    weights = [
        (1, 0, 8 / 42),
        (2, 0, 4 / 42),
        (-2, 1, 2 / 42),
        (-1, 1, 4 / 42),
        (0, 1, 8 / 42),
        (1, 1, 4 / 42),
        (2, 1, 2 / 42),
        (-2, 2, 1 / 42),
        (-1, 2, 2 / 42),
        (0, 2, 4 / 42),
        (1, 2, 2 / 42),
        (2, 2, 1 / 42),
    ]

    for y in range(h):
        for x in range(w):
            old = work[y, x].copy()
            idx = nearest_color(old, palette)
            out[y, x] = idx
            new = palette[idx]
            er, eg, eb = old - new
            for dx, dy, wgt in weights:
                nx, ny = x + dx, y + dy
                if 0 <= nx < w and 0 <= ny < h:
                    work[ny, nx, 0] += er * wgt
                    work[ny, nx, 1] += eg * wgt
                    work[ny, nx, 2] += eb * wgt
    return out


def render_bars(palettes: dict[str, np.ndarray]) -> Image.Image:
    img = Image.new("RGB", (WIDTH, HEIGHT), (32, 32, 32))
    draw = ImageDraw.Draw(img)
    try:
        title_font = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 28)
        label_font = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 20)
        small_font = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 16)
    except OSError:
        title_font = ImageFont.load_default()
        label_font = title_font
        small_font = title_font

    rows = list(palettes.items())
    band_h = HEIGHT // len(rows)
    bar_area_w = WIDTH - LABEL_W

    for row_i, (name, palette) in enumerate(rows):
        y0 = row_i * band_h
        y1 = y0 + band_h
        draw.rectangle([0, y0, WIDTH, y0 + HEADER_H], fill=(24, 24, 24))
        draw.text((12, y0 + 12), name, fill=(240, 240, 240), font=title_font)
        draw.text(
            (12, y0 + HEADER_H + 8),
            "← photograph this row on Samsung to compare to source swatches",
            fill=(160, 160, 160),
            font=small_font,
        )

        swatch_top = y0 + HEADER_H + 36
        swatch_h = y1 - swatch_top - 8
        bar_w = bar_area_w // 6

        for i, cname in enumerate(COLOR_NAMES):
            r, g, b = palette[i]
            x0 = LABEL_W + i * bar_w
            x1 = x0 + bar_w - 2
            draw.rectangle([x0, swatch_top, x1, y1 - 4], fill=(int(r), int(g), int(b)))
            hex_rgb = f"#{int(r):02X}{int(g):02X}{int(b):02X}"
            lum = 0.299 * r + 0.587 * g + 0.114 * b
            fg = (0, 0, 0) if lum > 140 else (255, 255, 255)
            draw.text((x0 + 8, swatch_top + 8), cname, fill=fg, font=label_font)
            draw.text((x0 + 8, swatch_top + 32), hex_rgb, fill=fg, font=small_font)

        # Legend column: mini chips + RGB tuple
        draw.rectangle([0, swatch_top, LABEL_W - 8, y1 - 4], fill=(48, 48, 48))
        draw.text((8, swatch_top + 4), "source", fill=(200, 200, 200), font=small_font)
        chip_h = max(18, (swatch_h - 30) // 6)
        for i, cname in enumerate(COLOR_NAMES):
            r, g, b = palette[i]
            cy = swatch_top + 24 + i * chip_h
            draw.rectangle([8, cy, 28, cy + chip_h - 2], fill=(int(r), int(g), int(b)))
            draw.text(
                (34, cy),
                f"{i+1} {cname}",
                fill=(220, 220, 220),
                font=small_font,
            )

    return img


def hub_samsung_dither(source: Image.Image) -> Image.Image:
    """Stucki in P2 (display) space, render P1 (send) RGB — matches joyous-hub."""
    arr = np.array(source.convert("RGB"), dtype=np.float64)
    indices = stucki_dither(arr, PALETTE_SAMSUNG_DISPLAY)
    pal = PALETTE_SAMSUNG_SEND
    out = np.zeros_like(arr)
    for y in range(arr.shape[0]):
        for x in range(arr.shape[1]):
            out[y, x] = pal[indices[y, x]]
    return Image.fromarray(out.astype(np.uint8), "RGB")


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "-o",
        "--out-dir",
        type=Path,
        default=Path(__file__).resolve().parent,
        help="output directory",
    )
    args = ap.parse_args()
    out_dir = args.out_dir
    out_dir.mkdir(parents=True, exist_ok=True)

    source = render_bars(PALETTES)
    source_path = out_dir / "palette-bars-2560x1440.png"
    source.save(source_path, optimize=True)
    print(f"wrote {source_path}")

    hub = hub_samsung_dither(source)
    hub_path = out_dir / "palette-bars-hub-samsung.png"
    hub.save(hub_path, optimize=True)
    print(f"wrote {hub_path}")
    print()
    print("To push exact swatches (no hub re-dither):")
    print("  cp palette-bars-2560x1440.png ~/…/data/samsung/<frame-id>.png")
    print("  then POST /api/samsung/<frame-id>/push (or Samsung tab → Push to display)")
    print()
    print("Uploading via Samsung tab re-dithers to PaletteSamsung — use hub-samsung file to preview that.")


if __name__ == "__main__":
    main()
