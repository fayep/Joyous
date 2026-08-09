"""SD-probe v9: everything happens automatically on boot, in the exact
same context proven to work for the "IJ Compile: %s %s" log line — no
button press, no RTC/LP memory persistence, no cross-reset anything.

Hook: 0x4200e3c4 (right after the real, working esp_log_write() call for
"IJ Compile"), same site used by the RTC-persistence test. This time,
unconditionally:
  1. Log "SDPATCH: alive" — a pure sanity check. If this doesn't appear,
     the patch isn't running at all (settles the open question directly,
     no more ambiguity between "not running" and "running but silent").
  2. Probe /sdcard (opendir/readdir) and log the result, same as every
     prior attempt — but from this proven-safe context instead of the
     key-press debounce handler.

Caveat worth watching for: this hook fires very early in boot (right
after "IJ Compile", well before "SD: STA power debug: skip sd_test" and
the SPIFFS mount later in the log) — if /sdcard hasn't been mounted yet
at this point in the boot sequence, opendir() failing here would mean
"too early", not necessarily "no card". First goal is just seeing ANY
SDPATCH output at all; the exact hook timing for a meaningful SD result
can be adjusted once we know logging itself works from here.

Ground truth (all previously verified):
  opendir()   0x4200b4c8
  readdir()   0x4200b530
  esp_log_write()  0x408190aa
  timestamp fn  0x408191bc
  IJ_MAIN tag (reused for our own SDPATCH tag instead)  N/A — new string
Hook site: 0x4200e3c4, 8 bytes. Return: 0x4200e3cc.
"""
from fw_patch_tool import Firmware, Asm

OPENDIR_VA = 0x4200b4c8
READDIR_VA = 0x4200b530
LOG_WRITE_VA = 0x408190aa
TIMESTAMP_FN_VA = 0x408191bc

HOOK_VA = 0x4200e3c4
RETURN_VA = 0x4200e3cc

STR_SDCARD = b"/sdcard\x00"
STR_TAG = b"SDPATCH\x00"
STR_ALIVE = b"\x1b[0;32mI (%lu) %s: SDPATCH alive\x1b[0m\n\x00"
STR_FAIL_OPENDIR = b"\x1b[0;32mI (%lu) %s: SDPATCH opendir failed\x1b[0m\n\x00"
STR_FAIL_READDIR = b"\x1b[0;32mI (%lu) %s: SDPATCH readdir null\x1b[0m\n\x00"
STR_FMT_NAME = b"\x1b[0;32mI (%lu) %s: SDPATCH first file: %s\x1b[0m\n\x00"

fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()


def build_cave(cave_va, s):
    asm = Asm(base_va=cave_va)
    asm.call(TIMESTAMP_FN_VA)
    asm.mv("t1", "a0")  # save timestamp for every log call below

    def log(fmt_va, payload_reg=None):
        asm.li("a0", 3)
        asm.la("a1", s["tag"])
        asm.la("a2", fmt_va)
        asm.mv("a3", "t1")
        asm.la("a4", s["tag"])
        if payload_reg:
            asm.mv("a5", payload_reg)
        asm.call(LOG_WRITE_VA)

    log(s["alive"])

    asm.la("a0", s["sdcard"])
    asm.call(OPENDIR_VA)
    asm.mv("t0", "a0")
    asm.bne("t0", "zero", "opendir_ok")
    log(s["fail_opendir"])
    asm.j("done")

    asm.label("opendir_ok")
    asm.mv("a0", "t0")
    asm.call(READDIR_VA)
    asm.mv("t2", "a0")
    asm.bne("t2", "zero", "readdir_ok")
    log(s["fail_readdir"])
    asm.j("done")

    asm.label("readdir_ok")
    asm.addi("t3", "t2", 3)
    log(s["fmt_name"], payload_reg="t3")

    asm.label("done")
    asm.call(RETURN_VA, link="zero")
    return asm


placeholder = {k: cave_va for k in ("sdcard", "tag", "alive", "fail_opendir", "fail_readdir", "fmt_name")}
code_len = len(build_cave(cave_va, placeholder).assemble())

strings_va = cave_va + code_len
names_and_bytes = [
    ("sdcard", STR_SDCARD), ("tag", STR_TAG), ("alive", STR_ALIVE),
    ("fail_opendir", STR_FAIL_OPENDIR), ("fail_readdir", STR_FAIL_READDIR),
    ("fmt_name", STR_FMT_NAME),
]
string_vas = {}
offset = 0
for name, b in names_and_bytes:
    string_vas[name] = strings_va + offset
    offset += len(b)
data = b"".join(b for _, b in names_and_bytes)
data += b"\x00" * ((-len(data)) % 4)

code = build_cave(cave_va, string_vas).assemble()
assert len(code) == code_len, "instruction count changed between passes"

cave_bytes = code + data
cave_va_actual = fw.add_cave(cave_bytes, name="boot_probe_cave")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_boot_probe",
              note="v9: unconditional boot-time sanity print + SD probe, "
                   "same context proven safe for the IJ Compile log line")

manifest = fw.save("firmware-boot-probe-patch.bin")
print("saved firmware-boot-probe-patch.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
for name, va in string_vas.items():
    print(f"  {name}@{hex(va)}")
