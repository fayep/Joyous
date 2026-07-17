"""Verify a cave that calls the ROM's uart_tx_one_char (0x40000054, per
esp-idf's esp32c5.rom.ld) directly, one byte at a time, bypassing the
entire C-library/RTOS logging stack that test_v5_cave.py showed is
impractical to fake in a scoped emulator.

Not testing a real patched firmware file here — just the cave-building
logic itself, standalone, since this doesn't need to touch app code at
all (ROM functions have no dependency on our patch or its hook site).
"""
from unicorn import Uc, UC_ARCH_RISCV, UC_MODE_RISCV32, UC_HOOK_CODE, UcError
from unicorn.riscv_const import UC_RISCV_REG_A0, UC_RISCV_REG_RA, UC_RISCV_REG_PC, UC_RISCV_REG_SP

from fw_patch_tool import Asm

UART_TX_ONE_CHAR = 0x40000ac4
CAVE_VA = 0x42152b94  # arbitrary — just needs to be in a mapped, writable+executable region for the test

MESSAGE = b"SDPATCH: probing /sdcard\r\n\x00"
msg_va = CAVE_VA + 0x200  # plenty of room after the code

asm = Asm(base_va=CAVE_VA)
asm.la("t0", msg_va)
asm.label("loop")
asm.lbu("a0", 0, "t0")
asm.beq("a0", "zero", "done")
asm.call(UART_TX_ONE_CHAR)
asm.addi("t0", "t0", 1)
asm.j("loop")
asm.label("done")
asm.ret()
code = asm.assemble()

uc = Uc(UC_ARCH_RISCV, UC_MODE_RISCV32)
uc.mem_map(CAVE_VA & ~0xFFF, 0x2000)
uc.mem_write(CAVE_VA, code)
uc.mem_write(msg_va, MESSAGE)

uc.mem_map(UART_TX_ONE_CHAR & ~0xFFF, 0x1000)
uc.mem_write(UART_TX_ONE_CHAR & ~0xFFF, b"\x82\x80" * 0x800)  # c.jr ra placeholders

transmitted = bytearray()

def uart_hook(uc, address, size, _):
    if address == UART_TX_ONE_CHAR:
        ch = uc.reg_read(UC_RISCV_REG_A0) & 0xFF
        transmitted.append(ch)
        ra = uc.reg_read(UC_RISCV_REG_RA)
        uc.reg_write(UC_RISCV_REG_PC, ra)

uc.hook_add(UC_HOOK_CODE, uart_hook)

sp = 0x90008000
uc.mem_map(0x90000000, 0x10000)
uc.reg_write(UC_RISCV_REG_SP, sp)
uc.reg_write(UC_RISCV_REG_RA, 0x41414140)  # sentinel: if we ever return here, we're done
uc.mem_map(0x41414000, 0x1000)
uc.mem_write(0x41414000, b"\x82\x80" * 0x800)

try:
    uc.emu_start(CAVE_VA, 0x41414140, count=2000)
except UcError as e:
    print("emulation error:", e)

print("bytes transmitted:", bytes(transmitted))
print("matches message:", bytes(transmitted) == MESSAGE[:-1])
