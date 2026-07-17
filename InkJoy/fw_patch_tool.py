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

import copy
import hashlib
import io
import json
from dataclasses import dataclass, field
from pathlib import Path

from esptool.bin_image import ImageSegment, LoadFirmwareImage
from esptool.loader import ESPLoader

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


def enc_itype(rd, rs1, imm12, funct3, opcode):
    imm12 = _u(12, imm12)
    return _u(32, (imm12 << 20) | (rs1 << 15) | (funct3 << 12) | (rd << 7) | opcode)


def enc_lw(rd, rs1, imm12):
    return enc_itype(rd, rs1, imm12, 0b010, 0x03)


def enc_lb(rd, rs1, imm12):
    return enc_itype(rd, rs1, imm12, 0b000, 0x03)


def enc_lbu(rd, rs1, imm12):
    return enc_itype(rd, rs1, imm12, 0b100, 0x03)


def enc_stype(rs1, rs2, imm12, funct3, opcode):
    imm12 = _u(12, imm12)
    imm_hi = (imm12 >> 5) & 0x7F
    imm_lo = imm12 & 0x1F
    return _u(32, (imm_hi << 25) | (rs2 << 20) | (rs1 << 15) | (funct3 << 12) | (imm_lo << 7) | opcode)


def enc_sw(rs1, rs2, imm12):
    """store rs2 to imm12(rs1)."""
    return enc_stype(rs1, rs2, imm12, 0b010, 0x23)


def enc_btype(rs1, rs2, imm13, funct3):
    """imm13 is a signed byte offset (branch target - branch instr addr), must be even."""
    assert imm13 % 2 == 0
    imm = _u(13, imm13)
    bit12 = (imm >> 12) & 1
    bits10_5 = (imm >> 5) & 0x3F
    bits4_1 = (imm >> 1) & 0xF
    bit11 = (imm >> 11) & 1
    enc = (bit12 << 31) | (bits10_5 << 25) | (rs2 << 20) | (rs1 << 15) | (funct3 << 12) | (bits4_1 << 8) | (bit11 << 7) | 0x63
    return _u(32, enc)


def enc_beq(rs1, rs2, imm13):
    return enc_btype(rs1, rs2, imm13, 0b000)


def enc_bne(rs1, rs2, imm13):
    return enc_btype(rs1, rs2, imm13, 0b001)


def enc_addi_(rd, rs1, imm12):  # alias kept for readability at call sites
    return enc_addi(rd, rs1, imm12)


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


