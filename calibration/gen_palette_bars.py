#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy"]
# ///
"""Generate palette comparison PNG for Samsung EM32DX (default 2560×1440).

Three horizontal bands — InkJoy, Samsung spec, Reflection Spectra 6 — each with
six labeled swatches (black, white, yellow, red, blue, green).

Outputs:
  palette-bars-{WxH}.png           — exact source palette RGB (bypass hub dither)
  palette-bars-hub-samsung-{WxH}.png — hub two-palette Stucki (P2 dither → P1 send RGB)

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
from PIL import Image, ImageDraw

from palettes import (
    COLOR_NAMES,
    PALETTE_SAMSUNG_DISPLAY,
    PALETTE_SAMSUNG_SEND,
    PALETTES,
)
from layouts.chart import parse_size, scaled_margins, scaled_fonts

DEFAULT_SIZE = "2560x1440"


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


def render_bars(palettes: dict[str, np.ndarray], *, width: int, height: int) -> Image.Image:
    label_w, header_h = scaled_margins(width, height)
    # Palette bars use a wider label column than primaries charts.
    label_w = max(label_w, round(width * 220 / 2560))
    header_h = max(header_h, round(height * 56 / 1440))
    title_font, label_font, tiny_font = scaled_fonts(width)
    small_font = tiny_font

    img = Image.new("RGB", (width, height), (32, 32, 32))
    draw = ImageDraw.Draw(img)
    pad = max(8, width // 320)

    rows = list(palettes.items())
    band_h = height // len(rows)
    bar_area_w = width - label_w

    for row_i, (name, palette) in enumerate(rows):
        y0 = row_i * band_h
        y1 = y0 + band_h
        draw.rectangle([0, y0, width, y0 + header_h], fill=(24, 24, 24))
        draw.text((pad, y0 + pad), name, fill=(240, 240, 240), font=title_font)
        draw.text(
            (pad, y0 + header_h + pad // 2),
            "← photograph this row on Samsung to compare to source swatches",
            fill=(160, 160, 160),
            font=small_font,
        )

        swatch_top = y0 + header_h + round(height * 36 / 1440)
        swatch_h = y1 - swatch_top - pad
        bar_w = bar_area_w // 6

        for i, cname in enumerate(COLOR_NAMES):
            r, g, b = palette[i]
            x0 = label_w + i * bar_w
            x1 = x0 + bar_w - 2
            draw.rectangle([x0, swatch_top, x1, y1 - pad // 2], fill=(int(r), int(g), int(b)))
            hex_rgb = f"#{int(r):02X}{int(g):02X}{int(b):02X}"
            lum = 0.299 * r + 0.587 * g + 0.114 * b
            fg = (0, 0, 0) if lum > 140 else (255, 255, 255)
            draw.text((x0 + pad, swatch_top + pad), cname, fill=fg, font=label_font)
            draw.text((x0 + pad, swatch_top + pad + round(height * 24 / 1440)), hex_rgb, fill=fg, font=small_font)

        draw.rectangle([0, swatch_top, label_w - pad, y1 - pad // 2], fill=(48, 48, 48))
        draw.text((pad, swatch_top + pad // 2), "source", fill=(200, 200, 200), font=small_font)
        chip_h = max(18, (swatch_h - 30) // 6)
        for i, cname in enumerate(COLOR_NAMES):
            r, g, b = palette[i]
            cy = swatch_top + 24 + i * chip_h
            draw.rectangle([pad, cy, pad + 20, cy + chip_h - 2], fill=(int(r), int(g), int(b)))
            draw.text((pad + 26, cy), f"{i+1} {cname}", fill=(220, 220, 220), font=small_font)

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
    ap.add_argument("--size", default=DEFAULT_SIZE, help=f"WIDTHxHEIGHT (default {DEFAULT_SIZE})")
    ap.add_argument(
        "-o",
        "--out-dir",
        type=Path,
        default=Path(__file__).resolve().parent,
        help="output directory",
    )
    args = ap.parse_args()
    width, height = parse_size(args.size)
    out_dir = args.out_dir
    out_dir.mkdir(parents=True, exist_ok=True)
    tag = f"{width}x{height}"

    source = render_bars(PALETTES, width=width, height=height)
    source_path = out_dir / f"palette-bars-{tag}.png"
    source.save(source_path, optimize=True)
    print(f"wrote {source_path}")

    hub = hub_samsung_dither(source)
    hub_path = out_dir / f"palette-bars-hub-samsung-{tag}.png"
    hub.save(hub_path, optimize=True)
    print(f"wrote {hub_path}")
    print()
    print("To push exact swatches (no hub re-dither):")
    print(f"  cp palette-bars-{tag}.png ~/…/data/samsung/<frame-id>.png")
    print("  then POST /api/samsung/<frame-id>/push (or Samsung tab → Push to display)")
    print()
    print("Uploading via Samsung tab re-dithers to PaletteSamsung — use hub-samsung file to preview that.")


if __name__ == "__main__":
    main()
