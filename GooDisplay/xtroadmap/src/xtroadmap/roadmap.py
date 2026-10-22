"""Function-at-a-time static roadmap.

Same session shape as gerald.roadmap: exhaust one function's own
reachable code (both arms of every direct branch, every direct jump),
queue callees instead of walking into them, persist F/L names.

No emulator. Indirect `callx*` / `jx` are recorded and left unresolved.
`l32r` literal-pool loads are resolved so call sites and functions can
pick up C-string names the way gerald used live a0..a3.
"""
from __future__ import annotations

from collections import deque

from xtroadmap.image import Image
from xtroadmap.registry import Registry
from xtroadmap.xtensa import (
    classify,
    dest_reg,
    disasm_one,
    is_entry,
    l32r_value,
    looks_like_entry,
    looks_like_log_tag,
    resolve_target,
)


def _format_cstr(s: str, limit: int = 48) -> str:
    if len(s) > limit:
        s = s[: limit - 3] + "..."
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def _walk_one(
    img: Image,
    func_va: int,
    registry: Registry,
    max_insns: int,
) -> tuple[list[str], set[int], str]:
    work: deque[int] = deque([func_va])
    seen: set[int] = set()
    rows: list[tuple[int, str, str]] = []
    discovered: set[int] = set()
    last_defs: dict[str, str] = {}
    tags: set[str] = set()
    n = 0
    reason = "exhausted"

    while work and n < max_insns:
        va = work.popleft()
        if va in seen:
            continue
        insn = disasm_one(img, va)
        if insn is None:
            if va != func_va:
                reason = f"undecodable @0x{va:x} (literal pool or unmapped)"
            else:
                reason = f"not an instruction @0x{va:x}"
            break
        if va != func_va and is_entry(insn):
            # Walked into the next function's prologue — stop this path.
            continue

        seen.add(va)
        n += 1
        kind = classify(insn)
        comment = ""
        nxt = va + insn.size

        if kind == "l32r":
            slot, val, s = l32r_value(img, insn)
            reg = dest_reg(insn.op_str)
            if s:
                comment = f"; {_format_cstr(s)}"
                if reg:
                    last_defs[reg] = s
                if looks_like_log_tag(s):
                    tags.add(s)
            elif val is not None:
                comment = f"; [{slot:#x}] = {val:#x}"
                if reg and img.is_code(val) and looks_like_entry(img, val):
                    last_defs[reg] = f"fn:{val:#x}"
                    discovered.add(val)
                    registry.name_func(val, note=f"code pointer from l32r @0x{va:x}")
                    comment += f" {registry.display(val)}"
            work.append(nxt)

        elif kind == "call":
            tgt = resolve_target(img, insn)
            if tgt is None:
                comment = "; call target not resolvable"
            else:
                if img.is_code(tgt) and looks_like_entry(img, tgt):
                    discovered.add(tgt)
                    registry.name_func(tgt)
                    registry.note_call(func_va, tgt)
                    comment = f"; call to {registry.display(tgt)}"
                elif img.is_code(tgt):
                    comment = f"; call to 0x{tgt:x} (no entry; not queued)"
                else:
                    comment = f"; call to 0x{tgt:x} (outside image — ROM/stub)"
                args = []
                for r in ("a10", "a11", "a12", "a13", "a14", "a15"):
                    if r in last_defs:
                        v = last_defs[r]
                        if v.startswith("fn:"):
                            args.append(f"{r}={v[3:]}")
                        elif v[:1] in "-0123456789":
                            args.append(f"{r}={v}")
                        else:
                            args.append(f"{r}={_format_cstr(v)}")
                if args:
                    comment += " [" + ", ".join(args) + "]"
            for r in ("a10", "a11", "a12", "a13", "a14", "a15"):
                last_defs.pop(r, None)
            work.append(nxt)

        elif kind == "callx":
            comment = f"; indirect {insn.op_str.strip()}"
            for r in ("a10", "a11", "a12", "a13", "a14", "a15"):
                last_defs.pop(r, None)
            work.append(nxt)

        elif kind == "jump":
            tgt = resolve_target(img, insn)
            if tgt is None:
                comment = "; jump target not resolvable"
            elif looks_like_entry(img, tgt) and tgt != func_va:
                discovered.add(tgt)
                registry.name_func(tgt, note=f"tail from 0x{func_va:x}")
                registry.note_call(func_va, tgt)
                comment = f"; tail to {registry.display(tgt)}"
            else:
                registry.name_label(tgt, note=f"jump from 0x{va:x}")
                comment = f"; -> {registry.display(tgt)}"
                work.append(tgt)

        elif kind == "jx":
            comment = f"; indirect jump {insn.op_str.strip()}"

        elif kind == "branch":
            tgt = resolve_target(img, insn)
            if tgt is not None:
                registry.name_label(tgt, note=f"branch from 0x{va:x}")
                comment = f"; -> {registry.display(tgt)}"
                work.append(tgt)
            work.append(nxt)

        elif kind == "ret":
            comment = "; return"
        else:
            reg = dest_reg(insn.op_str)
            if insn.mnemonic in ("movi", "movi.n") and reg:
                imm = insn.op_str.split(",")[-1].strip()
                last_defs[reg] = imm
            elif insn.mnemonic in ("mov.n", "mov") and reg:
                src = insn.op_str.split(",")[-1].strip()
                if src in last_defs:
                    last_defs[reg] = last_defs[src]
                else:
                    last_defs.pop(reg, None)
            work.append(nxt)

        rows.append((va, insn.text, comment))

    if n >= max_insns:
        reason = f"max-insns ({max_insns})"
    elif not work and reason == "exhausted":
        reason = "exhausted"
    if len(tags) == 1:
        registry.set_alias(func_va, next(iter(tags)), source="logtag")

    rows.sort(key=lambda r: r[0])
    lines: list[str] = []
    labeled = {int(k, 16) for k in registry.data["label"]}
    for addr, text, comment in rows:
        if addr in labeled and addr != func_va:
            lab = registry.data["label"][hex(addr)]["name"]
            lines.append(f"{lab}:")
        if comment:
            lines.append(f"0x{addr:x}\t{text}\t{comment}")
        else:
            lines.append(f"0x{addr:x}\t{text}")
    return lines, discovered, reason


