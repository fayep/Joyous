#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["numpy", "Pillow", "pillow-heif"]
# ///
"""Tests for P2 photo calibration helpers."""

from __future__ import annotations

import numpy as np

from calibrate_p2_from_photo import (
    _ink_likelihood_rgb,
    _is_wood_rgb,
    detect_primaries_panel_bounds,
    normalize_p2_tonal,
    resolve_orientation,
    rgb_to_lab,
    sample_point_layout_oriented,
    warp_quad_to_rect,
)
from primaries_geometry import (
    _detect_columns_from_row,
    _ink_scores,
    analyze_primaries_portrait,
    scan_coherent_runs,
    sample_column_coherent,
)

GREEN_INDEX = 5
WHITE_INDEX = 1


def test_normalize_p2_tonal_neutralizes_grey_axis() -> None:
    # Shared warm cast on all inks; black/white should land near neutral after normalize.
    cast = np.array([18.0, 8.0, -4.0])  # Δa*, Δb* added to every swatch
    samples = []
    for base in [
        [40, 25, 15],
        [200, 205, 210],
        [200, 190, 40],
        [120, 10, 5],
        [10, 60, 120],
        [60, 90, 70],
    ]:
        lab = rgb_to_lab(np.array(base) / 255.0)
        lab[1:] += cast[1:]
        from calibrate_p2_from_photo import lab_to_rgb

        samples.append(np.clip(np.round(lab_to_rgb(lab) * 255), 0, 255))

    out = normalize_p2_tonal(samples)
    black_lab = rgb_to_lab(out[0] / 255.0)
    white_lab = rgb_to_lab(out[1] / 255.0)
    # Grey axis midpoint should be neutral after shared cast removal.
    assert abs(0.5 * (black_lab[1] + white_lab[1])) < 2.0
    assert abs(0.5 * (black_lab[2] + white_lab[2])) < 2.0
    # Chromatic inks keep separation from black along L*.
    assert rgb_to_lab(out[5] / 255.0)[0] > rgb_to_lab(out[0] / 255.0)[0] + 5


def test_warp_quad_to_rect_size() -> None:
    img = np.zeros((100, 80, 3), dtype=np.uint8)
    img[20:80, 15:65] = 128
    quad = ((15.0, 20.0), (65.0, 20.0), (15.0, 80.0), (65.0, 80.0))
    warped = warp_quad_to_rect(img, quad, 160, 120)
    assert warped.shape == (120, 160, 3)
    assert int(np.median(warped)) == 128


def test_resolve_orientation_auto() -> None:
    tall = np.zeros((1200, 800, 3), dtype=np.uint8)
    wide = np.zeros((800, 1200, 3), dtype=np.uint8)
    assert resolve_orientation(tall, "auto") is True
    assert resolve_orientation(wide, "auto") is False
    assert resolve_orientation(wide, "portrait") is True


def test_sample_point_portrait_maps_columns() -> None:
    warped = np.zeros((1600, 1200, 3), dtype=np.uint8)
    bounds = (40, 80, 1160, 1520)
    px_b, _ = sample_point_layout_oriented(
        0.6, 0.12, warped, portrait=True, label_frac=0.1, header_frac=44 / 1200, content_bounds=bounds
    )
    px_g, _ = sample_point_layout_oriented(
        0.6, 0.92, warped, portrait=True, label_frac=0.1, header_frac=44 / 1200, content_bounds=bounds
    )
    assert px_b < px_g
    assert px_b < bounds[0] + (bounds[2] - bounds[0]) * 0.25
    assert px_g > bounds[0] + (bounds[2] - bounds[0]) * 0.75


def test_ink_aware_wood_excludes_yellow_and_green() -> None:
    rgb = np.array(
        [
            [215, 205, 55],   # yellow ink
            [87, 135, 113],   # green ink
            [190, 140, 80],   # oak wood
            [40, 35, 38],     # black ink
        ],
        dtype=np.float64,
    )
    ink = _ink_likelihood_rgb(rgb)
    assert ink[0].argmax() == 2  # yellow
    assert ink[1].argmax() == 5  # green
    assert ink[3].argmax() == 0  # black
    wood = _is_wood_rgb(rgb)
    assert not wood[0]
    assert not wood[1]
    assert wood[2]
    assert not wood[3]


def _synthetic_primaries_portrait() -> np.ndarray:
    """Six vertical stripes on neutral background (portrait warped panel)."""
    h, w = 1600, 1200
    img = np.full((h, w, 3), 200, dtype=np.uint8)
    colors = [
        [35, 30, 32],
        [200, 205, 210],
        [220, 210, 50],
        [120, 20, 10],
        [20, 60, 180],
        [70, 120, 80],
    ]
    col_w = w // 6
    for i, rgb in enumerate(colors):
        x0 = i * col_w
        x1 = (i + 1) * col_w if i < 5 else w
        img[200:1400, x0:x1] = rgb
    return img


