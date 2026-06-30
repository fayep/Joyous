"""Parameterized calibration chart layouts (primaries + color guesses)."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

import numpy as np
from PIL import Image, ImageDraw, ImageFont

from layouts.guess_data import GUESSES as GUESS_SWATCHES
from layouts.guess_data import ROW_ORDER
from palettes import COLOR_NAMES, PALETTE_INKJOY_SEND, PALETTE_SAMSUNG_SEND

PaletteName = Literal["inkjoy", "samsung"]
ChartKind = Literal["primaries", "guesses"]

INKJOY_SIZE = (1600, 1200)
SAMSUNG_SIZE = (2560, 1440)

LAYOUT_ALIASES: dict[str, tuple[ChartKind, tuple[int, int], PaletteName]] = {
    "inkjoy-primaries": ("primaries", INKJOY_SIZE, "inkjoy"),
    "samsung-primaries": ("primaries", SAMSUNG_SIZE, "samsung"),
    "inkjoy": ("guesses", INKJOY_SIZE, "inkjoy"),
    "2560x1440": ("guesses", SAMSUNG_SIZE, "samsung"),
}

HI_BYTES = [0x01, 0x02, 0x03, 0x04, 0x06, 0x07]


@dataclass(frozen=True)
class ChartLayout:
    """Module-like layout descriptor consumed by calibrate_p2_from_photo."""

    WIDTH: int
    HEIGHT: int
    LABEL_W: int
    HEADER_H: int
    GUESSES: dict[str, list[tuple[str, int, int, int]]]
    ROW_ORDER: list[str]
    kind: ChartKind
    palette: PaletteName

    @property
    def size_tag(self) -> str:
        return f"{self.WIDTH}x{self.HEIGHT}"


def parse_size(size: str) -> tuple[int, int]:
    w, h = size.lower().split("x", 1)
    width, height = int(w), int(h)
    if width < 320 or height < 240:
        raise ValueError(f"chart size too small: {size}")
    return width, height


def scaled_margins(width: int, height: int) -> tuple[int, int]:
    """Label column and header band scaled from InkJoy 1600×1200 reference."""
    label_w = max(120, round(width * 160 / 1600))
    header_h = max(36, round(height * 44 / 1200))
    return label_w, header_h


def scaled_fonts(width: int) -> tuple:
    scale = width / 1600.0
    try:
        title = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", max(14, round(22 * scale)))
        label = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", max(12, round(18 * scale)))
        tiny = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", max(10, round(11 * scale)))
        return title, label, tiny
    except OSError:
        d = ImageFont.load_default()
        return d, d, d


def palette_send(name: PaletteName) -> np.ndarray:
    if name == "inkjoy":
        return PALETTE_INKJOY_SEND
    return PALETTE_SAMSUNG_SEND


def primaries_guesses(send: np.ndarray) -> dict[str, list[tuple[str, int, int, int]]]:
    out: dict[str, list[tuple[str, int, int, int]]] = {}
    for i, name in enumerate(COLOR_NAMES):
        r, g, b = int(send[i, 0]), int(send[i, 1]), int(send[i, 2])
        hex_rgb = f"#{r:02X}{g:02X}{b:02X}"
        out[name] = [(hex_rgb, r, g, b)]
    return out


def build_layout(
    kind: ChartKind,
    width: int,
    height: int,
    palette: PaletteName = "inkjoy",
) -> ChartLayout:
    label_w, header_h = scaled_margins(width, height)
    if kind == "primaries":
        guesses = primaries_guesses(palette_send(palette))
    else:
        guesses = GUESS_SWATCHES
    return ChartLayout(
        WIDTH=width,
        HEIGHT=height,
        LABEL_W=label_w,
        HEADER_H=header_h,
        GUESSES=guesses,
        ROW_ORDER=list(ROW_ORDER),
        kind=kind,
        palette=palette,
    )


def resolve_layout(
    layout: str,
    *,
    width: int | None = None,
    height: int | None = None,
    palette: PaletteName | None = None,
) -> ChartLayout:
    if layout in LAYOUT_ALIASES:
        kind, default_size, default_palette = LAYOUT_ALIASES[layout]
        w = width if width is not None else default_size[0]
        h = height if height is not None else default_size[1]
        pal = palette if palette is not None else default_palette
        return build_layout(kind, w, h, pal)
    if "x" in layout and layout[0].isdigit():
        kind: ChartKind = "guesses"
        w, h = parse_size(layout)
        pal: PaletteName = palette or "samsung"
        if width is not None:
            w = width
        if height is not None:
            h = height
        return build_layout(kind, w, h, pal)
    raise ValueError(f"unknown layout {layout!r}")


def render_primaries(layout: ChartLayout, *, title: str | None = None) -> Image.Image:
    title_font, label_font, _ = scaled_fonts(layout.WIDTH)
    img = Image.new("RGB", (layout.WIDTH, layout.HEIGHT), (24, 24, 24))
    draw = ImageDraw.Draw(img)
    n_rows = len(layout.ROW_ORDER)
    body_h = layout.HEIGHT - layout.HEADER_H
    band_h = body_h // n_rows
    pad = max(2, layout.WIDTH // 800)

    draw.rectangle([0, 0, layout.WIDTH, layout.HEADER_H], fill=(12, 12, 12))
    draw.text(
        (pad, pad),
        title or f"P1 primaries — flat RGB ({layout.size_tag})",
        fill=(230, 230, 230),
        font=title_font,
    )

    for row_i, family in enumerate(layout.ROW_ORDER):
        name, r, g, b = layout.GUESSES[family][0]
        y0 = layout.HEADER_H + row_i * band_h
        y1 = layout.HEADER_H + (row_i + 1) * band_h
        draw.rectangle([0, y0, layout.LABEL_W - pad, y1], fill=(36, 36, 36))
        draw.text((pad, y0 + pad), family, fill=(240, 240, 240), font=label_font)
        draw.text((pad, y0 + pad + 20), name, fill=(200, 200, 200), font=label_font)
        draw.rectangle([layout.LABEL_W, y0 + pad, layout.WIDTH - pad, y1 - pad], fill=(r, g, b))

    return img


def render_guesses(layout: ChartLayout, *, title: str | None = None) -> Image.Image:
    title_font, label_font, tiny_font = scaled_fonts(layout.WIDTH)
    img = Image.new("RGB", (layout.WIDTH, layout.HEIGHT), (28, 28, 28))
    draw = ImageDraw.Draw(img)
    n_rows = len(layout.ROW_ORDER)
    body_h = layout.HEIGHT - layout.HEADER_H
    band_h = body_h // n_rows
    pad = max(4, layout.WIDTH // 800)

    draw.rectangle([0, 0, layout.WIDTH, layout.HEADER_H], fill=(16, 16, 16))
    draw.text(
        (pad, pad),
        title or f"Color guesses — flat RGB ({layout.size_tag})",
        fill=(230, 230, 230),
        font=title_font,
    )

    for row_i, family in enumerate(layout.ROW_ORDER):
        guesses = layout.GUESSES[family]
        y0 = layout.HEADER_H + row_i * band_h
        y1 = layout.HEADER_H + (row_i + 1) * band_h
        swatch_top = y0 + pad
        bar_area_w = layout.WIDTH - layout.LABEL_W
        bar_w = max(1, bar_area_w // len(guesses))

        draw.rectangle([0, y0, layout.LABEL_W - pad, y1], fill=(40, 40, 40))
        draw.text((pad, y0 + pad), family, fill=(240, 240, 240), font=label_font)
        draw.text((pad, y0 + pad + 18), f"{len(guesses)}", fill=(160, 160, 160), font=tiny_font)

        for i, (name, r, g, b) in enumerate(guesses):
            x0 = layout.LABEL_W + i * bar_w
            x1 = x0 + bar_w - 2
            draw.rectangle([x0, swatch_top, x1, y1 - pad], fill=(r, g, b))
            if layout.WIDTH >= 2000:
                hex_rgb = f"#{r:02X}{g:02X}{b:02X}"
                lum = 0.299 * r + 0.587 * g + 0.114 * b
                fg = (0, 0, 0) if lum > 130 else (255, 255, 255)
                draw.text((x0 + pad, swatch_top + pad), name, fill=fg, font=label_font)
                draw.text((x0 + pad, swatch_top + pad + 16), hex_rgb, fill=fg, font=tiny_font)
            elif i == 0:
                hex_rgb = f"#{r:02X}{g:02X}{b:02X}"
                draw.text((pad, y0 + pad + 36), hex_rgb, fill=(180, 180, 180), font=tiny_font)

    return img


def render_chart(layout: ChartLayout, *, title: str | None = None) -> Image.Image:
    if layout.kind == "primaries":
        return render_primaries(layout, title=title)
    return render_guesses(layout, title=title)


def nearest_color(rgb: np.ndarray, palette: np.ndarray) -> int:
    return int(np.argmin(np.sum((palette - rgb) ** 2, axis=1)))


def load_box_wipe(cols: int, rows: int) -> np.ndarray:
    from pathlib import Path

    wipe_path = Path(__file__).resolve().parent.parent.parent / "joyous-hub" / "wipes" / "wipe_box.png"
    if wipe_path.is_file():
        gray = np.array(Image.open(wipe_path).convert("L"), dtype=np.uint8)
        if gray.shape == (rows, cols):
            return gray
    raise FileNotFoundError(f"missing or wrong-size box wipe: {wipe_path}")


def flat_snap_bin(img: Image.Image, palette: np.ndarray) -> bytes:
    arr = np.array(img.convert("RGB"), dtype=np.float64)
    h, w, _ = arr.shape
    hi = np.zeros((h, w), dtype=np.uint8)
    for y in range(h):
        for x in range(w):
            hi[y, x] = HI_BYTES[nearest_color(arr[y, x], palette)]
    lo = load_box_wipe(w, h)
    return np.stack([hi[::-1], lo[::-1]], axis=2).reshape(-1).tobytes()
