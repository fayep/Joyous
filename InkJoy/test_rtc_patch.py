"""End-to-end emulation of the v8 RTC capture+report patch: run the
capture cave with opendir/readdir stubbed to succeed, confirm the RTC
memory contents it writes, then separately run the report cave with that
RTC state pre-populated and confirm the exact esp_log_write() call it
makes.
"""
from unicorn.riscv_const import UC_RISCV_REG_A0, UC_RISCV_REG_A1, UC_RISCV_REG_A2, UC_RISCV_REG_A3, UC_RISCV_REG_A4, UC_RISCV_REG_A5, UC_RISCV_REG_A6, UC_RISCV_REG_RA, UC_RISCV_REG_PC
from emulate_cave import CaveEmulator, make_return_stub, UcError

DELAY_FN_VA = 0x40815a80
OPENDIR_VA = 0x4200b4c8
READDIR_VA = 0x4200b530
TIMESTAMP_FN_VA = 0x408191bc
LOG_WRITE_VA = 0x408190aa

CAPTURE_HOOK_VA = 0x42022806
CAPTURE_RETURN_VA = 0x42022810
REPORT_HOOK_VA = 0x4200e3c4
REPORT_RETURN_VA = 0x4200e3cc

RTC_ADDR = 0x50000200
FAKE_DIRENT_ADDR = 0x90008000

print("=== stage 1: capture ===")
emu = CaveEmulator("firmware-rtc-capture-patch.bin")
# RTC/LP memory (0x50000000 region) is already mapped by CaveEmulator's
# segment loader, since the real firmware has RTC_IRAM/RTC_DRAM segments there.


def opendir_stub(emu):
    emu.uc.reg_write(UC_RISCV_REG_A0, 0x1234)
    emu.uc.reg_write(UC_RISCV_REG_PC, emu.uc.reg_read(UC_RISCV_REG_RA))


def readdir_stub(emu):
    emu.uc.mem_write(FAKE_DIRENT_ADDR, b"\x00\x00\x00" + b"TESTFILE.BIN\x00")
    emu.uc.reg_write(UC_RISCV_REG_A0, FAKE_DIRENT_ADDR)
    emu.uc.reg_write(UC_RISCV_REG_PC, emu.uc.reg_read(UC_RISCV_REG_RA))


emu.stub(DELAY_FN_VA, make_return_stub("delay_fn"))
emu.stub(OPENDIR_VA, opendir_stub)
emu.stub(READDIR_VA, readdir_stub)

try:
    emu.run(CAPTURE_HOOK_VA, CAPTURE_RETURN_VA, max_insns=2000)
    print("reached CAPTURE_RETURN_VA cleanly")
except UcError:
    print("FAILED")

rtc_bytes = emu.uc.mem_read(RTC_ADDR, 20)
print("RTC memory:", bytes(rtc_bytes))
magic = int.from_bytes(rtc_bytes[0:4], "little")
status = rtc_bytes[4]
name = bytes(rtc_bytes[8:20]).split(b"\x00")[0]
print(f"magic={hex(magic)} status={status} name={name}")

print("\n=== stage 2: report ===")
emu2 = CaveEmulator("firmware-rtc-capture-patch.bin")
# pre-populate RTC memory as if stage 1 already ran
emu2.uc.mem_write(RTC_ADDR, bytes(rtc_bytes))

log_calls = []


def timestamp_stub(emu):
    emu.uc.reg_write(UC_RISCV_REG_A0, 0xDEADBEEF)
    emu.uc.reg_write(UC_RISCV_REG_PC, emu.uc.reg_read(UC_RISCV_REG_RA))


def log_write_stub(emu):
    a0 = emu.uc.reg_read(UC_RISCV_REG_A0)
    a1 = emu.uc.reg_read(UC_RISCV_REG_A1)
    a2 = emu.uc.reg_read(UC_RISCV_REG_A2)
    a3 = emu.uc.reg_read(UC_RISCV_REG_A3)
    a4 = emu.uc.reg_read(UC_RISCV_REG_A4)
    a5 = emu.uc.reg_read(UC_RISCV_REG_A5)
    a6 = emu.uc.reg_read(UC_RISCV_REG_A6)
    tag = emu.read_cstr(a1)
    fmt = emu.read_cstr(a2)
    name = emu.read_cstr(a6)
    log_calls.append((a0, tag, fmt, a3, emu.read_cstr(a4), a5, name))
    print(f"[log_write] level={a0} tag={tag} fmt={fmt!r} timestamp={hex(a3)} "
          f"tag2={emu.read_cstr(a4)} status={a5} name={name}")
    emu.uc.reg_write(UC_RISCV_REG_PC, emu.uc.reg_read(UC_RISCV_REG_RA))


emu2.stub(TIMESTAMP_FN_VA, timestamp_stub)
emu2.stub(LOG_WRITE_VA, log_write_stub)

try:
    emu2.run(REPORT_HOOK_VA, REPORT_RETURN_VA, max_insns=2000)
    print("reached REPORT_RETURN_VA cleanly")
except UcError:
    print("FAILED")

print("\nlog_write called:", len(log_calls), "time(s)")
rtc_after = emu2.uc.mem_read(RTC_ADDR, 4)
print("RTC magic after report (should be cleared/zero):", bytes(rtc_after))