def test_scan_coherent_runs_finds_stripes() -> None:
    img = _synthetic_primaries_portrait()
    y = 800
    runs = scan_coherent_runs(img[y].astype(np.float64))
    panel = [r for r in runs if r.width >= 40]
    assert len(panel) >= 6


def test_analyze_primaries_extrapolates_six_columns() -> None:
    img = _synthetic_primaries_portrait()
    geom = analyze_primaries_portrait(img, header_frac=0.0)
    assert len(geom.centers) == 6
    assert geom.centers[0] < geom.centers[1] < geom.centers[5]
    assert geom.white_run.width >= 8
    assert geom.green_run.width >= 8
    assert geom.bounds[0] < geom.white_run.start
    assert geom.bounds[2] >= geom.green_run.end - 5


def test_sample_column_coherent_inside_panel() -> None:
    img = _synthetic_primaries_portrait()
    geom = analyze_primaries_portrait(img, header_frac=0.0)
    cl, ct, cr, cb = geom.bounds
    for i in range(6):
        px, py = sample_column_coherent(img, geom, i)
        assert cl <= px <= cr
        assert ct <= py <= cb


def test_img0158_scan_row_ink_order() -> None:
    """Regression: peak boundaries on a real warp must label columns black→green."""
    from pathlib import Path

    from PIL import Image

    try:
        import pillow_heif

        pillow_heif.register_heif_opener()
    except ImportError:
        pass

    from calibrate_p2_from_photo import detect_display_quad, warp_quad_to_rect

    photo = Path.home() / "Downloads" / "IMG_0158.HEIC"
    if not photo.is_file():
        return
    img = np.array(Image.open(photo).convert("RGB"))
    warped = warp_quad_to_rect(img, detect_display_quad(img, frame="wood"), 1200, 1600)
    y = 797
    _, columns, _, _ = _detect_columns_from_row(warped[y].astype(np.float64), warped.shape[1])
    for i, col in enumerate(columns[:5]):
        assert int(_ink_scores(col.mean).argmax()) == i, (
            f"col {i} mean {col.mean.astype(int)} scores {_ink_scores(col.mean)}"
        )
    green_scores = _ink_scores(columns[5].mean)
    assert green_scores[GREEN_INDEX] > 8.0, columns[5].mean
    geom = analyze_primaries_portrait(warped, header_frac=44.0 / 1200.0)
    assert len(geom.boundaries) == 5
    assert geom.boundaries == sorted(geom.boundaries)
    # Measured peaks on IMG_0158 (not equal spacing — green side is wider after keystone).
    for got, expect in zip(geom.boundaries, (309, 406, 519, 652, 805)):
        assert abs(got - expect) < 30, f"boundary {got} vs expected {expect}"


def test_bar_heights_decrease_toward_black() -> None:
    img = _synthetic_primaries_portrait()
    geom = analyze_primaries_portrait(img, header_frac=0.0)
    heights = [b - t + 1 for t, b in geom.column_vert]
    assert heights[GREEN_INDEX] >= heights[WHITE_INDEX] > heights[0]


def test_boundaries_are_measured_transitions() -> None:
    img = _synthetic_primaries_portrait()
    geom = analyze_primaries_portrait(img, header_frac=0.0)
    assert len(geom.boundaries) == 5
    cols = geom.column_runs
    for b, left, right in zip(geom.boundaries, cols[:-1], cols[1:]):
        assert left.end < b <= right.start


def test_primaries_panel_bounds_black_to_green() -> None:
    img = np.zeros((200, 600, 3), dtype=np.uint8)
    img[:, 40:120] = [35, 30, 32]
    img[:, 120:200] = [200, 205, 210]
    img[:, 200:280] = [220, 210, 50]
    img[:, 280:360] = [120, 20, 10]
    img[:, 360:440] = [20, 60, 180]
    img[:, 440:520] = [70, 120, 80]
    img[:, 520:] = [190, 150, 90]  # wood past green
    bounds = detect_primaries_panel_bounds(img, frame="wood")
    assert bounds[0] < 80
    assert bounds[2] < 540
    assert bounds[2] > 480


if __name__ == "__main__":
    test_normalize_p2_tonal_neutralizes_grey_axis()
    test_warp_quad_to_rect_size()
    test_resolve_orientation_auto()
    test_sample_point_portrait_maps_columns()
    test_ink_aware_wood_excludes_yellow_and_green()
    test_scan_coherent_runs_finds_stripes()
    test_analyze_primaries_extrapolates_six_columns()
    test_bar_heights_decrease_toward_black()
    test_sample_column_coherent_inside_panel()
    test_boundaries_are_measured_transitions()
    test_img0158_scan_row_ink_order()
    test_primaries_panel_bounds_black_to_green()
    print("ok")
