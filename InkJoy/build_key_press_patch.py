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

fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()

STR_SDCARD = b"/sdcard\x00"
STR_TAG = b"SDPATCH\x00"
STR_FMT = b"SD root first file: %s\n\x00"

# first pass: figure out code length so we can place strings right after it
asm = Asm(base_va=cave_va)
asm.call(PRINTF_VA)  # a0 already holds the KEY_STATE_PRESSED string pointer
asm.la("a0", cave_va)
asm.call(OPENDIR_VA)
asm.mv("t0", "a0")
asm.beq("t0", "zero", "done")
asm.mv("a0", "t0")
asm.call(READDIR_VA)
asm.mv("t1", "a0")
asm.beq("t1", "zero", "done")
asm.addi("t2", "t1", 3)
asm.li("a0", 1)
asm.la("a1", cave_va)
asm.la("a2", cave_va)
asm.mv("a3", "t2")
asm.call(LOG_WRITE_VA)
asm.label("done")
asm.call(RETURN_VA, link="zero")
code_len = len(asm.assemble())

strings_va = cave_va + code_len
sdcard_va = strings_va
tag_va = sdcard_va + len(STR_SDCARD)
fmt_va = tag_va + len(STR_TAG)
data = STR_SDCARD + STR_TAG + STR_FMT
data += b"\x00" * ((-len(data)) % 4)

asm2 = Asm(base_va=cave_va)
asm2.call(PRINTF_VA)
asm2.la("a0", sdcard_va)
asm2.call(OPENDIR_VA)
asm2.mv("t0", "a0")
asm2.beq("t0", "zero", "done")
asm2.mv("a0", "t0")
asm2.call(READDIR_VA)
asm2.mv("t1", "a0")
asm2.beq("t1", "zero", "done")
asm2.addi("t2", "t1", 3)
asm2.li("a0", 1)
asm2.la("a1", tag_va)
asm2.la("a2", fmt_va)
asm2.mv("a3", "t2")
asm2.call(LOG_WRITE_VA)
asm2.label("done")
asm2.call(RETURN_VA, link="zero")
code = asm2.assemble()
assert len(code) == code_len

cave_bytes = code + data
cave_va_actual = fw.add_cave(cave_bytes, name="log_first_sd_file_on_keypress")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_keypress_sd_probe",
              note="replaces the auipc+jalr call to the KEY_STATE_PRESSED "
                   "printf at 0x42022806; cave replicates that call first "
                   "(a0 unmodified) then probes /sdcard and jumps back to "
                   "0x4202280e")

manifest = fw.save("firmware-keypress-sd-patch.bin")
print("saved firmware-keypress-sd-patch.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
print("  strings: sdcard@%s tag@%s fmt@%s" % (hex(sdcard_va), hex(tag_va), hex(fmt_va)))
