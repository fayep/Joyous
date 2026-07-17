"""SD-probe patch v5: v4 with a second, more fundamental addressing bug
fixed. Every function address used across this whole patch series
(opendir, readdir, esp_log_write, this printf-like logger) was resolved
early on via raw `data[file_offs:...]` slicing — but a segment's
`file_offs` (from any source, esptool's own image-info included) points
at that segment's 8-byte header, not its data. Every one of those
addresses was silently 8 bytes into the real function instead of at its
entry — calling opendir/readdir mid-prologue skips part of their own
register-save sequence, which is exactly the kind of thing that can look
like it "mostly works" until it doesn't.

Re-verified all four this time using esptool's own parsed `segment.data`
(no manual offset arithmetic at all), and confirmed each corrected
address has a clean, real function prologue:
  opendir()            0x4200b4c8  (was wrongly 0x4200b4d0)
  readdir()             0x4200b530  (was wrongly 0x4200b538)
  esp_log_write()-ish   0x408190aa  (was wrongly 0x408190b2) — its
    prologue saves a3-a7 and masks the level arg to 3 bits, a much
    better match for a real variadic log function than before
  printf-like logger    0x4213da56  (was wrongly 0x4213da5e)

v4's "esp_log_write is silently filtered" theory (isolation patch #5)
was itself built on the wrong address for that function — it's very
possible the real esp_log_write() would have worked fine all along.
Sticking with the printf-like logger here anyway since it's simpler
(no filtering/registration questions to worry about) and this patch
already uses it.

Ground truth:
  opendir()   0x4200b4c8
  readdir()   0x4200b530
  printf-like logger  0x4213da56
  delay-ish function  0x40815a80 (a0=3000, replicated — this hook skips it)
  dirent->d_name offset: +3
Hook site: 0x42022806, 8 bytes. Return: 0x42022810 (confirmed real boundary).
"""
from fw_patch_tool import Firmware, Asm

PRINTF_VA = 0x4213da56
DELAY_FN_VA = 0x40815a80
OPENDIR_VA = 0x4200b4c8
READDIR_VA = 0x4200b530

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
cave_va_actual = fw.add_cave(cave_bytes, name="log_first_sd_file_on_keypress_v5")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_keypress_sd_probe_v5",
              note="v5: corrected opendir/readdir/log-fn/printf addresses "
                   "(all were off by 8, same bug class as RETURN_VA)")

manifest = fw.save("firmware-keypress-sd-patch-v5.bin")
print("saved firmware-keypress-sd-patch-v5.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
for name, va in string_vas.items():
    print(f"  {name}@{hex(va)}")
