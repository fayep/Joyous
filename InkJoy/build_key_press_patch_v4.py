"""SD-probe patch v4: same corrected hook/return addressing as v3, but all
logging switched from esp_log_write() to the plain printf-like function at
0x4213da5e (the one that's printed "key_task: KEY_STATE_PRESSED" reliably
on every single test in this series).

v3 (esp_log_write, corrected addressing) ran with zero crashes but zero
visible output — isolation patch #5 confirmed esp_log_write() itself is
being silently filtered (likely ESP-IDF's newer log system keying runtime
level checks off a compile-time table of known format strings that our
injected strings were never part of), not a safety issue.

PRINTF_VA takes a single string pointer and prints it raw — no %s
substitution — so the filename gets its own separate call right after a
static "first file: " prefix, instead of one esp_log_write call with a
vararg. Two/three back-to-back calls to it are safe (proven by v3/patch4
running cleanly with multiple calls once the addressing bug was fixed).

Ground truth:
  opendir()   0x4200b4d0
  readdir()   0x4200b538
  printf-like logger  0x4213da5e
  delay-ish function  0x40815a80 (a0=3000, replicated — this hook skips it)
  dirent->d_name offset: +3
Hook site: 0x42022806, 8 bytes. Return: 0x42022810 (corrected boundary).
"""
from fw_patch_tool import Firmware, Asm

PRINTF_VA = 0x4213da5e
DELAY_FN_VA = 0x40815a80
OPENDIR_VA = 0x4200b4d0
READDIR_VA = 0x4200b538

HOOK_VA = 0x42022806
RETURN_VA = 0x42022810

STR_SDCARD = b"/sdcard\x00"
STR_BEFORE = b"SDPATCH: probing /sdcard\r\n\x00"
STR_FAIL_OPENDIR = b"SDPATCH: opendir failed\r\n\x00"
STR_FAIL_READDIR = b"SDPATCH: readdir returned null\r\n\x00"
STR_PREFIX = b"SDPATCH: first file: \x00"
STR_NEWLINE = b"\r\n\x00"


def build_cave(cave_va, s):
    asm = Asm(base_va=cave_va)
    asm.li("a0", 3000)
    asm.call(DELAY_FN_VA)

    asm.la("a0", s["before"])
    asm.call(PRINTF_VA)

    asm.la("a0", s["sdcard"])
    asm.call(OPENDIR_VA)
    asm.mv("t0", "a0")
    asm.bne("t0", "zero", "opendir_ok")
    asm.la("a0", s["fail_opendir"])
    asm.call(PRINTF_VA)
    asm.j("done")

    asm.label("opendir_ok")
    asm.mv("a0", "t0")
    asm.call(READDIR_VA)
    asm.mv("t1", "a0")
    asm.bne("t1", "zero", "readdir_ok")
    asm.la("a0", s["fail_readdir"])
    asm.call(PRINTF_VA)
    asm.j("done")

    asm.label("readdir_ok")
    asm.addi("t2", "t1", 3)
    asm.la("a0", s["prefix"])
    asm.call(PRINTF_VA)
    asm.mv("a0", "t2")
    asm.call(PRINTF_VA)
    asm.la("a0", s["newline"])
    asm.call(PRINTF_VA)

    asm.label("done")
    asm.call(RETURN_VA, link="zero")
    return asm


fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()

placeholder = {k: cave_va for k in ("sdcard", "before", "fail_opendir", "fail_readdir", "prefix", "newline")}
code_len = len(build_cave(cave_va, placeholder).assemble())

strings_va = cave_va + code_len
names_and_bytes = [
    ("sdcard", STR_SDCARD), ("before", STR_BEFORE), ("fail_opendir", STR_FAIL_OPENDIR),
    ("fail_readdir", STR_FAIL_READDIR), ("prefix", STR_PREFIX), ("newline", STR_NEWLINE),
]
string_vas = {}
offset = 0
for name, b in names_and_bytes:
    string_vas[name] = strings_va + offset
    offset += len(b)
data = b"".join(b for _, b in names_and_bytes)
data += b"\x00" * ((-len(data)) % 4)

code = build_cave(cave_va, string_vas).assemble()
assert len(code) == code_len

cave_bytes = code + data
cave_va_actual = fw.add_cave(cave_bytes, name="log_first_sd_file_on_keypress_v4")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_keypress_sd_probe_v4",
              note="v4: switched all logging from esp_log_write (silently "
                   "filtered per isolation patch #5) to the plain printf-like "
                   "logger at 0x4213da5e, which has printed reliably on every "
                   "test in this series")

manifest = fw.save("firmware-keypress-sd-patch-v4.bin")
print("saved firmware-keypress-sd-patch-v4.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
for name, va in string_vas.items():
    print(f"  {name}@{hex(va)}")