class Asm:
    """Tiny two-pass RV32I assembler for hand-written cave bodies. Every
    instruction is 4 bytes (uncompressed) so addresses are trivial to
    pre-compute; labels let branches/jumps be written without hand-counting
    byte offsets.

    asm = Asm(base_va=cave_va)
    asm.li("t0", 5)
    asm.label("loop")
    asm.addi("t0", "t0", -1)
    asm.bne("t0", "zero", "loop")
    asm.ret()
    code = asm.assemble()
    """

    def __init__(self, base_va: int):
        self.base_va = base_va
        self._items = []  # list of ("label", name) | ("insn", encode_fn)

    def label(self, name):
        self._items.append(("label", name))
        return self

    def _addr_of(self, labels, name):
        if name not in labels:
            raise KeyError(f"undefined label {name!r}")
        return labels[name]

    def _emit(self, fn):
        self._items.append(("insn", fn))
        return self

    # --- raw instructions (register args accept names or ints) ---
    def _r(self, x):
        return REG[x] if isinstance(x, str) else x

    def lui(self, rd, imm20):
        return self._emit(lambda labels, addr: enc_lui(self._r(rd), imm20))

    def addi(self, rd, rs1, imm12):
        return self._emit(lambda labels, addr: enc_addi(self._r(rd), self._r(rs1), imm12))

    def jal(self, rd, label):
        return self._emit(lambda labels, addr: enc_jal(self._r(rd), self._addr_of(labels, label) - addr))

    def jalr(self, rd, rs1, imm12):
        return self._emit(lambda labels, addr: enc_jalr(self._r(rd), self._r(rs1), imm12))

    def lw(self, rd, imm12, rs1):
        return self._emit(lambda labels, addr: enc_lw(self._r(rd), self._r(rs1), imm12))

    def lbu(self, rd, imm12, rs1):
        return self._emit(lambda labels, addr: enc_lbu(self._r(rd), self._r(rs1), imm12))

    def sw(self, rs2, imm12, rs1):
        return self._emit(lambda labels, addr: enc_sw(self._r(rs1), self._r(rs2), imm12))

    def beq(self, rs1, rs2, label):
        return self._emit(lambda labels, addr: enc_beq(self._r(rs1), self._r(rs2), self._addr_of(labels, label) - addr))

    def bne(self, rs1, rs2, label):
        return self._emit(lambda labels, addr: enc_bne(self._r(rs1), self._r(rs2), self._addr_of(labels, label) - addr))

    # --- pseudo-instructions ---
    def mv(self, rd, rs):
        return self.addi(rd, rs, 0)

    def li(self, rd, imm):
        if -2048 <= imm < 2048:
            return self.addi(rd, "zero", imm)
        hi, lo = hi_lo(imm)

        def two(labels, addr, rd=self._r(rd), hi=hi, lo=lo):
            return enc_lui(rd, hi).to_bytes(4, "little") + enc_addi(rd, rd, lo).to_bytes(4, "little")
        # split into two single-instruction emits so addresses stay 4-byte granular
        self._emit(lambda labels, addr, rd=self._r(rd), hi=hi: enc_lui(rd, hi))
        return self._emit(lambda labels, addr, rd=self._r(rd), lo=lo: enc_addi(rd, rd, lo))

    def la(self, rd, target_va):
        """Load an absolute address (not PC-relative — same convention as the
        rest of this tool, since these firmware images use plain lui+addi
        for statics rather than auipc+addi)."""
        return self.li(rd, target_va)

    def call(self, target_va, link="ra", scratch=None):
        """Absolute far call: lui+jalr. If scratch is None, uses `link` as
        the scratch register too (matches call_abs's convention)."""
        # scratch must be a real (non-zero) register even for a tail call
        # (link="zero"): "zero" can't hold the lui'd high bits, so a naive
        # `scratch = scratch or link` here would silently emit `lui zero,
        # ...` (a discarded no-op) followed by `jalr zero, zero, lo` —
        # jumping to a near-zero address instead of target_va.
        scratch = scratch or (link if link != "zero" else "ra")
        hi, lo = hi_lo(target_va)
        self._emit(lambda labels, addr, rs=self._r(scratch), hi=hi: enc_lui(rs, hi))
        return self._emit(lambda labels, addr, rd=self._r(link), rs=self._r(scratch), lo=lo: enc_jalr(rd, rs, lo))

    def j(self, label):
        return self.jal("zero", label)

    def ret(self):
        return self.jalr("zero", "ra", 0)

    def nop(self):
        return self.addi("zero", "zero", 0)

    def assemble(self) -> bytes:
        # pass 1: assign addresses (every item is exactly one 4-byte instruction)
        labels = {}
        addr = self.base_va
        for kind, payload in self._items:
            if kind == "label":
                labels[payload] = addr
            else:
                addr += 4
        # pass 2: encode
        out = bytearray()
        addr = self.base_va
        for kind, payload in self._items:
            if kind == "label":
                continue
            word = payload(labels, addr)
            out += word.to_bytes(4, "little")
            addr += 4
        return bytes(out)


