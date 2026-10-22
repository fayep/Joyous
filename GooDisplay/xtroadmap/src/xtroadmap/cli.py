"""xtroadmap — static Capstone map of an ESP32-S3 firmware blob."""
from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

from xtroadmap.image import Image
from xtroadmap.registry import Registry
from xtroadmap.xtensa import disasm_one, l32r_value, looks_like_entry

_DUMPS = Path(__file__).resolve().parents[3] / "dumps"
_DEFAULT_IMAGE = _DUMPS / "wifi_firmware_app0_segments"
_DEFAULT_APP = _DUMPS / "app0.bin"


def _default_image() -> Path:
    env = os.environ.get("XTROADMAP_IMAGE")
    if env:
        return Path(env)
    if _DEFAULT_IMAGE.is_dir():
        return _DEFAULT_IMAGE
    if _DEFAULT_APP.is_file():
        return _DEFAULT_APP
    return Path("app0.bin")


def _registry(path: str | None) -> Registry:
    p = path or os.environ.get("XTROADMAP_REGISTRY") or "xtroadmap_registry.json"
    return Registry(p)


def _load(path: str | None) -> Image:
    return Image.load(path or _default_image())


def _parse_va(token: str, registry: Registry) -> int:
    t = token.strip()
    if t.startswith("0x") or t.startswith("0X") or t.isdigit():
        return int(t, 0)
    for key, entry in registry.data.get("func", {}).items():
        if entry.get("name") == t or entry.get("alias") == t:
            return int(key, 16)
    raise ValueError(f"unknown function {token!r} (need a VA, F-name, or alias)")


def _cmd_roadmap(ns: argparse.Namespace) -> int:
    img = _load(ns.image)
    reg = _registry(ns.registry)
    try:
        entry = _parse_va(ns.address, reg)
    except ValueError as e:
        print(e, file=sys.stderr)
        return 2
    from xtroadmap.roadmap import build

    try:
        build(
            entry,
            img,
            reg,
            max_functions=ns.max_functions,
            max_insns_per_func=ns.max_insns_per_func,
            preseed=ns.preseed,
            listing=ns.listing,
        )
    except ValueError as e:
        print(e, file=sys.stderr)
        return 1
    return 0


def _cmd_disas(ns: argparse.Namespace) -> int:
    img = _load(ns.image)
    va = int(ns.address, 0)
    n = ns.count
    printed = 0
    cur = va
    while printed < n:
        insn = disasm_one(img, cur)
        if insn is None:
            raw = img.read(cur, 4)
            print(f"0x{cur:x}\t<undecodable>  bytes={raw.hex() if raw else '?'}")
            break
        extra = ""
        if insn.mnemonic == "l32r":
            _slot, val, s = l32r_value(img, insn)
            if s:
                extra = f'\t; "{s}"'
            elif val is not None:
                extra = f"\t; {val:#x}"
        print(f"0x{insn.address:x}\t{insn.text}{extra}")
        cur += insn.size
        printed += 1
    return 0


def _cmd_strings(ns: argparse.Namespace) -> int:
    img = _load(ns.image)
    needle = (ns.grep or "").lower()
    n = 0
    for va, s in img.iter_strings(min_len=ns.min_len):
        if needle and needle not in s.lower():
            continue
        print(f"{va:#010x}\t{s}")
        n += 1
        if ns.limit and n >= ns.limit:
            break
    return 0


def _cmd_xref(ns: argparse.Namespace) -> int:
    """Find literal-pool slots holding this VA, and any `entry` that l32r's them."""
    img = _load(ns.image)
    target = int(ns.address, 0)
    slots = img.find_u32(target)
    print(f"pointer slots for {target:#x}: {len(slots)}")
    for slot in slots:
        print(f"  slot {slot:#010x}")
    if not slots:
        return 0
    want = set(slots)
    # Scan code segments for l32r whose pool VA is one of those slots.
    from xtroadmap.xtensa import classify, resolve_target

    hits = []
    for seg in img.segments:
        if not (0x40000000 <= seg.addr < 0x50000000):
            continue
        data = seg.data
        for i in range(len(data) - 2):
            if (data[i] & 0x0F) != 0x1:
                continue
            va = seg.addr + i
            insn = disasm_one(img, va)
            if insn is None or classify(insn) != "l32r":
                continue
            slot = resolve_target(img, insn)
            if slot is not None and (slot & ~3) in want:
                hits.append((insn.address, slot & ~3))
    print(f"l32r sites: {len(hits)}")
    for addr, slot in hits:
        owner = _owning_entry(img, addr)
        print(f"  {addr:#010x}  slot {slot:#x}  entry {owner and hex(owner)}")
    return 0


def _owning_entry(img: Image, va: int) -> int | None:
    for back in range(0, 0x800):
        a = va - back
        if looks_like_entry(img, a):
            return a
    return None


def main(argv: list[str] | None = None) -> None:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--image", default=None, help="app0.bin or extracted segments dir")
    p.add_argument("--registry", default=None, help="xtroadmap_registry.json")
    sub = p.add_subparsers(dest="cmd", required=True)

    r = sub.add_parser("roadmap", help="worklist-driven function-at-a-time map")
    r.add_argument("address", help="function entry: VA, F-name, or alias")
    r.add_argument("--max-functions", type=int, default=40)
    r.add_argument("--max-insns-per-func", type=int, default=8_000)
    r.add_argument(
        "--preseed",
        action=argparse.BooleanOptionalAction,
        default=True,
    )
    r.add_argument("--listing", action="store_true")
    r.set_defaults(func=_cmd_roadmap)

    d = sub.add_parser("disas", help="linear disassembly from a VA")
    d.add_argument("address")
    d.add_argument("-n", "--count", type=int, default=32)
    d.set_defaults(func=_cmd_disas)

    s = sub.add_parser("strings", help="printable C strings in the image")
    s.add_argument("--grep", default=None)
    s.add_argument("--min-len", type=int, default=4)
    s.add_argument("--limit", type=int, default=0)
    s.set_defaults(func=_cmd_strings)

    x = sub.add_parser("xref", help="l32r / pointer-slot xrefs to a VA")
    x.add_argument("address")
    x.set_defaults(func=_cmd_xref)

    ns = p.parse_args(argv)
    # image/registry live on the parent; subcommands read ns.image
    raise SystemExit(ns.func(ns))


if __name__ == "__main__":
    main()
