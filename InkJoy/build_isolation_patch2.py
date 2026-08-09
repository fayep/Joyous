"""Isolation patch #2: same hook as build_isolation_patch.py, but instead
of calling esp_log_write() a second time, call the SAME printf-like
function we already know is safe here (0x4213da5e — it successfully
replicated "key_task: KEY_STATE_PRESSED") with a NEW string of our own.

Purpose: patch #1 (esp_log_write) produced no output at all — not even
its own unconditional call. This isolates whether esp_log_write() itself
is the problem (wrong address/signature, or genuinely unsafe from this
context), or whether ANY second/nested call from this hook is unsafe
(e.g. stack exhaustion one level deeper, regardless of which function).

If "SDPATCH: second call ok" prints: esp_log_write specifically is the
problem. If it doesn't print either: the problem is calling anything a
second time from this hook, not esp_log_write specifically.

Hook site: 0x42022806, 8 bytes. Return: 0x4202280e.
"""
from fw_patch_tool import Firmware, Asm

PRINTF_VA = 0x4213da5e
RETURN_VA = 0x4202280e
HOOK_VA = 0x42022806

STR_MSG = b"SDPATCH: second call ok\r\n\x00"

fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()


def build_cave(cave_va, msg_va):
    asm = Asm(base_va=cave_va)
    asm.call(PRINTF_VA)  # replicate the original KEY_STATE_PRESSED log, a0 untouched
    asm.la("a0", msg_va)
    asm.call(PRINTF_VA)  # second call, same known-safe function, our own string
    asm.call(RETURN_VA, link="zero")
    return asm


code_len = len(build_cave(cave_va, cave_va).assemble())
msg_va = cave_va + code_len
data = STR_MSG + b"\x00" * ((-len(STR_MSG)) % 4)

code = build_cave(cave_va, msg_va).assemble()
assert len(code) == code_len

cave_bytes = code + data
cave_va_actual = fw.add_cave(cave_bytes, name="isolation_probe_second_printf")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_isolation_probe2",
              note="isolation test #2: replicate KEY_STATE_PRESSED printf, "
                   "call the SAME printf-like function again with a new "
                   "string (not esp_log_write), jump back to 0x4202280e")

manifest = fw.save("firmware-isolation-patch2.bin")
print("saved firmware-isolation-patch2.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(cave_bytes), "bytes")
print("  code:", code_len, "bytes")
print("  msg@%s" % hex(msg_va))
