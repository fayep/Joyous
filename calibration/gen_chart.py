#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy"]
# ///
"""Generate primaries or color-guess calibration charts at any frame size.

Examples:
  uv run calibration/gen_chart.py primaries --size 1600x1200 --palette inkjoy
  uv run calibration/gen_chart.py primaries --size 2560x1440 --palette samsung
  uv run calibration/gen_chart.py guesses --size 1600x1200
  uv run calibration/gen_chart.py guesses --size 2560x1440 -o /tmp/chart.png
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

_CAL_DIR = Path(__file__).resolve().parent
if str(_CAL_DIR) not in sys.path:
    sys.path.insert(0, str(_CAL_DIR))

from layouts.chart import (
    build_layout,
    flat_snap_bin,
    palette_send,
    parse_size,
    render_chart,
)


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("kind", choices=("primaries", "guesses"))
    ap.add_argument("--size", default="1600x1200", help="WIDTHxHEIGHT (default 1600x1200)")
    ap.add_argument("--palette", choices=("inkjoy", "samsung"), default="inkjoy")
    ap.add_argument("-o", "--out-dir", type=Path, default=Path.cwd())
    ap.add_argument("--bin", action="store_true", help="also write flat .bin (primaries/guesses on inkjoy size)")
    args = ap.parse_args()

    w, h = parse_size(args.size)
    layout = build_layout(args.kind, w, h, args.palette)
    img = render_chart(layout)
    args.out_dir.mkdir(parents=True, exist_ok=True)

    stem = f"color-{args.kind}-{w}x{h}"
    png_path = args.out_dir / f"{stem}.png"
    img.save(png_path, optimize=True)
    print(f"wrote {png_path}")

    if args.bin or (args.kind == "primaries" and args.palette == "inkjoy" and (w, h) == (1600, 1200)):
        bin_path = args.out_dir / f"{stem}.bin"
        bin_path.write_bytes(flat_snap_bin(img, palette_send(args.palette)))
        print(f"wrote {bin_path} ({bin_path.stat().st_size} bytes)")

    print()
    print("Photograph after send, then:")
    layout_flag = f"{w}x{h}" if args.kind == "guesses" else f"{args.palette}-primaries"
    print(f"  uv run calibration/calibrate_p2_from_photo.py photo --layout {layout_flag} --size {w}x{h}")


if __name__ == "__main__":
    main()
