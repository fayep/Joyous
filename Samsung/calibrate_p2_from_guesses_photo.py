#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy"]
# ///
"""Measure Palette*Display (P2) from a color-guesses photograph.

Samples the on-panel appearance at each confirmed P1 primary swatch
(#000000, #FFFFFF, #FFFF00, #FF0000, #0000FF, #00FF00). Uses the white
bezel to find display bounds, then maps back to the source grid layout.

Example:
    uv run calibrate_p2_from_guesses_photo.py IMG_0103.png
    uv run calibrate_p2_from_guesses_photo.py photo.png --layout 2560x1440 --debug p2-debug.png
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw

from samsung_palettes import COLOR_NAMES, PALETTE_SAMSUNG_SEND

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
    if layout == "inkjoy":
        import gen_color_guesses_inkjoy as mod

        return mod
    if layout == "inkjoy-primaries":
        import gen_color_primaries_inkjoy as mod

        return mod
    if layout == "samsung-primaries":
        import gen_color_primaries_samsung as mod

        return mod
    import gen_color_guesses as mod

    return mod


def _luminance(img: np.ndarray) -> np.ndarray:
    return 0.299 * img[:, :, 0] + 0.587 * img[:, :, 1] + 0.114 * img[:, :, 2]


def detect_display_bounds(img: np.ndarray, *, frame: str = "auto") -> tuple[int, int, int, int]:
    """Return left, top, right, bottom. Screen is darker than bezel (wood or white)."""
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
    # Wood frame / tight crop: if detection spans nearly the whole photo, inset ~6%.
    if (disp_r - disp_l) > 0.90 * w or (disp_b - disp_t) > 0.90 * h:
        mx, my = int(w * 0.06), int(h * 0.06)
        disp_l, disp_t = max(disp_l, mx), max(disp_t, my)
        disp_r, disp_b = min(disp_r, w - mx), min(disp_b, h - my)
    return disp_l, disp_t, disp_r, disp_b


def detect_display_quad(
    img: np.ndarray, *, frame: str = "auto"
) -> tuple[tuple[float, float], tuple[float, float], tuple[float, float], tuple[float, float]]:
    """Return display corners TL, TR, BL, BR inside wood/white bezel (keystone-aware)."""
    disp_l, disp_t, disp_r, disp_b = detect_display_bounds(img, frame=frame)
    lum = _luminance(img)
    col_thr = 200 if frame in ("wood", "auto") else 180

    left_pts: list[tuple[float, float]] = []
    right_pts: list[tuple[float, float]] = []
    y_span = disp_b - disp_t
    for y in np.linspace(disp_t + y_span * 0.06, disp_b - y_span * 0.06, 20):
        row = lum[int(y), disp_l:disp_r]
        if row.size < 20:
            continue
        screen = np.where(row < col_thr)[0]
        if len(screen) < row.size * 0.35:
            continue
        left_pts.append((float(disp_l + screen[0]), float(y)))
        right_pts.append((float(disp_l + screen[-1]), float(y)))

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
    # Wood bezel: warm (R≫B), mid luminance. Screen + label: not wood.
    wood = (r - b > 28) & (row_lum > 110) & (row_lum < 225)
    screen = (~wood) & (row_lum < col_thr)
    idx = np.where(screen)[0]
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
    label_frac = layout_mod.LABEL_W / layout_mod.WIDTH
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
    return nx, ny, best_name, cx, layout_mod.HEADER_H + band_h * (fi + 0.5)


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
    ap.add_argument("--patch", type=float, default=0.012, help="sample patch size (fraction of width)")
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
    ap.add_argument("--debug", type=Path, help="write overlay PNG")
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

    if args.output:
        payload = {
            "photo": str(args.photo),
            "layout": args.layout,
            "keystone": args.keystone,
            "quad": [[float(x), float(y)] for x, y in quad],
            "bounds": list(bounds),
            "p1_primary": {k: list(v) for k, v in P1_PRIMARY.items()},
            "swatches": rows,
            "palette_display": p2_arr.tolist(),
        }
        args.output.write_text(json.dumps(payload, indent=2) + "\n")
        print(f"Wrote {args.output}")


if __name__ == "__main__":
    main()
