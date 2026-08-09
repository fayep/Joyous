"""Minimal isolation patch: same hook site as build_key_press_patch.py
(0x42022806, the KEY_STATE_PRESSED printf call), but does NOTHING beyond
replicating that printf and then making exactly one esp_log_write() call.
No opendir/readdir at all.

Purpose: v2 of the key-press patch produced zero output beyond the
replicated printf — not even the unconditional "before" log line that
should have printed before any filesystem call. That points at
esp_log_write() itself being unsafe to call from this task context
(different internal locking/stack depth than the plain printf-style
logger we successfully replicated), rather than opendir/readdir. This
patch isolates that: if "SDPATCH: alive" doesn't print, the problem is
calling esp_log_write() from here at all, not filesystem I/O.

Ground truth (all reused from build_key_press_patch.py):
  esp_log_write()  0x408190b2
  printf-like KEY_STATE_PRESSED logger  0x4213da5e
Hook site: 0x42022806, 8 bytes. Return: 0x4202280e.
"""
from fw_patch_tool import Firmware, Asm

PRINTF_VA = 0x4213da5e
LOG_WRITE_VA = 0x408190b2

HOOK_VA = 0x42022806
RETURN_VA = 0x4202280e

STR_TAG = b"SDPATCH\x00"
STR_ALIVE = b"alive\n\x00"

fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()


def build_cave(cave_va, s):
    asm = Asm(base_va=cave_va)
    asm.call(PRINTF_VA)  # replicate the original KEY_STATE_PRESSED log, a0 untouched
    asm.li("a0", 1)
    asm.la("a1", s["tag"])
    asm.la("a2", s["alive"])
    asm.call(LOG_WRITE_VA)
    asm.call(RETURN_VA, link="zero")
    return asm


placeholder = {"tag": cave_va, "alive": cave_va}
code_len = len(build_cave(cave_va, placeholder).assemble())

strings_va = cave_va + code_len
tag_va = strings_va
alive_va = tag_va + len(STR_TAG)
data = STR_TAG + STR_ALIVE
data += b"\x00" * ((-len(data)) % 4)

code = build_cave(cave_va, {"tag": tag_va, "alive": alive_va}).assemble()
assert len(code) == code_len

cave_bytes = code + data
cave_va_actual = fw.add_cave(cave_bytes, name="isolation_probe_log_only")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_isolation_probe",
              note="minimal isolation test: replicate KEY_STATE_PRESSED printf, "
                   "call esp_log_write() exactly once with a trivial message, "
                   "no filesystem calls at all, jump back to 0x4202280e")

manifest = fw.save("firmware-isolation-patch.bin")
print("saved firmware-isolation-patch.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes /", code_len // 4, "instructions")
print("  tag@%s alive@%s" % (hex(tag_va), hex(alive_va)))
