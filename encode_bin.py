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
import os
import struct
import sys
import math
import numpy as np
from PIL import Image

# Palette order: black, white, yellow, red, blue, green
# InkJoy: muted physical pigment colors (reverse-engineered from ISFR-lite.exe BGR values)
PALETTE_INKJOY = np.array([
    [ 30,  30,  30],   # black
    [149, 162, 165],   # white
    [166, 165,  17],   # yellow
    [121,  23,  17],   # red
    [  0,  76, 136],   # blue
    [ 46,  91,  65],   # green
], dtype=np.float64)

# Samsung EM32DX: sRGB monitor equivalents from official spec sheet
PALETTE_SAMSUNG = np.array([
    [  0,   0,   0],   # black  #000000
    [255, 255, 255],   # white  #FFFFFF
    [255, 235,   0],   # yellow #FFEB00
    [154,   0,   0],   # red    #9A0000
    [  0,  36, 154],   # blue   #00249A
    [ 20,  85,  16],   # green  #145510
], dtype=np.float64)

# Reflection Frame: Spectra 6 palette from LUT header (BGR→RGB)
# Sourced from Spectra6_Render_LUT_Default_v2.bin embedded in com.feibi.frame APK.
# These are E Ink spec values — display may appear more muted in practice.
PALETTE_REFLECTION = np.array([
    [  8,   0,   0],   # black
    [239, 255, 255],   # white
    [255, 215,   0],   # yellow
    [134,   0,   0],   # red
    [  0,  28, 138],   # blue
    [ 20,  93,  20],   # green
], dtype=np.float64)

PALETTES = {
    'inkjoy':     PALETTE_INKJOY,
    'samsung':    PALETTE_SAMSUNG,
    'reflection': PALETTE_REFLECTION,
}

HI_BYTES = [0x01, 0x02, 0x03, 0x04, 0x06, 0x07]

# Native PNG format: paletted (mode P), 192 entries used (6 colors × 31 wipe steps).
# Palette index = hi_index * 32 + lo // 8
# Palette RGB: R=hi_byte, G=lo_byte, B=0
_HI_TO_IDX = {v: i for i, v in enumerate(HI_BYTES)}
_NATIVE_PAL = np.zeros((256, 3), dtype=np.uint8)
for _hi_i, _hi_v in enumerate(HI_BYTES):
    for _lo_s in range(31):
        _NATIVE_PAL[_hi_i * 32 + _lo_s] = [_hi_v, _lo_s * 8, 0]


