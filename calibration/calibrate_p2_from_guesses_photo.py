#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow", "numpy", "pillow-heif"]
# ///
"""Deprecated name — use calibrate_p2_from_photo.py in this directory."""

import sys
from pathlib import Path

_CAL_DIR = Path(__file__).resolve().parent
if str(_CAL_DIR) not in sys.path:
    sys.path.insert(0, str(_CAL_DIR))

from calibrate_p2_from_photo import main

if __name__ == "__main__":
    main()
