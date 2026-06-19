#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy", "pillow-heif"]
# ///
"""
InkJoy local bin encoder — reverse-engineered from ISFR-lite.exe.

Algorithm (from Binary Ninja static analysis of sub_140002af0):
  - Palette: 6 ink colors (hardcoded BGR values from sub_1400017b0)
  - Dithering: Stucki error diffusion in RGB space
  - Weights: A=8/42, B=4/42, C=2/42, D=1/42 (kernel below)
      . . * A B
      C B A B C
      D C B C D
  - Nearest color: Euclidean RGB distance (sub_140001850)

Bin file format (1600×1200, bottom-to-top row order):
  - 2 bytes per pixel: hi=color index, lo=wipe order
  - Color indices: 0x01=black 0x02=white 0x03=yellow 0x04=red 0x06=blue 0x07=green

Usage:
    uv run encode_bin.py input.jpg output.bin [--lo-template reference.bin]
"""

import pillow_heif
pillow_heif.register_heif_opener()

import argparse
import struct
import sys
import math
import numpy as np
from PIL import Image

# ISFR-lite internal palette (BGR from sub_1400017b0 → RGB for our use)
# Palette order matches bin hi-byte values:
#   index 0 → hi=0x01 (black)
#   index 1 → hi=0x02 (white)
#   index 2 → hi=0x03 (yellow)
#   index 3 → hi=0x04 (red)
#   index 4 → hi=0x06 (blue)
#   index 5 → hi=0x07 (green)
PALETTE_RGB = np.array([
    [ 30,  30,  30],   # 0x01 black
    [149, 162, 165],   # 0x02 white
    [166, 165,  17],   # 0x03 yellow
    [121,  23,  17],   # 0x04 red
    [  0,  76, 136],   # 0x06 blue
    [ 46,  91,  65],   # 0x07 green
], dtype=np.float64)

HI_BYTES = [0x01, 0x02, 0x03, 0x04, 0x06, 0x07]

# InkJoy frame (default)
W, H = 1600, 1200

# Known target resolutions
RESOLUTIONS = {
    'inkjoy':  (1600, 1200),  # InkJoy portrait frame
    'samsung': (2560, 1440),  # Samsung EM32DX
}

# Stucki kernel weights (A=8/42, B=4/42, C=2/42, D=1/42)
A, B, C, D = 8/42, 4/42, 2/42, 1/42


def nearest_color(rgb: np.ndarray) -> int:
    """Euclidean RGB nearest color — matches sub_140001850."""
    dists = np.sum((PALETTE_RGB - rgb) ** 2, axis=1)
    return int(np.argmin(dists))


def stucki_dither(img_rgb: np.ndarray) -> np.ndarray:
    """
    Stucki error diffusion in RGB float space.
    img_rgb: (H, W, 3) float64 in [0, 255]
    Returns: (H, W) uint8 array of palette indices 0-5.

    Kernel (from ISFR-lite sub_140002af0, weights A=8/42 B=4/42 C=2/42 D=1/42):
        . . * A B
        C B A B C
        D C B C D
    """
    rows, cols = img_rgb.shape[:2]
    # Float64 buffer with 2-row lookahead padding
    buf = np.zeros((rows + 4, cols + 4, 3), dtype=np.float64)
    buf[2:rows+2, 2:cols+2] = img_rgb  # offset by (2 rows, 2 cols) for boundary safety
    out = np.zeros((rows, cols), dtype=np.uint8)

    for y in range(rows):
        brow = y + 2   # buf row index for current image row
        bcol_base = 2  # buf col offset for image col 0

        for x in range(cols):
            bc = bcol_base + x
            pixel = np.clip(buf[brow, bc], 0, 255)
            idx = nearest_color(pixel)
            out[y, x] = idx
            err = pixel - PALETTE_RGB[idx]

            # Row 0
            buf[brow,   bc+1] += err * A
            buf[brow,   bc+2] += err * B
            # Row +1
            buf[brow+1, bc-2] += err * C
            buf[brow+1, bc-1] += err * B
            buf[brow+1, bc  ] += err * A
            buf[brow+1, bc+1] += err * B
            buf[brow+1, bc+2] += err * C
            # Row +2
            buf[brow+2, bc-2] += err * D
            buf[brow+2, bc-1] += err * C
            buf[brow+2, bc  ] += err * B
            buf[brow+2, bc+1] += err * C
            buf[brow+2, bc+2] += err * D

    return out


def make_clock_wipe_lo(cols=1600, rows=1200) -> np.ndarray:
    """
    Generate the lo-byte clock wipe pattern (31 quantized steps, 0-248).
    Square clock wipe: pixels sweep outward from center in clockwise order,
    quantized into 31 uniform steps (matching newer server bins).
    """
    cy, cx = (rows - 1) / 2.0, (cols - 1) / 2.0
    lo = np.zeros((rows, cols), dtype=np.float64)

    ys, xs = np.mgrid[0:rows, 0:cols]
    dy = ys - cy
    dx = xs - cx

    # Angle: 0 at top, clockwise. atan2 gives angle from -pi to pi,
    # with 0 at right. We rotate so 0 = top and go clockwise.
    angle = (np.arctan2(dx, -dy) + 2 * math.pi) % (2 * math.pi)  # 0..2pi, 0=top

    # Normalize distance: max distance to any corner
    max_d = math.sqrt(cy**2 + cx**2)
    dist = np.sqrt(dy**2 + dx**2) / max_d

    # Combine angle (0..1) and distance (0..1) for wipe order.
    # The "square clock" means we sweep angle first, then distance.
    # From observation: values near 0 are at the start of sweep (center),
    # near 255 at the end. Outer pixels come later per angle sector.
    order = (angle / (2 * math.pi) + dist * 0.01) % 1.0

    # Quantize to 31 steps matching newer server bins (0, 8, 16, ..., 248)
    steps = 31
    lo = np.floor(order * steps).astype(np.uint8) * 8
    return lo.astype(np.uint8)