def hi_lo_to_native(hi: np.ndarray, lo: np.ndarray) -> "Image.Image":
    """Encode hi/lo byte arrays (top-to-bottom) as a paletted PNG image."""
    hi_idx = np.vectorize(_HI_TO_IDX.get)(hi).astype(np.uint8)
    idx = (hi_idx * 32 + lo // 8).astype(np.uint8)
    img = Image.frombytes('P', (hi.shape[1], hi.shape[0]), idx.tobytes())
    img.putpalette(_NATIVE_PAL.flatten().tolist())
    return img


def native_to_hi_lo(img: "Image.Image"):
    """Decode a native paletted PNG back to (hi, lo) byte arrays (top-to-bottom)."""
    idx = np.array(img)
    pal = np.array(img.getpalette(), dtype=np.uint8).reshape(256, 3)
    return pal[idx, 0], pal[idx, 1]

# InkJoy frame (default)
W, H = 1600, 1200

# Known target resolutions
RESOLUTIONS = {
    'inkjoy':     (1600, 1200),  # InkJoy portrait frame
    'samsung':    (2560, 1440),  # Samsung EM32DX
    'reflection': (1600, 1200),  # Reflection Frame (same res as InkJoy)
}

# Stucki kernel weights (A=8/42, B=4/42, C=2/42, D=1/42)
A, B, C, D = 8/42, 4/42, 2/42, 1/42

# ── LAB enhancement ───────────────────────────────────────────────────────────
# Approximates the ISFR server's pre-dither colour pipeline: chroma boost in
# LAB space (pulls muted colours away from white/grey) plus an optional
# highlight rolloff S-curve (compresses near-white L values so bright pixels
# are more likely to be assigned a saturated colour rather than white ink).

_XYZ_D65 = np.array([0.95047, 1.00000, 1.08883])
_RGB_TO_XYZ = np.array([[0.4124564, 0.3575761, 0.1804375],
                         [0.2126729, 0.7151522, 0.0721750],
                         [0.0193339, 0.1191920, 0.9503041]])
_XYZ_TO_RGB = np.array([[ 3.2404542, -1.5371385, -0.4985314],
                         [-0.9692660,  1.8760108,  0.0415560],
                         [ 0.0556434, -0.2040259,  1.0572252]])

def _srgb_to_lab(rgb01: np.ndarray) -> np.ndarray:
    lin = np.where(rgb01 <= 0.04045, rgb01 / 12.92, ((rgb01 + 0.055) / 1.055) ** 2.4)
    xyz = lin @ _RGB_TO_XYZ.T / _XYZ_D65
    f = np.where(xyz > 0.008856, xyz ** (1/3), 7.787 * xyz + 16/116)
    L = 116 * f[..., 1] - 16
    a = 500 * (f[..., 0] - f[..., 1])
    b = 200 * (f[..., 1] - f[..., 2])
    return np.stack([L, a, b], axis=-1)

def _lab_to_srgb(lab: np.ndarray) -> np.ndarray:
    L, a, b = lab[..., 0], lab[..., 1], lab[..., 2]
    fy = (L + 16) / 116
    fx = a / 500 + fy
    fz = fy - b / 200
    x = np.where(fx > 0.206897, fx**3, (fx - 16/116) / 7.787)
    y = np.where(fy > 0.206897, fy**3, (fy - 16/116) / 7.787)
    z = np.where(fz > 0.206897, fz**3, (fz - 16/116) / 7.787)
    lin = np.stack([x, y, z], axis=-1) * _XYZ_D65 @ _XYZ_TO_RGB.T
    lin = np.clip(lin, 0, 1)
    srgb = np.where(lin <= 0.0031308, 12.92 * lin, 1.055 * lin ** (1/2.4) - 0.055)
    return np.clip(srgb, 0, 1)

def lab_enhance(img_np: np.ndarray, strength: float = 1.0) -> np.ndarray:
    """
    Pre-dither LAB enhancement approximating ISFR server processing.
    strength=1.0 targets the observed server colour balance (less white, more red).
      - Chroma boost: a*/b* × (1 + 0.3 × strength)
      - Highlight rolloff: smoothstep S-curve compresses L > 75 by up to 20 × strength
    img_np: float64 [0, 255] — returns float64 [0, 255].
    """
    lab = _srgb_to_lab(np.clip(img_np, 0, 255) / 255.0)

    # Chroma boost — pulls near-white/grey pixels toward their dominant hue
    chroma = 1.0 + 0.3 * strength
    lab[..., 1] *= chroma
    lab[..., 2] *= chroma

    # Highlight rolloff — compresses L≥75 so near-whites dither toward colours
    L = lab[..., 0]
    t = np.clip((L - 75.0) / 25.0, 0.0, 1.0)
    rolloff = t * t * (3.0 - 2.0 * t)  # smoothstep
    lab[..., 0] = L - rolloff * 20.0 * strength

    return _lab_to_srgb(lab) * 255.0


def apply_lut(img_np: np.ndarray, lut_path: str) -> np.ndarray:
    """Apply a 64³ 3D LUT (Spectra6_Render_LUT_Default_v2.bin format) to img_np.
    LUT file: 18-byte BGR palette header + 64³×3 bytes indexed [R,G,B] → BGR output.
    img_np: float64 [0,255] → returns float64 [0,255].
    """
    data = open(lut_path, 'rb').read()
    lut = np.frombuffer(data[18:], dtype=np.uint8).reshape(64, 64, 64, 3)
    idx = np.clip((img_np / 255.0 * 63 + 0.5), 0, 63).astype(np.uint8)
    bgr = lut[idx[..., 0], idx[..., 1], idx[..., 2]]   # [R,G,B] → BGR
    return bgr[..., ::-1].astype(np.float64)             # BGR → RGB


def nearest_color(rgb: np.ndarray, palette: np.ndarray) -> int:
    """Euclidean RGB nearest color — matches sub_140001850."""
    dists = np.sum((palette - rgb) ** 2, axis=1)
    return int(np.argmin(dists))


def stucki_dither(img_rgb: np.ndarray, palette: np.ndarray) -> np.ndarray:
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
    buf = np.zeros((rows + 4, cols + 4, 3), dtype=np.float64)
    buf[2:rows+2, 2:cols+2] = img_rgb
    out = np.zeros((rows, cols), dtype=np.uint8)

    for y in range(rows):
        brow = y + 2
        bcol_base = 2

        for x in range(cols):
            bc = bcol_base + x
            pixel = np.clip(buf[brow, bc], 0, 255)
            idx = nearest_color(pixel, palette)
            out[y, x] = idx
            err = pixel - palette[idx]

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
           target: str = 'inkjoy', native_path: str | None = None,
           enhance: float | None = None, lut_path: str | None = None,
           palette_override: str | None = None):
    tw, th = RESOLUTIONS.get(target, RESOLUTIONS['inkjoy'])
    palette_name = palette_override or target
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

    palette = PALETTES.get(palette_name, PALETTE_INKJOY)
    if palette_override:
        print(f"Palette override: {palette_override} (resolution from --target {target})")

    img_np = np.array(img, dtype=np.float64)
    if gamma != 1.0:
        img_np = 255.0 * np.power(img_np / 255.0, gamma)
        print(f"Applied gamma {gamma} (highlights {'reduced' if gamma > 1 else 'boosted'})")
    if enhance is not None:
        img_np = lab_enhance(img_np, enhance)
        print(f"Applied LAB enhance (strength={enhance}: chroma ×{1+0.3*enhance:.2f}, highlight rolloff ×{enhance})")
    if lut_path is not None:
        img_np = apply_lut(img_np, lut_path)
        print(f"Applied 3D LUT: {lut_path}")

    print(f"Dithering {tw}×{th} image with Stucki in RGB space...", flush=True)
    indices = stucki_dither(img_np, palette)

    if lo_template:
        print(f"Loading lo-byte from {lo_template}...")
        lo = load_lo_template(lo_template)
    else:
        print("Generating clock wipe lo-byte...")
        lo = make_clock_wipe_lo(tw, th)

    hi = np.array([[HI_BYTES[i] for i in row] for row in indices], dtype=np.uint8)

    is_png = out_path.lower().endswith('.png')

    if native_path:
        hi_lo_to_native(hi, lo).save(native_path, format='PNG', optimize=True)
        print(f"Written native PNG to {native_path} ({os.path.getsize(native_path)} bytes)")

    if is_png:
        # Save RGB preview using the palette so the image is actually visible
        pal_arr = palette.astype(np.uint8)
        rgb = pal_arr[indices]
        Image.fromarray(rgb, 'RGB').save(out_path, format='PNG')
        print(f"Written preview PNG to {out_path} ({os.path.getsize(out_path)} bytes)")
    else:
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


