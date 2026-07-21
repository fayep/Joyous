"""End-to-end emulation of the actual v6 patched cave: opendir/readdir/delay
stubbed, uart_tx_one_char captured, everything else (the byte-transmit
loops) runs as real cave instructions. This is the full dry run before
ever flashing v6 to hardware.
"""
from unicorn.riscv_const import UC_RISCV_REG_A0, UC_RISCV_REG_RA, UC_RISCV_REG_PC

from emulate_cave import CaveEmulator, make_return_stub, UcError

DELAY_FN_VA = 0x40815a80
OPENDIR_VA = 0x4200b4c8
READDIR_VA = 0x4200b530
UART_TX_ONE_CHAR = 0x40000054
HOOK_VA = 0x42022806
RETURN_VA = 0x42022810

FAKE_DIRENT_ADDR = 0x90008000

emu = CaveEmulator("firmware-keypress-sd-patch-v6.bin")

transmitted = bytearray()


def uart_stub(emu):
    ch = emu.uc.reg_read(UC_RISCV_REG_A0) & 0xFF
    transmitted.append(ch)
    ra = emu.uc.reg_read(UC_RISCV_REG_RA)
    emu.uc.reg_write(UC_RISCV_REG_PC, ra)


def opendir_stub(emu):
    emu.uc.reg_write(UC_RISCV_REG_A0, 0x1234)
    ra = emu.uc.reg_read(UC_RISCV_REG_RA)
    emu.uc.reg_write(UC_RISCV_REG_PC, ra)


def readdir_stub(emu):
    name = b"TESTFILE.BIN\x00"
    emu.uc.mem_write(FAKE_DIRENT_ADDR, b"\x00\x00\x00" + name)
    emu.uc.reg_write(UC_RISCV_REG_A0, FAKE_DIRENT_ADDR)
    ra = emu.uc.reg_read(UC_RISCV_REG_RA)
    emu.uc.reg_write(UC_RISCV_REG_PC, ra)


emu.stub(DELAY_FN_VA, make_return_stub("delay_fn"))
emu.stub(OPENDIR_VA, opendir_stub)
emu.stub(READDIR_VA, readdir_stub)
emu.stub(UART_TX_ONE_CHAR, uart_stub)

try:
    emu.run(HOOK_VA, RETURN_VA, max_insns=5000)
    print("\n=== reached RETURN_VA cleanly ===")
except UcError:
    print("\n=== FAILED to reach RETURN_VA ===")

print("\ntransmitted bytes:")
print(repr(bytes(transmitted)))
