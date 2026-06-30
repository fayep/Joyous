"""Primaries-chart geometry from measured RGB-step peaks on a keystone-warped bisect scan.

The warped image is already trapezoid-corrected; column widths are unequal in the
photo but should be read from visible ink transitions, never from layout pitch.

Portrait (green column closer to camera before warp):
  1. Exclude the layout header band (dark + white title text) from vertical extent.
  2. Bisect the ink body horizontally; pick the row with five clear column peaks.
  3. panel_l = start of black ink; five boundaries = consecutive transition peaks.
  4. panel_r = end of green ink at the green→wood step (green side is near camera).
  5. Sample on the bisect row inside each measured column span.
"""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np

from palettes import COLOR_NAMES

WHITE_INDEX = COLOR_NAMES.index("white")
GREEN_INDEX = COLOR_NAMES.index("green")
N_PRIMARIES = len(COLOR_NAMES)
N_BOUNDARIES = N_PRIMARIES - 1


@dataclass(frozen=True)
class CoherentRun:
    start: int
    end: int
    mean: np.ndarray

    @property
    def center(self) -> float:
        return 0.5 * (self.start + self.end)

    @property
    def width(self) -> int:
        return self.end - self.start + 1


@dataclass(frozen=True)
class PrimariesGeometry:
    bounds: tuple[int, int, int, int]
    centers: list[float]
    boundaries: list[int]
    scan_y: int
    sample_y: float
    white_run: CoherentRun
    green_run: CoherentRun
    column_runs: tuple[CoherentRun, ...]
    # Trapezoid ink panel: bar height grows green (near) → black (far).
    panel_trap: tuple[tuple[int, int], tuple[int, int], tuple[int, int], tuple[int, int]]
    column_vert: tuple[tuple[int, int], ...]  # per-column (top, bottom) y


def _rgb_step(a: np.ndarray, b: np.ndarray) -> float:
    return float(np.linalg.norm(a.astype(np.float64) - b.astype(np.float64)))


def _run_internal_std(seg: np.ndarray) -> float:
    return float(np.std(seg.astype(np.float64), axis=0).mean())


def _lum(rgb: np.ndarray) -> float:
    r, g, b = float(rgb[0]), float(rgb[1]), float(rgb[2])
    return 0.299 * r + 0.587 * g + 0.114 * b


def _is_wood_mean(mean: np.ndarray) -> bool:
    r, g, b = float(mean[0]), float(mean[1]), float(mean[2])
    lum = _lum(mean)
    chroma = float(np.max(mean) - np.min(mean))
    if (r - b > 28.0) and (r - g > 10.0) and (110.0 < lum < 225.0):
        return True
    if lum > 195.0 and chroma < 22.0 and r >= g >= b:
        return True
    return False


def _is_header_row(row: np.ndarray) -> bool:
    """Title header: dark background with bright text (high row variance)."""
    row = row.astype(np.float64)
    lum = float(row.mean())
    std = float(row.std())
    return lum < 95.0 and std > 16.0


def _smooth_row(row: np.ndarray, k: int = 5) -> np.ndarray:
    row = row.astype(np.float64)
    if k <= 1:
        return row
    pad = k // 2
    out = np.empty_like(row)
    for i in range(len(row)):
        lo = max(0, i - pad)
        hi = min(len(row), i + pad + 1)
        out[i] = np.median(row[lo:hi], axis=0)
    return out


def scan_coherent_runs(
    strip: np.ndarray,
    *,
    step_thresh: float = 26.0,
    min_len: int = 8,
    std_max: float = 40.0,
) -> list[CoherentRun]:
    """Segment a 1×N or N×3 strip into chromatically coherent runs."""
    strip = np.asarray(strip)
    if strip.ndim == 1:
        strip = strip.reshape(-1, 1)
        step_thresh = min(step_thresh, 18.0)
    n = len(strip)
    runs: list[CoherentRun] = []
    i = 0
    while i < n:
        j = i + 1
        while j < n and _rgb_step(strip[j - 1], strip[j]) <= step_thresh:
            j += 1
        seg = strip[i:j]
        if len(seg) >= min_len and _run_internal_std(seg) <= std_max:
            runs.append(CoherentRun(i, j - 1, seg.mean(axis=0)))
        i = j if j > i else i + 1
    return runs