def compose(dithered_path: str, transition_path: str, out_path: str, target: str = 'inkjoy'):
    """Combine a pre-dithered RGB PNG + greyscale transition PNG into a .bin."""
    palette = PALETTES.get(target, PALETTE_INKJOY)

    dithered = np.array(Image.open(dithered_path).convert('RGB'), dtype=np.float64)
    h, w = dithered.shape[:2]

    # Map each pixel to nearest palette color → palette index → HI_BYTE
    flat = dithered.reshape(-1, 3)
    dists = np.sum((flat[:, None, :] - palette[None, :, :]) ** 2, axis=2)
    indices = np.argmin(dists, axis=1).reshape(h, w).astype(np.uint8)
    hi = np.vectorize(HI_BYTES.__getitem__)(indices).astype(np.uint8)

    # Load transition greyscale — values stored directly as lo bytes (0-248, no scaling)
    lo = np.array(Image.open(transition_path).convert('L'), dtype=np.uint8)

    out = np.stack([hi[::-1], lo[::-1]], axis=2).reshape(-1)
    with open(out_path, 'wb') as f:
        f.write(bytes(out))

    unique, counts = np.unique(indices, return_counts=True)
    labels = ['black', 'white', 'yellow', 'red', 'blue', 'green']
    print(f"Written {len(out)} bytes to {out_path}")
    print("\nColor balance:")
    for u, c in zip(unique, counts):
        print(f"  {labels[u]:6s}: {c:7d} ({c/h/w:.1%})")


def main():
    parser = argparse.ArgumentParser(description="Encode image to InkJoy .bin")
    parser.add_argument("input", nargs='?', help="Input image path")
    parser.add_argument("output", nargs='?', help="Output .bin path")
    parser.add_argument("--lo-template", help="Reference .bin to copy lo-byte wipe pattern from")
    parser.add_argument("--crop-bottom", action="store_true",
                        help="Crop the bottom portion before encoding (aspect matches --portrait)")
    parser.add_argument("--portrait", action="store_true",
                        help="Frame is in portrait mode: use 3:4 crop and pre-rotate 90° CCW")
    parser.add_argument("--gamma", type=float, default=1.0,
                        help="Gamma correction before dithering (>1 reduces highlights, e.g. 1.3)")
    parser.add_argument("--target", choices=list(RESOLUTIONS.keys()), default='inkjoy',
                        help="Target display (affects resolution and output format)")
    parser.add_argument("--palette", choices=list(PALETTES.keys()),
                        help="Override dither palette independently of --target (default: matches --target)")
    parser.add_argument("--save-native", metavar="PATH",
                        help="Also save a compact lossless native PNG (R=hi byte, G=lo byte in palette)")
    parser.add_argument("--enhance", nargs="?", const=1.0, type=float, metavar="STRENGTH",
                        help="LAB chroma boost + highlight rolloff before dithering (default strength 1.0)")
    parser.add_argument("--lut", metavar="PATH",
                        help="Apply a 64³ 3D LUT (Spectra6_Render_LUT_Default_v2.bin format) before dithering")
    parser.add_argument("--compose", nargs=2, metavar=("DITHERED_PNG", "TRANSITION_PNG"),
                        help="Combine pre-dithered RGB PNG + greyscale transition PNG into .bin")
    args = parser.parse_args()

    if args.compose:
        out = args.output or args.input
        if not out:
            parser.error("output path required with --compose")
        compose(args.compose[0], args.compose[1], out, args.target)
    elif args.input and args.output:
        encode(args.input, args.output, args.lo_template, args.crop_bottom, args.portrait,
               args.gamma, args.target, args.save_native, args.enhance, args.lut, args.palette)
    else:
        parser.print_help()


if __name__ == "__main__":
    main()
