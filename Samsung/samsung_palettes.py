"""Shared Samsung / InkJoy / Reflection palette constants (matches joyous-hub/dither.go).

Samsung two-palette model (EM32DX):
  P1 (send)    — RGB written to PNG; values the frame quantizer matches.
  P2 (display) — measured on-panel appearance after vxtplayer render.

Hub Stucki dithers in P2 space, then maps ink indices → P1 for the PNG.
Calibrated from color-guesses grid photo IMG_0102 (keystone-corrected sampling).
"""

from __future__ import annotations

import numpy as np

COLOR_NAMES = ["black", "white", "yellow", "red", "blue", "green"]

# P1 — pure sRGB primaries (send values written to PNG).
PALETTE_SAMSUNG_SEND = np.array(
    [
        [0, 0, 0],        # #000000 black
        [255, 255, 255],  # #FFFFFF white
        [255, 255, 0],    # #FFFF00 yellow
        [255, 0, 0],      # #FF0000 red
        [0, 0, 255],      # #0000FF blue
        [0, 255, 0],      # #00FF00 green
    ],
    dtype=np.float64,
)

# P2 — on-panel Stucki targets (IMG_0111 primaries, right sample, white frame).
PALETTE_SAMSUNG_DISPLAY = np.array(
    [
        [35, 35, 45],
        [176, 186, 195],
        [195, 183, 12],
        [121, 6, 0],
        [0, 74, 159],
        [50, 105, 98],
    ],
    dtype=np.float64,
)

# InkJoy P1 — pure primaries (verify on frame; same slots as Samsung).
PALETTE_INKJOY_SEND = np.array(
    [
        [0, 0, 0],
        [255, 255, 255],
        [255, 255, 0],
        [255, 0, 0],
        [0, 0, 255],
        [0, 255, 0],
    ],
    dtype=np.float64,
)

# InkJoy P2 — on-panel Stucki targets (IMG_0110 primaries; green uses legacy physical ink).
PALETTE_INKJOY_DISPLAY = np.array(
    [
        [71, 38, 47],
        [214, 215, 201],
        [222, 205, 0],
        [164, 15, 5],
        [30, 106, 188],
        [46, 91, 65],
    ],
    dtype=np.float64,
)

PALETTES: dict[str, np.ndarray] = {
    "InkJoy": np.array(
        [
            [30, 30, 30],
            [149, 162, 165],
            [166, 165, 17],
            [121, 23, 17],
            [0, 76, 136],
            [46, 91, 65],
        ],
        dtype=np.float64,
    ),
    "Samsung (hub)": PALETTE_SAMSUNG_SEND.copy(),
    "Reflection": np.array(
        [
            [8, 0, 0],
            [239, 255, 255],
            [255, 215, 0],
            [134, 0, 0],
            [0, 28, 138],
            [20, 93, 20],
        ],
        dtype=np.float64,
    ),
}

# Layout of palette-bars-2560x1440.png (see gen_palette_bars.py)
BARS_WIDTH, BARS_HEIGHT = 2560, 1440
LABEL_W = 220
HEADER_H = 56
ROW_NAMES = list(PALETTES.keys())

# Empirical sample points for a trimmed straight-on photo of the chart (1024×549-ish).
# Use --layout source for an uncropped 2560×1440 screenshot.
CROPPED_PHOTO_ROW_YS = [0.19, 0.46, 0.74]
CROPPED_PHOTO_COL_XS = [0.24, 0.38, 0.52, 0.66, 0.78, 0.90]

# Fine-tune individual swatches when a trimmed photo doesn't match the source grid.
CROPPED_PHOTO_OVERRIDES: dict[tuple[str, str], tuple[float, float]] = {
    ("Samsung (hub)", "black"): (0.23, 0.47),
    ("Samsung (hub)", "green"): (0.94, 0.43),
}


