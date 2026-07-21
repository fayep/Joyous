"""Minimal, standalone test of one specific assumption: does LP/RTC memory
(0x50000200) actually survive a software reset on this chip/config?

No button press needed — only the report hook is patched. Logic:
  - On boot, check for our magic at RTC_ADDR.
  - If ABSENT: this is the first boot since flashing. Write the magic
    with a sentinel status (77), do NOT report anything.
  - If PRESENT: a previous boot already wrote it — memory persisted
    across whatever reset got us here. Report status=77 to confirm,
    then refresh the magic for the next cycle.

If persistence works: boot 1 shows nothing new, boot 2+ (any reset —
double-click, OTA re-flash, whatever) shows "SDPATCH status=77
name=(none)" every time. If it never appears no matter how many reboots,
LP memory isn't surviving resets the way assumed, and the RTC-capture
design needs to change (e.g. write to a flash-backed location instead).
"""
from fw_patch_tool import Firmware, Asm

LOG_WRITE_VA = 0x408190aa
TIMESTAMP_FN_VA = 0x408191bc

REPORT_HOOK_VA = 0x4200e3c4
REPORT_RETURN_VA = 0x4200e3cc

RTC_ADDR = 0x50000200
RTC_MAGIC = 0x53445044  # "SDPD"
SENTINEL_STATUS = 77

STR_TAG = b"SDPATCH\x00"
STR_FMT = b"\x1b[0;32mI (%lu) %s: SDPATCH status=%d (persistence test)\x1b[0m\n\x00"

fw = Firmware.load("firmware.bin")
report_cave_va = fw.next_cave_va()


def build_report(cave_va, s):
    asm = Asm(base_va=cave_va)
    asm.call(TIMESTAMP_FN_VA)
    asm.mv("t1", "a0")

    asm.la("t0", RTC_ADDR)
    asm.lw("t2", 0, "t0")
    asm.li("t3", RTC_MAGIC)
    asm.bne("t2", "t3", "first_boot")

    # magic present: report, then refresh for next cycle
    asm.li("a0", 3)
    asm.la("a1", s["tag"])
    asm.la("a2", s["fmt"])
    asm.mv("a3", "t1")
    asm.la("a4", s["tag"])
    asm.li("a5", SENTINEL_STATUS)
    asm.call(LOG_WRITE_VA)
    asm.j("write_magic")

    asm.label("first_boot")
    # nothing to report yet, just write the magic for next time

    asm.label("write_magic")
    asm.li("t4", SENTINEL_STATUS)
    asm.sb("t4", 4, "t0")
    asm.sw("t3", 0, "t0")

    asm.call(REPORT_RETURN_VA, link="zero")
    return asm


placeholder = {"tag": report_cave_va, "fmt": report_cave_va}
code_len = len(build_report(report_cave_va, placeholder).assemble())
tag_va = report_cave_va + code_len
fmt_va = tag_va + len(STR_TAG)
data = STR_TAG + STR_FMT
data += b"\x00" * ((-len(data)) % 4)

code = build_report(report_cave_va, {"tag": tag_va, "fmt": fmt_va}).assemble()
assert len(code) == code_len

cave_bytes = code + data
cave_va_actual = fw.add_cave(cave_bytes, name="rtc_persistence_test")
assert cave_va_actual == report_cave_va

fw.write_jump(REPORT_HOOK_VA, report_cave_va, name="hook_rtc_persistence_test",
              note="isolated test: does LP mem at 0x50000200 survive a "
                   "reset at all, independent of the capture/opendir logic")

manifest = fw.save("firmware-rtc-persistence-test.bin")
print("saved firmware-rtc-persistence-test.bin, manifest at", manifest)
print("cave at", hex(report_cave_va), "size", len(cave_bytes), "bytes")
