"""Run the actual v5 patched cave under Unicorn, stubbing opendir/readdir/
delay but letting PRINTF_VA (and whatever it calls) execute for real,
to directly observe why no output reaches the console — without
touching hardware.
"""
from unicorn.riscv_const import UC_RISCV_REG_A0, UC_RISCV_REG_A1, UC_RISCV_REG_RA, UC_RISCV_REG_PC

from emulate_cave import CaveEmulator, make_return_stub, UcError

DELAY_FN_VA = 0x40815a80
OPENDIR_VA = 0x4200b4c8
READDIR_VA = 0x4200b530
HOOK_VA = 0x42022806
RETURN_VA = 0x42022810

FAKE_DIRENT_ADDR = 0x90008000  # scratch region within our mapped stack page

emu = CaveEmulator("firmware-keypress-sd-patch-v5.bin")

# 0x40828c1c is BSS (not present in the flash image) — a global read via
# `lui a5,0x40829; lw a0,-0x3e4(a5)` right after an a0==0 check inside
# PRINTF_VA's call chain (0x40816014-0x40816026). Looks like a default
# output-stream pointer populated during real boot, before our hook could
# ever fire — our cold emulation never ran that init code, so it's
# unmapped. Map a scratch page there and pre-fill it with a plausible fake
# "stream" struct so we can see how much further execution goes once this
# isn't the blocker.
FAKE_STREAM_ADDR = 0x90009000
emu.uc.mem_map(0x40828000, 0x2000)
emu.uc.mem_write(0x40828c1c, FAKE_STREAM_ADDR.to_bytes(4, "little"))
emu.uc.mem_write(FAKE_STREAM_ADDR, b"\x00" * 0x80)  # zeroed struct: flags=0, etc.
# (FAKE_STREAM_ADDR is already inside the stack region CaveEmulator mapped)

# 0x4082934c: another BSS global, checked in what looks like a lazy
# mutex-init pattern right after the ROM string-ish call. Set nonzero so
# the "already initialized" path is taken and we skip whatever real
# init call would otherwise be needed.
emu.uc.mem_write(0x4082934c, (1).to_bytes(4, "little"))

# stub opendir: return a nonzero fake DIR* so we proceed to readdir
def opendir_stub(emu):
    print(f"[stub] opendir(a0={hex(emu.uc.reg_read(UC_RISCV_REG_A0))}) -> fake handle")
    emu.uc.reg_write(UC_RISCV_REG_A0, 0x1234)
    ra = emu.uc.reg_read(UC_RISCV_REG_RA)
    emu.uc.reg_write(UC_RISCV_REG_PC, ra)

# stub readdir: write a fake dirent (d_name at +3) into scratch memory, return its address
def readdir_stub(emu):
    print(f"[stub] readdir(a0={hex(emu.uc.reg_read(UC_RISCV_REG_A0))}) -> fake dirent")
    name = b"TESTFILE.BIN\x00"
    emu.uc.mem_write(FAKE_DIRENT_ADDR, b"\x00\x00\x00" + name)
    emu.uc.reg_write(UC_RISCV_REG_A0, FAKE_DIRENT_ADDR)
    ra = emu.uc.reg_read(UC_RISCV_REG_RA)
    emu.uc.reg_write(UC_RISCV_REG_PC, ra)

emu.stub(DELAY_FN_VA, make_return_stub("delay_fn"))
emu.stub(OPENDIR_VA, opendir_stub)
emu.stub(READDIR_VA, readdir_stub)
# 0x400004d8: a mask-ROM function (silicon-resident, not in firmware.bin at
# all — Unicorn correctly reports it unmapped). Stub generically for now;
# revisit if the trace shows it matters for reaching the actual UART call.
emu.stub(0x400004d8, make_return_stub("rom_fn_400004d8"))

# NOT stubbing PRINTF_VA (0x4213da56) or its callee (0x4213d9c0) — let them
# run for real so we can see exactly what they do / where they fail.

try:
    emu.run(HOOK_VA, RETURN_VA, max_insns=5000)
    print("\n=== SUCCESS: cave ran to completion cleanly ===")
except UcError:
    print("\n=== cave did NOT reach RETURN_VA cleanly (see error above) ===")
