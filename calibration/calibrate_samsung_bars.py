#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy"]
# ///
"""Sample a photograph of palette-bars on Samsung and calibrate PaletteSamsung.

Measures median RGB at each swatch center (robust to on-panel dither noise), compares
to source targets, and prints a pre-compensated hub palette:

    palette_send[c] = target[c] * target[c] / measured[c]   (per channel, clamped)

Assumes the photo is roughly aligned to palette-bars-2560x1440.png (trimmed frame OK).
Use --debug-image to write an overlay showing sample patches.

Example:
    uv run calibration/calibrate_samsung_bars.py ~/photo.png
    uv run calibration/calibrate_samsung_bars.py photo.png --row "Samsung (hub)" -o calibrated.json
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

_CAL_DIR = Path(__file__).resolve().parent
if str(_CAL_DIR) not in sys.path:
    sys.path.insert(0, str(_CAL_DIR))

import numpy as np
from PIL import Image, ImageDraw

from palettes import (
    COLOR_NAMES,
    CROPPED_PHOTO_COL_XS,
    CROPPED_PHOTO_ROW_YS,
    PALETTES,
    ROW_NAMES,
    build_sample_centers,
)


def sample_patch(img: np.ndarray, cx: float, cy: float, patch_frac: float) -> np.ndarray:
    """Median RGB in a central patch; cx/cy are normalized 0–1."""
    h, w = img.shape[:2]
    pw = max(8, int(w * patch_frac))
    ph = max(8, int(h * patch_frac))
    px = int(cx * w)
    py = int(cy * h)
    x0 = max(0, px - pw // 2)
    y0 = max(0, py - ph // 2)
    x1 = min(w, x0 + pw)
    y1 = min(h, y0 + ph)
    patch = img[y0:y1, x0:x1].reshape(-1, 3)
    return np.median(patch, axis=0)


def compensate_channel(target: float, measured: float) -> int:
    """Send this sRGB so panel output moves toward target (first-order inverse)."""
    if measured < 1.0:
        return int(round(min(255, max(0, target))))
    return int(round(min(255, max(0, target * target / measured))))


def compensate_rgb(target: np.ndarray, measured: np.ndarray) -> np.ndarray:
    return np.array(
        [compensate_channel(t, m) for t, m in zip(target, measured)],
        dtype=np.int32,
    )


def delta_e(rgb1: np.ndarray, rgb2: np.ndarray) -> float:
    return float(np.linalg.norm(rgb1.astype(np.float64) - rgb2.astype(np.float64)))


def format_go_palettes(send: np.ndarray, display: np.ndarray) -> str:
    lines = [
        "// Calibrated from Samsung photo — two-palette encode",
        "var PaletteSamsungSend = [6][3]float64{",
    ]
    for i, label in enumerate(COLOR_NAMES):
        r, g, b = int(send[i, 0]), int(send[i, 1]), int(send[i, 2])
        comma = "," if i < 5 else ","
        lines.append(f"\t{{{r}, {g}, {b}}}{comma} // {label}")
    lines.append("}")
    lines.append("")
    lines.append("var PaletteSamsungDisplay = [6][3]float64{")
    for i, label in enumerate(COLOR_NAMES):
        r, g, b = int(display[i, 0]), int(display[i, 1]), int(display[i, 2])
        comma = "," if i < 5 else ","
        lines.append(f"\t{{{r}, {g}, {b}}}{comma} // {label}")
    lines.append("}")
    return "\n".join(lines)


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("photo", type=Path, help="photograph of palette bars on Samsung")
    ap.add_argument(
        "--row",
        default="Samsung (hub)",
        help='row to calibrate against (default: "Samsung (hub)")',
    )
    ap.add_argument(
        "--patch",
        type=float,
        default=0.045,
        help="sample patch size as fraction of image width/height (default: 0.045)",
    )
    ap.add_argument(
        "--auto-tune",
        action="store_true",
        help="scan photo to refine black/green sample positions",
    )
    ap.add_argument(
        "--layout",
        choices=("cropped", "source"),
        default="cropped",
        help="sample grid: cropped photo (default) or full 2560×1440 source screenshot",
    )
    ap.add_argument(
        "--row-ys",
        type=str,
        default="",
        help="comma-separated normalized row centers (overrides layout)",
    )
    ap.add_argument(
        "--col-xs",
        type=str,
        default="",
        help="comma-separated normalized column centers (overrides layout)",
    )
    ap.add_argument(
        "--debug-image",
        type=Path,
        default=None,
        help="write PNG with sample rectangles drawn",
    )
    ap.add_argument("-o", "--output", type=Path, help="write JSON results")
    args = ap.parse_args()

    if args.row not in PALETTES:
        raise SystemExit(f"unknown row {args.row!r}; choose from {ROW_NAMES}")

    img = np.array(Image.open(args.photo).convert("RGB"))
    h, w = img.shape[:2]
    print(f"Photo: {args.photo} ({w}×{h})")
    print(f"Calibrating from row: {args.row}")
    if args.row_ys:
        row_ys = [float(x) for x in args.row_ys.split(",")]
    elif args.layout == "cropped":
        row_ys = CROPPED_PHOTO_ROW_YS
    else:
        row_ys = None

    if args.col_xs:
        col_xs = [float(x) for x in args.col_xs.split(",")]
    elif args.layout == "cropped":
        col_xs = CROPPED_PHOTO_COL_XS
    else:
        col_xs = None

    centers = build_sample_centers(
        args.layout,
        row_ys,
        col_xs,
        img=img,
        auto_tune=args.auto_tune,
    )
    print(f"Layout: {args.layout}")
    print()

    targets = PALETTES[args.row]
    measured_by_color: dict[str, np.ndarray] = {}
    rows_out: list[dict] = []

    debug = Image.fromarray(img)
    draw = ImageDraw.Draw(debug) if args.debug_image else None

    for row_name, color_name, cx, cy in centers:
        rgb = sample_patch(img, cx, cy, args.patch)

        if row_name == args.row:
            measured_by_color[color_name] = rgb

        if draw is not None:
            pw = max(8, int(w * args.patch))
            ph = max(8, int(h * args.patch))
            px, py = int(cx * w), int(cy * h)
            color = (0, 255, 0) if row_name == args.row else (255, 128, 0)
            draw.rectangle(
                [px - pw // 2, py - ph // 2, px + pw // 2, py + ph // 2],
                outline=color,
                width=2,
            )
            draw.text((px + 4, py + 4), f"{row_name[:3]}/{color_name[:3]}", fill=color)

    print(f"{'color':<8} {'target':>16} {'measured':>16} {'Δ':>6}  compensated")
    print("-" * 72)

    compensated = []
    for i, color_name in enumerate(COLOR_NAMES):
        target = targets[i]
        measured = measured_by_color[color_name]
        comp = compensate_rgb(target, measured)
        compensated.append(comp)
        t_hex = f"#{int(target[0]):02X}{int(target[1]):02X}{int(target[2]):02X}"
        m_hex = f"#{int(measured[0]):02X}{int(measured[1]):02X}{int(measured[2]):02X}"
        c_hex = f"#{comp[0]:02X}{comp[1]:02X}{comp[2]:02X}"
        d = delta_e(target, measured)
        print(
            f"{color_name:<8} {t_hex} ({int(target[0]):3},{int(target[1]):3},{int(target[2]):3})"
            f"  {m_hex} ({int(measured[0]):3},{int(measured[1]):3},{int(measured[2]):3})"
            f"  {d:5.1f}  {c_hex} ({comp[0]:3},{comp[1]:3},{comp[2]:3})"
        )
        rows_out.append(
            {
                "color": color_name,
                "target_rgb": [int(x) for x in target],
                "measured_rgb": [int(x) for x in measured],
                "delta_e": round(d, 2),
                "compensated_rgb": [int(x) for x in comp],
            }
        )

    compensated_arr = np.array(compensated, dtype=np.int32)
    measured_arr = np.array(
        [measured_by_color[c] for c in COLOR_NAMES], dtype=np.int32
    )
    print("Suggested joyous-hub/dither.go two-palette constants:")
    print(format_go_palettes(compensated_arr, measured_arr))
    print()
    print("encode_bin.py PALETTE_SAMSUNG rows:")
    for color_name, rgb in zip(COLOR_NAMES, compensated_arr):
        print(f"    [{rgb[0]:3}, {rgb[1]:3}, {rgb[2]:3}],   # {color_name}")

    if args.debug_image:
        args.debug_image.parent.mkdir(parents=True, exist_ok=True)
        debug.save(args.debug_image)
        print(f"\nWrote debug overlay: {args.debug_image}")

    if args.output:
        payload = {
            "photo": str(args.photo),
            "photo_size": [w, h],
            "calibration_row": args.row,
            "layout": args.layout,
            "patch_frac": args.patch,
            "swatches": rows_out,
            "palette_samsung_compensated": compensated_arr.tolist(),
        }
        args.output.write_text(json.dumps(payload, indent=2) + "\n")
        print(f"Wrote {args.output}")


if __name__ == "__main__":
    main()