def _ink_scores(mean: np.ndarray) -> np.ndarray:
    r, g, b = float(mean[0]), float(mean[1]), float(mean[2])
    lum = _lum(mean)
    chroma = float(np.max(mean) - np.min(mean))
    black = max(0.0, 85.0 - lum) * (1.0 if chroma < 60.0 else 0.0)
    white = max(0.0, lum - 115.0) * (1.0 if chroma < 50.0 else 0.0)
    yellow = max(0.0, min(r, g) - b - 15.0) * (1.0 if r > 150 and g > 145 else 0.0)
    red = max(0.0, r - max(g, b) - 15.0) * (1.0 if g < 95 and b < 95 else 0.0)
    blue = max(0.0, b - max(r, g) - 15.0)
    green = max(0.0, g - max(r, b) - 8.0) * (1.0 if 20 < lum < 185 else 0.0)
    return np.array([black, white, yellow, red, blue, green], dtype=np.float64)


def _row_steps(row: np.ndarray) -> np.ndarray:
    steps = np.zeros(len(row), dtype=np.float64)
    for i in range(1, len(row)):
        steps[i] = _rgb_step(row[i - 1], row[i])
    return steps


def _body_vertical_range(
    img: np.ndarray, ink_x0: int, ink_x1: int, *, header_frac: float
) -> tuple[int, int]:
    """Ink body top/bottom, skipping layout header and bottom wood."""
    h = img.shape[0]
    x0 = max(0, ink_x0)
    x1 = min(img.shape[1], ink_x1)
    layout_top = int(h * header_frac * 0.85)

    body_top = layout_top
    for y in range(layout_top, int(h * 0.28)):
        strip = img[y, x0:x1]
        if _is_header_row(strip):
            body_top = y + 1
            continue
        if float(strip.std()) < 55.0:
            body_top = y
            break

    body_bot = h - 1
    for y in range(h - 1, int(h * 0.65), -1):
        mean = img[y, x0:x1].mean(axis=0)
        if _is_wood_mean(mean):
            body_bot = y - 1
            continue
        break
    return body_top, max(body_top + 40, body_bot)


def _black_column_left(row: np.ndarray, img_w: int) -> int:
    """Start x of the black ink column (skip wood and layout header strip)."""
    smooth = _smooth_row(row, k=5)
    edge = int(img_w * 0.04)
    for x in range(edge, int(img_w * 0.35)):
        px = smooth[x]
        if _is_wood_mean(px):
            continue
        if _lum(px) < 100 and _ink_scores(px)[0] > 15:
            run_x = x
            while run_x > edge and _lum(smooth[run_x - 1]) < 100 and not _is_wood_mean(
                smooth[run_x - 1]
            ):
                run_x -= 1
            return run_x
    return edge


def _wood_start_x(row: np.ndarray, img_w: int) -> int:
    """First x (from left) where the row enters the wood bezel on the green side."""
    smooth = _smooth_row(row, k=5)
    edge = int(img_w * 0.04)
    for x in range(img_w - edge - 1, int(img_w * 0.45), -1):
        if _is_wood_mean(smooth[x]):
            return x
    return img_w - edge - 1


def _local_peaks(steps: np.ndarray, x0: int, x1: int, *, min_step: float) -> list[tuple[float, int]]:
    peaks: list[tuple[float, int]] = []
    for x in range(x0 + 2, x1 - 2):
        s = steps[x]
        if s < min_step:
            continue
        if s >= steps[x - 1] and s >= steps[x + 1]:
            peaks.append((float(s), x))
    return peaks


def _five_column_peaks(
    steps: np.ndarray, panel_l: int, wood_x: int, *, min_step: float = 28.0
) -> list[int]:
    """Five consecutive ink transition peaks left→right (measured, not equal spacing)."""
    peaks = _local_peaks(steps, panel_l + 5, wood_x - 5, min_step=min_step)
    if len(peaks) < N_BOUNDARIES:
        peaks = _local_peaks(steps, panel_l + 5, wood_x - 5, min_step=min_step * 0.65)
    if len(peaks) < N_BOUNDARIES:
        raise ValueError(f"found {len(peaks)} transition peaks, need {N_BOUNDARIES}")
    peaks.sort(key=lambda t: t[1])

    min_sep = 28
    best_sum = -1.0
    best: list[int] | None = None
    for i in range(len(peaks) - N_BOUNDARIES + 1):
        window = peaks[i : i + N_BOUNDARIES]
        xs = [x for _, x in window]
        if any(xs[j + 1] - xs[j] < min_sep for j in range(N_BOUNDARIES - 1)):
            continue
        total = sum(s for s, _ in window)
        if total > best_sum:
            best_sum = total
            best = xs
    if best is None:
        raise ValueError("no consecutive group of five transition peaks")
    return best


