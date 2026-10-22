"""Load an ESP32-S3 app image as VA-addressable segments."""
from __future__ import annotations

import re
import struct
from dataclasses import dataclass
from pathlib import Path

_SEG_NAME = re.compile(r"0x([0-9a-fA-F]+)")


@dataclass
class Segment:
    addr: int
    data: bytes
    name: str

    @property
    def end(self) -> int:
        return self.addr + len(self.data)


class Image:
    def __init__(self, segments: list[Segment], *, source: Path, entry: int | None = None):
        self.segments = segments
        self.source = source
        self.entry = entry

    @classmethod
    def load(cls, path: str | Path) -> Image:
        p = Path(path).expanduser()
        if p.is_dir():
            return cls._load_segdir(p)
        return cls._load_appbin(p)

    @classmethod
    def _load_segdir(cls, root: Path) -> Image:
        segs: list[Segment] = []
        for f in sorted(root.glob("seg*.bin")):
            m = _SEG_NAME.search(f.name)
            if not m:
                continue
            segs.append(Segment(int(m.group(1), 16), f.read_bytes(), f.name))
        if not segs:
            raise FileNotFoundError(f"no seg*_0xADDR.bin files in {root}")
        return cls(segs, source=root)

    @classmethod
    def _load_appbin(cls, path: Path) -> Image:
        raw = path.read_bytes()
        if not raw or raw[0] != 0xE9:
            raise ValueError(f"{path} is not an ESP app image (magic != 0xE9)")
        nseg = raw[1]
        entry = struct.unpack_from("<I", raw, 4)[0]
        # ESP32/S3: 8-byte image header + 16-byte extended header, then segments.
        off = 24
        segs: list[Segment] = []
        for i in range(nseg):
            if off + 8 > len(raw):
                raise ValueError(f"{path}: truncated segment header {i}")
            addr, size = struct.unpack_from("<II", raw, off)
            off += 8
            if off + size > len(raw):
                raise ValueError(f"{path}: truncated segment {i} at {addr:#x}")
            segs.append(Segment(addr, raw[off : off + size], f"seg{i}_{addr:#x}"))
            off += size
        return cls(segs, source=path, entry=entry)

    def seg_for(self, va: int) -> Segment | None:
        for s in self.segments:
            if s.addr <= va < s.end:
                return s
        return None

    def contains(self, va: int) -> bool:
        return self.seg_for(va) is not None

    def read(self, va: int, n: int) -> bytes | None:
        s = self.seg_for(va)
        if s is None:
            return None
        off = va - s.addr
        return s.data[off : off + n]

    def read_u32(self, va: int) -> int | None:
        b = self.read(va, 4)
        if b is None or len(b) < 4:
            return None
        return struct.unpack_from("<I", b)[0]

    def read_cstr(self, va: int, maxlen: int = 96) -> str | None:
        b = self.read(va, maxlen)
        if not b:
            return None
        if 0 in b:
            b = b[: b.index(0)]
        if not b or not all(32 <= x < 127 for x in b):
            return None
        return b.decode("ascii")

    def is_code(self, va: int) -> bool:
        """IROM / IRAM on ESP32-S3 (not DROM/DRAM/RTC)."""
        return 0x40000000 <= va < 0x50000000 and self.contains(va)

    def find_u32(self, value: int) -> list[int]:
        pat = struct.pack("<I", value & 0xFFFFFFFF)
        out: list[int] = []
        for s in self.segments:
            start = 0
            while True:
                i = s.data.find(pat, start)
                if i < 0:
                    break
                out.append(s.addr + i)
                start = i + 1
        return out

    def iter_strings(self, min_len: int = 4):
        for s in self.segments:
            cur = bytearray()
            start = 0
            for i, b in enumerate(s.data):
                if 32 <= b < 127:
                    if not cur:
                        start = i
                    cur.append(b)
                else:
                    if len(cur) >= min_len:
                        yield s.addr + start, bytes(cur).decode("ascii")
                    cur = bytearray()
            if len(cur) >= min_len:
                yield s.addr + start, bytes(cur).decode("ascii")
