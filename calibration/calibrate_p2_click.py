#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy", "pillow-heif"]
# ///
"""Interactive P2 calibration: loupe under cursor, 6 clicks per ink, averaged.

Example:
    uv run calibration/calibrate_p2_click.py ~/Downloads/IMG_0158.HEIC \\
      --layout inkjoy-primaries --frame wood --orientation portrait \\
      -o inkjoy-p2-click.json

Keys: 1–6 pick ink · click add point · u undo last point · c clear ink · e export · q quit
"""

from __future__ import annotations

import argparse
import json
import sys
import tkinter as tk
from pathlib import Path
from tkinter import ttk

_CAL_DIR = Path(__file__).resolve().parent
if str(_CAL_DIR) not in sys.path:
    sys.path.insert(0, str(_CAL_DIR))

import numpy as np
from PIL import Image, ImageDraw, ImageTk

try:
    import pillow_heif

    pillow_heif.register_heif_opener()
except ImportError:
    pass

from calibrate_p2_from_photo import (
    P1_PRIMARY,
    _ink_likelihood_rgb,
    _is_wood_rgb,
    detect_display_quad,
    load_layout_module,
    normalize_p2_tonal,
    resolve_orientation,
    sample_patch_xy,
    warp_quad_to_rect,
)
from palettes import COLOR_NAMES, PALETTE_INKJOY_DISPLAY

INK_KEYS = {str(i + 1): name for i, name in enumerate(COLOR_NAMES)}
INK_COLORS = {
    "black": "#555555",
    "white": "#eeeeee",
    "yellow": "#dddd00",
    "red": "#ff2222",
    "blue": "#2222ff",
    "green": "#22aa22",
}

LOUPE_RADIUS = 12  # source pixels each side of cursor
LOUPE_MAG = 9  # pixels per source pixel in loupe


def _nearest_palette(rgb: np.ndarray, palette: np.ndarray) -> tuple[int, float]:
    d = np.sum((palette - rgb.reshape(1, 3)) ** 2, axis=1)
    i = int(np.argmin(d))
    return i, float(np.sqrt(d[i]))


def _analyze_pixel(rgb: np.ndarray) -> dict:
    rgb = rgb.astype(np.float64)
    scores = _ink_likelihood_rgb(rgb.reshape(1, 3))[0]
    best = int(scores.argmax())
    wood = bool(_is_wood_rgb(rgb.reshape(1, 3))[0])
    ni, nd = _nearest_palette(rgb, PALETTE_INKJOY_DISPLAY)
    return {
        "rgb": [int(rgb[0]), int(rgb[1]), int(rgb[2])],
        "hex": f"#{int(rgb[0]):02X}{int(rgb[1]):02X}{int(rgb[2]):02X}",
        "ink": COLOR_NAMES[best],
        "scores": {COLOR_NAMES[i]: round(float(scores[i]), 1) for i in range(6)},
        "nearest_p2": COLOR_NAMES[ni],
        "nearest_p2_dist": round(nd, 1),
        "wood": wood,
    }


