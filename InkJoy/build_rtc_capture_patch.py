"""SD-probe v8: two-stage capture/report via LP (RTC) memory, sidestepping
the entire "get esp_log_write/printf to work from a hot-injected context"
problem that patches v1-v7 all ran into in one form or another.

Stage 1 (CAPTURE): the same key-press hook as before (0x42022806 /
0x42022810, confirmed-correct addressing) probes opendir/readdir on
/sdcard and writes the result — magic marker, status byte, filename —
into LP/RTC memory instead of trying to print anything. No log calls at
all in this cave, so none of the runtime-dependency problems apply.

Stage 2 (REPORT): a new hook right after the real, already-proven-working
esp_log_write() call that prints "IJ Compile: %s %s" during early boot
(0x4200e3bc/0x4200e3c0 -> confirmed target 0x408190aa, same LOG_WRITE_VA
as everywhere else in this series — the address was never the problem,
only the calling *context* was). Hooked at the very next instruction
(0x4200e3c4, the timestamp-fetch call for the *next* boot line), this
cave replicates that timestamp call, checks LP memory for our magic
marker, and if present calls esp_log_write with a freshly-fetched
timestamp/tag exactly matching the real macro-expanded convention — since
we're now in the exact context where that's proven to work — then clears
the magic so it doesn't re-report every subsequent boot.

LP/RTC memory: ESP32-C5 has 16KB of LP SRAM (serves the RTC_SLOW_MEM
role). The real firmware only uses the first ~0xf4 bytes of it (per the
image's own RTC segments); 0x50000200 is comfortably clear.

Ground truth:
  opendir()   0x4200b4c8
  readdir()   0x4200b530
  delay-ish function  0x40815a80 (a0=3000, replicated in stage 1)
  esp_log_write()  0x408190aa
  timestamp fn (esp_log_timestamp-ish)  0x408191bc (a0=timestamp, replicated in stage 2)
  IJ_MAIN tag string  0x42162568
  dirent->d_name offset: +3
"""
from fw_patch_tool import Firmware, Asm

DELAY_FN_VA = 0x40815a80
OPENDIR_VA = 0x4200b4c8
READDIR_VA = 0x4200b530
LOG_WRITE_VA = 0x408190aa
TIMESTAMP_FN_VA = 0x408191bc
IJ_MAIN_TAG_VA = 0x42162568

CAPTURE_HOOK_VA = 0x42022806
CAPTURE_RETURN_VA = 0x42022810

REPORT_HOOK_VA = 0x4200e3c4
REPORT_RETURN_VA = 0x4200e3cc

RTC_ADDR = 0x50000200
RTC_MAGIC = 0x53445044  # "SDPD"
FILENAME_MAX = 48  # bytes reserved after the header (magic:4 + status:1 + pad:3)

STR_SDCARD = b"/sdcard\x00"
STR_TAG = b"SDPATCH\x00"
STR_FMT = b"\x1b[0;32mI (%lu) %s: SDPATCH status=%d name=%s\x1b[0m\n\x00"

fw = Firmware.load("firmware.bin")

# --- stage 1: capture cave ---

capture_cave_va = fw.next_cave_va()


def build_capture(cave_va, s):
    asm = Asm(base_va=cave_va)
    asm.li("a0", 3000)
    asm.call(DELAY_FN_VA)

    asm.la("a0", s["sdcard"])
    asm.call(OPENDIR_VA)
    asm.mv("t0", "a0")
    asm.li("t1", 0)  # status = opendir failed (default)
    asm.beq("t0", "zero", "write_status")

    asm.mv("a0", "t0")
    asm.call(READDIR_VA)
    asm.mv("t2", "a0")
    asm.li("t1", 1)  # status = readdir returned null
    asm.beq("t2", "zero", "write_status")

    asm.li("t1", 2)  # status = success
    asm.addi("t3", "t2", 3)  # t3 = &dirent->d_name
    asm.la("t4", RTC_ADDR + 8)
    asm.label("copy_loop")
    asm.lbu("a0", 0, "t3")
    asm.sb("a0", 0, "t4")
    asm.beq("a0", "zero", "write_status")
    asm.addi("t3", "t3", 1)
    asm.addi("t4", "t4", 1)
    asm.j("copy_loop")

    asm.label("write_status")
    asm.la("t5", RTC_ADDR)
    asm.sb("t1", 4, "t5")
    asm.li("t6", RTC_MAGIC)
    asm.sw("t6", 0, "t5")

    asm.call(CAPTURE_RETURN_VA, link="zero")
    return asm


