"""SD-probe v10: v9 confirmed the whole mechanism works — "SDPATCH alive"
printed on real hardware, then "SDPATCH opendir failed". But that hook
fires right after "IJ Compile", well before "SD: STA power debug: skip
sd_test" and the SPIFFS mount later in boot, so the opendir() failure
there likely just means "/sdcard isn't mounted yet", not "no card". This
moves the same proven mechanism to a later hook: right after the
esp_log_write() call for "SD: STA power debug: skip sd_test" itself
(confirmed via disassembly to target the same 0x408190aa), by which
point the SD subsystem should have had its chance to initialize.

Different hook shape than v9: this time we hook the log_write CALL
itself (0x42023286, 8 bytes) rather than the instruction after it —
a0-a4 are already correctly set up by the untouched preceding code (the
real TAG/format/timestamp for the "skip sd_test" line), so the cave just
replicates that exact call unchanged, then does its own SD probe with
its own log calls using the same already-valid timestamp (saved from a3
before it gets clobbered).

Ground truth:
  opendir()   0x4200b4c8
  readdir()   0x4200b530
  esp_log_write()  0x408190aa
Hook site: 0x42023286, 8 bytes (the log_write call for "skip sd_test").
Return: 0x4202328e.
"""
from fw_patch_tool import Firmware, Asm

OPENDIR_VA = 0x4200b4c8
READDIR_VA = 0x4200b530
LOG_WRITE_VA = 0x408190aa

HOOK_VA = 0x42023286
RETURN_VA = 0x4202328e

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
    # a0-a4 are already correctly set by the untouched preceding code
    # (level=3, TAG, the real "skip sd_test" format string, timestamp,
    # TAG again) — just replicate the call this hook otherwise skips,
    # unchanged, before doing anything of our own.
    asm.mv("t1", "a3")  # save the already-valid timestamp before it's clobbered
    asm.call(LOG_WRITE_VA)

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
cave_va_actual = fw.add_cave(cave_bytes, name="boot_probe_cave_v2")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_boot_probe_v2",
              note="v10: hooks the 'skip sd_test' log_write call itself, "
                   "replicates it unchanged, then does the SD probe later "
                   "in boot than v9 (after SD subsystem init chance)")

manifest = fw.save("firmware-boot-probe-patch-v2.bin")
print("saved firmware-boot-probe-patch-v2.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
for name, va in string_vas.items():
    print(f"  {name}@{hex(va)}")