def _save_preserving_segment_order(img, filename: str) -> None:
    """Same as ESP32C5FirmwareImage.save(), except flash/RAM segments are
    ordered by their *original file position* (segment.file_offs) instead of
    ascending virtual address.

    esptool's stock save() does `sorted(self.segments, key=lambda s: s.addr)`.
    On ij_epd's memory map DROM sits at a *higher* VA (0x42160020) than IROM
    (0x42000020), so that sort silently swaps them: IROM ends up first in
    the saved file, DROM second. That breaks OTA validation — ESP-IDF's
    esp_ota_ops reads the esp_app_desc_t struct (project name, version,
    min/max_efuse_blk_rev_full, ...) assuming it's in the first segment, so
    with the reordering it reads our code bytes instead and gets garbage
    (observed on real hardware: "Image requires efuse blk rev >= v174.42,
    but chip is v0.3" — 17442 decoded from bytes that aren't the app_desc
    struct at all). The original, unmodified firmware.bin has DROM as
    segment 0 despite its higher address — file_offs preserves that.

    This is a verbatim copy of esptool 5.3.0's ESP32FirmwareImage.save()
    with only the two sort keys changed; re-check against esptool's source
    if bumping the esptool dependency ever starts failing here.
    """
    total_segments = 0
    with io.BytesIO() as f:
        img.write_common_header(f, img.segments)
        img.save_extended_header(f)

        checksum = ESPLoader.ESP_CHECKSUM_MAGIC

        def order_key(s):
            return s.file_offs if s.file_offs is not None else s.addr

        flash_segments = [
            copy.deepcopy(s)
            for s in sorted(img.segments, key=order_key)
            if img.is_flash_addr(s.addr)
        ]
        ram_segments = [
            copy.deepcopy(s)
            for s in sorted(img.segments, key=order_key)
            if not img.is_flash_addr(s.addr)
        ]

        if len(flash_segments) > 0:
            last_addr = flash_segments[0].addr
            for segment in flash_segments[1:]:
                if segment.addr // img.IROM_ALIGN == last_addr // img.IROM_ALIGN:
                    raise ValueError(
                        f"Segment loaded at {segment.addr:#010x} lands in same "
                        f"{img.IROM_ALIGN // 1024} KB flash mapping as segment "
                        f"loaded at {last_addr:#010x}."
                    )
                last_addr = segment.addr

        def get_alignment_data_needed(segment):
            align_past = (segment.addr % img.IROM_ALIGN) - img.SEG_HEADER_LEN
            pad_len = (img.IROM_ALIGN - (f.tell() % img.IROM_ALIGN)) + align_past
            if pad_len == 0 or pad_len == img.IROM_ALIGN:
                return 0
            pad_len -= img.SEG_HEADER_LEN
            if pad_len < 0:
                pad_len += img.IROM_ALIGN
            return pad_len

        while len(flash_segments) > 0:
            segment = flash_segments[0]
            pad_len = get_alignment_data_needed(segment)
            if pad_len > 0:
                if len(ram_segments) > 0 and pad_len > img.SEG_HEADER_LEN:
                    pad_segment = ram_segments[0].split_image(pad_len)
                    if len(ram_segments[0].data) == 0:
                        ram_segments.pop(0)
                else:
                    pad_segment = ImageSegment(0, b"\x00" * pad_len, f.tell())
                checksum = img.save_segment(f, pad_segment, checksum, segment.name)
                total_segments += 1
            else:
                assert (f.tell() + 8) % img.IROM_ALIGN == segment.addr % img.IROM_ALIGN
                checksum = img.save_flash_segment(f, segment, checksum)
                flash_segments.pop(0)
                total_segments += 1

        for segment in ram_segments:
            checksum = img.save_segment(f, segment, checksum, segment.name)
            total_segments += 1

        img.append_checksum(f, checksum)
        image_length = f.tell()

        f.seek(1)
        f.write(bytes([total_segments]))

        if img.append_digest:
            f.seek(0)
            digest = hashlib.sha256()
            digest.update(f.read(image_length))
            f.write(digest.digest())

        if img.pad_to_size:
            image_length = f.tell()
            if image_length % img.pad_to_size != 0:
                pad_by = img.pad_to_size - (image_length % img.pad_to_size)
                f.write(b"\xff" * pad_by)

        with open(filename, "wb") as real_file:
            real_file.write(f.getvalue())


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

    def next_cave_va(self, align: int = 4) -> int:
        """VA the next add_cave() call will place its data at, for building
        code that needs to know its own address ahead of time (e.g. Asm
        code that embeds and references string constants after itself)."""
        seg = self.irom_segment()
        cur_len = len(seg.data)
        pad = (-cur_len) % align
        return seg.addr + cur_len + pad

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
        _save_preserving_segment_order(self.img, out_path)
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