def _loupe_photo(img: np.ndarray, ix: int, iy: int) -> Image.Image:
    """Magnified patch with crosshair on center pixel."""
    h, w = img.shape[:2]
    r = LOUPE_RADIUS
    side = 2 * r + 1
    crop = np.zeros((side, side, 3), dtype=np.uint8)
    crop[:] = 128

    x0, y0 = ix - r, iy - r
    for dy in range(side):
        for dx in range(side):
            sx, sy = x0 + dx, y0 + dy
            if 0 <= sx < w and 0 <= sy < h:
                crop[dy, dx] = img[sy, sx]

    out_w = side * LOUPE_MAG
    pil = Image.fromarray(crop).resize((out_w, out_w), Image.Resampling.NEAREST)
    draw = ImageDraw.Draw(pil)
    c = out_w // 2
    cross = max(2, LOUPE_MAG // 2)
    draw.line([(c, 0), (c, out_w - 1)], fill=(255, 64, 64), width=1)
    draw.line([(0, c), (out_w - 1, c)], fill=(255, 64, 64), width=1)
    box = LOUPE_MAG // 2
    draw.rectangle(
        [c - box, c - box, c + box - 1, c + box - 1],
        outline=(255, 255, 0),
        width=2,
    )
    return pil


def _aggregate_points(points: list[dict]) -> dict | None:
    if not points:
        return None
    rgbs = np.array([p["p2_rgb_raw"] for p in points], dtype=np.float64)
    return {
        "p2_rgb_raw": np.round(rgbs.mean(axis=0)).astype(int).tolist(),
        "dither": round(float(np.mean([p["dither"] for p in points])), 2),
        "points": points,
    }


def _load_image(
    photo: Path,
    *,
    layout: str,
    frame: str,
    orientation: str,
    keystone: bool,
) -> tuple[np.ndarray, str]:
    layout_mod = load_layout_module(layout)
    img = np.array(Image.open(photo).convert("RGB"))
    portrait = resolve_orientation(img, orientation)
    if not keystone:
        return img, "raw photo"
    quad = detect_display_quad(img, frame=frame)
    out_w = layout_mod.HEIGHT if portrait else layout_mod.WIDTH
    out_h = layout_mod.WIDTH if portrait else layout_mod.HEIGHT
    warped = warp_quad_to_rect(img, quad, out_w, out_h)
    orient = "portrait" if portrait else "landscape"
    return warped, f"keystone {out_w}×{out_h} ({orient})"


class ClickCalibratorApp:
    def __init__(
        self,
        img: np.ndarray,
        *,
        photo: Path,
        output: Path | None,
        patch_frac: float,
        mode: str,
        points_per_ink: int,
    ) -> None:
        self.img = img
        self.photo = photo
        self.output = output
        self.patch_frac = patch_frac
        self.mode = mode
        self.points_per_ink = points_per_ink
        self.active = COLOR_NAMES[0]
        self.points: dict[str, list[dict]] = {n: [] for n in COLOR_NAMES}
        self.marker_ids: dict[str, list[tuple[int, ...]]] = {n: [] for n in COLOR_NAMES}
        self._loupe_tk: ImageTk.PhotoImage | None = None

        self.root = tk.Tk()
        self.root.title("P2 click calibration")
        self.root.bind("<Key>", self._on_key)

        top = ttk.Frame(self.root, padding=6)
        top.pack(fill=tk.X)

        self.ink_var = tk.StringVar(value=self.active)
        ink_row = ttk.Frame(top)
        ink_row.pack(fill=tk.X)
        ttk.Label(ink_row, text="Ink:").pack(side=tk.LEFT, padx=(0, 8))
        for name in COLOR_NAMES:
            ttk.Radiobutton(
                ink_row, text=name, value=name, variable=self.ink_var, command=self._sync_active
            ).pack(side=tk.LEFT, padx=2)

        self.status = tk.StringVar(value="Move over the chart…")
        ttk.Label(top, textvariable=self.status, wraplength=960, justify=tk.LEFT).pack(
            fill=tk.X, pady=(6, 0)
        )

        self.sample_lbl = tk.StringVar(value="Averages: (need 6 points per ink)")
        ttk.Label(top, textvariable=self.sample_lbl, font=("Menlo", 11), justify=tk.LEFT).pack(
            fill=tk.X, pady=(4, 0)
        )

        btn_row = ttk.Frame(top)
        btn_row.pack(fill=tk.X, pady=(6, 0))
        ttk.Button(btn_row, text="Export (e)", command=self.export).pack(side=tk.LEFT, padx=2)
        ttk.Button(btn_row, text="Undo point (u)", command=self.undo).pack(side=tk.LEFT, padx=2)
        ttk.Button(btn_row, text="Clear ink (c)", command=self.clear_ink).pack(side=tk.LEFT, padx=2)
        ttk.Button(btn_row, text="Quit (q)", command=self.root.destroy).pack(side=tk.LEFT, padx=2)

        body = ttk.Frame(self.root, padding=6)
        body.pack(fill=tk.BOTH, expand=True)

        self.h, self.w = img.shape[:2]
        self.scale = min(1.0, 1100 / self.w, 800 / self.h)
        disp_w, disp_h = int(self.w * self.scale), int(self.h * self.scale)
        self.disp_w, self.disp_h = disp_w, disp_h
        self.loupe_disp = (2 * LOUPE_RADIUS + 1) * LOUPE_MAG
        disp = Image.fromarray(img).resize((disp_w, disp_h), Image.Resampling.LANCZOS)
        self.tk_img = ImageTk.PhotoImage(disp)

        canvas_frame = ttk.Frame(body)
        canvas_frame.pack(side=tk.LEFT)
        self.canvas = tk.Canvas(
            canvas_frame,
            width=disp_w,
            height=disp_h,
            highlightthickness=0,
            cursor="crosshair",
        )
        self.canvas.pack()
        self.bg_id = self.canvas.create_image(0, 0, anchor=tk.NW, image=self.tk_img, tags=("bg",))
        self.loupe_id: int | None = None
        self.loupe_frame_id: int | None = None
        self.canvas.bind("<Motion>", self._on_motion)
        self.canvas.bind("<Leave>", self._on_leave)
        self.canvas.bind("<Enter>", self._on_enter)
        self.canvas.bind("<Button-1>", self._on_click)

        side = ttk.Frame(body, padding=(12, 0))
        side.pack(side=tk.LEFT, fill=tk.Y)
        ttk.Label(side, text="Center pixel").pack(anchor=tk.W)
        self.swatch = tk.Canvas(side, width=72, height=72, highlightthickness=1, highlightbackground="#888")
        self.swatch.pack(pady=4)
        self.detail = tk.StringVar(value="")
        ttk.Label(side, textvariable=self.detail, font=("Menlo", 10), justify=tk.LEFT).pack(anchor=tk.W)
        ttk.Label(
            side,
            text=f"Place {points_per_ink} points per ink.\n"
            "Loupe centered on cursor.\n"
            "1–6 ink · u undo · c clear · e export",
            justify=tk.LEFT,
            wraplength=220,
        ).pack(anchor=tk.W, pady=(12, 0))

    def _sync_active(self) -> None:
        self.active = self.ink_var.get()
        self._update_status_cursor()

    def _canvas_to_image(self, cx: int, cy: int) -> tuple[int, int]:
        ix = int(np.clip(round(cx / self.scale), 0, self.w - 1))
        iy = int(np.clip(round(cy / self.scale), 0, self.h - 1))
        return ix, iy

    def _draw_swatch(self, rgb: list[int]) -> None:
        self.swatch.delete("all")
        hex_c = f"#{rgb[0]:02x}{rgb[1]:02x}{rgb[2]:02x}"
        self.swatch.create_rectangle(2, 2, 70, 70, fill=hex_c, outline="")

    def _hide_loupe(self) -> None:
        if self.loupe_id is not None:
            self.canvas.itemconfigure(self.loupe_id, state=tk.HIDDEN)
        if self.loupe_frame_id is not None:
            self.canvas.itemconfigure(self.loupe_frame_id, state=tk.HIDDEN)

    def _show_loupe(self, cx: int, cy: int, ix: int, iy: int) -> None:
        pil = _loupe_photo(self.img, ix, iy)
        self._loupe_tk = ImageTk.PhotoImage(pil)
        half = self.loupe_disp // 2
        lx = int(np.clip(cx, half, self.disp_w - half))
        ly = int(np.clip(cy, half, self.disp_h - half))
        x0, y0 = lx - half, ly - half
        x1, y1 = lx + half, ly + half
        if self.loupe_id is None:
            self.loupe_id = self.canvas.create_image(
                lx, ly, image=self._loupe_tk, tags=("loupe",)
            )
            self.loupe_frame_id = self.canvas.create_rectangle(
                x0, y0, x1, y1, outline="#fff", width=2, tags=("loupe",)
            )
        else:
            self.canvas.itemconfigure(self.loupe_id, image=self._loupe_tk, state=tk.NORMAL)
            self.canvas.coords(self.loupe_id, lx, ly)
            self.canvas.coords(self.loupe_frame_id, x0, y0, x1, y1)
            self.canvas.itemconfigure(self.loupe_frame_id, state=tk.NORMAL)
        self.canvas.tag_raise("loupe")

    def _on_enter(self, _event: tk.Event) -> None:
        pass

    def _on_leave(self, _event: tk.Event) -> None:
        self._hide_loupe()

    def _on_motion(self, event: tk.Event) -> None:
        ix, iy = self._canvas_to_image(event.x, event.y)
        info = _analyze_pixel(self.img[iy, ix])
        self._draw_swatch(info["rgb"])
        wood = " · WOOD?" if info["wood"] else ""
        n = len(self.points[self.active])
        self.status.set(
            f"{self.active} {n}/{self.points_per_ink}  "
            f"({ix},{iy}) {info['hex']} → {info['ink']}{wood}  "
            f"P2~{info['nearest_p2']} Δ{info['nearest_p2_dist']:.0f}"
        )
        score_line = "  ".join(f"{k[:3]}:{v:.0f}" for k, v in info["scores"].items())
        self.detail.set(f"{score_line}\nP1 {P1_PRIMARY[info['ink']]}")
        self._show_loupe(event.x, event.y, ix, iy)

    def _update_status_cursor(self) -> None:
        n = len(self.points[self.active])
        self.status.set(f"{self.active}: {n}/{self.points_per_ink} points")

    def _on_click(self, event: tk.Event) -> None:
        if len(self.points[self.active]) >= self.points_per_ink:
            self.status.set(f"{self.active} already has {self.points_per_ink} points — switch ink or clear (c)")
            return
        ix, iy = self._canvas_to_image(event.x, event.y)
        med, dither = sample_patch_xy(self.img, ix, iy, self.patch_frac)
        rgb = [int(med[0]), int(med[1]), int(med[2])]
        idx = len(self.points[self.active]) + 1
        self.points[self.active].append(
            {"x": ix, "y": iy, "p2_rgb_raw": rgb, "dither": round(dither, 2)}
        )
        cx, cy = int(ix * self.scale), int(iy * self.scale)
        color = INK_COLORS.get(self.active, "#0f0")
        r = 4
        oid = self.canvas.create_oval(cx - r, cy - r, cx + r, cy + r, outline=color, width=2)
        tid = self.canvas.create_text(cx + 7, cy - 7, text=str(idx), fill=color, font=("Menlo", 9, "bold"))
        self.marker_ids[self.active].append((oid, tid))
        self.canvas.tag_raise("loupe")
        self._refresh_sample_line()
        n = len(self.points[self.active])
        agg = _aggregate_points(self.points[self.active])
        assert agg is not None
        ar, ag, ab = agg["p2_rgb_raw"]
        self.status.set(
            f"{self.active} {n}/{self.points_per_ink}  "
            f"point #{idx} → #{rgb[0]:02X}{rgb[1]:02X}{rgb[2]:02X}  "
            f"avg #{ar:02X}{ag:02X}{ab:02X}"
        )

    def _refresh_sample_line(self) -> None:
        parts = []
        for name in COLOR_NAMES:
            agg = _aggregate_points(self.points[name])
            n = len(self.points[name])
            if agg is None:
                parts.append(f"{name[:3]}:{n}/{self.points_per_ink}")
            else:
                r, g, b = agg["p2_rgb_raw"]
                parts.append(f"{name[:3]}:{n}/{self.points_per_ink} #{r:02X}{g:02X}{b:02X}")
        self.sample_lbl.set("  ".join(parts))

    def undo(self) -> None:
        pts = self.points[self.active]
        if not pts:
            return
        pts.pop()
        ids = self.marker_ids[self.active].pop()
        for item in ids:
            self.canvas.delete(item)
        self._refresh_sample_line()
        self._update_status_cursor()

    def clear_ink(self) -> None:
        self.points[self.active].clear()
        for ids in self.marker_ids[self.active]:
            for item in ids:
                self.canvas.delete(item)
        self.marker_ids[self.active].clear()
        self._refresh_sample_line()
        self._update_status_cursor()

    def _on_key(self, event: tk.Event) -> None:
        key = event.keysym.lower()
        if key in INK_KEYS:
            self.active = INK_KEYS[key]
            self.ink_var.set(self.active)
            self._update_status_cursor()
        elif key == "u":
            self.undo()
        elif key == "c":
            self.clear_ink()
        elif key == "e":
            self.export()
        elif key == "q":
            self.root.destroy()

    def export(self) -> None:
        incomplete = [
            n for n in COLOR_NAMES if len(self.points[n]) < self.points_per_ink
        ]
        if incomplete:
            self.status.set(
                f"Need {self.points_per_ink} points each; short: {', '.join(incomplete)}"
            )
            return
        raw = []
        swatches = []
        for name in COLOR_NAMES:
            agg = _aggregate_points(self.points[name])
            assert agg is not None
            raw.append(np.array(agg["p2_rgb_raw"], dtype=np.float64))
        tonal = normalize_p2_tonal(raw)
        for i, name in enumerate(COLOR_NAMES):
            agg = _aggregate_points(self.points[name])
            assert agg is not None
            p2 = tonal[i].astype(int).tolist()
            swatches.append(
                {
                    "color": name,
                    "p1_rgb": list(P1_PRIMARY[name]),
                    "p2_rgb_raw": agg["p2_rgb_raw"],
                    "p2_rgb": p2,
                    "dither": agg["dither"],
                    "points": agg["points"],
                }
            )
        payload = {
            "photo": str(self.photo),
            "mode": self.mode,
            "method": "click",
            "points_per_ink": self.points_per_ink,
            "patch_frac": self.patch_frac,
            "swatches": swatches,
            "palette_display": [s["p2_rgb"] for s in swatches],
        }
        out = self.output or self.photo.with_suffix(".p2-click.json")
        out.write_text(json.dumps(payload, indent=2) + "\n")
        self.status.set(f"Wrote {out}")
        print(f"Wrote {out}")
        print("// PaletteInkJoyDisplay")
        for name, s in zip(COLOR_NAMES, swatches):
            r, g, b = s["p2_rgb"]
            print(f"  {{{r}, {g}, {b}}}, // {name}")

    def run(self) -> None:
        self.root.mainloop()


def main() -> None:
    ap = argparse.ArgumentParser(description="Click P2 calibration with loupe and 6-point average")
    ap.add_argument("photo", type=Path)
    ap.add_argument(
        "--layout",
        choices=("inkjoy-primaries", "samsung-primaries", "inkjoy", "2560x1440"),
        default="inkjoy-primaries",
    )
    ap.add_argument("--frame", choices=("auto", "white", "wood"), default="wood")
    ap.add_argument("--orientation", choices=("auto", "landscape", "portrait"), default="auto")
    ap.add_argument("--keystone", action=argparse.BooleanOptionalAction, default=True)
    ap.add_argument("--patch", type=float, default=0.012)
    ap.add_argument("--points", type=int, default=6, help="clicks per ink to average (default 6)")
    ap.add_argument("-o", "--output", type=Path)
    args = ap.parse_args()

    img, mode = _load_image(
        args.photo,
        layout=args.layout,
        frame=args.frame,
        orientation=args.orientation,
        keystone=args.keystone,
    )
    print(f"{args.photo.name}  {img.shape[1]}×{img.shape[0]}  {mode}  {args.points} pts/ink")
    ClickCalibratorApp(
        img,
        photo=args.photo.resolve(),
        output=args.output,
        patch_frac=args.patch,
        mode=mode,
        points_per_ink=max(1, args.points),
    ).run()


if __name__ == "__main__":
    main()