def build(
    entry_va: int,
    img: Image,
    registry: Registry,
    *,
    max_functions: int = 40,
    max_insns_per_func: int = 8_000,
    preseed: bool = True,
    listing: bool = False,
) -> dict[int, tuple[list[str], str]]:
    if not looks_like_entry(img, entry_va):
        insn = disasm_one(img, entry_va)
        what = insn.text if insn else "unmapped/undecodable"
        raise ValueError(f"{entry_va:#x} is not an `entry` prologue ({what})")

    registry.name_func(entry_va, note="roadmap seed")
    worklist: deque[int] = deque([entry_va])
    queued: set[int] = {entry_va}

    def enqueue(va: int) -> bool:
        if va in queued:
            return False
        if not img.is_code(va) or not looks_like_entry(img, va):
            queued.add(va)
            return False
        queued.add(va)
        i = 0
        while i < len(worklist) and worklist[i] < va:
            i += 1
        worklist.insert(i, va)
        return True

    if preseed:
        n_pre = 0
        for va in registry.func_vas():
            if va == entry_va:
                continue
            if va in queued or not img.is_code(va) or not looks_like_entry(img, va):
                queued.add(va)
                continue
            queued.add(va)
            worklist.append(va)
            n_pre += 1
        if n_pre:
            print(f"(preseeded {n_pre} registry function(s); entry 0x{entry_va:x} first)")

    results: dict[int, tuple[list[str], str]] = {}
    while worklist and len(results) < max_functions:
        func_va = worklist.popleft()
        name = registry.display(func_va)
        if listing:
            print(f"\n=== {name} ===")
        with registry.deferred():
            lines, discovered, reason = _walk_one(
                img, func_va, registry, max_insns_per_func
            )
            n_new = 0
            for tgt in discovered:
                registry.name_func(tgt)
                if enqueue(tgt):
                    n_new += 1
        name = registry.display(func_va)
        if listing:
            for line in lines:
                print(line)
            print(f"--- {name} done: {reason} ({len(lines)} lines) ---")
        else:
            parts = [name, f"{len(lines)} lines", reason]
            if n_new:
                parts.append(f"+{n_new} queued")
            parts.append(f"{len(results) + 1}/{max_functions}")
            if worklist:
                parts.append(f"{len(worklist)} left")
            print("  ".join(parts))
        results[func_va] = (lines, reason)

    left = len(worklist)
    print(
        f"\n=== roadmap session done: {len(results)} function(s) processed"
        + (f", {left} still queued (max_functions reached)" if left else "")
        + " ==="
    )
    return results
