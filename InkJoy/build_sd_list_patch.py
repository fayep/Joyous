"""First real fw_patch_tool patch: open /sdcard's root directory, readdir()
once, and log the first entry's d_name — a minimal, low-risk probe to prove
opendir/readdir actually work on this hardware (H2's TF-card-absent history
means this has never been exercised) before building anything bigger like
the H3 RLE plane decoder port.

Ground truth used here (found via Binary Ninja on firmware.bndb + manual
capstone cross-referencing on firmware.bin, ij_epd v0.5.6):
  opendir()        0x4200b4d0   (a0 = path) -> a0 = DIR* or NULL
  readdir()        0x4200b538   (a0 = DIR*) -> a0 = struct dirent* or NULL
  esp_log_write()  0x408190b2   (a0=level, a1=tag, a2=fmt, a3.. = varargs)
  dirent->d_name offset: +3 (confirmed from the one real opendir/readdir
    caller found in this firmware, VA ~0x42023b14 — this vendor's dirent
    struct is not the same layout as upstream esp_vfs_dirent.h)

Hook site: 0x4201afd6, 8 bytes ("lui a2,0x42167" / "addi a2,a2,0x550")
inside the "sc12"-tagged slideshow bookkeeping function. Those two
instructions just materialize the ESP_LOG format-string pointer for the
call at 0x4201afe4 — our cave replicates that computation itself right
before jumping back, and preserves a1/a3/a4 (all still read by the
surviving log call after the hook) across a 16-byte stack scratch area.
"""
from fw_patch_tool import Firmware, Asm

OPENDIR_VA = 0x4200b4d0
READDIR_VA = 0x4200b538
LOG_WRITE_VA = 0x408190b2

HOOK_VA = 0x4201afd6
RETURN_VA = 0x4201afde  # first instruction after the 8 patched bytes
RESTORED_A2 = 0x42167550  # value the overwritten lui+addi originally produced

fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()

asm = Asm(base_va=cave_va)

# --- code ---
asm.addi("sp", "sp", -16)
asm.sw("a1", 12, "sp")
asm.sw("a3", 8, "sp")
asm.sw("a4", 4, "sp")

asm.la("a0", cave_va)  # placeholder overwritten below once STR_SDCARD is known — see two-pass note
asm.call(OPENDIR_VA)
asm.mv("t0", "a0")
asm.beq("t0", "zero", "done")

asm.mv("a0", "t0")
asm.call(READDIR_VA)
asm.mv("t1", "a0")
asm.beq("t1", "zero", "done")

asm.addi("t2", "t1", 3)  # t2 = &dirent->d_name

asm.li("a0", 1)  # log level (matches the INFO-ish constant seen elsewhere in this firmware)
asm.la("a1", cave_va)  # placeholder — STR_TAG, patched below
asm.la("a2", cave_va)  # placeholder — STR_FMT, patched below
asm.mv("a3", "t2")
asm.call(LOG_WRITE_VA)

asm.label("done")
asm.lw("a1", 12, "sp")
asm.lw("a3", 8, "sp")
asm.lw("a4", 4, "sp")
asm.addi("sp", "sp", 16)
asm.li("a2", RESTORED_A2)
asm.call(RETURN_VA, link="zero")

# The three `la` placeholders above need real strings, but the strings
# themselves need to live right after the code, and their offsets aren't
# known until we know how many instructions the code compiles to. Simplest
# fix: assemble once to get the code length, place strings right after,
# then rebuild the asm with the real addresses substituted in.
placeholder_code = asm.assemble()
code_len = len(placeholder_code)
strings_va = cave_va + code_len

STR_SDCARD = b"/sdcard\x00"
STR_TAG = b"SDPATCH\x00"
STR_FMT = b"SD root first file: %s\n\x00"

sdcard_va = strings_va
tag_va = sdcard_va + len(STR_SDCARD)
fmt_va = tag_va + len(STR_TAG)
data = STR_SDCARD + STR_TAG + STR_FMT
pad = (-len(data)) % 4
data += b"\x00" * pad

# rebuild with real string addresses
asm2 = Asm(base_va=cave_va)
asm2.addi("sp", "sp", -16)
asm2.sw("a1", 12, "sp")
asm2.sw("a3", 8, "sp")
asm2.sw("a4", 4, "sp")

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
asm2.lw("a1", 12, "sp")
asm2.lw("a3", 8, "sp")
asm2.lw("a4", 4, "sp")
asm2.addi("sp", "sp", 16)
asm2.li("a2", RESTORED_A2)
asm2.call(RETURN_VA, link="zero")

code = asm2.assemble()
assert len(code) == code_len, "instruction count changed between passes — a la()/li() picked a different encoding"

cave_bytes = code + data
cave_va_actual = fw.add_cave(cave_bytes, name="log_first_sd_file")
assert cave_va_actual == cave_va, f"cave landed at {hex(cave_va_actual)}, expected {hex(cave_va)}"

fw.write_jump(HOOK_VA, cave_va, name="hook_sc12_sd_probe",
              note="replaces 'lui a2,0x42167 / addi a2,a2,0x550' (fmt-string ptr "
                   "setup for the log call at 0x4201afe4); cave restores a2 before "
                   "jumping back to 0x4201afde")

manifest = fw.save("firmware-sd-list-patch.bin")
print("saved firmware-sd-list-patch.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
print("  strings: sdcard@%s tag@%s fmt@%s" % (hex(sdcard_va), hex(tag_va), hex(fmt_va)))
