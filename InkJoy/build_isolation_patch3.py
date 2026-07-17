"""Isolation patch #3: the most minimal possible test. The cave makes NO
calls at all — not even replicating the original KEY_STATE_PRESSED printf.
It just jumps in and immediately jumps back to 0x4202280e.

Purpose: patches #1 and #2 both hung on their SECOND call from this hook
(the replicated printf always succeeded as call #1, but a second call —
whether esp_log_write or the same printf function again — hung every
time). This isolates the trampoline mechanism itself from the "is a call
safe here" question: if the hook+jump-back alone works cleanly (device
stays healthy, and downstream code like "Multi 1 clicks" still fires
normally — just without "key_task: KEY_STATE_PRESSED" this time, since
we're deliberately not replicating that call), it confirms our hook
placement/return address are correct and the problem really is "no calls
at all are safe from this exact spot," not a broken trampoline.

Hook site: 0x42022806, 8 bytes. Return: 0x4202280e.
"""
from fw_patch_tool import Firmware, Asm

RETURN_VA = 0x4202280e
HOOK_VA = 0x42022806

fw = Firmware.load("firmware.bin")
cave_va = fw.next_cave_va()

asm = Asm(base_va=cave_va)
asm.call(RETURN_VA, link="zero")
code = asm.assemble()

cave_va_actual = fw.add_cave(code, name="isolation_probe_noop")
assert cave_va_actual == cave_va

fw.write_jump(HOOK_VA, cave_va, name="hook_isolation_probe3",
              note="isolation test #3: cave does nothing but jump straight "
                   "back to 0x4202280e, no calls at all, not even the "
                   "replicated KEY_STATE_PRESSED printf")

manifest = fw.save("firmware-isolation-patch3.bin")
print("saved firmware-isolation-patch3.bin, manifest at", manifest)
print("cave at", hex(cave_va), "size", len(code), "bytes")
