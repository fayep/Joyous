#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy"]
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

W, H = 1600, 1200

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


def make_clock_wipe_lo(rows=H, cols=W) -> np.ndarray:
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
           crop_bottom: bool = False, portrait: bool = False):
    img = Image.open(img_path).convert('RGB')
    if crop_bottom:
        iw, ih = img.size
        if portrait:
            # Portrait frame: crop ratio is H:W = 1200:1600 = 3:4 (tall)
            crop_h = round(iw * W / H)  # iw * (4/3) for portrait 3:4 source
        else:
            # Landscape frame: crop ratio is W:H = 1600:1200 = 4:3 (wide)
            crop_h = round(iw * H / W)  # iw * (3/4)
        crop_h = min(crop_h, ih)
        top = ih - crop_h
        img = img.crop((0, top, iw, ih))
        mode = "portrait 3:4" if portrait else "landscape 4:3"
        print(f"Cropped bottom {mode}: ({0}, {top}, {iw}, {ih}) → {img.size}")
    if portrait:
        # Frame display maps bin X→display Y (portrait rotation).
        # Pre-rotate 90° CW so content appears upright on portrait display.
        # If image appears upside-down on frame, use --rotate-ccw instead.
        img = img.transpose(Image.ROTATE_90)
        print(f"Rotated 90° CCW for portrait display → {img.size}")
    img = img.resize((W, H), Image.LANCZOS)

    # The bin is stored bottom-to-top. ISFR-lite processes top-to-bottom.
    # We dither top-to-bottom then flip when writing.
    img_np = np.array(img, dtype=np.float64)  # (H, W, 3), top-to-bottom

    print(f"Dithering {W}×{H} image with Stucki in RGB space...", flush=True)
    indices = stucki_dither(img_np)  # (H, W) palette indices 0-5

    if lo_template:
        print(f"Loading lo-byte from {lo_template}...")
        lo = load_lo_template(lo_template)  # (H, W), top-to-bottom display order
    else:
        print("Generating clock wipe lo-byte...")
        lo = make_clock_wipe_lo()  # (H, W), top-to-bottom display order

    # Build bin: bottom-to-top row order
    # hi byte = HI_BYTES[palette_index]
    hi = np.array([[HI_BYTES[i] for i in row] for row in indices], dtype=np.uint8)

    # Interleave hi and lo: each pixel is (hi, lo)
    # Rows stored bottom-to-top: flip both arrays
    hi_flip = hi[::-1, :]   # (H, W)
    lo_flip = lo[::-1, :]   # (H, W)

    out = np.stack([hi_flip, lo_flip], axis=2).reshape(-1)  # (H*W*2,)

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
                        help="Frame is in portrait mode: use 3:4 crop and pre-rotate 90° CW")
    args = parser.parse_args()
    encode(args.input, args.output, args.lo_template, args.crop_bottom, args.portrait)


if __name__ == "__main__":
    main()