def _green_column_right(smooth: np.ndarray, steps: np.ndarray, last_peak: int, wood_x: int) -> int:
    """End of green ink at the measured green→wood transition (near-camera side)."""
    for x in range(last_peak + 12, min(wood_x + 8, len(smooth) - 2)):
        if steps[x] < 30:
            continue
        ahead = smooth[min(x + 6, len(smooth) - 1)]
        if _is_wood_mean(ahead):
            return max(last_peak + 1, x - 1)
    x = last_peak + 1
    while x < wood_x and _ink_scores(smooth[x])[GREEN_INDEX] > 8 and not _is_wood_mean(smooth[x]):
        x += 1
    return max(last_peak + 1, x - 1)


def _columns_from_edges(row: np.ndarray, edges: list[int]) -> list[CoherentRun]:
    cols: list[CoherentRun] = []
    for i in range(N_PRIMARIES):
        x0 = edges[i]
        x1 = edges[i + 1] - 1
        seg = row[x0 : x1 + 1]
        cols.append(CoherentRun(x0, x1, seg.mean(axis=0)))
    return cols


def _ink_match_score(columns: list[CoherentRun]) -> float:
    score = 0.0
    for i, col in enumerate(columns):
        scores = _ink_scores(col.mean)
        if int(scores.argmax()) == i:
            score += 100.0
        score += float(scores[i])
    return score


def _detect_columns_from_row(row: np.ndarray, img_w: int) -> tuple[list[int], list[CoherentRun], int, int]:
    smooth = _smooth_row(row, k=5)
    steps = _row_steps(smooth)
    panel_l = _black_column_left(smooth, img_w)
    wood_x = _wood_start_x(smooth, img_w)
    boundaries = _five_column_peaks(steps, panel_l, wood_x)
    panel_r = _green_column_right(smooth, steps, boundaries[-1], wood_x)
    edges = [panel_l, *boundaries, panel_r]
    columns = _columns_from_edges(smooth, edges)
    return boundaries, columns, panel_l, panel_r


def _matches_ink(px: np.ndarray, color_index: int) -> bool:
    scores = _ink_scores(px)
    return float(scores[color_index]) > 10.0 and float(scores[color_index]) >= float(scores.max()) * 0.5


def _column_vertical_extent(
    img: np.ndarray,
    col_run: CoherentRun,
    scan_y: int,
    color_index: int,
    *,
    y_min: int,
    y_max: int,
) -> tuple[int, int]:
    """Walk up/down from scan_y while pixels match this ink column."""
    x = int(round(col_run.center))
    col = _smooth_row(img[y_min : y_max + 1, x].astype(np.float64), k=3)
    rel_scan = int(np.clip(scan_y - y_min, 0, len(col) - 1))
    step_lim = 26.0 if color_index != 0 else 22.0

    top = rel_scan
    while top > 0:
        if _is_wood_mean(col[top - 1]):
            break
        if _rgb_step(col[top], col[top - 1]) > step_lim:
            break
        if not _matches_ink(col[top - 1], color_index):
            break
        top -= 1

    bot = rel_scan
    while bot < len(col) - 1:
        if _is_wood_mean(col[bot + 1]):
            break
        if _rgb_step(col[bot], col[bot + 1]) > step_lim:
            break
        if not _matches_ink(col[bot + 1], color_index):
            break
        bot += 1

    return top + y_min, bot + y_min


def _enforce_bar_progression(
    vert: list[tuple[int, int]], scan_y: int
) -> list[tuple[int, int]]:
    """Bar heights decrease green→black; black thinner than white."""
    out = list(vert)
    h_green = out[GREEN_INDEX][1] - out[GREEN_INDEX][0] + 1
    min_step = 8
    for i in range(N_PRIMARIES):
        if i == GREEN_INDEX:
            continue
        max_h = max(min_step, int(h_green * (i + 1) / float(N_PRIMARIES)))
        top, bot = out[i]
        cur_h = bot - top + 1
        if cur_h > max_h:
            half = max_h // 2
            out[i] = (scan_y - half, scan_y + max_h - half - 1)
    for i in range(GREEN_INDEX - 1, -1, -1):
        hi = out[i][1] - out[i][0] + 1
        hj = out[i + 1][1] - out[i + 1][0] + 1
        if hi >= hj:
            max_h = max(min_step, hj - min_step)
            half = max_h // 2
            out[i] = (scan_y - half, scan_y + max_h - half - 1)
    return out


def _panel_trapezoid(
    columns: list[CoherentRun], vert: list[tuple[int, int]]
) -> tuple[tuple[int, int], tuple[int, int], tuple[int, int], tuple[int, int]]:
    """TL/TR/BR/BL — top slopes up toward green (near camera, taller bars)."""
    left_x = columns[0].start
    right_x = columns[-1].end
    top_l, bot_l = vert[0]
    top_r, bot_r = vert[GREEN_INDEX]
    return (
        (left_x, top_l),
        (right_x, top_r),
        (right_x, bot_r),
        (left_x, bot_l),
    )


