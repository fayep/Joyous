"""Unicorn-based harness for verifying a patched cave's control flow before
ever flashing real hardware. Scope is deliberately narrow: this doesn't
model any real peripheral (that's a full-system emulator's job, e.g.
Renode) — it maps the firmware's actual IROM/DROM code+rodata (so our
cave's own instructions execute for real and any embedded string reads
are real bytes), then STUBS OUT the handful of external function
addresses our cave calls (opendir/readdir/delay/UART) with Python
callbacks that record what happened and immediately return, rather than
executing their real (unavailable/unmapped) bodies.

This catches exactly the class of bug that cost real device-hang cycles
today: wrong function addresses (no valid mapped code there -> Unicorn
raises immediately instead of silently executing garbage), a return
address that lands mid-instruction (execution deviates from the expected
label trace), and confirms precisely which bytes would hit "UART" for
each of our log strings.

Usage: point STUB_HANDLERS at whatever addresses this cave calls, run,
read the trace.
"""
from __future__ import annotations

import struct

from unicorn import (
    Uc, UC_ARCH_RISCV, UC_MODE_RISCV32,
    UC_HOOK_CODE, UC_HOOK_MEM_UNMAPPED, UcError,
)
from unicorn.riscv_const import (
    UC_RISCV_REG_A0, UC_RISCV_REG_A1, UC_RISCV_REG_A2, UC_RISCV_REG_A3,
    UC_RISCV_REG_RA, UC_RISCV_REG_SP, UC_RISCV_REG_PC,
)

from esptool.bin_image import LoadFirmwareImage

PAGE = 0x1000


def page_align_len(n):
    return (n + PAGE - 1) // PAGE * PAGE


class CaveEmulator:
    def __init__(self, firmware_path: str, chip: str = "esp32c5"):
        self.img = LoadFirmwareImage(chip, firmware_path)
        self.uc = Uc(UC_ARCH_RISCV, UC_MODE_RISCV32)
        self.trace = []
        self.stubs = {}  # addr -> callable(uc, trace) -> None (must set PC to ra itself)
        self._map_segments()
        self._setup_stack()

    def _map_segments(self):
        # page-aligned segment ranges can overlap/duplicate (RTC segments in
        # particular share a page, and adjacent DRAM segments can too) —
        # merge into a flat set of non-overlapping [start,end) page ranges
        # before mapping, then write each segment's real data on top.
        ranges = []
        for seg in self.img.segments:
            base = seg.addr & ~(PAGE - 1)
            pad_front = seg.addr - base
            length = page_align_len(pad_front + len(seg.data))
            ranges.append((base, base + length))
        ranges.sort()
        merged = []
        for start, end in ranges:
            if merged and start <= merged[-1][1]:
                merged[-1] = (merged[-1][0], max(merged[-1][1], end))
            else:
                merged.append((start, end))
        for start, end in merged:
            self.uc.mem_map(start, end - start)
        for seg in self.img.segments:
            self.uc.mem_write(seg.addr, bytes(seg.data))

    def _setup_stack(self):
        self.stack_base = 0x90000000
        self.stack_size = 0x10000
        self.uc.mem_map(self.stack_base, self.stack_size)
        # start sp a little below the top, 16-byte aligned
        sp = (self.stack_base + self.stack_size - 0x100) & ~0xF
        self.uc.reg_write(UC_RISCV_REG_SP, sp)

    def stub(self, addr: int, handler):
        """handler(emu) is called when execution reaches addr; it must
        arrange for PC to continue (typically: read ra, set pc=ra) unless
        it wants to halt (raise UcError / emu.stop()).

        Unicorn only fires a code hook for an address it can actually
        fetch an instruction from, so this maps a placeholder page there
        if nothing's mapped yet (e.g. real ROM addresses, which aren't in
        firmware.bin at all) and fills it with `c.jr ra` (0x8082) — content
        that's never actually executed, since the Python handler runs
        before the fetched instruction and redirects PC itself, but valid
        so the fetch succeeds."""
        base = addr & ~(PAGE - 1)
        try:
            self.uc.mem_map(base, PAGE)
            self.uc.mem_write(base, b"\x82\x80" * (PAGE // 2))  # c.jr ra, x2048
        except UcError:
            pass  # already mapped (e.g. real app code we're intentionally overriding)
        self.stubs[addr] = handler

    def reg(self, name):
        return self.uc.reg_read(getattr(__import__("unicorn.riscv_const", fromlist=["x"]), f"UC_RISCV_REG_{name.upper()}"))

    def read_cstr(self, addr, maxlen=200):
        if addr == 0:
            return None
        out = bytearray()
        for i in range(maxlen):
            b = self.uc.mem_read(addr + i, 1)
            if b == b"\x00":
                break
            out += b
        return bytes(out)

    def _code_hook(self, uc, address, size, user_data):
        self.trace.append(address)
        handler = self.stubs.get(address)
        if handler is not None:
            handler(self)

    def _stop_at(self, addr):
        def _h(emu):
            raise _Halt(addr)
        return _h

    def run(self, start_addr: int, stop_addr: int, max_insns: int = 20000):
        self.stub(stop_addr, self._stop_at(stop_addr))
        self.uc.reg_write(UC_RISCV_REG_PC, start_addr)
        self.uc.hook_add(UC_HOOK_CODE, self._code_hook)
        try:
            self.uc.emu_start(start_addr, 0, count=max_insns)
        except _Halt:
            print(f"reached stop address {hex(stop_addr)} cleanly after {len(self.trace)} instructions")
        except UcError as e:
            pc = self.uc.reg_read(UC_RISCV_REG_PC)
            print(f"UNICORN ERROR at pc={hex(pc)}: {e}")
            print(f"last {min(10, len(self.trace))} instructions executed:")
            for a in self.trace[-10:]:
                print(" ", hex(a))
            raise


class _Halt(Exception):
    pass


def make_return_stub(name, extra=None):
    """Generic stub: log the call (with a0-a3), optionally run `extra`
    for custom behavior, then return via ra (mimicking a normal function
    call/return without executing a real body)."""
    def handler(emu):
        a0 = emu.uc.reg_read(UC_RISCV_REG_A0)
        a1 = emu.uc.reg_read(UC_RISCV_REG_A1)
        pc = emu.uc.reg_read(UC_RISCV_REG_PC)
        print(f"[stub] {name} called at pc={hex(pc)} a0={hex(a0)} a1={hex(a1)}")
        if extra:
            extra(emu, a0, a1)
        ra = emu.uc.reg_read(UC_RISCV_REG_RA)
        emu.uc.reg_write(UC_RISCV_REG_PC, ra)
    return handler


if __name__ == "__main__":
    import sys
    print("This module provides CaveEmulator — import and use from a test script.")
