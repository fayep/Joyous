"""SD-probe patch #2: hook the raw key-press debounce log instead of the
"sc12" bookkeeping function, since that one turned out not to fire on a
quiet boot. This hook fires on literally any physical button press
("key_task: KEY_STATE_PRESSED" in the boot log) — reliable, user-triggered,
no waiting.

Hook site: 0x42022806, 8 bytes ("auipc ra,0x11b" / "jalr ra,ra,0x258" —
the call to a printf-like function logging the KEY_STATE_PRESSED string).
Unlike the sc12 hook, nothing downstream depends on any register this call
touches: a0 (the string pointer, still valid on cave entry) is immediately
overwritten with an unrelated constant right after (0x4202280e: `c.lui a0,1`
/ `addi a0,a0,-0x448`), and no other register is read before being freshly
set. So the cave needs zero save/restore — just replicate the original
printf call (a0 is already correct), do the SD probe, jump back.

v2: logs at every step (before attempting opendir, and on each failure
branch), not just on full success — round 1 produced no output at all, and
without a "we got this far" marker there was no way to tell whether the
hook fired and opendir() returned NULL, or something else entirely.

Ground truth reused from build_sd_list_patch.py:
  opendir()        0x4200b4d0
  readdir()        0x4200b538
  esp_log_write()  0x408190b2
  dirent->d_name offset: +3
Plus the raw printf-like logger at 0x4213da5e (found via the same
auipc+jalr resolution method, confirmed identical call convention: single
arg in a0, no return value used).
"""
from fw_patch_tool import Firmware, Asm

PRINTF_VA = 0x4213da5e
OPENDIR_VA = 0x4200b4d0
READDIR_VA = 0x4200b538
LOG_WRITE_VA = 0x408190b2

HOOK_VA = 0x42022806
RETURN_VA = 0x4202280e

STR_SDCARD = b"/sdcard\x00"
STR_TAG = b"SDPATCH\x00"
STR_BEFORE = b"probing /sdcard\n\x00"
STR_FAIL_OPENDIR = b"opendir failed\n\x00"
STR_FAIL_READDIR = b"readdir returned null\n\x00"
STR_FMT = b"first file: %s\n\x00"


def build_cave(cave_va, s):
    """s: dict of string name -> VA (placeholder values are fine for the
    length-measuring first pass, since every instruction here is 4 bytes
    regardless of the actual address value)."""
    asm = Asm(base_va=cave_va)
    asm.call(PRINTF_VA)  # replicate the original KEY_STATE_PRESSED log, a0 untouched

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
cave_va_actual = fw.add_cave(cave_bytes, name="log_first_sd_file_on_keypress_v2")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_keypress_sd_probe_v2",
              note="replaces the auipc+jalr call to the KEY_STATE_PRESSED "
                   "printf at 0x42022806; cave replicates that call first "
                   "(a0 unmodified), logs at every step of the /sdcard "
                   "probe, then jumps back to 0x4202280e")

manifest = fw.save("firmware-keypress-sd-patch.bin")
print("saved firmware-keypress-sd-patch.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
for name, va in string_vas.items():
    print(f"  {name}@{hex(va)}")
