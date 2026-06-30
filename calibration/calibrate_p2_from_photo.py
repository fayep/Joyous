#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy", "pillow-heif"]
# ///
"""Measure Palette*Display (P2) from a primaries-chart photograph.

InkJoy and Samsung: samples on-panel appearance at each P1 primary swatch,
keystone-corrects inside the wood/white bezel, samples all six inks, then
applies tonal normalization (shared ambient cast removed via black/white).

Example:
    uv run /path/to/PhotoFrames/calibration/calibrate_p2_from_photo.py photo.heic \\
      --layout inkjoy-primaries --frame wood --orientation portrait \\
      --debug-warp p2-warp.png -o inkjoy-p2.json
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

# uv run /any/path/calibrate_p2_from_photo.py must resolve local imports.
_CAL_DIR = Path(__file__).resolve().parent
if str(_CAL_DIR) not in sys.path:
    sys.path.insert(0, str(_CAL_DIR))

import numpy as np
from PIL import Image, ImageDraw

try:
    import pillow_heif

    pillow_heif.register_heif_opener()
except ImportError:
    pass

from palettes import COLOR_NAMES, PALETTE_SAMSUNG_SEND
from primaries_geometry import PrimariesGeometry, analyze_primaries_portrait, sample_column_coherent

# Confirmed P1 primaries (Samsung EM32DX).
P1_PRIMARY: dict[str, tuple[int, int, int]] = {
    "black": (0, 0, 0),
    "white": (255, 255, 255),
    "yellow": (255, 255, 0),
    "red": (255, 0, 0),
    "blue": (0, 0, 255),
    "green": (0, 255, 0),
}


def load_layout_module(layout: str):
    from layouts import inkjoy_guesses, inkjoy_primaries, samsung_guesses, samsung_primaries

    if layout == "inkjoy":
        return inkjoy_guesses
    if layout == "inkjoy-primaries":
        return inkjoy_primaries
    if layout == "samsung-primaries":
        return samsung_primaries
    return samsung_guesses


def _luminance(img: np.ndarray) -> np.ndarray:
    return 0.299 * img[:, :, 0] + 0.587 * img[:, :, 1] + 0.114 * img[:, :, 2]


def _srgb_to_linear(c: np.ndarray) -> np.ndarray:
    c = np.clip(c, 0.0, 1.0)
    return np.where(c <= 0.04045, c / 12.92, ((c + 0.055) / 1.055) ** 2.4)


def _linear_to_srgb(c: np.ndarray) -> np.ndarray:
    c = np.clip(c, 0.0, 1.0)
    return np.where(c <= 0.0031308, c * 12.92, 1.055 * (c ** (1.0 / 2.4)) - 0.055)


def rgb_to_lab(rgb01: np.ndarray) -> np.ndarray:
    """sRGB 0..1 → CIE L*a*b*."""
    lin = _srgb_to_linear(rgb01)
    x = 0.4124564 * lin[0] + 0.3575761 * lin[1] + 0.1804375 * lin[2]
    y = 0.2126729 * lin[0] + 0.7151522 * lin[1] + 0.0721750 * lin[2]
    z = 0.0193339 * lin[0] + 0.1191920 * lin[1] + 0.9503041 * lin[2]

    def f(t: np.ndarray) -> np.ndarray:
        d = 6.0 / 29.0
        return np.where(t > d**3, np.cbrt(t), t / (3 * d * d) + 4.0 / 29.0)

    fx, fy, fz = f(x / 0.95047), f(y / 1.0), f(z / 1.08883)
    return np.array([116.0 * fy - 16.0, 500.0 * (fx - fy), 200.0 * (fy - fz)])


def lab_to_rgb(lab: np.ndarray) -> np.ndarray:
    """CIE L*a*b* → sRGB 0..1."""
    fy = (lab[0] + 16.0) / 116.0
    fx = lab[1] / 500.0 + fy
    fz = fy - lab[2] / 200.0

    def finv(t: np.ndarray) -> np.ndarray:
        d = 6.0 / 29.0
        return np.where(t > d, t**3, 3 * d * d * (t - 4.0 / 29.0))

    x, y, z = 0.95047 * finv(fx), finv(fy), 1.08883 * finv(fz)
    lin = np.array(
        [
            3.2404542 * x - 1.5371385 * y - 0.4985314 * z,
            -0.9692660 * x + 1.8760108 * y + 0.0415560 * z,
            0.0556434 * x - 0.2040259 * y + 1.0572252 * z,
        ]
    )
    return _linear_to_srgb(lin)


def normalize_p2_tonal(rgb_samples: list[np.ndarray]) -> list[np.ndarray]:
    """Remove shared ambient color cast using black/white from the same photo.

    All six inks must be sampled under the same lighting; this keeps their
    tonal relationships (e.g. how white relates to green) intact while
    neutralizing the photograph's grey-axis bias.
    """
    labs = [rgb_to_lab(s / 255.0) for s in rgb_samples]
    black_lab, white_lab = labs[0], labs[1]
    da = 0.5 * (black_lab[1] + white_lab[1])
    db = 0.5 * (black_lab[2] + white_lab[2])
    out: list[np.ndarray] = []
    for lab in labs:
        corrected = np.array([lab[0], lab[1] - da, lab[2] - db])
        rgb01 = lab_to_rgb(corrected)
        out.append(np.clip(np.round(rgb01 * 255.0), 0, 255))
    return out


DisplayQuad = tuple[
    tuple[float, float],
    tuple[float, float],
    tuple[float, float],
    tuple[float, float],
]


def bounds_from_quad(quad: DisplayQuad) -> tuple[int, int, int, int]:
    xs = [p[0] for p in quad]
    ys = [p[1] for p in quad]
    return int(min(xs)), int(min(ys)), int(max(xs)), int(max(ys))


def warp_quad_to_rect(img: np.ndarray, quad: DisplayQuad, out_w: int, out_h: int) -> np.ndarray:
    """Perspective-rectify the keystoned panel inside the wood frame."""
    tl, tr, bl, br = quad
    # Pillow QUAD: UL, UR, LR, LL in source → full destination rectangle.
    data = (tl[0], tl[1], tr[0], tr[1], br[0], br[1], bl[0], bl[1])
    warped = Image.fromarray(img).transform(
        (out_w, out_h),
        Image.Transform.QUAD,
        data,
        Image.Resampling.BILINEAR,
    )
    return np.array(warped)


def sample_point_layout(nx: float, ny: float, warped: np.ndarray) -> tuple[float, float]:
    h, w = warped.shape[:2]
    return nx * (w - 1), ny * (h - 1)


def layout_column_frac(ny: float, header_frac: float) -> float:
    """Map layout row center (ny) to 0..1 column index in portrait panel."""
    return float(np.clip((ny - header_frac) / max(1e-6, 1.0 - header_frac), 0.0, 1.0))


def sample_point_layout_oriented(
    nx: float,
    ny: float,
    warped: np.ndarray,
    *,
    portrait: bool,
    label_frac: float,
    header_frac: float = 0.0,
    content_bounds: tuple[int, int, int, int] | None = None,
    column_centers: list[float] | None = None,
    color_index: int = 0,
) -> tuple[float, float]:
    """Map layout-normalized coords to warped pixels."""
    h, w = warped.shape[:2]
    if content_bounds is None:
        content_bounds = (0, 0, w - 1, h - 1)
    cl, ct, cr, cb = content_bounds
    cw, ch = cr - cl, cb - ct
    bar_nx = float(np.clip((nx - label_frac) / max(1e-6, 1.0 - label_frac), 0.0, 1.0))
    row = 1.0 - bar_nx
    if not portrait:
        return cl + nx * cw, ct + ny * ch
    if column_centers is not None and 0 <= color_index < len(column_centers):
        return column_centers[color_index], ct + row * ch
    col = layout_column_frac(ny, header_frac)
    return cl + col * cw, ct + row * ch


def detect_portrait_column_centers(
    warped: np.ndarray,
    bounds: tuple[int, int, int, int],
    *,
    n_cols: int = 6,
    row_frac: float = 0.55,
    primaries: bool = True,
) -> tuple[list[float], list[int]]:
    """Detect six column centers; primaries charts use per-ink classification."""
    cl, ct, cr, cb = bounds
    if primaries:
        band = warped[ct:cb, cl:cr].astype(np.float64)
        col_median = np.median(band, axis=0)
        ink = _ink_likelihood_rgb(col_median)
        ink_idx = np.argmax(ink, axis=1)
        centers: list[float] = []
        boundaries: list[int] = []
        for i in range(n_cols):
            cols = np.where((ink_idx == i) & (ink[:, i] > 8.0))[0]
            if len(cols) == 0:
                cols = np.where(ink_idx == i)[0]
            if len(cols) == 0:
                span = cr - cl
                centers.append(cl + (i + 0.5) / n_cols * span)
                continue
            # Largest contiguous run for this ink.
            runs: list[tuple[int, int]] = []
            start = int(cols[0])
            prev = int(cols[0])
            for c in cols[1:]:
                c = int(c)
                if c <= prev + 3:
                    prev = c
                else:
                    runs.append((start, prev))
                    start = prev = c
            runs.append((start, prev))
            a, b = max(runs, key=lambda ab: ab[1] - ab[0])
            centers.append(cl + 0.5 * (a + b))
        for i in range(1, n_cols):
            boundaries.append(int(0.5 * (centers[i - 1] + centers[i])))
        return centers, boundaries

    y = int(ct + row_frac * (cb - ct))
    strip = warped[y, cl:cr].astype(np.float64)
    diff = np.sum(np.abs(strip[1:] - strip[:-1]), axis=1)
    sm = np.convolve(diff, np.ones(7) / 7, mode="same")
    n = len(sm)
    search_end = max(12, int(n * 0.96))
    peaks: list[tuple[float, int]] = []
    thr = float(np.percentile(sm[:search_end], 88))
    for i in range(4, search_end - 4):
        if sm[i] >= sm[i - 3 : i + 4].max() * 0.95 and sm[i] > thr:
            peaks.append((float(sm[i]), i))
    peaks.sort(reverse=True)
    chosen: list[tuple[float, int]] = []
    for strength, i in peaks:
        if all(abs(i - j) > 18 for _, j in chosen):
            chosen.append((strength, i))
        if len(chosen) == n_cols - 1:
            break
    chosen.sort(key=lambda t: t[1])
    boundaries = [cl + i for _, i in chosen]
    edges = [cl, *boundaries, cr]
    centers = [(edges[i] + edges[i + 1]) / 2.0 for i in range(n_cols)]
    return centers, boundaries


def detect_portrait_column_boundaries(
    warped: np.ndarray,
    bounds: tuple[int, int, int, int],
    *,
    n_cols: int = 6,
    row_frac: float = 0.58,
) -> list[int]:
    """Find internal vertical edges between portrait color columns."""
    _, boundaries = detect_portrait_column_centers(
        warped, bounds, n_cols=n_cols, row_frac=row_frac
    )
    return boundaries


def portrait_column_centers(
    bounds: tuple[int, int, int, int],
    boundaries: list[int],
) -> list[float]:
    """Midpoint of each column region from detected boundaries."""
    cl, _, cr, _ = bounds
    edges = [cl, *boundaries, cr]
    return [(edges[i] + edges[i + 1]) / 2.0 for i in range(len(edges) - 1)]


def draw_bounds_overlay_photo(
    draw: ImageDraw.ImageDraw,
    quad: DisplayQuad,
    bounds: tuple[int, int, int, int],
) -> None:
    """Keystone quad + axis-aligned bounds on the source photograph."""
    draw.polygon(
        [quad[0], quad[1], quad[3], quad[2]],
        outline=(255, 0, 0),
        width=3,
    )
    for name, pt in zip(("TL", "TR", "BR", "BL"), (quad[0], quad[1], quad[3], quad[2])):
        draw.ellipse([pt[0] - 5, pt[1] - 5, pt[0] + 5, pt[1] + 5], outline=(255, 0, 0), width=2)
        draw.text((pt[0] + 7, pt[1] - 6), name, fill=(255, 80, 80))
    bl, bt, br, bb = bounds
    draw.rectangle([bl, bt, br, bb], outline=(255, 220, 0), width=2)
    draw.text((bl + 4, bt + 4), "bounds", fill=(255, 220, 0))


def draw_bounds_overlay_warp(
    draw: ImageDraw.ImageDraw,
    warp_bounds: tuple[int, int, int, int],
    col_boundaries: list[int],
    layout_col_xs: list[float],
    sample_py: float,
    *,
    scan_y: int | None = None,
    geom: PrimariesGeometry | None = None,
    col_centers: list[float] | None = None,
) -> None:
    """Screen bounds, column edges, bisect scan, sample row on warped panel."""
    if geom is not None and hasattr(geom, "panel_trap"):
        trap = geom.panel_trap
        draw.polygon(list(trap), outline=(0, 255, 255), width=3)
        draw.text((trap[0][0] + 4, trap[0][1] + 4), "panel", fill=(0, 255, 255))
        cl = min(p[0] for p in trap)
        ct = min(p[1] for p in trap)
        cr = max(p[0] for p in trap)
        cb = max(p[1] for p in trap)
    else:
        cl, ct, cr, cb = warp_bounds
        draw.rectangle(warp_bounds, outline=(0, 255, 255), width=3)
        draw.text((cl + 4, ct + 4), "panel", fill=(0, 255, 255))

    if scan_y is not None:
        draw.line([(cl, scan_y), (cr, scan_y)], fill=(255, 128, 0), width=2)
        draw.text((cl + 4, scan_y + 4), "bisect scan", fill=(255, 128, 0))

    if geom is not None:
        scan = scan_y if scan_y is not None else int(sample_py)
        for i, run in enumerate(geom.column_runs):
            y0 = max(ct, scan - 8)
            y1 = min(cb, scan + 8)
            draw.rectangle([run.start, y0, run.end, y1], outline=(255, 200, 0), width=2)
            draw.text((int(run.center) - 10, y0 - 16), COLOR_NAMES[i][:3], fill=(255, 200, 0))
        for x in geom.boundaries:
            draw.line([(x, scan - 14), (x, scan + 14)], fill=(0, 120, 255), width=3)
        if hasattr(geom, "column_vert"):
            for col_run, (y0, y1) in zip(geom.column_runs, geom.column_vert, strict=True):
                xc = int(col_run.center)
                draw.line([(xc, y0), (xc, y1)], fill=(180, 180, 180), width=1)

    elif col_boundaries:
        for i, x in enumerate(col_boundaries):
            draw.line([(x, ct), (x, cb)], fill=(0, 120, 255), width=2)
            draw.text((x + 3, ct + 18 + i * 14), f"b{i+1}", fill=(0, 120, 255))

    centers = (
        geom.centers
        if geom is not None
        else col_centers
        if col_centers is not None
        else portrait_column_centers(warp_bounds, col_boundaries)
    )
    if geom is None:
        for name, cx in zip(COLOR_NAMES, centers[: len(COLOR_NAMES)]):
            x = int(cx)
            draw.line([(x, ct), (x, cb)], fill=(100, 100, 100), width=1)

    if geom is None:
        for name, x in zip(COLOR_NAMES, layout_col_xs):
            xi = int(x)
            draw.line([(xi, ct), (xi, cb)], fill=(255, 0, 200), width=1)
            draw.text((xi + 3, ct + 18), name[:3], fill=(255, 0, 200))

    py = int(sample_py)
    if geom is None:
        draw.line([(cl, py), (cr, py)], fill=(255, 255, 0), width=2)
        draw.text((cr - 90, py + 4), "sample row", fill=(255, 255, 0))

    draw.text(
        (cl + 4, cb - 52),
        "orange=cols  blue=peaks  cyan=trapezoid panel  grey=bar heights (green tallest)",
        fill=(255, 255, 255),
    )


def resolve_orientation(photo: np.ndarray, orientation: str) -> bool:
    """True when the chart was sent/viewed in portrait (hub rotate90CCW)."""
    if orientation == "portrait":
        return True
    if orientation == "landscape":
        return False
    # auto: portrait photo of a portrait-mounted frame is the common InkJoy case.
    h, w = photo.shape[:2]
    return h > w * 1.05


def _ink_likelihood_rgb(rgb: np.ndarray) -> np.ndarray:
    """Per-pixel scores for black, white, yellow, red, blue, green (higher = better)."""
    r = rgb[:, 0].astype(np.float64)
    g = rgb[:, 1].astype(np.float64)
    b = rgb[:, 2].astype(np.float64)
    lum = 0.299 * r + 0.587 * g + 0.114 * b
    chroma = np.max(rgb, axis=1).astype(np.float64) - np.min(rgb, axis=1).astype(np.float64)

    black = np.clip(85.0 - lum, 0.0, 85.0) * (chroma < 60.0)
    white = np.clip(lum - 115.0, 0.0, 110.0) * (chroma < 50.0)
    yellow = np.clip(np.minimum(r, g) - b - 15.0, 0.0, 140.0) * (r > 150.0) * (g > 145.0)
    red = np.clip(r - np.maximum(g, b) - 15.0, 0.0, 140.0) * (g < 95.0) * (b < 95.0)
    blue = np.clip(b - np.maximum(r, g) - 15.0, 0.0, 140.0)
    green = np.clip(g - np.maximum(r, b) - 8.0, 0.0, 140.0) * (lum > 20.0) * (lum < 185.0)

    return np.stack([black, white, yellow, red, blue, green], axis=1)


def _is_wood_rgb(rgb: np.ndarray) -> np.ndarray:
    """Oak bezel — warm tan, not a primaries ink (yellow/green must not match)."""
    r = rgb[:, 0].astype(np.float64)
    g = rgb[:, 1].astype(np.float64)
    b = rgb[:, 2].astype(np.float64)
    lum = 0.299 * r + 0.587 * g + 0.114 * b
    ink = _ink_likelihood_rgb(rgb)
    strong_ink = np.max(ink, axis=1) > 22.0
    warm = (r - b > 28.0) & (r - g > 10.0) & (lum > 110.0) & (lum < 225.0)
    return warm & ~strong_ink


def _wood_mask(img: np.ndarray, frame: str) -> np.ndarray:
    """Warm tan oak bezel pixels (not panel ink)."""
    if frame not in ("wood", "auto"):
        return np.zeros(img.shape[:2], dtype=bool)
    flat = img.reshape(-1, 3).astype(np.float64)
    return _is_wood_rgb(flat).reshape(img.shape[:2])


def _panel_mask(img: np.ndarray, *, frame: str = "auto") -> np.ndarray:
    """Active display pixels (excludes wood bezel; keeps saturated primaries)."""
    if frame in ("wood", "auto"):
        return ~_wood_mask(img, frame)
    lum = _luminance(img)
    return lum < 180


def detect_primaries_panel_bounds(
    img: np.ndarray,
    *,
    frame: str = "auto",
) -> tuple[int, int, int, int]:
    """Panel bounds anchored on black (left) and green (right) ink columns."""
    h, w = img.shape[:2]
    yt, yb = int(h * 0.14), int(h * 0.86)
    band = img[yt:yb].astype(np.float64)
    col_median = np.median(band, axis=0)
    ink = _ink_likelihood_rgb(col_median)
    ink_idx = np.argmax(ink, axis=1)

    black_cols = np.where((ink_idx == 0) & (ink[:, 0] > 12.0))[0]
    green_cols = np.where((ink_idx == 5) & (ink[:, 5] > 10.0))[0]
    if len(black_cols) < 4 or len(green_cols) < 4:
        return detect_panel_bounds(img, frame=frame)

    disp_l = int(black_cols[0])
    disp_r = int(green_cols[-1])

    ink_rows: list[int] = []
    for y in range(h):
        row = img[y].astype(np.float64)
        wood_frac = float(np.mean(_is_wood_rgb(row)))
        if wood_frac > 0.92:
            continue
        scores = _ink_likelihood_rgb(row)
        if np.sum(np.max(scores, axis=1) > 18.0) > w * 0.12:
            ink_rows.append(y)
    if len(ink_rows) < 8:
        lum = _luminance(img)
        row_med = np.median(lum[:, disp_l : disp_r + 1], axis=1)
        ink_rows = list(np.where(row_med < 200)[0])
    disp_t = int(ink_rows[len(ink_rows) // 10])
    disp_b = int(ink_rows[9 * len(ink_rows) // 10])
    pad = 2
    return (
        max(0, disp_l + pad),
        max(0, disp_t + pad),
        min(w - 1, disp_r - pad),
        min(h - 1, disp_b - pad),
    )


def detect_panel_bounds(img: np.ndarray, *, frame: str = "auto") -> tuple[int, int, int, int]:
    """Tight axis-aligned bounds of the color panel, excluding wood bezel."""
    h, w = img.shape[:2]
    panel = _panel_mask(img, frame=frame)
    lefts: list[int] = []
    rights: list[int] = []
    valid_ys: list[int] = []
    min_span = max(20, int(w * 0.22))
    for y in range(h):
        cols = np.where(panel[y])[0]
        if len(cols) >= min_span:
            lefts.append(int(cols[0]))
            rights.append(int(cols[-1]))
            valid_ys.append(y)
    if len(lefts) < 8:
        return detect_display_bounds_lum(img, frame=frame)

    # Robust edges from middle rows (skip glare / shadow outliers).
    lo = len(lefts) // 5
    hi = 4 * len(lefts) // 5
    disp_l = int(np.percentile(lefts[lo:hi], 12))
    disp_r = int(np.percentile(rights[lo:hi], 88))
    disp_t = int(valid_ys[len(valid_ys) // 12])
    disp_b = int(valid_ys[11 * len(valid_ys) // 12])
    pad = 2
    return (
        max(0, disp_l + pad),
        max(0, disp_t + pad),
        min(w - 1, disp_r - pad),
        min(h - 1, disp_b - pad),
    )


def detect_display_bounds_lum(img: np.ndarray, *, frame: str = "auto") -> tuple[int, int, int, int]:
    """Fallback luminance bounds (white bezel frames)."""
    h, w = img.shape[:2]
    lum = _luminance(img)
    y0, y1 = int(h * 0.10), int(h * 0.95)
    col_med = np.median(lum[y0:y1], axis=0)
    col_thr = 200 if frame in ("wood", "auto") else 180
    screen_cols = np.where(col_med < col_thr)[0]
    if len(screen_cols) == 0:
        return int(w * 0.05), int(h * 0.10), int(w * 0.95), int(h * 0.92)
    disp_l, disp_r = int(screen_cols[0]), int(screen_cols[-1])
    row_med = np.median(lum[:, disp_l:disp_r], axis=1)
    row_thr = 175 if frame in ("wood", "auto") else 160
    screen_rows = np.where(row_med < row_thr)[0]
    disp_t = int(screen_rows[0]) if len(screen_rows) else int(h * 0.10)
    disp_b = int(screen_rows[-1]) if len(screen_rows) else int(h * 0.92)
    if (disp_r - disp_l) > 0.90 * w or (disp_b - disp_t) > 0.90 * h:
        mx, my = int(w * 0.06), int(h * 0.06)
        disp_l, disp_t = max(disp_l, mx), max(disp_t, my)
        disp_r, disp_b = min(disp_r, w - mx), min(disp_b, h - my)
    return disp_l, disp_t, disp_r, disp_b


def detect_display_bounds(img: np.ndarray, *, frame: str = "auto") -> tuple[int, int, int, int]:
    """Return left, top, right, bottom of the active color panel."""
    if frame in ("wood", "auto"):
        return detect_panel_bounds(img, frame=frame)
    return detect_display_bounds_lum(img, frame=frame)


def detect_display_quad(
    img: np.ndarray, *, frame: str = "auto"
) -> DisplayQuad:
    """Return inner panel corners TL, TR, BL, BR (wood excluded)."""
    rough = detect_display_bounds(img, frame=frame)
    disp_l, disp_t, disp_r, disp_b = rough
    y_span = disp_b - disp_t
    left_pts: list[tuple[float, float]] = []
    right_pts: list[tuple[float, float]] = []
    for y in np.linspace(disp_t + y_span * 0.03, disp_b - y_span * 0.03, 28):
        left, right = row_screen_edges(img, float(y), rough, frame=frame)
        if right - left < (disp_r - disp_l) * 0.35:
            continue
        left_pts.append((left, float(y)))
        right_pts.append((right, float(y)))

    if len(left_pts) < 4:
        return (disp_l, disp_t), (disp_r, disp_t), (disp_l, disp_b), (disp_r, disp_b)

    ly = np.array([p[1] for p in left_pts])
    lx = np.array([p[0] for p in left_pts])
    ry = np.array([p[1] for p in right_pts])
    rx = np.array([p[0] for p in right_pts])
    la, lb = np.polyfit(ly, lx, 1)
    ra, rb = np.polyfit(ry, rx, 1)

    top_y = float(left_pts[0][1])
    bot_y = float(left_pts[-1][1])
    tl = (la * top_y + lb, top_y)
    tr = (ra * top_y + rb, top_y)
    bl = (la * bot_y + lb, bot_y)
    br = (ra * bot_y + rb, bot_y)
    return tl, tr, bl, br


def row_screen_edges(
    img: np.ndarray,
    y: float,
    bounds: tuple[int, int, int, int],
    *,
    frame: str = "auto",
) -> tuple[float, float]:
    """Left/right screen x at row y, excluding wood bezel (warm tan) on the edges."""
    disp_l, disp_t, disp_r, disp_b = bounds
    lum = _luminance(img)
    col_thr = 200 if frame in ("wood", "auto") else 180
    iy = int(max(disp_t, min(disp_b - 1, y)))
    row_rgb = img[iy, disp_l:disp_r].astype(np.float64)
    row_lum = lum[iy, disp_l:disp_r]
    r, g, b = row_rgb[:, 0], row_rgb[:, 1], row_rgb[:, 2]
    if frame in ("wood", "auto"):
        wood = _is_wood_rgb(row_rgb)
        panel = ~wood
    else:
        panel = row_lum < col_thr
    idx = np.where(panel)[0]
    if len(idx) < row_rgb.shape[0] * 0.25:
        return float(disp_l), float(disp_r)
    return float(disp_l + idx[0]), float(disp_l + idx[-1])


def row_bar_edges(
    img: np.ndarray,
    y: float,
    bounds: tuple[int, int, int, int],
    *,
    frame: str = "auto",
) -> tuple[float, float]:
    """Color-bar left/right at row y (excludes label column and wood margins)."""
    left, right = row_screen_edges(img, y, bounds, frame=frame)
    span = right - left
    # Label column ≈10% of layout; trim wood on the right.
    return left + span * 0.12, right - span * 0.08


def sample_point_perspective(
    img: np.ndarray,
    bounds: tuple[int, int, int, int],
    nx: float,
    ny: float,
    layout_mod,
    *,
    frame: str = "auto",
) -> tuple[float, float]:
    """Map normalized layout coords to photo pixels with per-row edge correction."""
    disp_l, disp_t, disp_r, disp_b = bounds
    py = disp_t + ny * (disp_b - disp_t)
    bar_left, bar_right = row_bar_edges(img, py, bounds, frame=frame)
    header_frac = layout_mod.HEADER_H / layout_mod.HEIGHT
    bar_nx = (nx - label_frac) / max(1e-6, 1.0 - label_frac)
    bar_nx = float(np.clip(bar_nx, 0.0, 1.0))
    px = bar_left + bar_nx * (bar_right - bar_left)
    return px, py


def sample_patch(
    img: np.ndarray, bounds: tuple[int, int, int, int], nx: float, ny: float, patch_frac: float
) -> tuple[np.ndarray, float]:
    disp_l, disp_t, disp_r, disp_b = bounds
    h, w = img.shape[:2]
    px = disp_l + nx * (disp_r - disp_l)
    py = disp_t + ny * (disp_b - disp_t)
    return sample_patch_xy(img, px, py, patch_frac)


def sample_patch_xy(img: np.ndarray, px: float, py: float, patch_frac: float) -> tuple[np.ndarray, float]:
    h, w = img.shape[:2]
    pw = max(5, int(w * patch_frac))
    ph = max(5, int(h * patch_frac))
    ix, iy = int(px), int(py)
    x0, y0 = max(0, ix - pw), max(0, iy - ph)
    x1, y1 = min(w, ix + pw), min(h, iy + ph)
    patch = img[y0:y1, x0:x1].astype(np.float64)
    flat = patch.reshape(-1, 3)
    med = np.median(flat, axis=0)
    stds: list[float] = []
    if patch.shape[0] >= 3 and patch.shape[1] >= 3:
        for c in range(3):
            ch = patch[:, :, c]
            for y in range(1, ch.shape[0] - 1, 2):
                for x in range(1, ch.shape[1] - 1, 2):
                    stds.append(float(ch[y - 1 : y + 2, x - 1 : x + 2].std()))
    dither = float(np.mean(stds)) if stds else float(flat.std(axis=0).mean())
    return med, dither


def swatch_center(
    layout_mod,
    family: str,
    target_rgb: tuple[int, int, int],
    *,
    x_frac: float = 0.5,
) -> tuple[float, float, str]:
    guesses = layout_mod.GUESSES[family]
    fi = layout_mod.ROW_ORDER.index(family)
    best_i, best_d = 0, 1e9
    best_name = guesses[0][0]
    for i, (name, r, g, b) in enumerate(guesses):
        d = (r - target_rgb[0]) ** 2 + (g - target_rgb[1]) ** 2 + (b - target_rgb[2]) ** 2
        if d < best_d:
            best_d, best_i, best_name = d, i, name
    n = len(guesses)
    body_h = layout_mod.HEIGHT - layout_mod.HEADER_H
    band_h = body_h / len(layout_mod.ROW_ORDER)
    bar_w = layout_mod.WIDTH - layout_mod.LABEL_W
    if n == 1:
        cx = layout_mod.LABEL_W + bar_w * x_frac
    else:
        cx = layout_mod.LABEL_W + bar_w * (best_i + x_frac) / n
    nx = cx / layout_mod.WIDTH
    ny = (layout_mod.HEADER_H + band_h * (fi + 0.5)) / layout_mod.HEIGHT
    return nx, ny, best_name, cx, layout_mod.HEADER_H + band_h * (fi + 0.5), cx, layout_mod.HEADER_H + band_h * (fi + 0.5)


def format_go_display(name: str, palette: np.ndarray) -> str:
    lines = [f"// {name} — measured P2 from color-guesses photo", f"var {name} = [6][3]float64{{"]
    for i, label in enumerate(COLOR_NAMES):
        r, g, b = int(palette[i, 0]), int(palette[i, 1]), int(palette[i, 2])
        comma = "," if i < 5 else ","
        lines.append(f"\t{{{r}, {g}, {b}}}{comma} // {label}")
    lines.append("}")
    return "\n".join(lines)


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("photo", type=Path)
    ap.add_argument(
        "--layout",
        choices=("2560x1440", "samsung-primaries", "inkjoy", "inkjoy-primaries"),
        default="2560x1440",
        help="source grid layout (default: Samsung 2560×1440)",
    )
    ap.add_argument(
        "--frame",
        choices=("auto", "white", "wood"),
        default="auto",
        help="bezel type for display bounds (default: auto)",
    )
    ap.add_argument(
        "--frame",
        choices=("auto", "white", "wood"),
        default="auto",
        help="bezel type for display bounds (default: auto)",
    )
    ap.add_argument(
        "--sample-x",
        type=float,
        default=None,
        help="horizontal sample point in each swatch bar, 0=left 1=right (default: 0.22 primaries, 0.5 guess grid)",
    )
    ap.add_argument(
        "--keystone",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="perspective-correct display quad before sampling (default: on)",
    )
    ap.add_argument(
        "--orientation",
        choices=("auto", "landscape", "portrait"),
        default="auto",
        help="how the chart was sent on the frame (default: auto — portrait when photo is taller than wide)",
    )
    ap.add_argument(
        "--keystone",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="rectify keystoned wood frame to layout size before sampling (default: on)",
    )
    ap.add_argument(
        "--no-tonal",
        action="store_true",
        help="skip ambient grey-axis normalization (not recommended)",
    )
    ap.add_argument("--debug", type=Path, help="write overlay PNG with quad + sample dots")
    ap.add_argument("--debug-warp", type=Path, help="write rectified panel image used for sampling")
    ap.add_argument("-o", "--output", type=Path, help="write JSON results")
    args = ap.parse_args()

    layout_key = {
        "2560x1440": "2560x1440",
        "samsung-primaries": "samsung-primaries",
        "inkjoy": "inkjoy",
        "inkjoy-primaries": "inkjoy-primaries",
    }[args.layout]
    layout_mod = load_layout_module(layout_key)
    img = np.array(Image.open(args.photo).convert("RGB"))
    quad = detect_display_quad(img, frame=args.frame)
    bounds = detect_display_bounds(img, frame=args.frame)
    sample_img = img
    sample_img = img
    if args.sample_x is not None:
        sample_x = args.sample_x
    elif args.layout == "inkjoy-primaries":
        sample_x = 0.78  # right of bar — avoids glare on left of photo
    elif args.layout == "samsung-primaries":
        sample_x = 0.78  # right of bar — avoids glare on left of photo
    else:
        sample_x = 0.5

    measured = []
    rows = []
    debug = Image.fromarray(img)
    draw = ImageDraw.Draw(debug) if args.debug else None
    if draw is not None:
        draw.rectangle(bounds, outline=(255, 0, 0), width=2)

    mode = "keystone rows" if args.keystone else "bounds"
    print(
        f"Photo {img.shape[1]}×{img.shape[0]}  {mode} {bounds}  sample_x={sample_x:.2f}"
    )
    print(f"{'ink':<8} {'P1 send':>10} {'P2 meas':>10} {'dither':>6}  label")
    print("-" * 52)

    for family in COLOR_NAMES:
        p1 = P1_PRIMARY[family]
        nx, ny, label, cx_px, cy_px = swatch_center(layout_mod, family, p1, x_frac=sample_x)
        if args.keystone:
            px, py = sample_point_perspective(
                sample_img, bounds, nx, ny, layout_mod, frame=args.frame
            )
            med, dither = sample_patch_xy(sample_img, px, py, args.patch)
        else:
            med, dither = sample_patch(sample_img, bounds, nx, ny, args.patch)
        measured.append([int(med[0]), int(med[1]), int(med[2])])
        send_hex = f"#{p1[0]:02X}{p1[1]:02X}{p1[2]:02X}"
        meas_hex = f"#{int(med[0]):02X}{int(med[1]):02X}{int(med[2]):02X}"
        print(f"{family:<8} {send_hex:>10} {meas_hex:>10} {dither:6.1f}  {label}")
        rows.append(
            {
                "color": family,
                "p1_rgb": list(p1),
                "p2_rgb": [int(med[0]), int(med[1]), int(med[2])],
                "dither": round(dither, 2),
                "swatch_label": label,
            }
        )
        if draw is not None:
            if args.keystone:
                px, py = sample_point_perspective(
                    sample_img, bounds, nx, ny, layout_mod, frame=args.frame
                )
                px, py = int(px), int(py)
            else:
                px = int(bounds[0] + nx * (bounds[2] - bounds[0]))
                py = int(bounds[1] + ny * (bounds[3] - bounds[1]))
            draw.ellipse([px - 4, py - 4, px + 4, py + 4], fill=(0, 255, 0))
            draw.text((px + 6, py - 8), family[:3], fill=(0, 255, 0))

    p2_arr = np.array(measured, dtype=np.int32)
    var_name = (
        "PaletteSamsungDisplay"
        if args.layout in ("2560x1440", "samsung-primaries")
        else "PaletteInkJoyDisplay"
    )
    print()
    print(format_go_display(var_name, p2_arr))

    if args.debug:
        args.debug.parent.mkdir(parents=True, exist_ok=True)
        debug.save(args.debug)
        print(f"\nWrote {args.debug}")

    if args.debug_warp and args.keystone and warp_debug is not None:
        args.debug_warp.parent.mkdir(parents=True, exist_ok=True)
        warp_debug.save(args.debug_warp)
        print(f"Wrote {args.debug_warp}")

    if args.debug and args.keystone and warp_debug is not None and not args.debug_warp:
        bounds_path = args.debug.with_name(args.debug.stem + "-warp" + args.debug.suffix)
        bounds_path.parent.mkdir(parents=True, exist_ok=True)
        warp_debug.save(bounds_path)
        print(f"Wrote {bounds_path}")

    if args.output:
        payload = {
            "photo": str(args.photo),
            "layout": args.layout,
            "orientation": "portrait" if portrait else "landscape",
            "keystone": args.keystone,
            "tonal_normalize": not args.no_tonal,
            "tonal_note": tonal_note,
            "quad": [[float(x), float(y)] for x, y in quad],
            "bounds": list(bounds),
            "warp_bounds": list(warp_bounds),
            "column_boundaries": col_boundaries,
            "column_centers_detected": [round(c, 1) for c in col_centers],
            "column_centers_layout": [round(x, 1) for x in layout_col_xs],
            "warp_size": [out_w, out_h],
            "sample_x": sample_x,
            "p1_primary": {k: list(v) for k, v in P1_PRIMARY.items()},
            "swatches": rows,
            "palette_display": p2_arr.tolist(),
        }
        args.output.write_text(json.dumps(payload, indent=2) + "\n")
        print(f"Wrote {args.output}")


if __name__ == "__main__":
    main()