def swatch_centers_normalized(
    x_frac: float = 0.58,
    y_frac: float = 0.72,
    *,
    row_ys: list[float] | None = None,
    col_xs: list[float] | None = None,
    overrides: dict[tuple[str, str], tuple[float, float]] | None = None,
) -> list[tuple[str, str, float, float]]:
    """Return (row_name, color_name, x_norm, y_norm) for each swatch sample point."""
    if row_ys is not None and col_xs is not None:
        if len(row_ys) != len(ROW_NAMES) or len(col_xs) != len(COLOR_NAMES):
            raise ValueError("row_ys and col_xs must have 3 and 6 entries")
        out: list[tuple[str, str, float, float]] = []
        for row_name, row_y in zip(ROW_NAMES, row_ys):
            for color_name, col_x in zip(COLOR_NAMES, col_xs):
                cx, cy = col_x, row_y
                key = (row_name, color_name)
                if overrides and key in overrides:
                    cx, cy = overrides[key]
                out.append((row_name, color_name, cx, cy))
        return out

    n_rows = len(ROW_NAMES)
    band_h = BARS_HEIGHT // n_rows
    bar_area_w = BARS_WIDTH - LABEL_W
    bar_w = bar_area_w // 6
    out: list[tuple[str, str, float, float]] = []

    for row_i, row_name in enumerate(ROW_NAMES):
        y0 = row_i * band_h
        y1 = y0 + band_h
        swatch_top = y0 + HEADER_H + 36
        swatch_bottom = y1 - 4

        for i, color_name in enumerate(COLOR_NAMES):
            x0 = LABEL_W + i * bar_w
            x1 = x0 + bar_w - 2
            cx = (x0 + (x1 - x0) * x_frac) / BARS_WIDTH
            cy = (swatch_top + (swatch_bottom - swatch_top) * y_frac) / BARS_HEIGHT
            out.append((row_name, color_name, cx, cy))
    return out


def find_swatch_override(
    img: np.ndarray, row_name: str, color_name: str
) -> tuple[float, float] | None:
    """Scan for best black/green sample in a cropped photo (samsung row)."""
    if row_name != "Samsung (hub)" or color_name not in {"black", "green"}:
        return None
    h, w = img.shape[:2]
    if color_name == "black":
        yrange = np.linspace(0.46, 0.52, 7)
        xrange = np.linspace(0.20, 0.34, 15)
        best = None
        for y in yrange:
            for x in xrange:
                px, py = int(x * w), int(y * h)
                patch = img[py - 10 : py + 10, px - 10 : px + 10].reshape(-1, 3)
                med = np.median(patch, axis=0)
                score = -float(np.mean(med))
                if best is None or score > best[0]:
                    best = (score, x, y)
        return (best[1], best[2]) if best else None

    yrange = np.linspace(0.43, 0.50, 8)
    xrange = np.linspace(0.84, 0.96, 13)
    best = None
    for y in yrange:
        for x in xrange:
            px, py = int(x * w), int(y * h)
            patch = img[py - 10 : py + 10, px - 10 : px + 10].reshape(-1, 3)
            med = np.median(patch, axis=0)
            score = float(med[1] - (med[0] + med[2]) / 2)
            if best is None or score > best[0]:
                best = (score, x, y)
    return (best[1], best[2]) if best else None


def build_sample_centers(
    layout: str,
    row_ys: list[float] | None,
    col_xs: list[float] | None,
    img: np.ndarray | None = None,
    auto_tune: bool = False,
) -> list[tuple[str, str, float, float]]:
    if row_ys is None and layout == "cropped":
        row_ys = CROPPED_PHOTO_ROW_YS
    if col_xs is None and layout == "cropped":
        col_xs = CROPPED_PHOTO_COL_XS
    overrides = dict(CROPPED_PHOTO_OVERRIDES) if layout == "cropped" else {}
    if auto_tune and img is not None:
        for color in ("black", "green"):
            found = find_swatch_override(img, "Samsung (hub)", color)
            if found:
                overrides[("Samsung (hub)", color)] = found
    return swatch_centers_normalized(
        row_ys=row_ys, col_xs=col_xs, overrides=overrides or None
    )
