"""Isolation patch #5: retest esp_log_write() with the addressing bug fixed,
to determine whether it's silently filtered (not crashing, just producing
no output) rather than unsafe. Same corrected hook/return as patch #4.

v3 (full SD probe, corrected addressing) ran with zero crashes but also
zero SDPATCH output — including its unconditional "before" log, which
fires before any opendir call. That rules out opendir/readdir as the
cause of the silence. Leading theory: ESP-IDF's newer log system does
runtime tag/level filtering possibly keyed off a compile-time table of
known format strings, and our injected string/tag was never part of that
table, so the runtime check silently rejects it rather than crashing.

This patch is the same shape as isolation_patch #1 (single esp_log_write
call, nothing else) but with the fixed RETURN_VA/delay-call replication.

Hook site: 0x42022806, 8 bytes. Return: 0x42022810 (corrected).
"""
from fw_patch_tool import Firmware, Asm

DELAY_FN_VA = 0x40815a80
LOG_WRITE_VA = 0x408190b2
HOOK_VA = 0x42022806
RETURN_VA = 0x42022810

STR_TAG = b"SDPATCH\x00"
STR_ALIVE = b"alive\n\x00"

fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()


def build_cave(cave_va, s):
    asm = Asm(base_va=cave_va)
    asm.li("a0", 3000)
    asm.call(DELAY_FN_VA)
    asm.li("a0", 1)
    asm.la("a1", s["tag"])
    asm.la("a2", s["alive"])
    asm.call(LOG_WRITE_VA)
    asm.call(RETURN_VA, link="zero")
    return asm


placeholder = {"tag": cave_va, "alive": cave_va}
code_len = len(build_cave(cave_va, placeholder).assemble())
tag_va = cave_va + code_len
alive_va = tag_va + len(STR_TAG)
data = STR_TAG + STR_ALIVE
data += b"\x00" * ((-len(data)) % 4)

code = build_cave(cave_va, {"tag": tag_va, "alive": alive_va}).assemble()
assert len(code) == code_len

cave_bytes = code + data
cave_va_actual = fw.add_cave(cave_bytes, name="isolation_probe5_fixed_return")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_isolation_probe5",
              note="corrected RETURN_VA + delay-call replication, single "
                   "esp_log_write() call to determine if it's silently "
                   "filtered rather than unsafe")

manifest = fw.save("firmware-isolation-patch5.bin")
print("saved firmware-isolation-patch5.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  tag@%s alive@%s" % (hex(tag_va), hex(alive_va)))
