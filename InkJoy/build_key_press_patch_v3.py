"""SD-probe patch v3: same hook site and full opendir/readdir/log-at-every-step
logic as build_key_press_patch.py (v2), but with the RETURN_VA addressing
bug fixed (see build_isolation_patch4.py for the full root-cause writeup).

v2 hung the device on every attempt. Isolation testing (patches #1-#3)
seemed to implicate esp_log_write() or opendir/readdir specifically, but
patch #4 (bare trampoline, corrected return address, no log calls) ran
completely cleanly through a real button press and subsequent play
command — proving the actual bug was RETURN_VA=0x4202280e landing mid
instruction (the second half of a 4-byte auipc at 0x4202280c), not
anything about which functions are safe to call from this hook.

Changes from v2:
  - RETURN_VA: 0x4202280e -> 0x42022810 (real instruction boundary)
  - cave now replicates the auipc+jalr call this hook skips over
    (0x40815a80, called with a0=3000 — likely a delay function) before
    doing anything else, to preserve original behavior/timing exactly

Ground truth (unchanged from v2):
  opendir()        0x4200b4d0
  readdir()        0x4200b538
  esp_log_write()  0x408190b2
  dirent->d_name offset: +3
  printf-like KEY_STATE_PRESSED logger: 0x4213da5e (untouched by this
    hook now — it's BEFORE the hook site, not replicated)
  delay-ish function: 0x40815a80 (a0=3000)
"""
from fw_patch_tool import Firmware, Asm

DELAY_FN_VA = 0x40815a80
OPENDIR_VA = 0x4200b4d0
READDIR_VA = 0x4200b538
LOG_WRITE_VA = 0x408190b2

HOOK_VA = 0x42022806
RETURN_VA = 0x42022810  # corrected

STR_SDCARD = b"/sdcard\x00"
STR_TAG = b"SDPATCH\x00"
STR_BEFORE = b"probing /sdcard\n\x00"
STR_FAIL_OPENDIR = b"opendir failed\n\x00"
STR_FAIL_READDIR = b"readdir returned null\n\x00"
STR_FMT = b"first file: %s\n\x00"


def build_cave(cave_va, s):
    asm = Asm(base_va=cave_va)
    # replicate what this hook overwrites: a0 = 3000; call the delay fn
    asm.li("a0", 3000)
    asm.call(DELAY_FN_VA)

    def log(fmt_va, payload_reg=None):
        asm.li("a0", 1)
        asm.la("a1", s["tag"])
        asm.la("a2", fmt_va)
        if payload_reg:
            asm.mv("a3", payload_reg)
        asm.call(LOG_WRITE_VA)

    log(s["before"])
    asm.la("a0", s["sdcard"])
    asm.call(OPENDIR_VA)
    asm.mv("t0", "a0")
    asm.bne("t0", "zero", "opendir_ok")
    log(s["fail_opendir"])
    asm.j("done")

    asm.label("opendir_ok")
    asm.mv("a0", "t0")
    asm.call(READDIR_VA)
    asm.mv("t1", "a0")
    asm.bne("t1", "zero", "readdir_ok")
    log(s["fail_readdir"])
    asm.j("done")

    asm.label("readdir_ok")
    asm.addi("t2", "t1", 3)
    log(s["fmt"], payload_reg="t2")

    asm.label("done")
    asm.call(RETURN_VA, link="zero")
    return asm


fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()

placeholder = {k: cave_va for k in ("sdcard", "tag", "before", "fail_opendir", "fail_readdir", "fmt")}
code_len = len(build_cave(cave_va, placeholder).assemble())

strings_va = cave_va + code_len
names_and_bytes = [
    ("sdcard", STR_SDCARD), ("tag", STR_TAG), ("before", STR_BEFORE),
    ("fail_opendir", STR_FAIL_OPENDIR), ("fail_readdir", STR_FAIL_READDIR), ("fmt", STR_FMT),
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
cave_va_actual = fw.add_cave(cave_bytes, name="log_first_sd_file_on_keypress_v3")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_keypress_sd_probe_v3",
              note="corrected RETURN_VA (0x42022810, was wrongly 0x4202280e "
                   "in v1/v2); cave replicates the skipped delay call, then "
                   "logs at every step of the /sdcard probe")

manifest = fw.save("firmware-keypress-sd-patch-v3.bin")
print("saved firmware-keypress-sd-patch-v3.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
for name, va in string_vas.items():
    print(f"  {name}@{hex(va)}")