def load_lo_template(path: str) -> np.ndarray:
    """Load the lo byte from an existing reference bin."""
    data = open(path, 'rb').read()
    size = W * H * 2
    assert len(data) == size, f"Expected {size} bytes, got {len(data)}"
    arr = np.frombuffer(data, dtype=np.uint8).reshape(H, W, 2)
    # Bin rows are bottom-to-top; return in display order (top row first)
    return arr[::-1, :, 1]


def encode(img_path: str, out_path: str, lo_template: str | None = None,
           crop_bottom: bool = False, portrait: bool = False, gamma: float = 1.0,
           target: str = 'inkjoy'):
    tw, th = RESOLUTIONS.get(target, RESOLUTIONS['inkjoy'])
    img = Image.open(img_path).convert('RGB')
    if crop_bottom:
        iw, ih = img.size
        if portrait:
            crop_h = round(iw * tw / th)  # portrait: source height = iw * (tw/th)
        else:
            crop_h = round(iw * th / tw)  # landscape: source height = iw * (th/tw)
        crop_h = min(crop_h, ih)
        top = ih - crop_h
        img = img.crop((0, top, iw, ih))
        ratio = f"{'portrait' if portrait else 'landscape'} {tw}:{th}"
        print(f"Cropped bottom {ratio}: ({0}, {top}, {iw}, {ih}) → {img.size}")
    if portrait:
        img = img.transpose(Image.ROTATE_90)
        print(f"Rotated 90° CCW for portrait display → {img.size}")
    img = img.resize((tw, th), Image.LANCZOS)

    img_np = np.array(img, dtype=np.float64)
    if gamma != 1.0:
        img_np = 255.0 * np.power(img_np / 255.0, gamma)
        print(f"Applied gamma {gamma} (highlights {'reduced' if gamma > 1 else 'boosted'})")

    print(f"Dithering {tw}×{th} image with Stucki in RGB space...", flush=True)
    indices = stucki_dither(img_np)

    is_png = out_path.lower().endswith('.png')

    if is_png:
        # Map palette indices back to exact RGB values and save as PNG.
        # Every pixel is an exact Spectra 6 color so the display's internal
        # quantizer passes them through unchanged.
        rgb_out = PALETTE_RGB[indices].astype(np.uint8)
        Image.fromarray(rgb_out, 'RGB').save(out_path)
        print(f"Written {tw}×{th} pre-dithered PNG to {out_path}")
    else:
        if lo_template:
            print(f"Loading lo-byte from {lo_template}...")
            lo = load_lo_template(lo_template)
        else:
            print("Generating clock wipe lo-byte...")
            lo = make_clock_wipe_lo(tw, th)

        hi = np.array([[HI_BYTES[i] for i in row] for row in indices], dtype=np.uint8)
        hi_flip = hi[::-1, :]
        lo_flip = lo[::-1, :]
        out = np.stack([hi_flip, lo_flip], axis=2).reshape(-1)

        with open(out_path, 'wb') as f:
            f.write(bytes(out))
        print(f"Written {len(out)} bytes to {out_path}")

    # Print color balance
    unique, counts = np.unique(indices, return_counts=True)
    total = indices.size
    labels = ['black','white','yellow','red','blue','green']
    print("\nColor balance:")
    for u, c in zip(unique, counts):
        print(f"  {labels[u]:6s}: {c:7d} ({100*c/total:.1f}%)")


def main():
    parser = argparse.ArgumentParser(description="Encode image to InkJoy .bin")
    parser.add_argument("input", help="Input image path")
    parser.add_argument("output", help="Output .bin path")
    parser.add_argument("--lo-template", help="Reference .bin to copy lo-byte wipe pattern from")
    parser.add_argument("--crop-bottom", action="store_true",
                        help="Crop the bottom portion before encoding (aspect matches --portrait)")
    parser.add_argument("--portrait", action="store_true",
                        help="Frame is in portrait mode: use 3:4 crop and pre-rotate 90° CCW")
    parser.add_argument("--gamma", type=float, default=1.0,
                        help="Gamma correction before dithering (>1 reduces highlights, e.g. 1.3)")
    parser.add_argument("--target", choices=list(RESOLUTIONS.keys()), default='inkjoy',
                        help="Target display (affects resolution and output format)")
    args = parser.parse_args()
    encode(args.input, args.output, args.lo_template, args.crop_bottom, args.portrait, args.gamma, args.target)


if __name__ == "__main__":
    main()
