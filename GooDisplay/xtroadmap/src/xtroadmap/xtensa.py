"""Xtensa decode helpers on top of Capstone 6.

Capstone's printed operands are good enough for classification and for
l32r (absolute literal-pool VA), but CALLN's `. +imm` is *not* a simple
PC-relative add — use the ISA encoding. Branches and `j` print
`. +imm` / `. -imm` that *do* add to the instruction address
(confirmed against destinations that land on real `entry` / body).
"""
from __future__ import annotations

import re
from dataclasses import dataclass

import capstone

from xtroadmap.image import Image

_MD = capstone.Cs(capstone.CS_ARCH_XTENSA, capstone.CS_MODE_LITTLE_ENDIAN)
_MD.detail = False

_REL = re.compile(r"\.\s*([+-](?:0x[0-9a-fA-F]+|\d+))$")
_ABS = re.compile(r"0x[0-9a-fA-F]+")

_CALL = {"call0", "call4", "call8", "call12"}
_CALLX = {"callx0", "callx4", "callx8", "callx12"}
_RET = {"retw", "retw.n", "ret", "ret.n"}
_JUMP = {"j"}
_INDIR_JUMP = {"jx"}


@dataclass
class Insn:
    address: int
    size: int
    mnemonic: str
    op_str: str

    @property
    def text(self) -> str:
        return f"{self.mnemonic} {self.op_str}".strip()


def disasm_one(img: Image, va: int) -> Insn | None:
    raw = img.read(va, 4)
    if not raw:
        return None
    got = list(_MD.disasm(raw, va))
    if not got:
        return None
    i = got[0]
    return Insn(i.address, i.size, i.mnemonic, i.op_str)


def is_entry(insn: Insn) -> bool:
    return insn.mnemonic == "entry"


def looks_like_entry(img: Image, va: int) -> bool:
    insn = disasm_one(img, va)
    return bool(insn and is_entry(insn))


def classify(insn: Insn) -> str:
    m = insn.mnemonic
    if m == "entry":
        return "entry"
    if m in _RET:
        return "ret"
    if m in _CALL:
        return "call"
    if m in _CALLX:
        return "callx"
    if m in _JUMP:
        return "jump"
    if m in _INDIR_JUMP:
        return "jx"
    if m == "l32r":
        return "l32r"
    if m.startswith("loop"):
        return "loop"
    if m.startswith("b") and m not in {"break", "break.n"}:
        return "branch"
    return "other"


def calln_target(va: int, raw3: bytes) -> int | None:
    """Absolute target of a 24-bit CALLN (call4/call8/call12).

    word = b0 | b1<<8 | b2<<16
    op0 must be 5; n in bits[5:4] is 1/2/3 (call4/8/12)
    offset18 = signed bits[23:6]
    target = align4(va + 3) + offset18 * 4
    """
    if len(raw3) < 3:
        return None
    word = raw3[0] | (raw3[1] << 8) | (raw3[2] << 16)
    if (word & 0xF) != 0x5:
        return None
    n = (word >> 4) & 3
    if n == 0:
        return None
    off = word >> 6
    if off & (1 << 17):
        off -= 1 << 18
    return ((va + 3) & ~3) + off * 4


def _rel_or_abs(insn: Insn) -> int | None:
    """Parse Capstone's last operand as `. +imm` (add to VA) or `0xABS`."""
    ops = insn.op_str.strip()
    last = ops.split(",")[-1].strip()
    m = _REL.fullmatch(last)
    if m:
        return (insn.address + int(m.group(1), 0)) & 0xFFFFFFFF
    m = _ABS.search(last)
    if m:
        return int(m.group(0), 16) & 0xFFFFFFFF
    return None


def resolve_target(img: Image, insn: Insn) -> int | None:
    kind = classify(insn)
    if kind == "call":
        raw = img.read(insn.address, 3)
        if raw is None:
            return None
        return calln_target(insn.address, raw)
    if kind in ("jump", "branch", "l32r"):
        return _rel_or_abs(insn)
    return None


def l32r_value(img: Image, insn: Insn) -> tuple[int | None, int | None, str | None]:
    """Return (literal_slot_va, loaded_u32, cstring_or_none)."""
    slot = resolve_target(img, insn)
    if slot is None:
        return None, None, None
    slot &= ~3
    val = img.read_u32(slot)
    s = img.read_cstr(val) if val is not None else None
    return slot, val, s


def dest_reg(op_str: str) -> str | None:
    """First operand when it's a register name (`a10`, `a2`, …)."""
    first = op_str.split(",")[0].strip()
    if re.fullmatch(r"a\d+", first):
        return first
    return None


def looks_like_log_tag(s: str) -> bool:
    if not (2 <= len(s) <= 32):
        return False
    if any(c in s for c in "%\x1b\n\r\t /\\"):
        return False
    return all(c.isalnum() or c in "._-" for c in s)