def _trap_envelope(
    trap: tuple[tuple[int, int], tuple[int, int], tuple[int, int], tuple[int, int]],
    img_w: int,
    img_h: int,
) -> tuple[int, int, int, int]:
    xs = [p[0] for p in trap]
    ys = [p[1] for p in trap]
    return (
        max(0, min(xs)),
        max(0, min(ys)),
        min(img_w - 1, max(xs)),
        min(img_h - 1, max(ys)),
    )


def _body_column_extent(
    img: np.ndarray, x: float, scan_y: int, body_top: int, body_bot: int
) -> tuple[int, int]:
    """Vertical ink extent at x inside the body (excludes header band)."""
    col = _smooth_row(img[body_top : body_bot + 1, int(round(x))].astype(np.float64), k=5)
    runs = scan_coherent_runs(col, step_thresh=22.0, min_len=10, std_max=50.0)
    y_hint = scan_y - body_top
    panel = [r for r in runs if not _is_wood_mean(r.mean) and not _is_header_row(col[r.start : r.end + 1])]
    if not panel:
        return body_top, body_bot
    for r in panel:
        if r.start <= y_hint <= r.end:
            return r.start + body_top, r.end + body_top
    best = max(panel, key=lambda r: r.width)
    return best.start + body_top, best.end + body_top


def _best_horizontal_scan(
    img: np.ndarray, body_top: int, body_bot: int
) -> tuple[int, np.ndarray, list[int], list[CoherentRun]]:
    w = img.shape[1]
    y0 = body_top + int((body_bot - body_top) * 0.38)
    y1 = body_top + int((body_bot - body_top) * 0.62)
    step = 2
    best_score = -1.0
    best: tuple[int, np.ndarray, list[int], list[CoherentRun]] | None = None
    for y in range(y0, y1, step):
        if _is_header_row(img[y]):
            continue
        row = img[y].astype(np.float64)
        try:
            bounds, columns, _, _ = _detect_columns_from_row(row, w)
        except ValueError:
            continue
        score = _ink_match_score(columns)
        if score > best_score:
            best_score = score
            best = (y, _smooth_row(row, k=5), bounds, columns)
    if best is None:
        y = (y0 + y1) // 2
        row = _smooth_row(img[y].astype(np.float64), k=5)
        bounds, columns, _, _ = _detect_columns_from_row(row, w)
        return y, row, bounds, columns
    return best


def analyze_primaries_portrait(img: np.ndarray, *, header_frac: float = 44.0 / 1200.0) -> PrimariesGeometry:
    """Panel geometry from measured peaks on a keystone-warped bisect scan."""
    h, w = img.shape[:2]
    probe_y = int(h * 0.5)
    probe_l = _black_column_left(img[probe_y].astype(np.float64), w)
    wood_x = _wood_start_x(img[probe_y].astype(np.float64), w)
    body_top, body_bot = _body_vertical_range(img, probe_l, wood_x, header_frac=header_frac)

    scan_y, _scan_row, boundaries, columns = _best_horizontal_scan(img, body_top, body_bot)
    search_top = body_top
    search_bot = body_bot

    vert = [
        _column_vertical_extent(img, col, scan_y, i, y_min=search_top, y_max=search_bot)
        for i, col in enumerate(columns)
    ]
    vert = _enforce_bar_progression(vert, scan_y)
    trap = _panel_trapezoid(columns, vert)
    bounds = _trap_envelope(trap, w, h)

    return PrimariesGeometry(
        bounds=bounds,
        centers=[float(c.center) for c in columns],
        boundaries=boundaries,
        scan_y=scan_y,
        sample_y=float(scan_y),
        white_run=columns[WHITE_INDEX],
        green_run=columns[GREEN_INDEX],
        column_runs=tuple(columns),
        panel_trap=trap,
        column_vert=tuple(vert),
    )


def sample_column_coherent(
    img: np.ndarray,
    geom: PrimariesGeometry,
    color_index: int,
    *,
    x_frac: float = 0.55,
) -> tuple[float, float]:
    """Sample on the bisect scan row inside the measured column span."""
    col = geom.column_runs[color_index]
    margin = max(5.0, col.width * 0.15)
    usable = max(1.0, col.width - 2.0 * margin)
    px = col.start + margin + usable * float(np.clip(x_frac, 0.0, 1.0))
    return float(px), float(geom.scan_y)
