"""Reusable binary-patch tool for ij_epd (ESP32-C5) firmware images.

Built for patching H2 (firmware.bin) without source: adds a code cave at the
end of the IROM segment, hand-encodes small RISC-V (RV32IMAC, uncompressed
subset) snippets for the cave body, and rewrites a call site to trampoline
into it. All patches are recorded in a JSON sidecar next to the output image
so they can be inspected/reapplied/reverted.

Requires: esptool (`uv pip install esptool` into the project .venv).

Usage as a library:
    fw = Firmware.load("firmware.bin")
    cave_va = fw.add_cave(some_code_bytes, name="log_first_sd_file")
    fw.write_jump(hook_va=0x4201..., target_va=cave_va, name="hook_sc12_call")
    fw.save("firmware-patched.bin")

The IROM segment is segment index 2 in ij_epd v0.5.6 images (VA 0x42000020).
Growing it is safe as long as the total image stays under the OTA partition
size (0x2a3000 for `ota_0`/`ota_1` on this device, see partition table notes
in project memory) — `Firmware.load` records the partition budget so
`add_cave`/`save` can warn if a patch would overflow it.
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path

from esptool.bin_image import LoadFirmwareImage

IROM_SEGMENT_INDEX = 2
OTA_PARTITION_SIZE = 0x2A3000  # ota_0 / ota_1, see project memory


# --- minimal RV32IMAC encoder (uncompressed-only; no need for the C
#     extension in hand-written cave code, it's a strict superset) ---

def _u(bits, val):
    val &= (1 << bits) - 1
    return val


def enc_lui(rd, imm20):
    imm20 = _u(20, imm20)
    return _u(32, (imm20 << 12) | (rd << 7) | 0x37)


def enc_auipc(rd, imm20):
    imm20 = _u(20, imm20)
    return _u(32, (imm20 << 12) | (rd << 7) | 0x17)


def enc_addi(rd, rs1, imm12):
    imm12 = _u(12, imm12)
    return _u(32, (imm12 << 20) | (rs1 << 15) | (0 << 12) | (rd << 7) | 0x13)


def enc_jal(rd, imm21):
    """imm21 is a signed byte offset, must be even, within +-1MiB."""
    assert imm21 % 2 == 0
    imm = _u(21, imm21)
    bit20 = (imm >> 20) & 1
    bits10_1 = (imm >> 1) & 0x3FF
    bit11 = (imm >> 11) & 1
    bits19_12 = (imm >> 12) & 0xFF
    enc = (bit20 << 31) | (bits19_12 << 12) | (bit11 << 20) | (bits10_1 << 21) | (rd << 7) | 0x6F
    return _u(32, enc)


def enc_jalr(rd, rs1, imm12):
    imm12 = _u(12, imm12)
    return _u(32, (imm12 << 20) | (rs1 << 15) | (0 << 12) | (rd << 7) | 0x67)


REG = {name: i for i, name in enumerate(
    "zero ra sp gp tp t0 t1 t2 s0 s1 a0 a1 a2 a3 a4 a5 a6 a7 "
    "s2 s3 s4 s5 s6 s7 s8 s9 s10 s11 t3 t4 t5 t6".split()
)}


def hi_lo(target):
    """Split an absolute 32-bit address into (hi20, lo12-signed) for lui+addi,
    same convention the compiler uses (lo12 may be negative to compensate
    lui's implicit rounding)."""
    lo = target & 0xFFF
    if lo & 0x800:
        lo -= 0x1000
    hi = (target - lo) >> 12
    return hi & 0xFFFFF, lo


def call_abs(rd_reg, target_va, scratch="ra"):
    """Encode an absolute (non-PC-relative) far call: lui+jalr, clobbers `scratch`."""
    hi, lo = hi_lo(target_va)
    rs = REG[scratch]
    return (
        enc_lui(rs, hi).to_bytes(4, "little")
        + enc_jalr(REG[rd_reg], rs, lo).to_bytes(4, "little")
    )


def load_abs(rd, target_va):
    """lui+addi to materialize an absolute address in a register."""
    hi, lo = hi_lo(target_va)
    return enc_lui(REG[rd], hi).to_bytes(4, "little") + enc_addi(REG[rd], REG[rd], lo).to_bytes(4, "little")


@dataclass
class Patch:
    name: str
    kind: str  # "cave" | "hook"
    va: int
    length: int
    note: str = ""


@dataclass
class Firmware:
    path: Path
    img: object
    patches: list = field(default_factory=list)

    @classmethod
    def load(cls, path: str, chip: str = "esp32c5"):
        img = LoadFirmwareImage(chip, path)
        # esptool's save() unconditionally reads segment.name (normally only
        # set on ELFSection-derived segments); plain bin-loaded ImageSegments
        # don't have it, so give them a harmless placeholder.
        for seg in img.segments:
            if not hasattr(seg, "name"):
                seg.name = ""
        return cls(path=Path(path), img=img)

    # --- segment helpers ---

    def irom_segment(self):
        return self.img.segments[IROM_SEGMENT_INDEX]

    def irom_base_va(self):
        return self.irom_segment().addr

    def irom_len(self):
        return len(self.irom_segment().data)

    def va_to_irom_offset(self, va: int) -> int:
        base = self.irom_base_va()
        off = va - base
        if not (0 <= off < self.irom_len()):
            raise ValueError(f"VA {hex(va)} is outside the current IROM segment "
                              f"({hex(base)}..{hex(base + self.irom_len())})")
        return off

    def image_total_size(self) -> int:
        # rough: sum of segment data + per-segment 8-byte headers + main header.
        # good enough for a partition-budget sanity check, not bit-exact.
        return sum(len(s.data) + 8 for s in self.img.segments) + 24

    # --- patch primitives ---

    def add_cave(self, code: bytes, name: str, align: int = 4) -> int:
        """Append `code` to the end of the IROM segment. Returns the VA the
        cave now lives at. That VA is only valid for THIS Firmware instance
        until save() — recompute after loading a freshly-saved image if you
        chain patches across separate runs."""
        seg = self.irom_segment()
        cur_len = len(seg.data)
        pad = (-cur_len) % align
        if pad:
            seg.data += b"\x00" * pad
        cave_va = seg.addr + len(seg.data)
        seg.data += code
        new_total = self.image_total_size()
        if new_total > OTA_PARTITION_SIZE:
            raise ValueError(
                f"cave '{name}' would grow the image to {hex(new_total)}, "
                f"over the {hex(OTA_PARTITION_SIZE)} ota partition budget"
            )
        self.patches.append(Patch(name=name, kind="cave", va=cave_va, length=len(code)))
        return cave_va

    def read_irom(self, va: int, n: int) -> bytes:
        off = self.va_to_irom_offset(va)
        return bytes(self.irom_segment().data[off:off + n])

    def write_irom(self, va: int, data: bytes, name: str = "raw-write"):
        off = self.va_to_irom_offset(va)
        seg = self.irom_segment()
        buf = bytearray(seg.data)
        buf[off:off + len(data)] = data
        seg.data = bytes(buf)
        self.patches.append(Patch(name=name, kind="hook", va=va, length=len(data)))

    def write_jump(self, hook_va: int, target_va: int, name: str, note: str = ""):
        """Overwrite 8 bytes at hook_va with an absolute far call (lui+jalr,
        `ra` as link+scratch) to target_va. Caller is responsible for making
        sure hook_va lands on an instruction boundary and that 8 bytes there
        is safe to clobber (i.e. not split across two unrelated compressed
        instructions the rest of the function still depends on) — verify
        with a disassembly window first.
        """
        code = call_abs("ra", target_va, scratch="ra")
        self.write_irom(hook_va, code, name=name)
        self.patches[-1].note = note

    # --- persistence ---

    def save(self, out_path: str):
        self.img.save(out_path)
        manifest_path = Path(out_path).with_suffix(".patches.json")
        manifest_path.write_text(json.dumps(
            [dict(name=p.name, kind=p.kind, va=hex(p.va), length=p.length, note=p.note)
             for p in self.patches], indent=2))
        return manifest_path


if __name__ == "__main__":
    import sys
    fw = Firmware.load(sys.argv[1] if len(sys.argv) > 1 else "firmware.bin")
    print(f"IROM base: {hex(fw.irom_base_va())} len: {hex(fw.irom_len())}")
    print(f"image total (approx): {hex(fw.image_total_size())} / "
          f"partition budget {hex(OTA_PARTITION_SIZE)} "
          f"({OTA_PARTITION_SIZE - fw.image_total_size()} bytes free to grow)")