capture_code = build_capture(capture_cave_va, {"sdcard": capture_cave_va}).assemble()
capture_sdcard_va = capture_cave_va + len(capture_code)
capture_code2 = build_capture(capture_cave_va, {"sdcard": capture_sdcard_va}).assemble()
assert len(capture_code2) == len(capture_code)
capture_data = STR_SDCARD + b"\x00" * ((-len(STR_SDCARD)) % 4)
capture_bytes = capture_code2 + capture_data

capture_va_actual = fw.add_cave(capture_bytes, name="rtc_capture_cave")
assert capture_va_actual == capture_cave_va

fw.write_jump(CAPTURE_HOOK_VA, capture_cave_va, name="hook_rtc_capture",
              note="probes /sdcard, writes magic+status+filename to LP mem "
                   f"@{hex(RTC_ADDR)}; no log calls at all in this cave")

# --- stage 2: report cave ---

report_cave_va = fw.next_cave_va()


def build_report(cave_va, s):
    asm = Asm(base_va=cave_va)
    asm.call(TIMESTAMP_FN_VA)
    asm.mv("t1", "a0")  # save timestamp (a0 gets reused below)

    asm.la("t0", RTC_ADDR)
    asm.lw("t2", 0, "t0")
    asm.li("t3", RTC_MAGIC)
    asm.bne("t2", "t3", "done")

    asm.lbu("t4", 4, "t0")       # status byte
    asm.addi("t5", "t0", 8)      # filename ptr

    asm.li("a0", 3)              # level = INFO, matches the real call's convention
    asm.la("a1", s["tag"])
    asm.la("a2", s["fmt"])
    asm.mv("a3", "t1")           # timestamp
    asm.la("a4", s["tag"])       # tag again (matches the real macro's double-tag convention)
    asm.mv("a5", "t4")           # status
    asm.mv("a6", "t5")           # filename
    asm.call(LOG_WRITE_VA)

    asm.sw("zero", 0, "t0")      # clear magic so this only reports once per capture

    asm.label("done")
    asm.call(REPORT_RETURN_VA, link="zero")
    return asm


placeholder = {"tag": report_cave_va, "fmt": report_cave_va}
report_code_len = len(build_report(report_cave_va, placeholder).assemble())
report_tag_va = report_cave_va + report_code_len
report_fmt_va = report_tag_va + len(STR_TAG)
report_data = STR_TAG + STR_FMT
report_data += b"\x00" * ((-len(report_data)) % 4)

report_code = build_report(report_cave_va, {"tag": report_tag_va, "fmt": report_fmt_va}).assemble()
assert len(report_code) == report_code_len

report_bytes = report_code + report_data
report_va_actual = fw.add_cave(report_bytes, name="rtc_report_cave")
assert report_va_actual == report_cave_va

fw.write_jump(REPORT_HOOK_VA, report_cave_va, name="hook_rtc_report",
              note="replicates the timestamp-fetch call this hook skips, "
                   "then reports any pending LP-mem capture via the same "
                   "esp_log_write() call proven working immediately before "
                   "this hook site (the 'IJ Compile' line)")

manifest = fw.save("firmware-rtc-capture-patch.bin")
print("saved firmware-rtc-capture-patch.bin, manifest at", manifest)
print("capture cave at", hex(capture_cave_va), "size", len(capture_bytes), "bytes")
print("report cave at", hex(report_cave_va), "size", len(report_bytes), "bytes")
print("RTC scratch at", hex(RTC_ADDR))
