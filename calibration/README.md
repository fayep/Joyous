# P2 photo calibration (InkJoy + Samsung)

Measure on-panel ink colors from a photograph of the primaries chart.
Outputs `PaletteInkJoyDisplay` / `PaletteSamsungDisplay` for `joyous-hub/dither.go`.

Hub embeds send charts from `joyous-hub/calibration/*.png` (`calibration.go`).

## Click calibration (recommended when auto geometry struggles)

Interactive tool: move the cursor to see ink match under the crosshair; click to sample each swatch.

```bash
uv run /Volumes/CaseSensitive/PhotoFrames/calibration/calibrate_p2_click.py ~/Downloads/IMG_0158.HEIC \
  --layout inkjoy-primaries --frame wood --orientation portrait \
  -o inkjoy-p2-click.json
```

Keys: `1`–`6` select ink · click to add point (6 per ink, averaged) · `u` undo · `c` clear ink · `e` export · `q` quit

Loupe follows the cursor (center pixel = yellow box). Export needs all six inks at 6 points each.

## InkJoy (portrait wood frame)

```bash
uv run /Volumes/CaseSensitive/PhotoFrames/calibration/calibrate_p2_from_photo.py IMG_0158.HEIC \
  --layout inkjoy-primaries --frame wood --orientation portrait \
  --debug inkjoy-p2-debug.png --debug-warp inkjoy-p2-warp.png \
  -o inkjoy-p2-calibration.json
```

Works from any working directory. HEIC/JPEG/PNG supported.

## Samsung (white bezel)

```bash
uv run calibration/calibrate_p2_from_photo.py photo.jpg \
  --layout samsung-primaries --frame white -o samsung-p2.json
```

## Layout

| Path | Purpose |
|------|---------|
| `calibrate_p2_click.py` | Interactive click sampler |
| `calibrate_p2_from_photo.py` | Main script |
| `palettes.py` | P1/P2 constants (canonical) |
| `layouts/` | Chart generators |
| `dump_portrait_scan.py` | Column scan debug dump |
| `test_calibrate_p2.py` | Unit tests |

`calibrate_p2_from_guesses_photo.py` is a deprecated alias in this folder.
